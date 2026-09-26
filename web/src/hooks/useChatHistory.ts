import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { ChatMessage, GroupInfo, GroupSnapshot, RawHistoryMessage } from '../lib/types'
import { sessionKeysLooselyMatch } from './event-handlers/helpers'
import { snapshotToGroupInfo } from './messageEventHandlers'
import { useOnPageVisible, usePageVisible } from './usePageVisible'

const DEFAULT_LIMIT = 50

/**
 * Reconciliation poll period while a turn is processing (or this session still
 * has streaming messages to merge).
 */
const HISTORY_POLL_INTERVAL_MS = 4000

export type HistoryMessage = Array<RawHistoryMessage>

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

/**
 * Identity of a message that SURVIVES a change of the value of its `id`.
 *
 * The history payload carries no timestamp and no per-message uuid
 * (`RawHistoryMessage`), and `ChatMessage.createdAt` is minted locally on every
 * conversion, so neither is usable to recognise a message across responses.
 * What is stable is the tuple the server itself derives the id from — role +
 * content, plus the tool identity for tool cards. That matters because the id
 * DERIVATION changed (content digest → bounded hash) while keeping the same id
 * format, so the very same message comes back under a different id and only an
 * id-based dedupe cannot see it.
 */
function chatMessageIdentity(message: ChatMessage): string {
  return message.role === 'tool'
    ? ['tool', message.toolCallId ?? '', message.toolName ?? '', message.toolArgs ?? ''].join('\0')
    : [message.role, message.content].join('\0')
}

/** Raw-payload twin of `chatMessageIdentity` (see there for the rationale). */
function rawMessageIdentity(message: RawHistoryMessage): string {
  return message.role === 'tool'
    ? ['tool', message.tool_call_id ?? '', message.tool_name ?? '', message.content].join('\0')
    : [message.role, message.content].join('\0')
}

/**
 * A single coincidentally equal message is not proof of a re-serve, and
 * dropping a genuinely older message is worse than leaving a duplicate on
 * screen, so a window of at least this many messages must match.
 */
const RE_SERVED_MIN_RUN = 2

/**
 * How many leading messages of `incoming` are a RE-SERVE of the window already
 * displayed — i.e. the page the server returned is not actually older than what
 * is on screen.
 *
 * WHY: message ids are content-derived, and their derivation changed while
 * keeping the same format (content digest → bounded hash). A cursor minted
 * before that change no longer resolves, and the server's documented fallback
 * then answers with the NEWEST page (see pkg/channels/rest_chat.go) — the very
 * messages that are already rendered under their previous id. An id-based
 * dedupe misses all of them and prepends a second copy of each.
 *
 * HOW it is proven: `RawHistoryMessage` carries no timestamp and no per-message
 * uuid, and `ChatMessage.createdAt` is minted locally on every conversion, so
 * the only identity available is role + content (+ the tool identity, see
 * `chatMessageIdentity`). Content identity alone cannot distinguish "the same
 * message under a new id" from "another message with the same text" — the
 * issue-#324 case, where repeated content is legitimate. So the match has to
 * repeat EVERY message of the displayed window, in order, as the leading part
 * of the incoming page. A genuinely older page starts BEFORE our oldest
 * message, so it could only ever reach our window by a coincidence spanning the
 * whole thing. Anything shorter is left alone on purpose: a duplicate bubble
 * heals on the next refetch, a dropped message does not.
 */
