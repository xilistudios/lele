import { wsDebug } from '../../lib/debug'
import { assistantIdentityText } from '../../lib/messageIdentity'
import { sendErrorMessage } from '../../lib/sendErrors'
import type { ChatMessage, GroupSnapshot, HistoryToolCall } from '../../lib/types'
import { isAttachmentsOnly, isAttachmentsOnlyBase } from '../messageMerge'
import { registerStableId } from '../stableIdRegistry'
import {
  applyMessageComplete,
  attachToLastAssistant,
  markNewestOptimisticUserFailed,
  markOptimisticUserFailed,
  markStreamingAssistantsErrored,
  migrateRestoreId,
  removeDiscardedMessage,
  removeRestorePlaceholders,
  restoreInProgressAssistant,
  retractAttachmentsOnlyBubble,
} from '../streamingOps'
import {
  type HistoryMessage,
  buildChatHistoryQueryKey,
  chatHistoryQueryKey,
  updateChatHistoryFromRaw,
} from '../useChatHistory'
import { parseStreamMetrics } from '../useStreamMetrics'
import {
  effectiveSessionKey,
  getSessionKey,
  isSessionMismatch,
  sessionKeysLooselyMatch,
  snapshotToGroupInfo,
} from './helpers'
import { restoreInProgressTool } from './tools'
import type { ClientEvent, MessageEventContext } from './types'

export function handleWelcome(ctx: MessageEventContext, data: Record<string, unknown>) {
  const { session_key: sessionKey, processing } = data as {
    session_key?: string
    processing?: boolean
  }
  if (processing && sessionKey) {
    ctx.addProcessingSession(sessionKey)
  }

  ctx.clearStreamMetrics()

  const groupsEnabled = data.groups_enabled as boolean | undefined
  ctx.setGroupsEnabled(groupsEnabled ?? false)

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
            false,
          )
        },
        [...current],
      ),
    )
  }

  if (processing) {
    restoreInProgressTool(ctx, sessionKey ?? '', data.in_progress_tool as Record<string, unknown>)
  }

  const groups = data.groups as GroupSnapshot[] | undefined
  if (Array.isArray(groups) && groups.length > 0) {
    ctx.hydrateGroups(groups.map(snapshotToGroupInfo))
  }
}

export function handleMessageStream(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'message.stream')) return

  const msgId = data.message_id as string
  const sessionKey = effectiveSessionKey(ctx, eventSessionKey)
  const chunk = (data.chunk as string) ?? ''
  const done = (data.done as boolean) ?? false

  ctx.setStreamMetrics(parseStreamMetrics(data))

  wsDebug('[WS] message.stream received', {
    messageId: data.message_id,
    eventSessionKey,
    currentSessionKey: ctx.currentSessionKeyRef.current,
    chunkLength: (data.chunk as string)?.length ?? 0,
    done: data.done,
  })

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
  const sessionKey = effectiveSessionKey(ctx, eventSessionKey)

  ctx.setStreamMetrics(parseStreamMetrics(data))

  ctx.setStreamingMessages((current) => migrateRestoreId(current, msgId, sessionKey))

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

export function handleMessageDiscard(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'message.discard'))
    return

  const msgId = data.message_id as string
  if (!msgId) return

  ctx.clearQueue(msgId)

  const sessionKey = effectiveSessionKey(ctx, eventSessionKey)
  ctx.setStreamingMessages((current) => removeDiscardedMessage(current, msgId, sessionKey))
}

export function handleMessageAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  const rawAckKey = (data.session_key as string) ?? ''
  const ackSessionKey = rawAckKey ? effectiveSessionKey(ctx, rawAckKey) : ''
  if (ackSessionKey) {
    ctx.addProcessingSession(ackSessionKey)
    ctx.debouncedSessionRefresh()
  }
  ctx.ensureAssistantPlaceholder(data.message_id as string, ackSessionKey)

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

  if (completedSessionKey) {
    ctx.removeProcessingSession(completedSessionKey)
  }
  if (currentKey && completedSessionKey && currentKey !== completedSessionKey) {
    if (sessionKeysLooselyMatch(currentKey, completedSessionKey)) {
      ctx.removeProcessingSession(currentKey)
    } else {
      return
    }
  }

  const targetSessionKey = (currentKey ?? completedSessionKey ?? '') as string
  ctx.setStreamingMessages((current) =>
    applyMessageComplete(
      current,
      data.message_id as string,
      targetSessionKey,
      data.content as string | undefined,
      data.attachments as ChatMessage['attachments'],
      data.error === true,
    ),
  )
  ctx.setToolStatus(null)
  ctx.setPendingAttachments([])
  ctx.processingSessionKeyRef.current = null
  ctx.debouncedSessionRefresh()
}

function confirmationIdentity(msg: ChatMessage): string {
  return msg.role === 'assistant'
    ? assistantIdentityText(msg.content, msg.reasoningContent)
    : msg.content
}

