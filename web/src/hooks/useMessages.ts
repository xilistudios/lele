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

export const toChatMessages = (
  history: Array<{
    role: 'user' | 'assistant' | 'tool'
    content: string
    tool_calls?: HistoryToolCall[]
    tool_call_id?: string
  }>,
  sessionKey: string,
): ChatMessage[] =>
  history.flatMap((message, index) => {
    const baseMessage: ChatMessage = {
      id: `${sessionKey}:${index}:${message.role}`,
      role: message.role,
      content: message.content,
      streaming: false,
      createdAt: new Date().toISOString(),
      sessionKey,
    }

    if (message.role === 'assistant' && message.tool_calls?.length) {
      return [
        baseMessage,
        ...message.tool_calls.map((toolCall, toolIndex) => ({
          id: `${sessionKey}:${index}:tool:${toolCall.id || toolIndex}`,
          role: 'tool' as const,
          content: '',
          streaming: false,
          createdAt: new Date().toISOString(),
          sessionKey,
          toolName: toolCall.name ?? toolCall.id,
          toolArgs: formatToolCallArgs(toolCall),
          toolStatus: 'completed' as const,
        })),
      ]
    }

    if (message.role === 'tool') {
      return [
        {
          ...baseMessage,
          role: 'tool',
          toolName: message.tool_call_id ?? 'tool',
          toolResult: message.content,
          toolStatus: 'completed',
        },
      ]
    }

    return [baseMessage]
  })

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

  useEffect(() => {
    messagesRef.current = messages
  }, [messages])

  const loadHistory = useCallback(
    async (sessionKey: string) => {
      if (!token) return

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
        console.log('[HTTP] History loaded, messages:', history.messages.length)
        lastLoadedSessionKeyRef.current = sessionKey
        setMessages(toChatMessages(history.messages, history.session_key))
      } catch (error) {
        console.error('[HTTP] Failed to load history', error)
        if (lastLoadedSessionKeyRef.current === sessionKey) {
          lastLoadedSessionKeyRef.current = null
        }
        throw error
      }
    },
    [api, token, currentSessionKeyRef],
  )

  const ensureAssistantPlaceholder = useCallback(
    (messageId: string, sessionKey: string, chunk = '') => {
      setMessages((current) => {
        if (current.some((message) => message.id === messageId)) {
          return current.map((message) =>
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
          ...current,
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
        case 'message.stream':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) break
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
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) break
          setMessages((current) =>
            current.map((message) =>
              message.id === (data.message_id as string)
                ? {
                    ...message,
                    content: data.content as string,
                    attachments: data.attachments as ChatMessage['attachments'],
                    sessionKey: (eventSessionKey ??
                      currentSessionKeyRef.current ??
                      message.sessionKey) as string,
                    streaming: false,
                  }
                : message,
            ),
          )
          setToolStatus(null)
          setPendingAttachments([])
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
            const existingIds = new Set(messagesRef.current.map((m) => m.id))
            const newMessages = catchupData.messages
              .map((msg, idx) => ({
                id: `${eventSessionKey}:catchup:${idx}:${msg.role}`,
                role: msg.role,
                content: msg.content,
                streaming: false,
                createdAt: new Date().toISOString(),
                sessionKey: eventSessionKey as string,
                toolName: msg.tool_call_id,
                toolArgs: msg.tool_calls?.[0]?.name,
                toolStatus: msg.role === 'tool' ? ('completed' as const) : undefined,
                toolResult: msg.role === 'tool' ? msg.content : undefined,
              }))
              .filter((m) => !existingIds.has(m.id))
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
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) break
          setToolStatus(event.data as ToolStatus)
          const toolId = `tool-${data.tool as string}-${Date.now()}`
          setMessages((current) => {
            const lastAssistantIndex = [...current]
              .reverse()
              .findIndex((message) => message.role === 'assistant')
            if (lastAssistantIndex < 0) {
              return [
                ...current,
                {
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
                },
              ]
            }
            const targetIndex = current.length - lastAssistantIndex
            const newMessages = [...current]
            newMessages.splice(targetIndex, 0, {
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
            })
            return newMessages
          })
          break
        }
        case 'tool.result':
          if (eventSessionKey && eventSessionKey !== currentSessionKeyRef.current) break
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
          setMessages((current) => current.map((message) => ({ ...message, streaming: false })))
          break
        case 'subscribe.ack':
          lastLoadedSessionKeyRef.current = (data.session_key as string) ?? null
          break
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
  }, [])

  const reset = useCallback(() => {
    clearMessages()
  }, [clearMessages])

  return {
    messages,
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
