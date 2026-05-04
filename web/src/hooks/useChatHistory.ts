import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import type { ChatMessage, HistoryToolCall } from '../lib/types'
import { toChatMessages } from './useMessages'

const POLLING_INTERVAL = 5000
const DEFAULT_LIMIT = 50

type HistoryMessage = Array<{
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
    if (msg.role === 'tool' && msg.sessionKey && streamingToolSessions.has(msg.sessionKey)) {
      const hasExecutingTool = streamingMessages.some(
        (sm) =>
          sm.role === 'tool' && sm.sessionKey === msg.sessionKey && sm.toolStatus === 'executing',
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

  // Helper to normalize tool args for comparison.
  // Handles JSON key ordering differences by parsing and re-serializing with sorted keys.
  const normalizeToolArgs = (args: string | undefined): string => {
    if (!args) return ''
    // Try to extract JSON from the tool args string (format: "toolName {...}")
    const jsonMatch = args.match(/\{[\s\S]*\}/)
    if (!jsonMatch) return args // Not JSON, return as-is

    try {
      const parsed = JSON.parse(jsonMatch[0])
      // Re-serialize with sorted keys for consistent comparison
      return JSON.stringify(parsed, Object.keys(parsed).sort())
    } catch {
      return args // Parse failed, return original
    }
  }

  // Remove streaming messages that are now confirmed in history
  const filteredStreaming = streamingWithoutConfirmedUsers.filter((msg) => {
    // Remove completed non-streaming assistant messages when history has the current turn
    if (msg.role === 'assistant' && !msg.streaming && baseHasCurrentTurn) {
      return false
    }
    // Remove completed tool messages from streaming if they now exist in history
    // This prevents duplicate tool entries after history refreshes
    if (msg.role === 'tool' && msg.toolStatus === 'completed' && msg.sessionKey && msg.toolName) {
      const isConfirmedInHistory = filteredBase.some(
        (bm) =>
          bm.role === 'tool' &&
          bm.sessionKey === msg.sessionKey &&
          bm.toolName === msg.toolName &&
          normalizeToolArgs(bm.toolArgs) === normalizeToolArgs(msg.toolArgs) &&
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
  parentSessionKey?: string,
) {
  const queryClient = useQueryClient()
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [hasMore, setHasMore] = useState(true)
  const isLoadingMoreRef = useRef(false)

  // Query for fetching recent messages (polling and initial load)
  const query = useQuery({
    queryKey: parentSessionKey
      ? [...chatHistoryQueryKey(sessionKey ?? ''), parentSessionKey]
      : chatHistoryQueryKey(sessionKey ?? ''),
    queryFn: async () => {
      if (!sessionKey || !token) return null
      console.log('[RQ] Fetching recent history for session', sessionKey)
      const history = await api.history(sessionKey, parentSessionKey, undefined, DEFAULT_LIMIT)
      if (!history || !history.messages) {
        console.log('[RQ] History fetched, empty response')
        setHasMore(false)
        return {
          sessionKey,
          messages: [],
          rawMessages: [],
          hasMore: false,
        }
      }
      console.log(
        '[RQ] History fetched, messages:',
        history.messages.length,
        'has_more:',
        history.has_more,
      )
      setHasMore(history.has_more)

      // Convert to ChatMessage array
      const messages = toChatMessages(history.messages, history.session_key)

      return {
        sessionKey: history.session_key,
        messages,
        rawMessages: history.messages,
        hasMore: history.has_more,
      }
    },
    enabled:
      sessionKey !== null &&
      token !== null &&
      !(sessionKey.startsWith('subagent:') && !parentSessionKey),
    staleTime: 5_000,
    refetchInterval: POLLING_INTERVAL,
    refetchOnWindowFocus: false,
    refetchIntervalInBackground: true,
    retry: false,
  })

  // Function to load older messages (called when scrolling up)
  const loadMore = useCallback(async () => {
    if (!sessionKey || !token || isLoadingMoreRef.current) return
    const currentData = query.data
    if (!currentData || !currentData.messages.length || !hasMore) return

    // Get the ID of the oldest message to use as cursor
    const oldestMessage = currentData.messages[0]
    if (!oldestMessage) return

    console.log('[RQ] Loading older history before message:', oldestMessage.id)
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

      // Remove duplicates by checking IDs
      const existingIds = new Set(currentData.messages.map((m) => m.id))
      const uniqueOlderMessages = olderMessages.filter((m) => !existingIds.has(m.id))

      if (uniqueOlderMessages.length === 0) {
        console.log('[RQ] No new older messages to add')
        return
      }

      console.log('[RQ] Adding', uniqueOlderMessages.length, 'older messages')

      // Prepend older messages to the beginning
      queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
        sessionKey: currentData.sessionKey,
        messages: [...uniqueOlderMessages, ...currentData.messages],
        rawMessages: [...history.messages, ...(currentData.rawMessages || [])],
        hasMore: history.has_more,
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
    processing: false, // This would need to be updated if processing status is needed
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
) {
  queryClient.setQueryData(chatHistoryQueryKey(sessionKey), {
    sessionKey,
    messages: toChatMessages(rawMessages, sessionKey),
    rawMessages,
  })
}
