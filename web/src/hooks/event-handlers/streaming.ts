/**
 * Streaming / message lifecycle event handlers.
 *
 * Handles the full lifecycle of a chat message as it flows through
 * the WebSocket: welcome/reconnect, stream chunks, thinking reasoning,
 * acknowledgement, completion, history updates, catchup, attachments,
 * errors, and typing indicators.
 */
import { createAssistantMessage } from '../../lib/chatMessageBuilder'
import { wsDebug } from '../../lib/debug'
import type { ChatMessage, GroupSnapshot, HistoryToolCall } from '../../lib/types'
import {
  type HistoryMessage,
  chatHistoryQueryKey,
  updateChatHistoryFromRaw,
} from '../useChatHistory'
import { getSessionKey, isSessionMismatch, snapshotToGroupInfo } from './helpers'
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
    | Array<{
        role: string
        content?: string
        reasoning_content?: string
      }>
    | undefined
  if (inProgress && inProgress.length > 0 && sessionKey) {
    ctx.setStreamingMessages((current) => {
      const updated = [...current]
      for (const msg of inProgress) {
        if (msg.role !== 'assistant') continue
        const content = msg.content ?? ''
        const reasoning = msg.reasoning_content ?? ''
        // Use a deterministic ID so we don't create duplicates if the
        // real stream events arrive shortly after.
        const restoreId = `restore-${sessionKey}`
        const existingIdx = updated.findIndex((m) => m.id === restoreId)
        if (existingIdx >= 0) {
          updated[existingIdx] = {
            ...updated[existingIdx],
            content: content || updated[existingIdx].content,
            reasoningContent: reasoning || updated[existingIdx].reasoningContent,
            streaming: true,
          }
        } else {
          updated.push(
            createAssistantMessage({
              id: restoreId,
              sessionKey,
              content,
              reasoningContent: reasoning || undefined,
              streaming: true,
            }),
          )
        }
      }
      return updated
    })
  }

  // Rehydrate group snapshots from welcome/reconnected data
  const groups = data.groups as GroupSnapshot[] | undefined
  if (Array.isArray(groups) && groups.length > 0) {
    ctx.hydrateGroups(groups.map(snapshotToGroupInfo))
  }
}

export function handleMessageStream(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'message.stream')) return

  wsDebug('[WS] message.stream received', {
    messageId: data.message_id,
    eventSessionKey,
    currentSessionKey: ctx.currentSessionKeyRef.current,
    chunkLength: (data.chunk as string)?.length ?? 0,
    done: data.done,
  })

  const msgId = data.message_id as string
  const sessionKey = (eventSessionKey ?? ctx.currentSessionKeyRef.current ?? '') as string
  const chunk = (data.chunk as string) ?? ''
  const done = (data.done as boolean) ?? false

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
  const sessionKey = (eventSessionKey ?? ctx.currentSessionKeyRef.current ?? '') as string

  // After page reload, the welcome event creates a restore- message with
  // accumulated reasoning. When real thinking chunks arrive with the actual
  // message_id, we need to migrate the restore- message to the real ID so
  // reasoning content is preserved instead of creating a duplicate.
  ctx.setStreamingMessages((current) => {
    const hasReal = current.some((m) => m.id === msgId)
    if (!hasReal) {
      const restoreIdx = current.findIndex(
        (m) => m.id.startsWith('restore-') && m.sessionKey === sessionKey,
      )
      if (restoreIdx >= 0) {
        return current.map((m, i) => (i === restoreIdx ? { ...m, id: msgId } : m))
      }
    }
    return current
  })

  // Ensure the assistant placeholder exists before updating reasoning content.
  // Without this, thinking chunks arriving before message.stream (e.g., after
  // page reload or reconnection) are silently dropped because there's no
  // assistant message to attach them to.
  ctx.ensureAssistantPlaceholder(msgId, sessionKey)

  ctx.setStreamingMessages((current) =>
    current.map((m) =>
      m.id === msgId && m.role === 'assistant'
        ? { ...m, reasoningContent: `${m.reasoningContent ?? ''}${chunk}` }
        : m,
    ),
  )
}

export function handleMessageAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  const ackSessionKey = (data.session_key as string) ?? ''
  if (ackSessionKey) {
    ctx.addProcessingSession(ackSessionKey)
    ctx.debouncedSessionRefresh()
  }
  ctx.ensureAssistantPlaceholder(data.message_id as string, ackSessionKey)

  // Remove restored placeholder messages for this session now that a real
  // message is starting. This prevents duplicates when the restored content
  // and the real stream coexist briefly.
  if (ackSessionKey) {
    ctx.setStreamingMessages((current) =>
      current.filter((m) => !(m.id.startsWith('restore-') && m.sessionKey === ackSessionKey)),
    )
  }
}

