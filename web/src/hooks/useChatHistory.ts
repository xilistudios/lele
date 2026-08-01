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
  exclude_from_context?: boolean
}>

export const chatHistoryQueryKey = (sessionKey: string) => ['chatHistory', sessionKey] as const

export function buildChatHistoryQueryKey(sessionKey: string, parentSessionKey?: string) {
  if (parentSessionKey) {
    return [...chatHistoryQueryKey(sessionKey), parentSessionKey] as const
  }
  return chatHistoryQueryKey(sessionKey)
}

export function mergeMessages(
  baseMessages: ChatMessage[],
  streamingMessages: ChatMessage[],
): ChatMessage[] {
  // Track IDs of assistant messages that are still actively streaming.
  // Once streaming is done (streaming=false), the server/base version
  // should take over — otherwise both the base and streaming copies
  // get filtered out and the message disappears during HTTP polling.
  //
  // NOTE: WebSocket events use UUID-based IDs while HTTP history uses
  // content-hash-based IDs. Therefore we MUST use position-based matching
  // (not ID-based) to associate base messages with their streaming
  // counterparts. Failure to do this causes duplicate messages and
  // incorrect ordering during live updates.
  // Count optimistic users separately so the position-based matching doesn't
  // get confused by optimistic messages lingering in the query cache, while
  // the user dedup filter below still sees the full count.
  const baseUserCount = baseMessages.filter((message) => message.role === 'user').length
  const baseUserCountNonOptimistic = baseMessages.filter(
    (message) => message.role === 'user' && !message.optimistic,
  ).length

  const streamingToolCallIds = new Set<string>()
  const streamingToolSessions = new Set<string>()
  const streamingToolByCallId = new Map<string, ChatMessage>()
  // Build an ordered list of streaming assistants for position-based matching.
  // Store whether each is actively streaming so we know whether to prefer
  // the streaming copy or the base copy.
  const orderedStreamingAssistants: Array<{
    msg: ChatMessage
    isStreaming: boolean
    used: boolean
  }> = []
  for (const msg of streamingMessages) {
    if (msg.role === 'assistant') {
      orderedStreamingAssistants.push({ msg, isStreaming: msg.streaming === true, used: false })
    }
    if (msg.role === 'tool') {
      if (msg.toolCallId) {
        streamingToolCallIds.add(msg.toolCallId)
        streamingToolByCallId.set(msg.toolCallId, msg)
      }
      if (msg.sessionKey) {
        streamingToolSessions.add(msg.sessionKey)
      }
    }
  }

  const optimisticUser = streamingMessages.find((m) => m.role === 'user' && m.optimistic)
  const baseAssistantCount = baseMessages.filter((m) => m.role === 'assistant').length

  // baseHasCurrentTurn is true only when the base (HTTP history) already
  // contains BOTH the user message AND the assistant response for the current
  // turn. Previously it only checked if the user count exceeded the optimistic
  // base count, which became true as soon as the optimistic user message was
  // added to the query cache — before the assistant response existed in base.
  // This caused matchOffset to pair the streaming assistant with an OLD base
  // assistant, placing the new response ABOVE the user message.
  // Adding the `baseAssistantCount >= baseUserCount` guard ensures matching
  // only happens once the base has caught up with the full turn.
  const baseHasCurrentTurn =
    baseUserCountNonOptimistic > (optimisticUser?.optimisticBaseCount ?? 0) &&
    baseAssistantCount >= baseUserCountNonOptimistic

  // Calculate the offset for position-based matching. Streaming assistants
  // always correspond to the LAST N assistants in base (the most recent turn),
  // not the first ones. Without this offset, the matching pairs streaming
  // assistants with the wrong base assistants, causing duplicates when the
  // HTTP history already includes the completed message.
  //
  // When baseHasCurrentTurn is false, the streaming assistants are new messages
  // that don't exist in base yet, so no matching should occur (offset = total
  // base assistants, meaning none will be matched).
  const streamingAssistantCount = orderedStreamingAssistants.length
  const matchOffset = baseHasCurrentTurn
    ? Math.max(0, baseAssistantCount - streamingAssistantCount)
    : baseAssistantCount

  // Build filteredBase: keep base messages but update tool messages in-place
  // with streaming data instead of removing them. This preserves the canonical
  // order from the server while showing live streaming updates.
  //
  // For assistant messages, use position-based matching with offset: the Nth
  // assistant in base (counting from matchOffset) corresponds to the
  // (N - matchOffset)th assistant in streaming. If the streaming copy is
  // actively streaming, use it in-place to preserve message order.
  const consumedStreamingToolIds = new Set<string>()
  let baseAssistantIdx = 0
  let streamAsstIdx = 0
  const filteredBase: ChatMessage[] = []
  for (const msg of baseMessages) {
    if (msg.role === 'assistant') {
      // Only attempt matching for assistants at or after the offset
      if (baseAssistantIdx >= matchOffset && streamAsstIdx < orderedStreamingAssistants.length) {
        const entry = orderedStreamingAssistants[streamAsstIdx]
        if (entry.isStreaming) {
          // Actively streaming → this is a NEW message that doesn't exist in
          // base yet (base messages are always completed). Do NOT replace the
          // base assistant in-place; that would put a newer message (e.g. a
          // post-tool-call response) at the position of an older one, breaking
          // chronological order. Keep the base version and let the streaming
          // copy be appended via filteredStreaming.
          //
          // Skip this streaming entry so it remains available for dedup later.
          baseAssistantIdx++
          filteredBase.push(msg)
          continue
        }
        // Both are completed → base version takes precedence.
        // The streaming copy will be deduped in filteredStreaming below.
        entry.used = true
        streamAsstIdx++
      }
      baseAssistantIdx++
      // Fall through: keep the base version
      filteredBase.push(msg)
      continue
    }
    if (msg.role === 'tool' && msg.toolCallId && streamingToolCallIds.has(msg.toolCallId)) {
      // Replace base tool with streaming version, but keep base position
      const streamingTool = streamingToolByCallId.get(msg.toolCallId)
      if (streamingTool) {
        filteredBase.push(streamingTool)
        consumedStreamingToolIds.add(msg.toolCallId)
      } else {
        filteredBase.push(msg)
      }
      continue
    }
    if (
      msg.role === 'tool' &&
      !msg.toolCallId &&
      msg.sessionKey &&
      streamingToolSessions.has(msg.sessionKey)
    ) {
      const hasStreamingTool = streamingMessages.some(
        (sm) => sm.role === 'tool' && sm.sessionKey === msg.sessionKey,
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

  // Build a set of streaming assistant IDs that were already placed in filteredBase
  const usedStreamingAssistantIds = new Set(
    orderedStreamingAssistants.filter((e) => e.used).map((e) => e.msg.id),
  )

  // Only keep streaming items that haven't been incorporated into base yet
  const filteredStreaming = streamingWithoutConfirmedUsers.filter((msg) => {
    if (msg.role === 'assistant' && usedStreamingAssistantIds.has(msg.id)) {
      // Already placed in filteredBase via position-based matching
      return false
    }
    if (msg.role === 'assistant' && !msg.streaming && baseHasCurrentTurn) {
      // Completed streaming assistant that was NOT placed in filteredBase.
      // Check if it was matched with a base counterpart during position-based
      // matching. If used=true, the base copy is already in filteredBase and
      // this streaming copy is a duplicate.
      // If used=false, there is no base counterpart yet (HTTP poll hasn't
      // caught up), so keep the streaming copy.
      const entry = orderedStreamingAssistants.find((e) => e.msg.id === msg.id)
      if (entry?.used) {
        return false
      }
    }
    if (msg.role === 'tool' && msg.toolCallId && consumedStreamingToolIds.has(msg.toolCallId)) {
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
  hydrateGroups?: (infos: GroupInfo[]) => void,
) {
  const queryClient = useQueryClient()
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [hasMore, setHasMore] = useState(true)
  const isLoadingMoreRef = useRef(false)

  const query = useQuery({
    queryKey: buildChatHistoryQueryKey(sessionKey ?? '', parentSessionKey),
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
        // (i.e., messages not present in the latest batch)
        const olderCachedMessages = cachedData.messages.filter((m) => !newMessageIds.has(m.id))

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

      queryClient.setQueryData(buildChatHistoryQueryKey(sessionKey, parentSessionKey), {
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
    setHasMore(true)
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
