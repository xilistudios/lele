/**
 * Streaming / message lifecycle event handlers.
 *
 * Handles the full lifecycle of a chat message as it flows through the
 * WebSocket: welcome/reconnect, stream chunks, thinking reasoning,
 * acknowledgement, completion, history updates, catchup, attachments,
 * errors, and typing indicators.
 *
 * State transitions are delegated to the pure helpers in `streamingOps.ts`;
 * this file is responsible for event parsing, session-mismatch guards, and
 * orchestrating side effects (queues, cache, processing state).
 */
import { wsDebug } from '../../lib/debug'
import type { ChatMessage, GroupSnapshot, HistoryToolCall } from '../../lib/types'
import { registerStableId } from '../stableIdRegistry'
import {
  applyMessageComplete,
  attachToLastAssistant,
  markOptimisticUserFailed,
  markStreamingAssistantsErrored,
  migrateRestoreId,
  removeRestorePlaceholders,
  restoreInProgressAssistant,
} from '../streamingOps'
import {
  type HistoryMessage,
  buildChatHistoryQueryKey,
  chatHistoryQueryKey,
  updateChatHistoryFromRaw,
} from '../useChatHistory'
import {
  getSessionKey,
  isSessionMismatch,
  sessionKeysLooselyMatch,
  snapshotToGroupInfo,
} from './helpers'
import type { ClientEvent, MessageEventContext } from './types'

export function handleWelcome(ctx: MessageEventContext, data: Record<string, unknown>) {
  const { session_key: sessionKey, processing } = data as {
    session_key?: string
    processing?: boolean
  }
  if (processing && sessionKey) {
    ctx.addProcessingSession(sessionKey)
  }

  // Capture groups feature flag
  const groupsEnabled = data.groups_enabled as boolean | undefined
  ctx.setGroupsEnabled(groupsEnabled ?? false)

  // Restore in-progress streaming messages after page reload or reconnection.
  // The backend includes accumulated content and reasoning so the frontend
  // doesn't have to wait for the next chunk to see the current state.
  const inProgress = data.in_progress_messages as
    | Array<{ role: string; content?: string; reasoning_content?: string }>
    | undefined
  if (inProgress && inProgress.length > 0 && sessionKey) {
    ctx.setStreamingMessages((current) =>
      inProgress.reduce<ChatMessage[]>(
        (acc, msg) => {
          if (msg.role !== 'assistant') return acc
          return restoreInProgressAssistant(
            acc,
            sessionKey,
            msg.content ?? '',
            msg.reasoning_content ?? '',
            false, // welcome: no user context yet → append at end
          )
        },
        [...current],
      ),
    )
  }

  // Rehydrate group snapshots from welcome/reconnected data
  const groups = data.groups as GroupSnapshot[] | undefined
  if (Array.isArray(groups) && groups.length > 0) {
    ctx.hydrateGroups(groups.map(snapshotToGroupInfo))
  }
}

/**
 * Effective UI session key for an event that belongs to the current session.
 *
 * After /new or /agent the backend maps `base` -> `base:chat:N` and emits
 * message.ack — and every subsequent event (stream/complete/history) — with the
 * resolved conversation alias (pkg/channels/websocket.go calls
 * agentLoop.ResolveSessionKey before publishing the ack). The frontend re-tags
 * those events with the CURRENT session key because all UI state —
 * streamingMessages, the typewriter queues and the history cache — lives keyed
 * by the session currently on screen; an aliased key would be hidden by
 * mergeMessages (which filters strictly by sessionKey) and the loading state
 * would never clear.
 */
function effectiveSessionKey(ctx: MessageEventContext, eventSessionKey?: string): string {
  const current = ctx.currentSessionKeyRef.current
  if (!eventSessionKey) return current ?? ''
  if (current && sessionKeysLooselyMatch(eventSessionKey, current)) return current
  return eventSessionKey
}