export function handleMessageComplete(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  const completedSessionKey = eventSessionKey ?? ctx.currentSessionKeyRef.current

  ctx.clearQueue(data.message_id as string)
  wsDebug('[WS] message.complete received', {
    messageId: data.message_id,
    eventSessionKey,
    completedSessionKey,
  })

  if (completedSessionKey) {
    ctx.removeProcessingSession(completedSessionKey)
  }

  if (completedSessionKey && completedSessionKey !== ctx.currentSessionKeyRef.current) {
    console.warn('[WS] message.complete for different session, skipping streaming update')
    ctx.setPendingAttachments([])
    ctx.processingSessionKeyRef.current = null
    return
  }

  ctx.setStreamingMessages((current) => {
    const targetSessionKey = eventSessionKey ?? ctx.currentSessionKeyRef.current
    return current.flatMap((m) => {
      // Clean up stale restore- placeholders once the real message completes
      if (m.id.startsWith('restore-') && m.sessionKey === targetSessionKey) {
        return []
      }
      if (m.role === 'assistant' && m.id === (data.message_id as string)) {
        const content = (data.content as string) || m.content
        return [{ ...m, content, streaming: false }]
      }
      if (m.role === 'user' && m.sessionKey === targetSessionKey && m.content.trim() === '') {
        return []
      }
      if (m.role === 'tool' && m.sessionKey === targetSessionKey) {
        return [{ ...m, streaming: false }]
      }
      return [m]
    })
  })
  ctx.setToolStatus(null)
  ctx.setPendingAttachments([])
  ctx.processingSessionKeyRef.current = null
  ctx.debouncedSessionRefresh()
}

export function handleHistoryUpdated(ctx: MessageEventContext, data: Record<string, unknown>) {
  const historySessionKey = (data.session_key as string) ?? ctx.currentSessionKeyRef.current
  ctx.queryClient.invalidateQueries({ queryKey: chatHistoryQueryKey(historySessionKey) })
  ctx.debouncedSessionRefresh()

  if (historySessionKey === ctx.currentSessionKeyRef.current) {
    ctx.setStreamingMessages((current) =>
      current.filter((m) => {
        if (m.sessionKey !== historySessionKey) return true
        // Remove optimistic user messages — they're now confirmed in the HTTP history
        if (m.role === 'user' && m.optimistic) return false
        // Remove completed assistant messages — the HTTP refetch (triggered by
        // invalidateQueries above) will return them in baseMessages. Keeping
        // them in streamingMessages creates a second source of truth that the
        // position-based merge has to deduplicate, which is fragile because
        // WebSocket IDs (UUID) and HTTP IDs (content-hash) don't match.
        if (m.role === 'assistant' && !m.streaming) return false
        return true
      }),
    )
  }
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

  ctx.setStreamingMessages((current) =>
    current.filter((message) => {
      if (message.sessionKey !== targetSessionKey) return true
      // Catchup provides canonical history directly into baseMessages.
      // Remove all assistant/tool streaming messages to prevent duplicates
      // — the catchup data is the single source of truth.
      if (message.role === 'assistant') return false
      if (message.role === 'tool') return false
      return true
    }),
  )
}

export function handleAttachments(ctx: MessageEventContext, event: ClientEvent) {
  ctx.setStreamingMessages((current) => {
    const idx = [...current].reverse().findIndex((m) => m.role === 'assistant')
    if (idx < 0) return current
    const targetIndex = current.length - idx - 1
    return current.map((m, i) =>
      i === targetIndex
        ? { ...m, attachments: event.data as ChatMessage['attachments'], streaming: false }
        : m,
    )
  })
}

export function handleMessageError(ctx: MessageEventContext, data: Record<string, unknown>) {
  const errorSessionKey = (getSessionKey(data) ?? ctx.currentSessionKeyRef.current ?? '') as string

  // Mark optimistic user message as failed (instead of removing it)
  ctx.setStreamingMessages((current) =>
    current.map((m) =>
      m.role === 'user' && m.optimistic && m.sessionKey === errorSessionKey
        ? { ...m, failed: true, streaming: false }
        : m,
    ),
  )

  // Rollback from query cache
  const cached = ctx.queryClient.getQueryData<{ messages?: ChatMessage[] }>(
    chatHistoryQueryKey(errorSessionKey),
  )
  if (cached) {
    ctx.queryClient.setQueryData(chatHistoryQueryKey(errorSessionKey), {
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
  const errorSessionKey = (getSessionKey(data) ?? ctx.currentSessionKeyRef.current ?? '') as string

  ctx.setStreamingMessages((current) =>
    current.map((m) => {
      if (m.sessionKey === errorSessionKey && m.role === 'assistant' && m.streaming) {
        return { ...m, streaming: false, error: (data.error as string) || 'Stream error' }
      }
      return m
    }),
  )

  ctx.removeProcessingSession(errorSessionKey)
  ctx.processingSessionKeyRef.current = null
}

export function handleTypingIndicator(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'typing.indicator')) return

  ctx.setTypingIndicator({
    deviceId: (data.client_id as string) ?? '',
    deviceName: (data.device_name as string) ?? 'Another device',
    timestamp: Date.now(),
  })
}
