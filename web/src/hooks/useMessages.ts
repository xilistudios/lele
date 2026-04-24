import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ApprovalRequest, ChatMessage, HistoryToolCall, ToolStatus } from '../lib/types'
import { updateChatHistoryFromRaw } from './useChatHistory'

const formatToolCallArgs = (toolCall: HistoryToolCall) => {
  if (typeof toolCall.arguments === 'undefined') {
    return toolCall.name ?? ''
  }

  return toolCall.name
    ? `${toolCall.name} ${JSON.stringify(toolCall.arguments)}`
    : JSON.stringify(toolCall.arguments)
}

const parseSubagentSessionKey = (value: string | undefined) => {
  if (!value) return undefined

  const trimmed = value.trim()
  if (trimmed === '') return undefined

  const directMatch = trimmed.match(/subagent:([A-Za-z0-9_-]+)/i)
  if (directMatch) {
    return `subagent:${directMatch[1]}`
  }

  const taskMatch = trimmed.match(/\btask(?:\s+id)?\s*:?[ \t]+([A-Za-z0-9_-]+)/i)
  if (taskMatch) {
    return `subagent:${taskMatch[1]}`
  }

  return undefined
}

export const toChatMessages = (
  history: Array<{
    role: 'user' | 'assistant' | 'tool'
    content: string
    tool_calls?: HistoryToolCall[]
    tool_call_id?: string
  }>,
  sessionKey: string,
): ChatMessage[] => {
  const toolCallMap = new Map<string, HistoryToolCall>()
  for (const message of history) {
    if (message.role === 'assistant' && message.tool_calls?.length) {
      for (const tc of message.tool_calls) {
        if (tc.id) {
          toolCallMap.set(tc.id, tc)
        }
      }
    }
  }

  return history.flatMap((message, index) => {
    const baseMessage: ChatMessage = {
      id: `${sessionKey}:${index}:${message.role}`,
      role: message.role,
      content: message.content,
      streaming: false,
      createdAt: new Date().toISOString(),
      sessionKey,
    }

    if (message.role === 'assistant' && message.tool_calls?.length) {
      if (message.content && message.content !== '') {
        return [baseMessage]
      }
      return []
    }

    if (message.role === 'tool') {
      const matchedToolCall = message.tool_call_id
        ? toolCallMap.get(message.tool_call_id)
        : undefined
      const toolName = matchedToolCall?.name ?? message.tool_call_id ?? 'tool'
      const toolArgs = matchedToolCall ? formatToolCallArgs(matchedToolCall) : ''
      const isSpawn = toolName === 'spawn'
      const inferredSubagentSessionKey = isSpawn
        ? parseSubagentSessionKey(message.content)
        : undefined

      return [
        {
          ...baseMessage,
          role: 'tool' as const,
          toolName,
          toolArgs,
          toolResult: message.content,
          toolStatus: 'completed' as const,
          subagentSessionKey: inferredSubagentSessionKey,
        },
      ]
    }

    return [baseMessage]
  })
}

