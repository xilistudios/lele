import { useMemo } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
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

  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
      streamingAssistantIds.add(msg.id)
    }
  }

  const filteredBase: ChatMessage[] = []
  for (const msg of baseMessages) {
    if (msg.role === 'assistant' && streamingAssistantIds.has(msg.id)) {
      continue
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

  return [...filteredBase, ...streamingWithoutConfirmedUsers]
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
