import { useEffect, useRef } from 'react'
import type { ApiClient } from '../lib/api'
import type { ChatMessage } from '../lib/types'

type HistoryMessage = Array<{
  role: 'user' | 'assistant' | 'tool'
  content: string
  tool_calls?: Array<{
    id: string
    type?: string
    name?: string
    arguments?: Record<string, unknown>
    thought_signature?: string
  }>
  tool_call_id?: string
}>

type Props = {
  api: ApiClient
  sessionToken: string | undefined
  currentSessionKey: string | null
  wsStatus: 'disconnected' | 'connecting' | 'connected'
  onMessages: (messages: ChatMessage[]) => void
  toChatMessages: (history: HistoryMessage, sessionKey: string) => ChatMessage[]
}

export function usePollingFallback({
  api,
  sessionToken,
  currentSessionKey,
  wsStatus,
  onMessages,
  toChatMessages,
}: Props) {
  const pendingPollingRef = useRef(false)
  const pollingIntervalRef = useRef<number | null>(null)
  const currentSessionKeyRef = useRef<string | null>(currentSessionKey)

  useEffect(() => {
    currentSessionKeyRef.current = currentSessionKey
  }, [currentSessionKey])

  useEffect(() => {
    if (!sessionToken || !currentSessionKey) {
      return
    }

    const performPolling = async () => {
      if (pendingPollingRef.current) {
        return
      }

      pendingPollingRef.current = true
      try {
        const sessionKey = currentSessionKey
        const newMessages = await api.history(currentSessionKey)
        if (currentSessionKeyRef.current !== sessionKey) {
          return
        }
        if (newMessages.messages.length > 0) {
          onMessages(toChatMessages(newMessages.messages, newMessages.session_key))
        }
      } catch {
        // Ignore polling errors, will retry.
      } finally {
        pendingPollingRef.current = false
      }
    }

    if (wsStatus === 'disconnected' || wsStatus === 'connecting') {
      if (pollingIntervalRef.current === null) {
        pollingIntervalRef.current = window.setInterval(() => {
          if (wsStatus === 'disconnected' || wsStatus === 'connecting') {
            void performPolling()
          }
        }, 3000)
      }
    } else if (pollingIntervalRef.current !== null) {
      window.clearInterval(pollingIntervalRef.current)
      pollingIntervalRef.current = null
    }

    return () => {
      if (pollingIntervalRef.current !== null) {
        window.clearInterval(pollingIntervalRef.current)
        pollingIntervalRef.current = null
      }
    }
  }, [api, currentSessionKey, onMessages, sessionToken, toChatMessages, wsStatus])
}