export function handleMessageStream(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'message.stream')) return

  const msgId = data.message_id as string
  // Re-tag aliased events with the current key (see effectiveSessionKey).
  const sessionKey = effectiveSessionKey(ctx, eventSessionKey)
  const chunk = (data.chunk as string) ?? ''
  const done = (data.done as boolean) ?? false

  wsDebug('[WS] message.stream received', {
    messageId: data.message_id,
    eventSessionKey,
    currentSessionKey: ctx.currentSessionKeyRef.current,
    chunkLength: (data.chunk as string)?.length ?? 0,
    done: data.done,
  })

  // Migrate any restore- placeholder to the real message id so accumulated
  // content is preserved instead of creating a duplicate.
  ctx.setStreamingMessages((current) => migrateRestoreId(current, msgId, sessionKey))

  if (done && chunk) {
    ctx.clearQueue(msgId)
    ctx.ensureAssistantPlaceholder(msgId, sessionKey, chunk, true)
    return
  }

  ctx.enqueueChunk(msgId, sessionKey, chunk, done)
}

export function handleMessageThinking(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'message.thinking'))
    return

  const msgId = data.message_id as string
  const chunk = (data.chunk as string) ?? ''
  // Re-tag aliased events with the current key (see effectiveSessionKey).
  const sessionKey = effectiveSessionKey(ctx, eventSessionKey)

  // Migrate any restore- placeholder to the real message id (see stream handler).
  ctx.setStreamingMessages((current) => migrateRestoreId(current, msgId, sessionKey))

  // Ensure the assistant placeholder exists before updating reasoning content.
  // Without this, thinking chunks arriving before message.stream (e.g., after
  // page reload or reconnection) are silently dropped.
  ctx.ensureAssistantPlaceholder(msgId, sessionKey)

  ctx.setStreamingMessages((current) =>
    current.map((m) => {
      if (m.id === msgId && m.role === 'assistant') {
        return { ...m, reasoningContent: `${m.reasoningContent ?? ''}${chunk}`, streaming: true }
      }
      if (m.id !== msgId && m.role === 'assistant' && m.sessionKey === sessionKey && m.streaming) {
        return { ...m, streaming: false }
      }
      return m
    }),
  )
}

export function handleMessageAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  const rawAckKey = (data.session_key as string) ?? ''
  // Backend may report the alias-resolved key (base:chat:N, see
  // pkg/channels/websocket.go); UI state is keyed by the current session —
  // re-tag so processing, the placeholder and restore-cleanup all target it.
  const ackSessionKey = rawAckKey ? effectiveSessionKey(ctx, rawAckKey) : ''
  if (ackSessionKey) {
    ctx.addProcessingSession(ackSessionKey)
    ctx.debouncedSessionRefresh()
  }
  ctx.ensureAssistantPlaceholder(data.message_id as string, ackSessionKey)

  // Remove restored placeholder messages for this session now that a real
  // message is starting, preventing duplicates when restored content and the
  // real stream coexist briefly.
  if (ackSessionKey) {
    ctx.setStreamingMessages((current) => removeRestorePlaceholders(current, ackSessionKey))
  }
}

export function handleMessageComplete(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  const currentKey = ctx.currentSessionKeyRef.current
  const completedSessionKey = eventSessionKey ?? currentKey

  ctx.clearQueue(data.message_id as string)
  wsDebug('[WS] message.complete received', {
    messageId: data.message_id,
    eventSessionKey,
    currentSessionKey: currentKey,
    completedSessionKey,
  })

  // The ack registers the BASE key as processing while complete can carry the
  // alias (and vice versa), so clear both sides of an aliased pair — otherwise
  // the entry added by the ack leaks and the spinner stays stuck.
  if (completedSessionKey) {
    ctx.removeProcessingSession(completedSessionKey)
  }
  if (currentKey && completedSessionKey && currentKey !== completedSessionKey) {
    if (sessionKeysLooselyMatch(currentKey, completedSessionKey)) {
      ctx.removeProcessingSession(currentKey)
    } else {
      // Event for an unrelated session: never touch the current session's
      // global refs, tool status or streaming state.
      return
    }
  }

  // Streaming state is keyed by the current session key, so complete the
  // placeholder under it even when the event carried an alias.
  const targetSessionKey = (currentKey ?? completedSessionKey ?? '') as string
  ctx.setStreamingMessages((current) =>
    applyMessageComplete(
      current,
      data.message_id as string,
      targetSessionKey,
      data.content as string | undefined,
      data.attachments as ChatMessage['attachments'],
    ),
  )
  ctx.setToolStatus(null)
  ctx.setPendingAttachments([])
  ctx.processingSessionKeyRef.current = null
  ctx.debouncedSessionRefresh()
}

