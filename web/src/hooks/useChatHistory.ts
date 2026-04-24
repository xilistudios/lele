import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo } from 'react'
import type { ApiClient } from '../lib/api'
import type { ChatMessage, HistoryToolCall } from '../lib/types'
import { toChatMessages } from './useMessages'

const POLLING_INTERVAL = 5000

type HistoryMessage = Array<{
  role: 'user' | 'assistant' | 'tool'
  content: string
  tool_calls?: HistoryToolCall[]
  tool_call_id?: string
}>

export const chatHistoryQueryKey = (sessionKey: string) => ['chatHistory', sessionKey] as const

function mergeMessages(
  baseMessages: ChatMessage[],
  streamingMessages: ChatMessage[],
): ChatMessage[] {
  const streamingAssistantIds = new Set<string>()
  const baseUserCount = baseMessages.filter((message) => message.role === 'user').length

  const streamingToolSessions = new Set<string>()
  const streamingToolIds = new Set<string>()
  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
      streamingAssistantIds.add(msg.id)
    }
    if (msg.role === 'tool') {
      streamingToolIds.add(msg.id)
      if (msg.sessionKey) {
        streamingToolSessions.add(msg.sessionKey)
      }
    }
  }

  const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)
  const baseHasCurrentTurn = baseUserCount > (optimisticUser?.optimisticBaseCount ?? 0)

  // Filter base messages: keep tool messages from history unless there's
  // an actively executing tool in streaming (which is more up-to-date).
  const filteredBase: ChatMessage[] = []
  for (const msg of baseMessages) {
    if (msg.role === 'assistant' && streamingAssistantIds.has(msg.id)) {
      continue
    }
    // Remove base tool messages only when there's an executing tool in streaming
    // for the same session (streaming takes precedence during execution).
    // Completed tools in streaming are fine — they'll be removed below.
    if (
      msg.role === 'tool' &&
      msg.sessionKey &&
      streamingToolSessions.has(msg.sessionKey)
    ) {
      const hasExecutingTool = streamingMessages.some(
        (sm) =>
          sm.role === 'tool' &&
          sm.sessionKey === msg.sessionKey &&
          sm.toolStatus === 'executing',
      )
      if (hasExecutingTool) {
        continue
      }
    }
    filteredBase.push(msg)
  }

  const streamingWithoutConfirmedUsers = streamingMessages.filter((msg) => {
    if (msg.role !== 'user') return true
    if (!msg.optimistic) {
      return true
    }
    return baseUserCount <= (msg.optimisticBaseCount ?? 0)
  })

  // Remove streaming messages that are now confirmed in history
  const filteredStreaming = streamingWithoutConfirmedUsers.filter((msg) => {
    // Remove completed non-streaming assistant messages when history has the current turn
    if (msg.role === 'assistant' && !msg.streaming && baseHasCurrentTurn) {
      return false
    }
    // Remove completed tool messages from streaming if they now exist in history
    // This prevents duplicate tool entries after history refreshes
    if (
      msg.role === 'tool' &&
      msg.toolStatus === 'completed' &&
      msg.sessionKey &&
      msg.toolName
    ) {
      const isConfirmedInHistory = filteredBase.some(
        (bm) =>
          bm.role === 'tool' &&
          bm.sessionKey === msg.sessionKey &&
          bm.toolName === msg.toolName &&
          bm.toolArgs === msg.toolArgs &&
          bm.toolResult === msg.toolResult,
      )
      if (isConfirmedInHistory) {
        return false
      }
    }
    return true
  })

  return [...filteredBase, ...filteredStreaming]
}

export function useChatHistory(
  api: ApiClient,
  sessionKey: string | null,
  token: string | null,
  streamingMessages: ChatMessage[],
) {
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: chatHistoryQueryKey(sessionKey ?? ''),
    queryFn: async () => {
      if (!sessionKey || !token) return null
      console.log('[RQ] Fetching history for session', sessionKey)
      const history = await api.history(sessionKey)
      if (!history) {
        console.log('[RQ] History fetched, empty response')
        return {
          sessionKey,
          messages: [],
          rawMessages: [],
        }
      }
      console.log('[RQ] History fetched, messages:', history.messages.length)
      return {
        sessionKey: history.session_key,
        messages: toChatMessages(history.messages, history.session_key),
        rawMessages: history.messages,
        processing: history.processing,
      }
    },
    enabled: sessionKey !== null && token !== null,
    staleTime: 5_000,
    refetchInterval: POLLING_INTERVAL,
    refetchOnWindowFocus: false,
    refetchIntervalInBackground: true,
    retry: false,
  })

  const baseMessages = query.data?.messages ?? []
  const messages = useMemo(
    () => mergeMessages(baseMessages, streamingMessages),
    [baseMessages, streamingMessages],
  )

  const invalidateHistory = () => {
    if (!sessionKey) return
    queryClient.invalidateQueries({ queryKey: chatHistoryQueryKey(sessionKey) })
  }

  return {
    messages,
    rawMessages: query.data?.rawMessages ?? [],
    processing: query.data?.processing ?? false,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    error: query.error,
    invalidateHistory,
    refetch: query.refetch,
  }
}

export function updateChatHistoryFromRaw(
  queryClient: ReturnType<typeof useQueryClient>,
  sessionKey: string,
  rawMessages: HistoryMessage,
) {
  queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
    sessionKey,
    messages: toChatMessages(rawMessages, sessionKey),
    rawMessages,
  })
}