function countReServedMessages<T>(
  displayed: T[],
  incoming: T[],
  identityOf: (message: T) => string,
): number {
  if (displayed.length < RE_SERVED_MIN_RUN || incoming.length < displayed.length) return 0
  for (let i = 0; i < displayed.length; i++) {
    if (identityOf(displayed[i]) !== identityOf(incoming[i])) return 0
  }
  return displayed.length
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
  const pageVisible = usePageVisible()

  // Keep a ref to streamingMessages so the refetchInterval callback always
  // reads the latest value without depending on React Query re-evaluating
  // the query options on every render.
  const streamingMessagesRef = useRef(streamingMessages)
  streamingMessagesRef.current = streamingMessages

  // Would this session be polling over HTTP right now? True while the turn is
  // processing, or while this session still has WS-driven streaming messages to
  // reconcile. Shared by `refetchInterval` and the tab-visible refresh below so
  // both agree on when polling is wanted (and on when it is not).
  const wantsPolling = useCallback(
    (processing: boolean | undefined) =>
      Boolean(processing) ||
      streamingMessagesRef.current.some(
        (m) => m.streaming && sessionKeysLooselyMatch(m.sessionKey, sessionKey),
      ),
    [sessionKey],
  )

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
    // HTTP reconciliation poll while the session is live (see the callback).
    refetchInterval: (query) => {
      // Hidden tab: return `false` so React Query CLEARS the interval instead
      // of leaving it armed. React Query already skips the FETCH of a tick
      // that fires while the document is unfocused (`focusManager.isFocused()`
      // gate in @tanstack/query-core's queryObserver, `#updateRefetchInterval`),
      // so for THIS query the gate is defence in depth rather than the request
      // saving: hidden tabs already issued no request. What the pair of gates
      // adds is that no timer runs at all while hidden, plus the immediate
      // `useOnPageVisible` refresh below instead of waiting up to one interval
      // for the next tick. React Query compares the returned value, so a steady
      // 4000 does not restart the timer.
      if (!pageVisible) return false
      // Poll while the session is processing so that if the WebSocket drops
      // events (reconnect, tab throttle, etc.) the UI still updates via HTTP.
      // Stops polling automatically once processing ends.
      //
      // This polling is the authoritative safety net for message reconciliation.
      // handleHistoryUpdated (streaming.ts) conditionally retains completed
      // streaming messages until the HTTP cache catches up, but if that check
      // misses (e.g., content normalization), the next poll brings the message
      // into baseMessages and mergeMessages' position-based dedup removes the
      // stale streaming copy.
      //
      // Also poll if THIS session has streaming messages (WS-driven) to
      // reconcile. Scoped to sessionKey so an unrelated session streaming in
      // the background doesn't keep this query polling. Uses a ref to avoid
      // stale closures during batched state updates.
      return wantsPolling(query.state.data?.processing) ? HISTORY_POLL_INTERVAL_MS : false
    },
    retry: false,
  })

  // Refresh once when the tab becomes visible again: the interval above was
  // cleared for the whole time the tab was hidden, so a turn that finished — or
  // messages that streamed — during that period would otherwise stay invisible
  // until the next tick, or forever if processing had already ended. Only fires
  // when this query would be polling anyway (`wantsPolling`), so nothing is
  // fetched for a session that is neither processing nor streaming; the
  // WebSocket path and `refetchOnWindowFocus` stay untouched.
  //
  // `cancelRefetch: false` on purpose. This refresh races React Query's own
  // focus-driven refetch of the same query (and any in-flight interval tick):
  // the default `cancelRefetch: true` would ABORT that request and start a
  // second GET /api/v1/chat/history, so returning to the tab cost two requests
  // and threw one away. Joining the in-flight request instead keeps it at one.
  // (`refetchOnWindowFocus`'s internal path passes `cancelRefetch: false` for
  // exactly the same reason — see query.js in @tanstack/query-core.)
  useOnPageVisible(() => {
    if (!wantsPolling(query.data?.processing)) return
    void query.refetch({ cancelRefetch: false })
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

  // Default to false while there is no data yet so the UI does not flash a
  // "load more" affordance before the first history response arrives.
  const hasMore = query.data?.hasMore ?? false

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

      // Drop the re-served window (unresolvable cursor, see
      // countReServedMessages) from BOTH array views, so the prepended result
      // and the pagination cursor stay consistent. When it covers the whole page
      // this leaves nothing to merge and the `uniqueOlderMessages.length === 0`
      // path below just stops pagination.
      const reServedMessages = countReServedMessages(
        currentData.messages,
        olderMessages,
        chatMessageIdentity,
      )
      const incomingOlderMessages = olderMessages.slice(reServedMessages)
      const reServedRaw = countReServedMessages(
        currentData.rawMessages ?? [],
        history.messages,
        rawMessageIdentity,
      )
      const incomingRawMessages = history.messages.slice(reServedRaw)

      const existingIds = new Set(currentData.messages.map((m) => m.id))
      const uniqueOlderMessages = incomingOlderMessages.filter((m) => !existingIds.has(m.id))

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
        rawMessages: [...incomingRawMessages, ...(currentData.rawMessages || [])],
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
