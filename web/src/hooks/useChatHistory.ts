import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useMemo, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { ChatMessage, GroupInfo, GroupSnapshot, HistoryToolCall } from '../lib/types'
import { snapshotToGroupInfo } from './messageEventHandlers'

const DEFAULT_LIMIT = 50

export type HistoryMessage = Array<{
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  reasoning_content?: string
  tool_calls?: HistoryToolCall[]
  tool_call_id?: string
  tool_name?: string
  exclude_from_context?: boolean
}>

export const chatHistoryQueryKey = (sessionKey: string) => ['chatHistory', sessionKey] as const

export function buildChatHistoryQueryKey(sessionKey: string, parentSessionKey?: string) {
  if (parentSessionKey) {
    return [...chatHistoryQueryKey(sessionKey), parentSessionKey] as const
  }
  return chatHistoryQueryKey(sessionKey)
}

// Merge logic lives in its own pure module (see messageMerge.ts) so the
// reconciliation rules can be unit-tested without React. Re-exported here
// to preserve the existing import path used by consumers and tests.
import { mergeMessages } from './messageMerge'
export { mergeMessages }

export function useChatHistory(
  api: ApiClient,
  sessionKey: string | null,
  token: string | null,
  streamingMessages: ChatMessage[],
  parentSessionKey?: string,
  hydrateGroups?: (infos: GroupInfo[]) => void,
) {
  const queryClient = useQueryClient()
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const isLoadingMoreRef = useRef(false)

  // Keep a ref to streamingMessages so the refetchInterval callback always
  // reads the latest value without depending on React Query re-evaluating
  // the query options on every render.
  const streamingMessagesRef = useRef(streamingMessages)
  streamingMessagesRef.current = streamingMessages

  const query = useQuery({
    queryKey: buildChatHistoryQueryKey(sessionKey ?? '', parentSessionKey),
    queryFn: async () => {
      if (!sessionKey || !token) return null
      const history = await api.history(sessionKey, parentSessionKey, undefined, DEFAULT_LIMIT)
      if (!history || !history.messages) {
        return {
          sessionKey,
          messages: [],
          rawMessages: [],
          hasMore: false,
          processing: false,
        }
      }

      // Hydrate groups from history response if present
      const groups = history.groups as GroupSnapshot[] | undefined
      if (groups?.length && hydrateGroups) {
        hydrateGroups(groups.map(snapshotToGroupInfo))
      }

      const newMessages = toChatMessages(history.messages, history.session_key)

      // Merge with previously loaded older messages (from loadMore) so polling
      // doesn't wipe out paginated history. Without this, a polling refetch
      // replaces the entire cache with only the latest DEFAULT_LIMIT messages,
      // discarding any older messages the user loaded by scrolling up.
      const queryKey = buildChatHistoryQueryKey(sessionKey, parentSessionKey)
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
        // (i.e., messages not present in the latest batch, excluding ephemeral optimistic messages)
        const olderCachedMessages = cachedData.messages.filter(
          (m) => !m.optimistic && !newMessageIds.has(m.id),
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
    refetchOnWindowFocus: true, // safety net: recovers from WS gaps after tab switch
    // Poll every 4s while the session is processing so that if the WebSocket
    // drops events (reconnect, tab throttle, etc.) the UI still updates via
    // HTTP. Stops polling automatically once processing ends.
    //
    // This polling is the authoritative safety net for message reconciliation.
    // handleHistoryUpdated (streaming.ts) conditionally retains completed
    // streaming messages until the HTTP cache catches up, but if that check
    // misses (e.g., content normalization), the next poll brings the message
    // into baseMessages and mergeMessages' position-based dedup removes the
    // stale streaming copy.
    refetchInterval: (query) => {
      const data = query.state.data
      if (data?.processing) return 4000
      // Also poll if THIS session has streaming messages (WS-driven) to
      // reconcile. Scoped to sessionKey so an unrelated session streaming in
      // the background doesn't keep this query polling. Uses a ref to avoid
      // stale closures during batched state updates.
      if (streamingMessagesRef.current.some((m) => m.streaming && m.sessionKey === sessionKey))
        return 4000
      return false
    },
    retry: false,
  })

  const hasMore = query.data?.hasMore ?? true

  const loadMore = useCallback(async () => {
    if (!sessionKey || !token || isLoadingMoreRef.current) return
    // Read the latest cache data directly to avoid stale closures.
    // query.data in the useCallback deps may lag one render behind.
    const queryKey = buildChatHistoryQueryKey(sessionKey, parentSessionKey)
    const currentData = queryClient.getQueryData<{
      sessionKey: string
      messages: ChatMessage[]
      rawMessages: HistoryMessage
      hasMore: boolean
      processing?: boolean
    }>(queryKey)
    if (!currentData || !currentData.messages.length || currentData.hasMore === false) return

    const oldestRaw = currentData.rawMessages?.[0]
    const oldestMsg = currentData.messages?.[0]
    const beforeId = oldestRaw?.id || oldestMsg?.id
    if (!beforeId) return

    isLoadingMoreRef.current = true
    setIsLoadingMore(true)

    try {
      const history = await api.history(sessionKey, parentSessionKey, beforeId, DEFAULT_LIMIT)
      if (!history || !history.messages || history.messages.length === 0) {
        queryClient.setQueryData(queryKey, (old: typeof currentData | undefined) =>
          old ? { ...old, hasMore: false } : old,
        )
        return
      }

      const olderMessages = toChatMessages(history.messages, history.session_key)

      const existingIds = new Set(currentData.messages.map((m) => m.id))
      const uniqueOlderMessages = olderMessages.filter((m) => !existingIds.has(m.id))

      if (uniqueOlderMessages.length === 0) {
        queryClient.setQueryData(queryKey, (old: typeof currentData | undefined) =>
          old ? { ...old, hasMore: false } : old,
        )
        return
      }

      queryClient.setQueryData(queryKey, {
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
  }, [api, sessionKey, token, parentSessionKey, queryClient])

  const baseMessages = query.data?.messages ?? []

  // Filter streaming messages to only include those for the current session.
  // Without this, messages from the previous session can briefly appear when
  // switching chats because clearStreaming() runs asynchronously (in useEffect)
  // while the URL/sessionKey changes immediately.
  const sessionStreamingMessages = useMemo(
    () => (sessionKey ? streamingMessages.filter((m) => m.sessionKey === sessionKey) : []),
    [streamingMessages, sessionKey],
  )

  const messages = useMemo(
    () => mergeMessages(baseMessages, sessionStreamingMessages),
    [baseMessages, sessionStreamingMessages],
  )

  const invalidateHistory = useCallback(() => {
    if (!sessionKey) return
    queryClient.invalidateQueries({
      queryKey: buildChatHistoryQueryKey(sessionKey, parentSessionKey),
    })
  }, [sessionKey, parentSessionKey, queryClient])

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
  parentSessionKey?: string,
) {
  queryClient.setQueryData(buildChatHistoryQueryKey(sessionKey, parentSessionKey), {
    sessionKey,
    messages: toChatMessages(rawMessages, sessionKey),
    rawMessages,
    processing,
  })
}
