import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type {
  Attachment,
  ChatMessage,
  GroupInfo,
  GroupSnapshot,
  HistoryToolCall,
} from '../lib/types'
import { sessionKeysLooselyMatch } from './event-handlers/helpers'
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
  attachments?: Attachment[]
}>

export const chatHistoryQueryKey = (sessionKey: string) => ['chatHistory', sessionKey] as const

export function buildChatHistoryQueryKey(sessionKey: string, parentSessionKey?: string) {
  if (parentSessionKey) {
    return [...chatHistoryQueryKey(sessionKey), parentSessionKey] as const
  }
  return chatHistoryQueryKey(sessionKey)
}

/** Merge two GroupInfo lists by group id: entries from `incoming` overwrite
 *  same-id entries of `existing`; existing-only ids are kept. Mirrors the
 *  per-id overwrite semantics of the useGroupState 'hydrate' reducer so the
 *  cached data and the group Map stay consistent. */
function mergeGroupsById(existing: GroupInfo[], incoming: GroupInfo[]): GroupInfo[] {
  if (!incoming.length) return existing
  const byId = new Map(existing.map((g) => [g.groupID, g] as const))
  for (const g of incoming) byId.set(g.groupID, g)
  return Array.from(byId.values())
}

// Merge logic lives in its own pure module (see messageMerge.ts) so the
// reconciliation rules can be unit-tested without React. Re-exported here
// to preserve the existing import path used by consumers and tests.
import { mergeMessages } from './messageMerge'
export { mergeMessages }

// Shape of the cached chat-history query data. `groups` holds the group
// snapshots (already converted to the internal GroupInfo shape) that back the
// group cards. Keeping them INSIDE the cached data — instead of hydrating the
// group Map from a side-effect inside queryFn — is what makes rehydration work
// on cache hits: queryFn does not run when fresh cache data is served (see
// staleTime in lib/queryClient.ts), so a session switch that clears the group
// Map must be repaired by the useEffect below reading `groups` off the cache.
export type ChatHistoryData = {
  sessionKey: string
  messages: ChatMessage[]
  rawMessages: HistoryMessage
  hasMore: boolean
  processing?: boolean
  groups?: GroupInfo[]
}

/** Convert the optional groups payload of a history response to GroupInfos. */
function historyGroupsToInfos(history: { groups?: GroupSnapshot[] }): GroupInfo[] {
  return (history.groups ?? []).map(snapshotToGroupInfo)
}

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
          groups: history ? historyGroupsToInfos(history) : [],
        }
      }

      // Groups ride along in the cached data (see ChatHistoryData). The
      // hydrate side-effect that used to live here is what made cache hits
      // lose group cards: queryFn never runs when fresh cache data is served,
      // so hydration now happens in a useEffect on [query.data, sessionKey].
      const groups = historyGroupsToInfos(history)

      const newMessages = toChatMessages(history.messages, history.session_key)

      // Merge with previously loaded older messages (from loadMore) so polling
      // doesn't wipe out paginated history. Without this, a polling refetch
      // replaces the entire cache with only the latest DEFAULT_LIMIT messages,
      // discarding any older messages the user loaded by scrolling up.
      const queryKey = buildChatHistoryQueryKey(sessionKey, parentSessionKey)
      const cachedData = queryClient.getQueryData<ChatHistoryData>(queryKey)

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
          groups: groups.length > 0 ? groups : (cachedData.groups ?? []),
        }
      }

      return {
        sessionKey: history.session_key,
        messages: newMessages,
        rawMessages: history.messages,
        hasMore: history.has_more,
        processing: history.processing,
        groups,
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
      if (
        streamingMessagesRef.current.some(
          (m) => m.streaming && sessionKeysLooselyMatch(m.sessionKey, sessionKey),
        )
      )
        return 4000
      return false
    },
    retry: false,
  })

  // Rehydrate group cards from the query data instead of from a side-effect
  // inside queryFn. queryFn does not run on cache hits (staleTime 10s), so the
  // old side-effect left the group Map empty after a session switch
  // (clearStreaming() empties it) when the history was still fresh in cache —
  // cards vanished despite cached data. Driving hydration off
  // [query.data, sessionKey] covers every data source: fresh fetches,
  // cache-served remounts, polling refetches, and loadMore's setQueryData.
  // Idempotent: the reducer's 'hydrate' branch overwrites per groupID (the
  // same key 'upsert' uses), so re-applying the same infos changes nothing.
  // Empty/missing groups never dispatch — no garbage hydration.
  useEffect(() => {
    if (!sessionKey) return
    const groups = query.data?.groups
    if (groups?.length && hydrateGroups) {
      hydrateGroups(groups)
    }
  }, [query.data, sessionKey, hydrateGroups])

  const hasMore = query.data?.hasMore ?? true

  const loadMore = useCallback(async () => {
    if (!sessionKey || !token || isLoadingMoreRef.current) return
    // Read the latest cache data directly to avoid stale closures.
    // query.data in the useCallback deps may lag one render behind.
    const queryKey = buildChatHistoryQueryKey(sessionKey, parentSessionKey)
    const currentData = queryClient.getQueryData<ChatHistoryData>(queryKey)
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

      // The backend attaches the session's CURRENT group snapshots to every
      // page, so per-id overwrite (keeping cached-only ids) is safe and never
      // clobbers the session-level groups.
      const pageGroups = historyGroupsToInfos(history)
      const mergedGroups = pageGroups.length
        ? mergeGroupsById(currentData.groups ?? [], pageGroups)
        : (currentData.groups ?? [])

      queryClient.setQueryData(queryKey, {
        sessionKey: currentData.sessionKey,
        messages: [...uniqueOlderMessages, ...currentData.messages],
        rawMessages: [...history.messages, ...(currentData.rawMessages || [])],
        hasMore: history.has_more,
        processing: history.processing,
        groups: mergedGroups,
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
  // Matching is alias-tolerant: handlers re-tag transient messages with the
  // current key (effectiveSessionKey), but a stray `base:chat:N` event that
  // predates a session-key switch must still render for its own conversation
  // instead of silently vanishing (see sessionKeysLooselyMatch).
  const sessionStreamingMessages = useMemo(
    () =>
      sessionKey
        ? streamingMessages.filter((m) => sessionKeysLooselyMatch(m.sessionKey, sessionKey))
        : [],
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
  // Preserve the cached `groups` payload (catchup responses carry messages
  // only); dropping it here would re-break cache-hit group rehydration.
  queryClient.setQueryData(
    buildChatHistoryQueryKey(sessionKey, parentSessionKey),
    (old: ChatHistoryData | undefined) => ({
      sessionKey,
      messages: toChatMessages(rawMessages, sessionKey),
      rawMessages,
      processing,
      groups: old?.groups,
    }),
  )
}
