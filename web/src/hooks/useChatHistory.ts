import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ChatMessage, HistoryToolCall } from '../lib/types'
import { toChatMessages } from './useMessages'

const POLLING_INTERVAL = 5000
const DEFAULT_LIMIT = 50

export type HistoryMessage = Array<{
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  reasoning_content?: string
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

  const streamingToolCallIds = new Set<string>()
  const streamingToolSessions = new Set<string>()
  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
      streamingAssistantIds.add(msg.id)
    }
    if (msg.role === 'tool') {
      if (msg.toolCallId) {
        streamingToolCallIds.add(msg.toolCallId)
      }
      if (msg.sessionKey) {
        streamingToolSessions.add(msg.sessionKey)
      }
    }
  }

  const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)
  const baseHasCurrentTurn = baseUserCount > (optimisticUser?.optimisticBaseCount ?? 0)

  const filteredBase: ChatMessage[] = []
  for (const msg of baseMessages) {
    if (msg.role === 'assistant' && streamingAssistantIds.has(msg.id)) {
      continue
    }
    if (msg.role === 'tool' && msg.toolCallId && streamingToolCallIds.has(msg.toolCallId)) {
      continue
    }
    if (msg.role === 'tool' && !msg.toolCallId && msg.sessionKey && streamingToolSessions.has(msg.sessionKey)) {
      const hasStreamingTool = streamingMessages.some(
        (sm) =>
          sm.role === 'tool' && sm.sessionKey === msg.sessionKey,
      )
      if (hasStreamingTool) {
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

  const filteredStreaming = streamingWithoutConfirmedUsers.filter((msg) => {
    if (msg.role === 'assistant' && !msg.streaming && baseHasCurrentTurn) {
      return false
    }
    if (msg.role === 'tool' && msg.toolCallId) {
      const isConfirmedInHistory = filteredBase.some(
        (bm) => bm.role === 'tool' && bm.toolCallId === msg.toolCallId,
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
  parentSessionKey?: string,
  isStreaming?: boolean,
) {
  const queryClient = useQueryClient()
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [hasMore, setHasMore] = useState(true)
  const isLoadingMoreRef = useRef(false)

  const shouldPausePolling = isStreaming ?? false

  const query = useQuery({
    queryKey: parentSessionKey
      ? [...chatHistoryQueryKey(sessionKey ?? ''), parentSessionKey]
      : chatHistoryQueryKey(sessionKey ?? ''),
    queryFn: async () => {
      if (!sessionKey || !token) return null
      const history = await api.history(sessionKey, parentSessionKey, undefined, DEFAULT_LIMIT)
      if (!history || !history.messages) {
        setHasMore(false)
        return {
          sessionKey,
          messages: [],
          rawMessages: [],
          hasMore: false,
          processing: false,
        }
      }
      setHasMore(history.has_more)

      const newMessages = toChatMessages(history.messages, history.session_key)

      // Merge with previously loaded older messages (from loadMore) so polling
      // doesn't wipe out paginated history. Without this, a polling refetch
      // replaces the entire cache with only the latest DEFAULT_LIMIT messages,
      // discarding any older messages the user loaded by scrolling up.
      const queryKey = parentSessionKey
        ? [...chatHistoryQueryKey(sessionKey), parentSessionKey]
        : chatHistoryQueryKey(sessionKey)
      const cachedData = queryClient.getQueryData<{
        sessionKey: string
        messages: ChatMessage[]
        rawMessages: HistoryMessage
        hasMore: boolean
        processing?: boolean
      }>(queryKey)

      if (cachedData && cachedData.messages.length > DEFAULT_LIMIT) {
        const newMessageIds = new Set(newMessages.map((m) => m.id))
        // Keep cached messages that are older than the oldest new message
        // (i.e., messages not present in the latest batch)
        const olderCachedMessages = cachedData.messages.filter(
          (m) => !newMessageIds.has(m.id),
        )

        // Also merge rawMessages preserving order
        const newRawIds = new Set(history.messages.map((m: { id: string }) => m.id))
        const olderRawMessages = (cachedData.rawMessages || []).filter(
          (m: { id: string }) => !newRawIds.has(m.id),
        )

        return {
          sessionKey: history.session_key,
          messages: [...olderCachedMessages, ...newMessages],
          rawMessages: [...olderRawMessages, ...history.messages],
          hasMore: olderCachedMessages.length > 0 || history.has_more,
          processing: history.processing,
        }
      }

      return {
        sessionKey: history.session_key,
        messages: newMessages,
        rawMessages: history.messages,
        hasMore: history.has_more,
        processing: history.processing,
      }
    },
    enabled:
      sessionKey !== null &&
      token !== null &&
      !(sessionKey.startsWith('subagent:') && !parentSessionKey),
    staleTime: 5_000,
    refetchInterval: shouldPausePolling ? false : POLLING_INTERVAL,
    refetchOnWindowFocus: false,
    refetchIntervalInBackground: true,
    retry: false,
  })

  const loadMore = useCallback(async () => {
    if (!sessionKey || !token || isLoadingMoreRef.current) return
    const currentData = query.data
    if (!currentData || !currentData.messages.length || !hasMore) return

    const oldestMessage = currentData.messages[0]
    if (!oldestMessage) return

    isLoadingMoreRef.current = true
    setIsLoadingMore(true)

    try {
      const history = await api.history(
        sessionKey,
        parentSessionKey,
        oldestMessage.id,
        DEFAULT_LIMIT,
      )
      if (!history || !history.messages || history.messages.length === 0) {
        setHasMore(false)
        return
      }

      setHasMore(history.has_more)

      const olderMessages = toChatMessages(history.messages, history.session_key)

      const existingIds = new Set(currentData.messages.map((m) => m.id))
      const uniqueOlderMessages = olderMessages.filter((m) => !existingIds.has(m.id))

      if (uniqueOlderMessages.length === 0) {
        return
      }

      queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
        sessionKey: currentData.sessionKey,
        messages: [...uniqueOlderMessages, ...currentData.messages],
        rawMessages: [...history.messages, ...(currentData.rawMessages || [])],
        hasMore: history.has_more,
        processing: history.processing,
      })
    } catch (error) {
      console.error('[RQ] Error loading more history:', error)
    } finally {
      isLoadingMoreRef.current = false
      setIsLoadingMore(false)
    }
  }, [api, sessionKey, token, parentSessionKey, queryClient, query.data, hasMore])

  const baseMessages = query.data?.messages ?? []
  const messages = useMemo(
    () => mergeMessages(baseMessages, streamingMessages),
    [baseMessages, streamingMessages],
  )

  const invalidateHistory = useCallback(() => {
    if (!sessionKey) return
    setHasMore(true)
    queryClient.invalidateQueries({ queryKey: chatHistoryQueryKey(sessionKey) })
  }, [sessionKey, queryClient])

  return {
    messages,
    rawMessages: query.data?.rawMessages ?? [],
    processing: query.data?.processing ?? false,
    isLoading: query.isLoading,
    isFetching: query.isFetching,
    error: query.error,
    invalidateHistory,
    refetch: query.refetch,
    loadMore,
    hasMore,
    isLoadingMore,
  }
}

export function updateChatHistoryFromRaw(
  queryClient: ReturnType<typeof useQueryClient>,
  sessionKey: string,
  rawMessages: HistoryMessage,
  processing?: boolean,
) {
  queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
    sessionKey,
    messages: toChatMessages(rawMessages, sessionKey),
    rawMessages,
    processing,
  })
}
