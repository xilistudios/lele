import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  createAssistantMessage,
  createDeterministicToolMessageId,
  createOptimisticUserId,
  createToolMessage,
  createToolMessageId,
  createUserMessage,
  parseAttachmentsFromContent,
  parseSubagentSessionKey,
  toChatMessages,
} from '../lib/chatMessageBuilder'
import { wsDebug } from '../lib/debug'
import type {
  ApprovalRequest,
  ApprovalResult,
  ChatMessage,
  HistoryToolCall,
  ToolStatus,
} from '../lib/types'
import type { ClientCommand } from '../services/ws/events'
import {
  type HistoryMessage,
  chatHistoryQueryKey,
  updateChatHistoryFromRaw,
} from './useChatHistory'

export { parseAttachmentsFromContent, parseSubagentSessionKey, toChatMessages }

// Interval between characters when animating streaming text in the UI.
const STREAM_CHAR_INTERVAL_MS = 12

type ClientEvent = {
  event: string
  data: unknown
}

type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

export function useMessages(
  wsSend: SendFn,
  _currentSessionKey: string | null,
  currentSessionKeyRef: React.MutableRefObject<string | null>,
  onSessionUpdated?: () => void,
) {
  const [streamingMessages, setStreamingMessages] = useState<ChatMessage[]>([])
  const [toolStatus, setToolStatus] = useState<ToolStatus | null>(null)
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [approvalResult, setApprovalResult] = useState<ApprovalResult | null>(null)
  const approvalTimerRef = useRef<ReturnType<typeof setTimeout>>()
  const [pendingAttachments, setPendingAttachments] = useState<string[]>([])
  const [processingSessions, setProcessingSessions] = useState<Set<string>>(new Set())
  const streamingRef = useRef(streamingMessages)
  const processingSessionKeyRef = useRef<string | null>(null)
  const lastSessionRefreshRef = useRef<number>(0)
  const eventHandlerRef = useRef<(event: ClientEvent) => void>(() => {})
  const streamQueuesRef = useRef<
    Map<
      string,
      {
        sessionKey: string
        chars: string[]
        done: boolean
        timer: ReturnType<typeof setInterval> | null
      }
    >
  >(new Map())

  const queryClient = useQueryClient()

  const getHistoryUserCount = useCallback(
    (sessionKey: string) => {
      const history = queryClient.getQueryData<{ messages?: ChatMessage[] }>(
        chatHistoryQueryKey(sessionKey),
      )
      return history?.messages?.filter((message) => message.role === 'user').length ?? 0
    },
    [queryClient],
  )

  useEffect(() => {
    streamingRef.current = streamingMessages
  }, [streamingMessages])

  // Debounce session refreshes to avoid double sidebar updates when
  // message.ack and history.updated fire in rapid succession (< 300ms apart).
  const debouncedSessionRefresh = useCallback(() => {
    const now = Date.now()
    if (now - lastSessionRefreshRef.current < 300) return
    lastSessionRefreshRef.current = now
    onSessionUpdated?.()
  }, [onSessionUpdated])

  const ensureAssistantPlaceholder = useCallback(
    (messageId: string, sessionKey: string, chunk = '', isDone = false) => {
      setStreamingMessages((current) => {
        const existing = current.find((m) => m.id === messageId)
        if (existing) {
          return current.map((m) =>
            m.id === messageId
              ? {
                  ...m,
                  content: isDone ? chunk || m.content : chunk ? `${m.content}${chunk}` : m.content,
                  streaming: !isDone,
                  sessionKey,
                }
              : m,
          )
        }
        return [
          ...current,
          createAssistantMessage({
            id: messageId,
            sessionKey,
            content: chunk,
            streaming: !isDone,
          }),
        ]
      })
    },
    [],
  )

  const clearStreamQueue = useCallback((messageId: string) => {
    const queue = streamQueuesRef.current.get(messageId)
    if (queue?.timer) {
      clearInterval(queue.timer)
    }
    streamQueuesRef.current.delete(messageId)
  }, [])

  const clearAllStreamQueues = useCallback(() => {
    for (const queue of streamQueuesRef.current.values()) {
      if (queue.timer) {
        clearInterval(queue.timer)
      }
    }
    streamQueuesRef.current.clear()
  }, [])

  const drainStreamQueue = useCallback(
    (messageId: string) => {
      const queue = streamQueuesRef.current.get(messageId)
      if (!queue) return

      const nextChar = queue.chars.shift()
      if (nextChar) {
        ensureAssistantPlaceholder(messageId, queue.sessionKey, nextChar, false)
        return
      }

      if (queue.done) {
        clearStreamQueue(messageId)
        ensureAssistantPlaceholder(messageId, queue.sessionKey, '', true)
      }
    },
    [clearStreamQueue, ensureAssistantPlaceholder],
  )

  const enqueueStreamChunk = useCallback(
    (messageId: string, sessionKey: string, chunk: string, done: boolean) => {
      if (!messageId || !sessionKey) return

      let queue = streamQueuesRef.current.get(messageId)
      if (!queue) {
        queue = {
          sessionKey,
          chars: [],
          done: false,
          timer: null,
        }
        streamQueuesRef.current.set(messageId, queue)
      }

      queue.sessionKey = sessionKey
      if (chunk) {
        queue.chars.push(...Array.from(chunk))
      }
      if (done) {
        queue.done = true
      }

      if (!queue.timer) {
        queue.timer = setInterval(() => drainStreamQueue(messageId), STREAM_CHAR_INTERVAL_MS)
      }
    },
    [drainStreamQueue],
  )

  const sendMessage = useCallback(
    async (content: string, attachments: string[], sessionKey: string, agentId: string | null) => {
      if (!sessionKey) return

      const normalizedContent = content.trim()
      if (normalizedContent.length === 0) return

      const userMessage = createUserMessage({
        id: createOptimisticUserId(),
        sessionKey,
        content: normalizedContent,
        optimistic: true,
        optimisticBaseCount: getHistoryUserCount(sessionKey),
        attachments: attachments.map((path) => ({
          path,
          name: path.split('/').pop() ?? path,
          kind: 'file',
        })),
      })

      setStreamingMessages((current) => [...current, userMessage])
      setPendingAttachments([])

      // Save previous cache for rollback on error
      const previousCache = queryClient.getQueryData<{
        messages?: ChatMessage[]
      }>(chatHistoryQueryKey(sessionKey))

      const cacheData = previousCache

      if (cacheData) {
        queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
          ...cacheData,
          messages: [...(cacheData.messages ?? []), userMessage],
        })
      } else {
        queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
          sessionKey,
          messages: [userMessage],
          rawMessages: [],
          processing: false,
        })
      }

      // Send via WebSocket — the server responds with message.ack containing
      // the message_id, and all streaming events flow through the same WS connection.
      wsSend('message', {
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })
    },
    [wsSend, getHistoryUserCount, queryClient],
  )

  // biome-ignore lint/correctness/useExhaustiveDependencies: setters are stable, refs are intentionally excluded
  const handleEvent = useCallback(
    (event: ClientEvent) => {
      const data = event.data as Record<string, unknown>
      const eventSessionKey = data.session_key as string | undefined

      switch (event.event) {
        case 'welcome': {
          const welcomeData = data as {
            session_key?: string
            processing?: boolean
            status?: string
          }
          if (welcomeData.processing && welcomeData.session_key) {
            const sessionKey = welcomeData.session_key
            processingSessionKeyRef.current = sessionKey
            setProcessingSessions((prev) => new Set(prev).add(sessionKey))
          }
          break
        }
        case 'message.stream':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped message.stream: session mismatch', {
              eventSessionKey,
              currentSessionKey: currentSessionKeyRef.current,
            })
            break
          }
          wsDebug('[WS] message.stream received', {
            messageId: data.message_id,
            eventSessionKey,
            currentSessionKey: currentSessionKeyRef.current,
            chunkLength: (data.chunk as string)?.length ?? 0,
            done: data.done,
          })
          {
            const msgId = data.message_id as string
            const sessionKey = (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string
            const chunk = (data.chunk as string) ?? ''
            const done = (data.done as boolean) ?? false

            if (done && chunk) {
              clearStreamQueue(msgId)
              ensureAssistantPlaceholder(msgId, sessionKey, chunk, true)
              break
            }

            enqueueStreamChunk(msgId, sessionKey, chunk, done)
          }
          break
        case 'message.thinking':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped message.thinking: session mismatch')
            break
          }
          setStreamingMessages((current) =>
            current.map((m) =>
              m.id === (data.message_id as string) && m.role === 'assistant'
                ? {
                    ...m,
                    reasoningContent: `${m.reasoningContent ?? ''}${(data.chunk as string) ?? ''}`,
                  }
                : m,
            ),
          )
          break
        case 'message.ack': {
          const ackSessionKey = (data.session_key as string) ?? ''
          if (ackSessionKey) {
            setProcessingSessions((prev) => new Set(prev).add(ackSessionKey))
            processingSessionKeyRef.current = ackSessionKey
            // Trigger session refresh so backend data replaces optimistic state
            debouncedSessionRefresh()
          }
          ensureAssistantPlaceholder(data.message_id as string, ackSessionKey)
          break
        }
        case 'message.complete': {
          const completedSessionKey = eventSessionKey ?? currentSessionKeyRef.current
          clearStreamQueue(data.message_id as string)
          wsDebug('[WS] message.complete received', {
            messageId: data.message_id,
            eventSessionKey,
            completedSessionKey,
          })

          // Always remove the completed session from processingSessions.
          // This ensures the sidebar spinner disappears immediately when the
          // agent finishes, even for background sessions or when the HTTP poll
          // hasn't caught up yet.
          if (completedSessionKey) {
            setProcessingSessions((prev) => {
              const next = new Set(prev)
              next.delete(completedSessionKey)
              return next
            })
          }

          if (completedSessionKey && completedSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] message.complete for different session, skipping streaming update')
            setPendingAttachments([])
            processingSessionKeyRef.current = null
            break
          }

          setStreamingMessages((current) => {
            const targetSessionKey = eventSessionKey ?? currentSessionKeyRef.current
            return current.flatMap((m) => {
              if (m.role === 'assistant' && m.id === (data.message_id as string)) {
                const content = (data.content as string) || m.content
                return [{ ...m, content, streaming: false }]
              }
              if (
                m.role === 'user' &&
                m.sessionKey === targetSessionKey &&
                m.content.trim() === ''
              ) {
                return []
              }
              if (m.role === 'tool' && m.sessionKey === targetSessionKey) {
                return [{ ...m, streaming: false }]
              }
              return [m]
            })
          })
          setToolStatus(null)
          setPendingAttachments([])
          processingSessionKeyRef.current = null
          // Refresh sessions so sidebar shows updated message count
          debouncedSessionRefresh()
          break
        }
        case 'history.updated': {
          const historySessionKey = (data.session_key as string) ?? currentSessionKeyRef.current
          queryClient.invalidateQueries({
            queryKey: chatHistoryQueryKey(historySessionKey),
          })
          debouncedSessionRefresh()
          // Only remove optimistic user messages — let mergeMessages() handle
          // deduplication of completed assistants and tools against the base
          // (HTTP history). Removing them here causes a visual flicker because
          // the streaming copies disappear before the HTTP refetch delivers
          // the canonical versions with different IDs.
          if (historySessionKey === currentSessionKeyRef.current) {
            setStreamingMessages((current) =>
              current.filter((m) => {
                if (m.sessionKey !== historySessionKey) return true
                if (m.role === 'user' && m.optimistic) return false
                return true
              }),
            )
          }
          break
        }
        case 'messages.catchup': {
          const catchupData = data as {
            session_key?: string
            is_initial: boolean
            messages: Array<{
              id?: string
              role: 'user' | 'assistant' | 'tool'
              content: string
              tool_call_id?: string
              tool_calls?: HistoryToolCall[]
            }>
          }
          const targetSessionKey = catchupData.session_key || currentSessionKeyRef.current || ''
          if (catchupData.is_initial && targetSessionKey === currentSessionKeyRef.current) {
            const rawMessages = catchupData.messages.map((m, i) => ({
              ...m,
              id: m.id ?? `catchup-${i}`,
            })) as unknown as HistoryMessage
            updateChatHistoryFromRaw(queryClient, targetSessionKey, rawMessages)
            setStreamingMessages((current) =>
              current.filter((message) => {
                if (message.sessionKey !== targetSessionKey) {
                  return true
                }
                if (message.role === 'assistant') {
                  if (message.streaming) return true
                  if (message.content && message.content.length > 0) return true
                  return false
                }
                if (message.role === 'tool') {
                  return message.toolStatus === 'executing'
                }
                return true
              }),
            )
          }
          break
        }
        case 'attachments':
          setStreamingMessages((current) => {
            const idx = [...current].reverse().findIndex((m) => m.role === 'assistant')
            if (idx < 0) return current
            const targetIndex = current.length - idx - 1
            return current.map((m, i) =>
              i === targetIndex
                ? { ...m, attachments: event.data as ChatMessage['attachments'], streaming: false }
                : m,
            )
          })
          break
        case 'tool.executing': {
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped tool.executing: session mismatch')
            break
          }
          setToolStatus(event.data as ToolStatus)
          const toolCallId = data.tool_call_id as string | undefined
          const toolArgsStr = data.arguments
            ? `${data.tool as string} ${JSON.stringify(data.arguments)}`
            : (data.action as string)
          const toolMsg = createToolMessage({
            id: toolCallId
              ? createDeterministicToolMessageId('ws', toolCallId)
              : createToolMessageId(data.tool as string),
            sessionKey: (eventSessionKey ?? currentSessionKeyRef.current ?? undefined) as string,
            toolName: data.tool as string,
            toolArgs: toolArgsStr,
            toolStatus: 'executing',
            toolCallId,
            subagentSessionKey: data.subagent_session_key as string | undefined,
          })
          setStreamingMessages((current) => {
            if (toolCallId) {
              const existingIdx = current.findIndex(
                (m) => m.role === 'tool' && m.toolCallId === toolCallId,
              )
              if (existingIdx >= 0) {
                return current.map((m, i) =>
                  i === existingIdx
                    ? { ...m, toolArgs: toolArgsStr, toolStatus: 'executing' as const }
                    : m,
                )
              }
            }
            // Always insert tool messages after the last assistant message.
            // This preserves chronological order: all tool calls from the
            // current LLM iteration appear after the assistant text that
            // preceded them, and before any subsequent assistant text.
            const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
            if (lastAssistantIdx < 0) return [...current, toolMsg]
            const targetIndex = current.length - lastAssistantIdx
            const arr = [...current]
            arr.splice(targetIndex, 0, toolMsg)
            return arr
          })
          break
        }
        case 'tool.result':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped tool.result: session mismatch')
            break
          }
          setToolStatus(null)
          setStreamingMessages((current) => {
            const toolCallId = data.tool_call_id as string | undefined
            let targetIndex = -1
            if (toolCallId) {
              targetIndex = current.findIndex(
                (m) => m.role === 'tool' && m.toolCallId === toolCallId,
              )
            }
            if (targetIndex < 0) {
              const lastToolIdx = [...current]
                .reverse()
                .findIndex(
                  (m) =>
                    m.role === 'tool' &&
                    m.toolStatus === 'executing' &&
                    m.toolName === (data.tool as string),
                )
              if (lastToolIdx < 0) return current
              targetIndex = current.length - lastToolIdx - 1
            }
            const isError =
              data.result &&
              typeof data.result === 'string' &&
              (data.result.toLowerCase().includes('error') ||
                data.result.toLowerCase().includes('failed'))
            return current.map((m, i) =>
              i === targetIndex
                ? {
                    ...m,
                    toolResult: data.result as string,
                    toolStatus: isError ? 'error' : 'completed',
                    toolCallId: toolCallId ?? m.toolCallId,
                    subagentSessionKey:
                      (data.subagent_session_key as string) ||
                      m.subagentSessionKey ||
                      ((data.tool as string) === 'spawn'
                        ? parseSubagentSessionKey(data.result as string | undefined)
                        : undefined),
                  }
                : m,
            )
          })
          break
        case 'subagent.result':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped subagent.result: session mismatch')
            break
          }
          setStreamingMessages((current) => {
            const toolCallId = data.tool_call_id as string | undefined
            let targetIndex = -1
            if (toolCallId) {
              targetIndex = current.findIndex(
                (m) => m.role === 'tool' && m.toolCallId === toolCallId,
              )
            }
            if (targetIndex < 0) {
              const lastSpawnIdx = [...current]
                .reverse()
                .findIndex((m) => m.role === 'tool' && m.toolName === 'spawn')
              if (lastSpawnIdx < 0) return current
              targetIndex = current.length - lastSpawnIdx - 1
            }
            return current.map((m, i) =>
              i === targetIndex
                ? {
                    ...m,
                    subagentSessionKey:
                      (data.subagent_session_key as string) || m.subagentSessionKey,
                    toolResult: m.toolResult || (data.result as string),
                  }
                : m,
            )
          })
          break
        case 'approval.request':
          setApprovalRequest(event.data as ApprovalRequest)
          break
        case 'approve.result':
          setApprovalRequest(null)
          {
            const resultData = event.data as {
              request_id: string
              approved: boolean
              command: string
            }
            if (approvalTimerRef.current) clearTimeout(approvalTimerRef.current)
            setApprovalResult({
              requestId: resultData.request_id,
              approved: resultData.approved,
              command: resultData.command,
            })
            approvalTimerRef.current = setTimeout(() => setApprovalResult(null), 5000)
          }
          break
        case 'cancel.ack':
          setToolStatus(null)
          processingSessionKeyRef.current = null
          clearAllStreamQueues()
          // Only remove the cancelled session from processing set
          {
            const cancelledSessionKey = (data.session_key as string) ?? currentSessionKeyRef.current
            setProcessingSessions((prev) => {
              if (cancelledSessionKey && prev.has(cancelledSessionKey)) {
                const next = new Set(prev)
                next.delete(cancelledSessionKey)
                return next
              }
              return prev
            })
          }
          setStreamingMessages((current) =>
            current.map((m) => ({ ...m, streaming: false })),
          )
          break
        case 'subscribe.ack': {
          const ackSessionKey = (data.session_key as string) ?? ''
          const ackProcessing = data.processing === true
          if (ackSessionKey) {
            setProcessingSessions((prev) => {
              if (ackProcessing && !prev.has(ackSessionKey)) {
                const next = new Set(prev)
                next.add(ackSessionKey)
                return next
              }
              if (!ackProcessing && prev.has(ackSessionKey)) {
                const next = new Set(prev)
                next.delete(ackSessionKey)
                return next
              }
              return prev
            })
          }
          break
        }
        case 'message.error': {
          // Backend rejected the message (e.g., invalid content, rate limit).
          // Rollback optimistic user message from both streaming state and cache.
          const errorSessionKey = (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string
          setStreamingMessages((current) =>
            current.filter((m) => !(m.role === 'user' && m.optimistic && m.sessionKey === errorSessionKey)),
          )
          // Remove optimistic user from query cache
          const cached = queryClient.getQueryData<{ messages?: ChatMessage[] }>(
            chatHistoryQueryKey(errorSessionKey),
          )
          if (cached) {
            queryClient.setQueryData(chatHistoryQueryKey(errorSessionKey), {
              ...cached,
              messages: (cached.messages ?? []).filter(
                (m) => !(m.role === 'user' && m.optimistic),
              ),
            })
          }
          setProcessingSessions((prev) => {
            const next = new Set(prev)
            next.delete(errorSessionKey)
            return next
          })
          processingSessionKeyRef.current = null
          break
        }
        case 'stream.error': {
          // Backend stream failed — mark any in-progress assistant message as errored
          const errorSessionKey = (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string
          setStreamingMessages((current) =>
            current.map((m) => {
              if (m.sessionKey === errorSessionKey && m.role === 'assistant' && m.streaming) {
                return { ...m, streaming: false, error: (data.error as string) || 'Stream error' }
              }
              return m
            }),
          )
          setProcessingSessions((prev) => {
            const next = new Set(prev)
            next.delete(errorSessionKey)
            return next
          })
          processingSessionKeyRef.current = null
          break
        }
        default:
          break
      }
    },
    [
      clearAllStreamQueues,
      clearStreamQueue,
      currentSessionKeyRef,
      debouncedSessionRefresh,
      enqueueStreamChunk,
      ensureAssistantPlaceholder,
      queryClient,
      onSessionUpdated,
    ],
  )

  useEffect(() => {
    eventHandlerRef.current = handleEvent
  }, [handleEvent])

  const approveRequest = useCallback((approved: boolean, requestId: string, command: string) => {
    setApprovalRequest(null)
    if (approvalTimerRef.current) clearTimeout(approvalTimerRef.current)
    setApprovalResult({ requestId, approved, command })
    approvalTimerRef.current = setTimeout(() => setApprovalResult(null), 5000)
  }, [])

  const clearStreaming = useCallback(() => {
    clearAllStreamQueues()
    setStreamingMessages([])
    setToolStatus(null)
    setApprovalRequest(null)
    if (approvalTimerRef.current) clearTimeout(approvalTimerRef.current)
    setApprovalResult(null)
    setPendingAttachments([])
    processingSessionKeyRef.current = null
  }, [clearAllStreamQueues])

  const clearAll = useCallback(() => {
    clearAllStreamQueues()
    setStreamingMessages([])
    setToolStatus(null)
    setApprovalRequest(null)
    if (approvalTimerRef.current) clearTimeout(approvalTimerRef.current)
    setApprovalResult(null)
    setPendingAttachments([])
    setProcessingSessions(new Set())
    processingSessionKeyRef.current = null
  }, [clearAllStreamQueues])

  useEffect(() => {
    return () => {
      clearAllStreamQueues()
    }
  }, [clearAllStreamQueues])

  return {
    streamingMessages,
    streamingRef,
    toolStatus,
    approvalRequest,
    approvalResult,
    pendingAttachments,
    processingSessions,
    setProcessingSessions,
    processingSessionKeyRef,
    ensureAssistantPlaceholder,
    sendMessage,
    handleEvent,
    approveRequest,
    setPendingAttachments,
    clearStreaming,
    clearAll,
  }
}