/**
 * Whether a streaming message already has a confirmed (non-optimistic) copy in
 * the HTTP cache, matched by a 200-char content prefix. The prefix comparison
 * tolerates minor server-side normalization (trailing newlines, unicode). An
 * empty prefix never matches, so empty messages are kept and reconciled by the
 * 4s HTTP polling safety net instead.
 */
function isConfirmedInCache(
  msg: ChatMessage,
  cached: { messages?: ChatMessage[] } | undefined,
): boolean {
  const prefix = msg.content.slice(0, 200)
  if (prefix.length === 0) return false
  return (
    cached?.messages?.some((bm) => {
      if (msg.role === 'user') {
        return bm.role === 'user' && !bm.optimistic && bm.content.startsWith(prefix)
      }
      if (msg.role === 'assistant') {
        return bm.role === 'assistant' && bm.content.startsWith(prefix)
      }
      return false
    }) ?? false
  )
}

export function handleHistoryUpdated(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = data.session_key as string | undefined
  const currentKey = ctx.currentSessionKeyRef.current
  // The query cache is keyed by the session key the frontend knows (the base
  // key). An aliased event key (`base:chat:N`, pkg/agent/loop.go) refers to the
  // SAME conversation, so it must resolve to the current key — invalidating the
  // alias key would be a no-op and the UI would never refetch.
  const historySessionKey: string | null =
    eventSessionKey && sessionKeysLooselyMatch(eventSessionKey, currentKey)
      ? currentKey
      : (eventSessionKey ?? currentKey)

  if (historySessionKey) {
    ctx.queryClient.invalidateQueries({ queryKey: chatHistoryQueryKey(historySessionKey) })
  }
  ctx.debouncedSessionRefresh()

  // Genuinely another conversation: invalidation above is harmless, but the
  // stripping below only applies to the session currently on screen.
  if (!historySessionKey || historySessionKey !== currentKey) return

  // invalidateQueries triggers an ASYNC refetch. We must only strip optimistic
  // users / completed assistants once the HTTP cache already holds the
  // confirmed copy — otherwise the message vanishes until the refetch lands
  // (flicker, or the assistant appearing to jump ahead of the user). The 4s
  // HTTP poll + mergeMessages dedup are the safety net if this check misses.
  const cached = ctx.queryClient.getQueryData<{ messages?: ChatMessage[] }>(
    buildChatHistoryQueryKey(historySessionKey, ctx.parentSessionKeyRef.current ?? undefined),
  )

  ctx.setStreamingMessages((current) => {
    // Before stripping confirmed messages, record their ephemeral id in the
    // durable stableId registry so every future history build (including
    // refetches from the 4s poll) re-attaches it as `stableId`. This keeps
    // React render keys identical across the WebSocket→HTTP transition —
    // without it, the key flips from uuid to content-hash at confirmation
    // time and the bubble remounts (flicker).
    for (const m of current) {
      if (m.sessionKey !== historySessionKey) continue
      if (m.role === 'user' && m.optimistic && isConfirmedInCache(m, cached)) {
        registerStableId('user', m.content, m.id)
      }
      if (m.role === 'assistant' && !m.streaming && isConfirmedInCache(m, cached)) {
        registerStableId('assistant', m.content, m.id)
      }
    }
    return current.filter((m) => {
      if (m.sessionKey !== historySessionKey) return true
      if (m.role === 'user' && m.optimistic) return !isConfirmedInCache(m, cached)
      if (m.role === 'assistant' && !m.streaming) return !isConfirmedInCache(m, cached)
      return true
    })
  })
}

