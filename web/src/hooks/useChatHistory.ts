import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import { toChatMessages } from '../lib/chatMessageBuilder'
import { attachmentIdentityKeys } from '../lib/messageIdentity'
import { noteHistorySnapshot, observeMergedOrder } from '../lib/orderingMonitor'
import type { ChatMessage, GroupInfo, GroupSnapshot, RawHistoryMessage } from '../lib/types'
import { sessionKeysLooselyMatch } from './event-handlers/helpers'
import { snapshotToGroupInfo } from './messageEventHandlers'
import { useOnPageVisible, usePageVisible } from './usePageVisible'

const DEFAULT_LIMIT = 50

const HISTORY_POLL_INTERVAL_MS = 4000

const OPTIMISTIC_RECONCILE_GRACE_MS = 20_000

export type HistoryMessage = Array<RawHistoryMessage>

export const chatHistoryQueryKey = (sessionKey: string) => ['chatHistory', sessionKey] as const

export function buildChatHistoryQueryKey(sessionKey: string, parentSessionKey?: string) {
  if (parentSessionKey) {
    return [...chatHistoryQueryKey(sessionKey), parentSessionKey] as const
  }
  return chatHistoryQueryKey(sessionKey)
}

function mergeGroupsById(existing: GroupInfo[], incoming: GroupInfo[]): GroupInfo[] {
  if (!incoming.length) return existing
  const byId = new Map(existing.map((g) => [g.groupID, g] as const))
  for (const g of incoming) byId.set(g.groupID, g)
  return Array.from(byId.values())
}

import { mergeMessages } from './messageMerge'
export { mergeMessages }

export type ChatHistoryData = {
  sessionKey: string
  messages: ChatMessage[]
  rawMessages: HistoryMessage
  hasMore: boolean
  processing?: boolean
  groups?: GroupInfo[]
}

function historyGroupsToInfos(history: { groups?: GroupSnapshot[] }): GroupInfo[] {
  return (history.groups ?? []).map(snapshotToGroupInfo)
}

function chatMessageIdentity(message: ChatMessage): string {
  if (message.role === 'tool') {
    return ['tool', message.toolCallId ?? '', message.toolName ?? '', message.toolArgs ?? ''].join(
      '\0',
    )
  }
  if (message.role === 'assistant') {
    return ['assistant', message.content, ...attachmentIdentityKeys(message.attachments)].join('\0')
  }
  return [message.role, message.content].join('\0')
}

function rawMessageIdentity(message: RawHistoryMessage): string {
  if (message.role === 'tool') {
    return ['tool', message.tool_call_id ?? '', message.tool_name ?? '', message.content].join('\0')
  }
  if (message.role === 'assistant') {
    return ['assistant', message.content, ...attachmentIdentityKeys(message.attachments)].join('\0')
  }
  return [message.role, message.content].join('\0')
}

const RE_SERVED_MIN_RUN = 2

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

  const streamingMessagesRef = useRef(streamingMessages)
  streamingMessagesRef.current = streamingMessages

  const lastLiveActivityRef = useRef(0)

  const wantsPolling = useCallback(
    (processing: boolean | undefined) => {
      const live =
        Boolean(processing) ||
        streamingMessagesRef.current.some(
          (m) => m.streaming && sessionKeysLooselyMatch(m.sessionKey, sessionKey),
        )
      if (live) {
        lastLiveActivityRef.current = Date.now()
        return true
      }
      const hasPendingUser = streamingMessagesRef.current.some(
        (m) =>
          m.role === 'user' &&
          m.optimistic === true &&
          m.failed !== true &&
          sessionKeysLooselyMatch(m.sessionKey, sessionKey),
      )
      return (
        hasPendingUser && Date.now() - lastLiveActivityRef.current < OPTIMISTIC_RECONCILE_GRACE_MS
      )
    },
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

      const groups = historyGroupsToInfos(history)

      const newMessages = toChatMessages(history.messages, history.session_key, {
        bindStableIds: true,
      })
      noteHistorySnapshot(history.session_key ?? sessionKey, newMessages)

      const queryKey = buildChatHistoryQueryKey(sessionKey, parentSessionKey)
      const cachedData = queryClient.getQueryData<ChatHistoryData>(queryKey)

      if (cachedData && cachedData.messages.length > DEFAULT_LIMIT) {
        const newMessageIds = new Set(newMessages.map((m) => m.id))
        const olderCachedMessages = cachedData.messages.filter(
          (m) => !m.optimistic && !newMessageIds.has(m.id),
        )

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
    refetchOnWindowFocus: true,
    refetchInterval: (query) => {
      if (!pageVisible) return false
      return wantsPolling(query.state.data?.processing) ? HISTORY_POLL_INTERVAL_MS : false
    },
    retry: false,
  })

  useOnPageVisible(() => {
    if (!wantsPolling(query.data?.processing)) return
    void query.refetch({ cancelRefetch: false })
  })

  useEffect(() => {
    if (!sessionKey) return
    const groups = query.data?.groups
    if (groups?.length && hydrateGroups) {
      hydrateGroups(groups)
    }
  }, [query.data, sessionKey, hydrateGroups])

  const hasMore = query.data?.hasMore ?? false

  const loadMore = useCallback(async () => {
    if (!sessionKey || !token || isLoadingMoreRef.current) return
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

  const sessionStreamingMessages = useMemo(
    () =>
      sessionKey
        ? streamingMessages.filter((m) => sessionKeysLooselyMatch(m.sessionKey, sessionKey))
        : [],
    [streamingMessages, sessionKey],
  )

  const messages = useMemo(
    () =>
      observeMergedOrder(
        mergeMessages(baseMessages, sessionStreamingMessages),
        baseMessages,
        sessionStreamingMessages,
      ),
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
  queryClient.setQueryData(
    buildChatHistoryQueryKey(sessionKey, parentSessionKey),
    (old: ChatHistoryData | undefined) => ({
      sessionKey,
      messages: toChatMessages(rawMessages, sessionKey, { bindStableIds: true }),
      rawMessages,
      processing,
      groups: old?.groups,
    }),
  )
}
