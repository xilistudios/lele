import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
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
import {
  type HistoryMessage,
  chatHistoryQueryKey,
  updateChatHistoryFromRaw,
} from './useChatHistory'

export { parseAttachmentsFromContent, parseSubagentSessionKey, toChatMessages }

// Interval between characters when animating streaming text in the UI.
// 12ms ≈ 83 updates/second — chosen to feel fluid without overwhelming the
// browser's render loop. React 18 batches the setState calls, keeping actual
// paints near the display refresh rate.
const STREAM_CHAR_INTERVAL_MS = 12

type StreamClientEvent = {
  event: string
  data: unknown
  transport?: 'sse'
}

export function useMessages(
  api: ApiClient,
  token: string | null,
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
  const eventHandlerRef = useRef<(event: StreamClientEvent) => void>(() => {})
  const activeStreamControllersRef = useRef<Set<AbortController>>(new Set())
  const activeSSEMessageIdsRef = useRef<Set<string>>(new Set())
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
        const filtered = current.filter(
          (m) => !(m.id === '__processing_placeholder__' && m.sessionKey === sessionKey),
        )
        return [
          ...filtered,
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

  // Poll for active streams on session change (recovers stream content after page reload)
  useEffect(() => {
    const sessionKey = _currentSessionKey
    if (!sessionKey || !token) return

    let cancelled = false
    api
      .streamStatus(sessionKey)
      .then(({ streams }) => {
        if (cancelled) return
        for (const stream of streams) {
          if (stream.content) {
            ensureAssistantPlaceholder(
              stream.message_id,
              stream.session_key,
              stream.content,
              stream.done,
            )
          }
          if (stream.reasoning_content) {
            setStreamingMessages((current) =>
              current.map((m) =>
                m.id === stream.message_id && m.role === 'assistant'
                  ? { ...m, reasoningContent: stream.reasoning_content }
                  : m,
              ),
            )
          }
        }
      })
      .catch(() => {
        // Stream status query is non-critical; ignore failures silently
      })

    return () => {
      cancelled = true
    }
  }, [_currentSessionKey, token, api, ensureAssistantPlaceholder])

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

  const abortActiveStreams = useCallback(() => {
    for (const controller of activeStreamControllersRef.current) {
      controller.abort()
    }
    activeStreamControllersRef.current.clear()
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
      if (!token || !sessionKey) return

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

      const payload = {
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      }

      try {
        let response: Awaited<ReturnType<ApiClient['sendMessage']>> | undefined

        if (api.sendMessageStream) {
          const controller = new AbortController()
          let streamMessageId: string | null = null
          activeStreamControllersRef.current.add(controller)
          try {
            response = await api.sendMessageStream(
              payload,
              (event) => {
                if (event.event === 'message.ack') {
                  streamMessageId = event.data.message_id
                }
                eventHandlerRef.current({ ...event, transport: 'sse' })
              },
              {
                signal: controller.signal,
                onDone: () => {
                  activeStreamControllersRef.current.delete(controller)
                  if (streamMessageId) {
                    activeSSEMessageIdsRef.current.delete(streamMessageId)
                  }
                },
              },
            )
          } catch (streamError) {
            activeStreamControllersRef.current.delete(controller)
            // Clean up the SSE message ID so WebSocket events for this
            // message aren't permanently blocked. The SSE stream may have
            // registered the ID via message.ack but failed before onDone
            // could clean it up, leaving WebSocket events silently dropped.
            if (streamMessageId) {
              activeSSEMessageIdsRef.current.delete(streamMessageId)
            }
            if (controller.signal.aborted) {
              throw streamError
            }
            console.warn('[SSE] Streaming send failed, falling back to JSON send:', streamError)
          }
        }

        if (!response) {
          response = await api.sendMessage(payload)
        }

        // Mark session as processing only after API confirms the send
        setProcessingSessions((prev) => new Set(prev).add(sessionKey))
        processingSessionKeyRef.current = sessionKey

        ensureAssistantPlaceholder(response.message_id, response.session_key)
        console.log('[WS] Message sent, messageId:', response.message_id)
        return response
      } catch (error) {
        // Rollback cache on failure
        if (previousCache) {
          queryClient.setQueryData(chatHistoryQueryKey(sessionKey), previousCache)
        } else {
          queryClient.invalidateQueries({ queryKey: chatHistoryQueryKey(sessionKey) })
        }
        // Remove optimistic user message from streamingMessages
        setStreamingMessages((current) =>
          current.filter((m) => !(m.role === 'user' && m.optimistic)),
        )
        throw error
      }
    },
    [api, token, ensureAssistantPlaceholder, getHistoryUserCount, queryClient],
  )

  // biome-ignore lint/correctness/useExhaustiveDependencies: setters are stable, refs are intentionally excluded
  const handleEvent = useCallback(
    (event: StreamClientEvent) => {
      const data = event.data as Record<string, unknown>
      const eventSessionKey = data.session_key as string | undefined
      const messageId = data.message_id as string | undefined
      const isSSEEvent = event.transport === 'sse'

      if (!isSSEEvent && messageId && activeSSEMessageIdsRef.current.has(messageId)) {
        return
      }

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
            if (sessionKey === currentSessionKeyRef.current) {
              ensureAssistantPlaceholder('__processing_placeholder__', sessionKey)
            }
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
            const messageId = data.message_id as string
            const sessionKey = (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string
            const chunk = (data.chunk as string) ?? ''
            const done = (data.done as boolean) ?? false

            if (done && chunk) {
              clearStreamQueue(messageId)
              ensureAssistantPlaceholder(messageId, sessionKey, chunk, true)
              break
            }

            enqueueStreamChunk(messageId, sessionKey, chunk, done)
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
          const ackMessageId = data.message_id as string | undefined
          if (isSSEEvent && ackMessageId) {
            activeSSEMessageIdsRef.current.add(ackMessageId)
          }
          if (ackSessionKey) {
            setProcessingSessions((prev) => new Set(prev).add(ackSessionKey))
            processingSessionKeyRef.current = ackSessionKey
            // Trigger session refresh so backend data replaces optimistic state
            onSessionUpdated?.()
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
            currentSessionKey: currentSessionKeyRef.current,
            completedSessionKey,
          })

          // NOTE: intentionally NOT removing from processingSessions here.
          // message.complete fires for every individual assistant message,
          // but the agent may still be processing (tool calls, follow-ups).
          // processingSessions is cleaned up by the polling-based useEffect
          // in useAppLogic.ts when chatHistory.processing transitions to false.

          // Only update streaming messages if this is the current session
          if (completedSessionKey && completedSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] message.complete for different session, skipping streaming update')
            setPendingAttachments([])
            processingSessionKeyRef.current = null
            break
          }

          setStreamingMessages((current) => {
            const targetSessionKey = eventSessionKey ?? currentSessionKeyRef.current
            return current.flatMap((m) => {
              // Do NOT remove __processing_placeholder__ here.
              // message.complete fires per individual assistant message,
              // but the agent may still be processing (tool calls, follow-ups).
              // Let clearProcessingPlaceholder() handle cleanup when
              // chatHistory.processing transitions to false.
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
          if (isSSEEvent && messageId) {
            activeSSEMessageIdsRef.current.delete(messageId)
          }
          // Refresh sessions so sidebar shows updated message count
          onSessionUpdated?.()
          break
        }
        case 'history.updated': {
          const historySessionKey = (data.session_key as string) ?? currentSessionKeyRef.current
          // NOTE: intentionally NOT removing from processingSessions here.
          // history.updated fires for every individual assistant message,
          // but the agent may still be processing (tool calls, follow-ups).
          // This is cleaned up by the polling-based useEffect in useAppLogic.ts
          // when chatHistory.processing transitions to false.
          queryClient.invalidateQueries({
            queryKey: chatHistoryQueryKey(historySessionKey),
          })
          onSessionUpdated?.()
          // Clean up streaming state for the updated session.
          // Remove completed assistants and tools because mergeMessages() in
          // useChatHistory CANNOT deduplicate them by ID — WS events use UUID
          // while HTTP history uses content-hash-based IDs. The HTTP poll
          // triggered by invalidateQueries will deliver the canonical data.
          // Only keep: active streaming assistants, executing tools, and
          // messages from other sessions.
          if (historySessionKey === currentSessionKeyRef.current) {
            setStreamingMessages((current) =>
              current.filter((m) => {
                if (m.sessionKey !== historySessionKey) return true
                // Do NOT remove __processing_placeholder__ here.
                // Let clearProcessingPlaceholder() in useAppLogic.ts handle
                // cleanup when chatHistory.processing transitions to false.
                // Removing it here leaves a gap if processing stays true
                // (agent still working on tool calls/follow-ups).
                if (m.role === 'user' && m.optimistic) return false
                if (m.role === 'assistant' && !m.streaming) return false
                if (m.role === 'tool' && m.toolStatus !== 'executing') return false
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
                  return message.id === '__processing_placeholder__'
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
            const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
            if (lastAssistantIdx < 0) return [...current, toolMsg]
            const lastAssistant = current[current.length - lastAssistantIdx - 1]
            const insertBefore = lastAssistant.content === '' && lastAssistant.streaming
            const targetIndex = insertBefore
              ? current.length - lastAssistantIdx - 1
              : current.length - lastAssistantIdx
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
          abortActiveStreams()
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
            current
              .filter((m) => m.id !== '__processing_placeholder__')
              .map((m) => ({ ...m, streaming: false })),
          )
          break
        case 'subscribe.ack': {
          const ackSessionKey = (data.session_key as string) ?? ''
          const ackProcessing = data.processing === true
          if (ackProcessing && ackSessionKey) {
            setProcessingSessions((prev) => new Set(prev).add(ackSessionKey))
          }
          if (ackProcessing && ackSessionKey === currentSessionKeyRef.current) {
            processingSessionKeyRef.current = ackSessionKey
            ensureAssistantPlaceholder('__processing_placeholder__', ackSessionKey)
          }
          break
        }
        case 'stream.error': {
          // Backend SSE stream failed after delivering ack.
          // Mark any in-progress assistant message as errored so the UI
          // shows an indicator instead of an eternal typing animation.
          const errorSessionKey = (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string
          setStreamingMessages((current) =>
            current.map((m) => {
              if (m.sessionKey === errorSessionKey && m.role === 'assistant' && m.streaming) {
                return { ...m, streaming: false, error: (data.error as string) || 'Stream error' }
              }
              return m
            }),
          )
          break
        }
        default:
          break
      }
    },
    [
      abortActiveStreams,
      clearAllStreamQueues,
      clearStreamQueue,
      currentSessionKeyRef,
      enqueueStreamChunk,
      ensureAssistantPlaceholder,
      queryClient,
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
    abortActiveStreams()
    clearAllStreamQueues()
    activeSSEMessageIdsRef.current.clear()
    setStreamingMessages([])
    setToolStatus(null)
    setApprovalRequest(null)
    if (approvalTimerRef.current) clearTimeout(approvalTimerRef.current)
    setApprovalResult(null)
    setPendingAttachments([])
    // Don't clear processingSessions - this tracks ALL sessions processing,
    // not just the current one. It's updated by WebSocket events.
    processingSessionKeyRef.current = null
  }, [abortActiveStreams, clearAllStreamQueues])

  const clearAll = useCallback(() => {
    abortActiveStreams()
    clearAllStreamQueues()
    activeSSEMessageIdsRef.current.clear()
    setStreamingMessages([])
    setToolStatus(null)
    setApprovalRequest(null)
    if (approvalTimerRef.current) clearTimeout(approvalTimerRef.current)
    setApprovalResult(null)
    setPendingAttachments([])
    setProcessingSessions(new Set())
    processingSessionKeyRef.current = null
  }, [abortActiveStreams, clearAllStreamQueues])

  // Remove the __processing_placeholder__ for a given session.
  // Used when polling detects the agent has finished processing
  // but the WebSocket events (message.complete) may have been missed.
  const clearProcessingPlaceholder = useCallback((sessionKey: string) => {
    setStreamingMessages((current) =>
      current.filter((m) => !(m.id === '__processing_placeholder__' && m.sessionKey === sessionKey)),
    )
  }, [])

  useEffect(() => {
    return () => {
      abortActiveStreams()
      clearAllStreamQueues()
      activeSSEMessageIdsRef.current.clear()
    }
  }, [abortActiveStreams, clearAllStreamQueues])

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
    clearProcessingPlaceholder,
  }
}