export function handleMessagesCatchup(ctx: MessageEventContext, data: Record<string, unknown>) {
  const catchupData = data as {
    session_key?: string
    is_initial: boolean
    messages: Array<{
      id?: string
      role: 'user' | 'assistant' | 'tool'
      content: string
      tool_call_id?: string
      tool_calls?: HistoryToolCall[]
    }>
  }
  const targetSessionKey = catchupData.session_key || ctx.currentSessionKeyRef.current || ''

  if (!catchupData.is_initial || targetSessionKey !== ctx.currentSessionKeyRef.current) return

  const rawMessages = catchupData.messages.map((m, i) => ({
    ...m,
    id: m.id ?? `catchup-${i}`,
  })) as unknown as HistoryMessage
  updateChatHistoryFromRaw(
    ctx.queryClient,
    targetSessionKey,
    rawMessages,
    undefined,
    ctx.parentSessionKeyRef.current ?? undefined,
  )

  // Catchup provides canonical history directly into baseMessages. Remove all
  // assistant/tool streaming messages to prevent duplicates — the catchup data
  // is the single source of truth.
  ctx.setStreamingMessages((current) =>
    current.filter((message) => {
      if (message.sessionKey !== targetSessionKey) return true
      if (message.role === 'assistant') return false
      if (message.role === 'tool') return false
      return true
    }),
  )
}

export function handleAttachments(ctx: MessageEventContext, event: ClientEvent) {
  ctx.setStreamingMessages((current) =>
    attachToLastAssistant(current, event.data as ChatMessage['attachments']),
  )
}

export function handleMessageError(ctx: MessageEventContext, data: Record<string, unknown>) {
  // Defense for the future: the backend does not emit message.error today; if
  // it ever does with an alias-resolved key, re-tag it to the current session
  // so the failure markers below hit the UI state (keyed by that key).
  const errorSessionKey = effectiveSessionKey(ctx, getSessionKey(data))

  // Mark optimistic user message as failed (instead of removing it)
  ctx.setStreamingMessages((current) => markOptimisticUserFailed(current, errorSessionKey))

  // Rollback from query cache
  const cacheKey = buildChatHistoryQueryKey(
    errorSessionKey,
    ctx.parentSessionKeyRef.current ?? undefined,
  )
  const cached = ctx.queryClient.getQueryData<{ messages?: ChatMessage[] }>(cacheKey)
  if (cached) {
    ctx.queryClient.setQueryData(cacheKey, {
      ...cached,
      messages: (cached.messages ?? []).map((m) =>
        m.role === 'user' && m.optimistic ? { ...m, failed: true } : m,
      ),
    })
  }

  ctx.removeProcessingSession(errorSessionKey)
  ctx.processingSessionKeyRef.current = null
}

export function handleStreamError(ctx: MessageEventContext, data: Record<string, unknown>) {
  // Defense for the future: the backend does not emit stream.error today; if
  // it ever does with an alias-resolved key, re-tag it to the current session
  // so the error markers below hit the streaming state (keyed by that key).
  const errorSessionKey = effectiveSessionKey(ctx, getSessionKey(data))

  ctx.setStreamingMessages((current) =>
    markStreamingAssistantsErrored(
      current,
      errorSessionKey,
      (data.error as string) || 'Stream error',
    ),
  )

  ctx.removeProcessingSession(errorSessionKey)
  ctx.processingSessionKeyRef.current = null
}

export function handleTypingIndicator(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'typing.indicator'))
    return

  ctx.setTypingIndicator({
    deviceId: (data.client_id as string) ?? '',
    deviceName: (data.device_name as string) ?? 'Another device',
    timestamp: Date.now(),
  })
}