function isConfirmedInCache(
  msg: ChatMessage,
  cached: { messages?: ChatMessage[] } | undefined,
): boolean {
  if (msg.role === 'tool' && msg.toolCallId) {
    return (
      cached?.messages?.some((bm) => bm.role === 'tool' && bm.toolCallId === msg.toolCallId) ??
      false
    )
  }

  const prefix = confirmationIdentity(msg).slice(0, 200)
  if (prefix.length === 0) {
    if (!isAttachmentsOnly(msg)) return false
    return cached?.messages?.some((bm) => isAttachmentsOnlyBase(bm)) ?? false
  }
  return (
    cached?.messages?.some((bm) => {
      if (msg.role === 'user') {
        return bm.role === 'user' && !bm.optimistic && bm.content.startsWith(prefix)
      }
      if (msg.role === 'assistant') {
        return bm.role === 'assistant' && confirmationIdentity(bm).startsWith(prefix)
      }
      if (msg.role === 'tool') {
        return bm.role === 'tool' && bm.content.startsWith(prefix)
      }
      return false
    }) ?? false
  )
}

export function handleHistoryUpdated(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = data.session_key as string | undefined
  const currentKey = ctx.currentSessionKeyRef.current
  const historySessionKey: string | null =
    eventSessionKey && sessionKeysLooselyMatch(eventSessionKey, currentKey)
      ? currentKey
      : (eventSessionKey ?? currentKey)

  if (historySessionKey) {
    ctx.queryClient.invalidateQueries({ queryKey: chatHistoryQueryKey(historySessionKey) })
  }
  ctx.debouncedSessionRefresh()

  if (!historySessionKey || historySessionKey !== currentKey) return

  const cached = ctx.queryClient.getQueryData<{ messages?: ChatMessage[] }>(
    buildChatHistoryQueryKey(historySessionKey, ctx.parentSessionKeyRef.current ?? undefined),
  )

  ctx.setStreamingMessages((current) => {
    for (const m of current) {
      if (m.sessionKey !== historySessionKey) continue
      if (m.role === 'user' && m.optimistic && isConfirmedInCache(m, cached)) {
        registerStableId('user', m.content, m.id)
      }
      if (m.role === 'assistant' && !m.streaming && isConfirmedInCache(m, cached)) {
        registerStableId('assistant', confirmationIdentity(m), m.id)
      }
      if (m.role === 'tool' && m.toolStatus !== 'executing' && isConfirmedInCache(m, cached)) {
        if (m.toolCallId) registerStableId('tool', `id:${m.toolCallId}`, m.id)
        if (m.toolResult) registerStableId('tool', m.toolResult, m.id)
      }
    }
    return current.filter((m) => {
      if (m.sessionKey !== historySessionKey) return true
      if (m.role === 'user' && m.optimistic) return !isConfirmedInCache(m, cached)
      if (m.role === 'assistant' && !m.streaming) {
        if (m.content.trim() === '' && !m.reasoningContent) return false
        return !isConfirmedInCache(m, cached)
      }
      if (m.role === 'tool' && m.toolStatus !== 'executing') {
        return !isConfirmedInCache(m, cached)
      }
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

  const catchupAssistantIds = new Set(
    catchupData.messages.filter((m) => m.role === 'assistant').map((m) => m.id ?? ''),
  )
  const catchupToolCallIds = new Set(
    catchupData.messages
      .filter((m) => m.role === 'tool' && m.tool_call_id)
      .map((m) => m.tool_call_id as string),
  )

  ctx.setStreamingMessages((current) =>
    current.filter((message) => {
      if (message.sessionKey !== targetSessionKey) return true

      if (message.role === 'assistant') {
        if (message.streaming) return true
        if (catchupAssistantIds.has(message.id)) return false
        return message.content.trim() !== '' || Boolean(message.reasoningContent)
      }

      if (message.role === 'tool') {
        if (message.toolStatus === 'executing') return true
        if (message.toolCallId && catchupToolCallIds.has(message.toolCallId)) return false
        return true
      }

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
  const errorSessionKey = effectiveSessionKey(ctx, getSessionKey(data))

  ctx.setStreamingMessages((current) => markOptimisticUserFailed(current, errorSessionKey))

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

export function handleServerError(
  ctx: MessageEventContext,
  data: Record<string, unknown> | undefined,
) {
  const payload = data ?? {}
  const code = typeof payload.code === 'string' ? payload.code : ''
  const message = typeof payload.message === 'string' ? payload.message : ''

  if (code === 'attachments_rejected') {
    const retracted = retractAttachmentsOnlyBubble(
      ctx.streamingMessagesRef.current,
      ctx.currentSessionKeyRef.current,
    )
    if (retracted) {
      ctx.setStreamingMessages(retracted.messages)
      if (retracted.restoredPaths.length > 0) {
        ctx.setPendingAttachments((pending) => [...pending, ...retracted.restoredPaths])
      }
    }
  } else {
    const current = ctx.streamingMessagesRef.current
    const marked = markNewestOptimisticUserFailed(current, ctx.currentSessionKeyRef.current ?? '')
    if (marked !== current) ctx.setStreamingMessages(marked)
  }

  ctx.setError(sendErrorMessage(code, message))
}