export function useMessages(
  api: ApiClient,
  token: string | null,
  _currentSessionKey: string | null,
  currentSessionKeyRef: React.MutableRefObject<string | null>,
) {
  const [streamingMessages, setStreamingMessages] = useState<ChatMessage[]>([])
  const [toolStatus, setToolStatus] = useState<ToolStatus | null>(null)
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [pendingAttachments, setPendingAttachments] = useState<string[]>([])
  const [processingSessions, setProcessingSessions] = useState<Set<string>>(new Set())
  const streamingRef = useRef(streamingMessages)
  const processingSessionKeyRef = useRef<string | null>(null)

  const queryClient = useQueryClient()

  const getHistoryUserCount = useCallback(
    (sessionKey: string) => {
      const history = queryClient.getQueryData<{ messages?: ChatMessage[] }>([
        'chatHistory',
        sessionKey,
      ])
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
                  // If this is the final chunk (done=true), replace content entirely
                  // to avoid duplicating already-streamed content when the backend
                  // sends the full final content as a final chunk.
                  content: isDone ? (chunk || m.content) : chunk ? `${m.content}${chunk}` : m.content,
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
          {
            id: messageId,
            role: 'assistant',
            content: chunk,
            streaming: !isDone,
            createdAt: new Date().toISOString(),
            sessionKey,
          },
        ]
      })
    },
    [],
  )

  const sendMessage = useCallback(
    async (content: string, attachments: string[], sessionKey: string, agentId: string | null) => {
      if (!token || !sessionKey) return

      const normalizedContent = content.trim()
      if (normalizedContent.length === 0) return

      const tempId = `temp-user-${Date.now()}`
      const userMessage: ChatMessage = {
        id: tempId,
        role: 'user',
        content: normalizedContent,
        streaming: false,
        optimistic: true,
        optimisticBaseCount: getHistoryUserCount(sessionKey),
        createdAt: new Date().toISOString(),
        sessionKey,
        attachments: attachments.map((path) => ({
          path,
          name: path.split('/').pop() ?? path,
          kind: 'file',
        })),
      }

      setStreamingMessages((current) => [...current, userMessage])
      setPendingAttachments([])

      const response = await api.sendMessage({
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })

      ensureAssistantPlaceholder(response.message_id, response.session_key)
      console.log('[WS] Message sent, messageId:', response.message_id)
      return response
    },
    [api, token, ensureAssistantPlaceholder, getHistoryUserCount],
  )

  const handleEvent = useCallback(
    (event: { event: string; data: unknown }) => {
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
            processingSessionKeyRef.current = welcomeData.session_key
            setProcessingSessions((prev) => new Set(prev).add(welcomeData.session_key!))
            if (welcomeData.session_key === currentSessionKeyRef.current) {
              ensureAssistantPlaceholder('__processing_placeholder__', welcomeData.session_key)
            }
          }
          break
        }
        case 'message.stream':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped message.stream: session mismatch')
            break
          }
          ensureAssistantPlaceholder(
            data.message_id as string,
            (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string,
            (data.chunk as string) ?? '',
            (data.done as boolean) ?? false,
          )
          break
        case 'message.ack':
          ensureAssistantPlaceholder(data.message_id as string, (data.session_key as string) ?? '')
          break
        case 'message.complete':
          const completedSessionKey = eventSessionKey ?? currentSessionKeyRef.current
          
          // Always update processingSessions regardless of which session
          setProcessingSessions((prev) => {
            if (completedSessionKey && prev.has(completedSessionKey)) {
              const next = new Set(prev)
              next.delete(completedSessionKey)
              return next
            }
            return prev
          })

          // Only update streaming messages if this is the current session
          if (completedSessionKey && completedSessionKey !== currentSessionKeyRef.current) {
            setPendingAttachments([])
            processingSessionKeyRef.current = null
            break
          }

          setStreamingMessages((current) => {
            const targetSessionKey = eventSessionKey ?? currentSessionKeyRef.current
            return current.flatMap((m) => {
              // Remove processing placeholder
              if (m.id === '__processing_placeholder__' && m.sessionKey === targetSessionKey) {
                return []
              }
              // Mark the assistant message as done streaming
              if (m.role === 'assistant' && m.id === (data.message_id as string)) {
                const content = (data.content as string) || m.content
                return [{ ...m, content, streaming: false }]
              }
              // Remove empty user messages (leftover from cancelled sends)
              if (
                m.role === 'user' &&
                m.sessionKey === targetSessionKey &&
                m.content.trim() === ''
              ) {
                return []
              }
              // KEEP tool messages in streaming state — they are transient UI state
              // that represents tool executions. We don't remove them here because:
              // 1. Removing them creates a visual gap (~500ms) until history refetches
              // 2. History will correctly merge/override these when it arrives
              // 3. The merge logic in useChatHistory handles deduplication
              if (m.role === 'tool' && m.sessionKey === targetSessionKey) {
                return [m]
              }
              return [m]
            })
          })
          setToolStatus(null)
          setPendingAttachments([])
          processingSessionKeyRef.current = null
          // Server emits history.updated after persisting, so we refetch then
          break
        case 'history.updated':
          const historySessionKey = (data.session_key as string) ?? currentSessionKeyRef.current
          if (historySessionKey) {
            queryClient.invalidateQueries({
              queryKey: ['chatHistory', historySessionKey],
            })
          }
          break
        case 'messages.catchup': {
          const catchupData = data as {
            session_key?: string
            is_initial: boolean
            messages: Array<{
              role: 'user' | 'assistant' | 'tool'
              content: string
              tool_call_id?: string
              tool_calls?: HistoryToolCall[]
            }>
          }
          const targetSessionKey = catchupData.session_key || currentSessionKeyRef.current || ''
          if (catchupData.is_initial && targetSessionKey === currentSessionKeyRef.current) {
            updateChatHistoryFromRaw(queryClient, targetSessionKey, catchupData.messages)
            setStreamingMessages((current) =>
              current.filter((message) => {
                if (message.sessionKey !== targetSessionKey) {
                  return true
                }
                // Keep assistant messages that are still streaming or have content
                // Only remove if it's a placeholder or the catchup has this message persisted
                if (message.role === 'assistant') {
                  // Keep if still streaming (in progress)
                  if (message.streaming) return true
                  // Keep if has meaningful content (don't remove just because streaming finished)
                  if (message.content && message.content.length > 0) return true
                  // Remove only placeholders or empty messages
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
          const toolId = `tool-${data.tool as string}-${Date.now()}`
          const newTool: ChatMessage = {
            id: toolId,
            role: 'tool',
            content: '',
            streaming: false,
            createdAt: new Date().toISOString(),
            sessionKey: (eventSessionKey ?? currentSessionKeyRef.current ?? undefined) as
              | string
              | undefined,
            toolName: data.tool as string,
            toolArgs: data.action as string,
            toolStatus: 'executing',
            subagentSessionKey: data.subagent_session_key as string | undefined,
          }
          setStreamingMessages((current) => {
            const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
            if (lastAssistantIdx < 0) return [...current, newTool]
            const lastAssistant = current[current.length - lastAssistantIdx - 1]
            const insertBefore = lastAssistant.content === '' && lastAssistant.streaming
            const targetIndex = insertBefore
              ? current.length - lastAssistantIdx - 1
              : current.length - lastAssistantIdx
            const arr = [...current]
            arr.splice(targetIndex, 0, newTool)
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
            const lastToolIdx = [...current]
              .reverse()
              .findIndex(
                (m) =>
                  m.role === 'tool' &&
                  m.toolStatus === 'executing' &&
                  m.toolName === (data.tool as string),
              )
            if (lastToolIdx < 0) return current
            const targetIndex = current.length - lastToolIdx - 1
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
            const lastSpawnIdx = [...current]
              .reverse()
              .findIndex((m) => m.role === 'tool' && m.toolName === 'spawn')
            if (lastSpawnIdx < 0) return current
            const targetIndex = current.length - lastSpawnIdx - 1
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
        case 'cancel.ack':
          setToolStatus(null)
          processingSessionKeyRef.current = null
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
        default:
          break
      }
    },
    [currentSessionKeyRef, ensureAssistantPlaceholder, queryClient],
  )

  const approveRequest = useCallback((approved: boolean, requestId: string) => {
    setApprovalRequest(null)
    return { request_id: requestId, approved }
  }, [])

  const clearStreaming = useCallback(() => {
    setStreamingMessages([])
    setToolStatus(null)
    setApprovalRequest(null)
    setPendingAttachments([])
    setProcessingSessions(new Set())
    processingSessionKeyRef.current = null
  }, [])

  return {
    streamingMessages,
    streamingRef,
    toolStatus,
    approvalRequest,
    pendingAttachments,
    processingSessions,
    processingSessionKeyRef,
    ensureAssistantPlaceholder,
    sendMessage,
    handleEvent,
    approveRequest,
    setPendingAttachments,
    clearStreaming,
  }
}
