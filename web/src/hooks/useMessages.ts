import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ApprovalRequest, ChatMessage, HistoryToolCall, ToolStatus } from '../lib/types'

const generateUUID = () => {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

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
  // Build a map of tool_call_id -> HistoryToolCall from all assistant messages
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
      // Don't create separate tool messages from tool_calls;
      // they will be created when the corresponding tool response messages are processed.
      // Only return the assistant text message if it has content.
      if (message.content && message.content !== '') {
        return [baseMessage]
      }
      return []
    }

    if (message.role === 'tool') {
      // Look up the corresponding tool_call to get the real tool name and args
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
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [toolStatus, setToolStatus] = useState<ToolStatus | null>(null)
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [pendingAttachments, setPendingAttachments] = useState<string[]>([])
  const messagesRef = useRef(messages)
  const lastLoadedSessionKeyRef = useRef<string | null>(null)
  const processingSessionKeyRef = useRef<string | null>(null)
  const historySeqRef = useRef(0)

  useEffect(() => {
    messagesRef.current = messages
  }, [messages])

  const loadHistory = useCallback(
    async (sessionKey: string) => {
      if (!token) return

      // Increment sequence number to track this request
      const seq = ++historySeqRef.current
      
      console.log('[HTTP] Loading history for session', sessionKey)
      try {
        const history = await api.history(sessionKey)
        if (currentSessionKeyRef.current !== sessionKey) {
          console.log('[HTTP] Session changed during load, skipping', {
            current: currentSessionKeyRef.current,
            expected: sessionKey,
          })
          return
        }
        // Check if this is still the latest history request
        if (historySeqRef.current !== seq) {
          console.log('[HTTP] Stale history response, ignoring', {
            seq,
            currentSeq: historySeqRef.current,
          })
          return
        }
        console.log('[HTTP] History loaded, messages:', history.messages.length)
        lastLoadedSessionKeyRef.current = sessionKey
        const chatMsgs = toChatMessages(history.messages, history.session_key)
        if (processingSessionKeyRef.current === sessionKey) {
          chatMsgs.push({
            id: '__processing_placeholder__',
            role: 'assistant',
            content: '',
            streaming: true,
            createdAt: new Date().toISOString(),
            sessionKey,
          })
        }
        setMessages(chatMsgs)
      } catch (error) {
        console.error('[HTTP] Failed to load history', error)
        if (lastLoadedSessionKeyRef.current === sessionKey) {
          lastLoadedSessionKeyRef.current = null
        }
        // Clear processing ref on error
        if (processingSessionKeyRef.current === sessionKey) {
          processingSessionKeyRef.current = null
        }
        throw error
      }
    },
    [api, token, currentSessionKeyRef],
  )

  const ensureAssistantPlaceholder = useCallback(
    (messageId: string, sessionKey: string, chunk = '') => {
      setMessages((current) => {
        let filtered = current
        const existingProcessing = current.findIndex(
          (m) => m.id === '__processing_placeholder__' && m.sessionKey === sessionKey,
        )
        if (existingProcessing >= 0 && messageId !== '__processing_placeholder__') {
          filtered = [...current.slice(0, existingProcessing), ...current.slice(existingProcessing + 1)]
        }

        if (filtered.some((message) => message.id === messageId)) {
          return filtered.map((message) =>
            message.id === messageId
              ? {
                  ...message,
                  content: chunk ? `${message.content}${chunk}` : message.content,
                  streaming: true,
                  sessionKey,
                }
              : message,
          )
        }

        return [
          ...filtered,
          {
            id: messageId,
            role: 'assistant',
            content: chunk,
            streaming: true,
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

      const userMessage: ChatMessage = {
        id: generateUUID(),
        role: 'user',
        content: normalizedContent,
        streaming: false,
        createdAt: new Date().toISOString(),
        sessionKey,
        attachments: attachments.map((path) => ({
          path,
          name: path.split('/').pop() ?? path,
          kind: 'file',
        })),
      }

      setMessages((current) => [...current, userMessage])
      setPendingAttachments([])

      const response = await api.sendMessage({
        content: normalizedContent,
        session_key: sessionKey,
        agent_id: agentId ?? undefined,
        attachments: attachments.length > 0 ? attachments : undefined,
      })

      ensureAssistantPlaceholder(response.message_id, response.session_key)
      return response
    },
    [api, token, ensureAssistantPlaceholder],
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
          }
          if (welcomeData.processing && welcomeData.session_key) {
            processingSessionKeyRef.current = welcomeData.session_key
            if (welcomeData.session_key === currentSessionKeyRef.current) {
              ensureAssistantPlaceholder('__processing_placeholder__', welcomeData.session_key)
            }
          }
          break
        }
        case 'message.stream':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped message.stream: session mismatch', {
              event: eventSessionKey,
              current: currentSessionKeyRef.current,
            })
            break
          }
          ensureAssistantPlaceholder(
            data.message_id as string,
            (eventSessionKey ?? currentSessionKeyRef.current ?? '') as string,
            (data.chunk as string) ?? '',
          )
          break
        case 'message.ack':
          ensureAssistantPlaceholder(data.message_id as string, (data.session_key as string) ?? '')
          break
        case 'message.complete':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped message.complete: session mismatch', {
              event: eventSessionKey,
              current: currentSessionKeyRef.current,
            })
            break
          }
          setMessages((current) => {
            const filtered = current.filter(
              (m) => !(m.id === '__processing_placeholder__' && m.sessionKey === (eventSessionKey ?? currentSessionKeyRef.current)),
            )
            const targetId = data.message_id as string
            const targetIndex = filtered.findIndex((message) => message.id === targetId)
            if (targetIndex < 0) return filtered

            const updatedMessage: ChatMessage = {
              ...filtered[targetIndex],
              content: data.content as string,
              attachments: data.attachments as ChatMessage['attachments'],
              sessionKey: (eventSessionKey ??
                currentSessionKeyRef.current ??
                filtered[targetIndex].sessionKey) as string,
              streaming: false,
            }

            const hasToolsAfter = filtered.slice(targetIndex + 1).some((m) => m.role === 'tool')
            if (hasToolsAfter) {
              const messagesWithoutTarget = [
                ...filtered.slice(0, targetIndex),
                ...filtered.slice(targetIndex + 1),
              ]
              return [...messagesWithoutTarget, updatedMessage]
            }

            return filtered.map((message, index) =>
              index === targetIndex ? updatedMessage : message,
            )
          })
          setToolStatus(null)
          setPendingAttachments([])
          processingSessionKeyRef.current = null
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
          if (catchupData.is_initial) {
            if (targetSessionKey === currentSessionKeyRef.current) {
              setMessages(toChatMessages(catchupData.messages, targetSessionKey))
            }
          } else {
            if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) break
            // Usar la misma transformación que history para consistencia
            const existingIds = new Set(messagesRef.current.map((m) => m.id))
            const newMessages = toChatMessages(
              catchupData.messages,
              eventSessionKey as string,
            ).filter((m) => !existingIds.has(m.id))
            if (newMessages.length > 0) {
              setMessages((current) => [...current, ...newMessages])
            }
          }
          break
        }
        case 'attachments':
          setMessages((current) => {
            const lastAssistantIndex = [...current]
              .reverse()
              .findIndex((message) => message.role === 'assistant')
            if (lastAssistantIndex < 0) return current
            const targetIndex = current.length - lastAssistantIndex - 1
            return current.map((message, index) =>
              index === targetIndex
                ? {
                    ...message,
                    attachments: event.data as ChatMessage['attachments'],
                    streaming: false,
                  }
                : message,
            )
          })
          break
        case 'tool.executing': {
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped tool.executing: session mismatch', {
              event: eventSessionKey,
              current: currentSessionKeyRef.current,
            })
            break
          }
          setToolStatus(event.data as ToolStatus)
          const toolId = `tool-${data.tool as string}-${Date.now()}`
          const newToolMessage: ChatMessage = {
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
          setMessages((current) => {
            const lastAssistantIndex = [...current]
              .reverse()
              .findIndex((message) => message.role === 'assistant')
            if (lastAssistantIndex < 0) {
              return [...current, newToolMessage]
            }
            const lastAssistant = current[current.length - lastAssistantIndex - 1]
            // Si el último assistant está vacío (streaming sin contenido aún),
            // insertar el tool ANTES de él para mantener orden cronológico
            const insertBeforeLastAssistant =
              lastAssistant.content === '' && lastAssistant.streaming
            const targetIndex = insertBeforeLastAssistant
              ? current.length - lastAssistantIndex - 1
              : current.length - lastAssistantIndex
            const newMessages = [...current]
            newMessages.splice(targetIndex, 0, newToolMessage)
            return newMessages
          })
          break
        }
        case 'tool.result':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped tool.result: session mismatch', {
              event: eventSessionKey,
              current: currentSessionKeyRef.current,
            })
            break
          }
          setToolStatus(null)
          setMessages((current) => {
            const lastToolIndex = [...current]
              .reverse()
              .findIndex(
                (message) =>
                  message.role === 'tool' &&
                  message.toolStatus === 'executing' &&
                  message.toolName === (data.tool as string),
              )
            if (lastToolIndex < 0) return current
            const targetIndex = current.length - lastToolIndex - 1
            const isError =
              data.result &&
              typeof data.result === 'string' &&
              (data.result.toLowerCase().includes('error') ||
                data.result.toLowerCase().includes('failed'))
            return current.map((message, index) =>
              index === targetIndex
                ? {
                    ...message,
                    toolResult: data.result as string,
                    toolStatus: isError ? 'error' : 'completed',
                    subagentSessionKey:
                      (data.subagent_session_key as string) ||
                      message.subagentSessionKey ||
                      ((data.tool as string) === 'spawn'
                        ? parseSubagentSessionKey(data.result as string | undefined)
                        : undefined),
                  }
                : message,
            )
          })
          break
        case 'subagent.result':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) {
            console.warn('[WS] Dropped subagent.result: session mismatch', {
              event: eventSessionKey,
              current: currentSessionKeyRef.current,
            })
            break
          }
          setMessages((current) => {
            const lastSpawnIndex = [...current]
              .reverse()
              .findIndex(
                (message) =>
                  message.role === 'tool' &&
                  message.toolName === 'spawn',
              )
            if (lastSpawnIndex < 0) return current
            const targetIndex = current.length - lastSpawnIndex - 1
            return current.map((message, index) =>
              index === targetIndex
                ? {
                    ...message,
                    subagentSessionKey:
                      (data.subagent_session_key as string) ||
                      message.subagentSessionKey,
                    toolResult: message.toolResult || (data.result as string),
                  }
                : message,
            )
          })
          break
        case 'approval.request':
          setApprovalRequest(event.data as ApprovalRequest)
          break
        case 'cancel.ack':
          setToolStatus(null)
          processingSessionKeyRef.current = null
          setMessages((current) =>
            current
              .filter((m) => m.id !== '__processing_placeholder__')
              .map((message) => ({ ...message, streaming: false })),
          )
          break
        case 'subscribe.ack': {
          lastLoadedSessionKeyRef.current = (data.session_key as string) ?? null
          const ackSessionKey = (data.session_key as string) ?? ''
          const ackProcessing = data.processing === true
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
    [currentSessionKeyRef, ensureAssistantPlaceholder],
  )

  const approveRequest = useCallback((approved: boolean, requestId: string) => {
    setApprovalRequest(null)
    return { request_id: requestId, approved }
  }, [])

  const clearMessages = useCallback(() => {
    setMessages([])
    setToolStatus(null)
    setApprovalRequest(null)
    setPendingAttachments([])
    lastLoadedSessionKeyRef.current = null
    processingSessionKeyRef.current = null
    historySeqRef.current = 0
  }, [])

  const reset = useCallback(() => {
    clearMessages()
  }, [clearMessages])

  return {
    messages,
    setMessages,
    messagesRef,
    toolStatus,
    approvalRequest,
    pendingAttachments,
    lastLoadedSessionKeyRef,
    loadHistory,
    ensureAssistantPlaceholder,
    sendMessage,
    handleEvent,
    approveRequest,
    setPendingAttachments,
    clearMessages,
    reset,
  }
}
