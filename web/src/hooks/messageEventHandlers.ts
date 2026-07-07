import type { QueryClient } from '@tanstack/react-query'
import {
  createAssistantMessage,
  createDeterministicToolMessageId,
  createToolMessage,
  createToolMessageId,
  parseSubagentSessionKey,
} from '../lib/chatMessageBuilder'
import { wsDebug } from '../lib/debug'
import type { ChatMessage, HistoryToolCall, ToolStatus } from '../lib/types'
import {
  type HistoryMessage,
  chatHistoryQueryKey,
  updateChatHistoryFromRaw,
} from './useChatHistory'

// ── Types ────────────────────────────────────────────────────────────────────

type ClientEvent = { event: string; data: unknown }

export type MessageEventContext = {
  currentSessionKeyRef: React.MutableRefObject<string | null>
  parentSessionKeyRef: React.MutableRefObject<string | null>
  queryClient: QueryClient
  debouncedSessionRefresh: () => void
  setStreamingMessages: React.Dispatch<React.SetStateAction<ChatMessage[]>>
  setToolStatus: React.Dispatch<React.SetStateAction<ToolStatus | null>>
  setPendingAttachments: React.Dispatch<React.SetStateAction<string[]>>
  setApprovalRequest: (req: unknown) => void
  showApprovalResult: (requestId: string, approved: boolean, command: string) => void
  enqueueChunk: (messageId: string, sessionKey: string, chunk: string, done: boolean) => void
  clearQueue: (messageId: string) => void
  clearAllQueues: () => void
  ensureAssistantPlaceholder: (
    messageId: string,
    sessionKey: string,
    chunk?: string,
    isDone?: boolean,
  ) => void
  addProcessingSession: (sessionKey: string) => void
  removeProcessingSession: (sessionKey: string) => void
  syncProcessingSession: (sessionKey: string, processing: boolean) => void
  processingSessionKeyRef: React.MutableRefObject<string | null>
}

// ── Helpers ──────────────────────────────────────────────────────────────────

function getSessionKey(data: Record<string, unknown>): string | undefined {
  return data.session_key as string | undefined
}

function isSessionMismatch(
  eventSessionKey: string | undefined,
  currentSessionKey: string | null,
  label: string,
): boolean {
  if (eventSessionKey && eventSessionKey !== currentSessionKey) {
    console.warn(`[WS] Dropped ${label}: session mismatch`, {
      eventSessionKey,
      currentSessionKey,
    })
    return true
  }
  return false
}

function findToolMessageIndex(
  messages: ChatMessage[],
  toolCallId: string | undefined,
  fallback: (messages: ChatMessage[]) => number,
): number {
  if (toolCallId) {
    const idx = messages.findIndex((m) => m.role === 'tool' && m.toolCallId === toolCallId)
    if (idx >= 0) return idx
  }
  return fallback(messages)
}

// ── Event Handlers ───────────────────────────────────────────────────────────

function handleWelcome(ctx: MessageEventContext, data: Record<string, unknown>) {
  const { session_key: sessionKey, processing } = data as {
    session_key?: string
    processing?: boolean
  }
  if (processing && sessionKey) {
    ctx.addProcessingSession(sessionKey)
  }

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
}

function handleMessageStream(ctx: MessageEventContext, data: Record<string, unknown>) {
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

function handleMessageThinking(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'message.thinking'))
    return

  const msgId = data.message_id as string
  const chunk = (data.chunk as string) ?? ''
  const sessionKey = (eventSessionKey ?? ctx.currentSessionKeyRef.current ?? '') as string

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

function handleMessageAck(ctx: MessageEventContext, data: Record<string, unknown>) {
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

function handleMessageComplete(ctx: MessageEventContext, data: Record<string, unknown>) {
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

function handleHistoryUpdated(ctx: MessageEventContext, data: Record<string, unknown>) {
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

function handleMessagesCatchup(ctx: MessageEventContext, data: Record<string, unknown>) {
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

function handleAttachments(ctx: MessageEventContext, event: ClientEvent) {
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

function handleToolExecuting(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'tool.executing')) return

  ctx.setToolStatus(data as unknown as ToolStatus)

  const toolCallId = data.tool_call_id as string | undefined
  const toolArgsStr = data.arguments
    ? `${data.tool as string} ${JSON.stringify(data.arguments)}`
    : (data.action as string)

  const toolMsg = createToolMessage({
    id: toolCallId
      ? createDeterministicToolMessageId('ws', toolCallId)
      : createToolMessageId(data.tool as string),
    sessionKey: (eventSessionKey ?? ctx.currentSessionKeyRef.current ?? undefined) as string,
    toolName: data.tool as string,
    toolArgs: toolArgsStr,
    toolStatus: 'executing',
    toolCallId,
    subagentSessionKey: data.subagent_session_key as string | undefined,
  })

  ctx.setStreamingMessages((current) => {
    if (toolCallId) {
      const existingIdx = current.findIndex((m) => m.role === 'tool' && m.toolCallId === toolCallId)
      if (existingIdx >= 0) {
        return current.map((m, i) =>
          i === existingIdx ? { ...m, toolArgs: toolArgsStr, toolStatus: 'executing' as const } : m,
        )
      }
    }
    // Insert tool messages after the last assistant message AND any existing
    // tool messages that follow it, to preserve chronological order within
    // the current LLM iteration. Previously this inserted right after the
    // assistant (before existing tools), causing reverse-chronological order.
    const lastAssistantIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
    if (lastAssistantIdx < 0) return [...current, toolMsg]
    const assistantOriginalIdx = current.length - 1 - lastAssistantIdx
    let insertIdx = assistantOriginalIdx + 1
    while (insertIdx < current.length && current[insertIdx].role === 'tool') {
      insertIdx++
    }
    const arr = [...current]
    arr.splice(insertIdx, 0, toolMsg)
    return arr
  })
}

function handleToolResult(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'tool.result')) return

  ctx.setToolStatus(null)

  ctx.setStreamingMessages((current) => {
    const toolCallId = data.tool_call_id as string | undefined
    const targetIndex = findToolMessageIndex(current, toolCallId, (msgs) => {
      const lastToolIdx = [...msgs]
        .reverse()
        .findIndex(
          (m) =>
            m.role === 'tool' &&
            m.toolStatus === 'executing' &&
            m.toolName === (data.tool as string),
        )
      return lastToolIdx < 0 ? -1 : msgs.length - lastToolIdx - 1
    })

    if (targetIndex < 0) return current

    const isError =
      data.result &&
      typeof data.result === 'string' &&
      (data.result.toLowerCase().includes('error') || data.result.toLowerCase().includes('failed'))

    return current.map((m, i) =>
      i === targetIndex
        ? {
            ...m,
            toolResult: data.result as string,
            toolStatus: isError ? 'error' : 'completed',
            toolCallId: toolCallId ?? m.toolCallId,
            subagentSessionKey:
              (data.subagent_session_key as string) ||
              m.subagentSessionKey ||
              ((data.tool as string) === 'spawn'
                ? parseSubagentSessionKey(data.result as string | undefined)
                : undefined),
          }
        : m,
    )
  })
}

function handleSubagentResult(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'subagent.result'))
    return

  ctx.setStreamingMessages((current) => {
    const toolCallId = data.tool_call_id as string | undefined
    const targetIndex = findToolMessageIndex(current, toolCallId, (msgs) => {
      const lastSpawnIdx = [...msgs]
        .reverse()
        .findIndex((m) => m.role === 'tool' && m.toolName === 'spawn')
      return lastSpawnIdx < 0 ? -1 : msgs.length - lastSpawnIdx - 1
    })

    if (targetIndex < 0) return current

    return current.map((m, i) =>
      i === targetIndex
        ? {
            ...m,
            subagentSessionKey: (data.subagent_session_key as string) || m.subagentSessionKey,
            toolResult: m.toolResult || (data.result as string),
          }
        : m,
    )
  })
}

function handleApprovalRequest(ctx: MessageEventContext, event: ClientEvent) {
  ctx.setApprovalRequest(event.data)
}

function handleApproveResult(ctx: MessageEventContext, event: ClientEvent) {
  const resultData = event.data as { request_id: string; approved: boolean; command: string }
  ctx.showApprovalResult(resultData.request_id, resultData.approved, resultData.command)
}

function handleCancelAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  ctx.setToolStatus(null)
  ctx.processingSessionKeyRef.current = null
  ctx.clearAllQueues()

  const cancelledSessionKey = (data.session_key as string) ?? ctx.currentSessionKeyRef.current
  if (cancelledSessionKey) {
    ctx.removeProcessingSession(cancelledSessionKey)
  }

  ctx.setStreamingMessages((current) => current.map((m) => ({ ...m, streaming: false })))
}

function handleSubscribeAck(ctx: MessageEventContext, data: Record<string, unknown>) {
  const ackSessionKey = (data.session_key as string) ?? ''
  const ackProcessing = data.processing === true
  if (ackSessionKey) {
    ctx.syncProcessingSession(ackSessionKey, ackProcessing)
  }
}

function handleMessageError(ctx: MessageEventContext, data: Record<string, unknown>) {
  const errorSessionKey = (getSessionKey(data) ?? ctx.currentSessionKeyRef.current ?? '') as string

  // Rollback optimistic user message from streaming state
  ctx.setStreamingMessages((current) =>
    current.filter((m) => !(m.role === 'user' && m.optimistic && m.sessionKey === errorSessionKey)),
  )

  // Rollback from query cache
  const cached = ctx.queryClient.getQueryData<{ messages?: ChatMessage[] }>(
    chatHistoryQueryKey(errorSessionKey),
  )
  if (cached) {
    ctx.queryClient.setQueryData(chatHistoryQueryKey(errorSessionKey), {
      ...cached,
      messages: (cached.messages ?? []).filter((m) => !(m.role === 'user' && m.optimistic)),
    })
  }

  ctx.removeProcessingSession(errorSessionKey)
  ctx.processingSessionKeyRef.current = null
}

function handleStreamError(ctx: MessageEventContext, data: Record<string, unknown>) {
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

// ── Dispatcher ───────────────────────────────────────────────────────────────

const HANDLERS: Record<string, (ctx: MessageEventContext, event: ClientEvent) => void> = {
  welcome: (ctx, e) => handleWelcome(ctx, e.data as Record<string, unknown>),
  reconnected: (ctx, e) => handleWelcome(ctx, e.data as Record<string, unknown>),
  'message.stream': (ctx, e) => handleMessageStream(ctx, e.data as Record<string, unknown>),
  'message.thinking': (ctx, e) => handleMessageThinking(ctx, e.data as Record<string, unknown>),
  'message.ack': (ctx, e) => handleMessageAck(ctx, e.data as Record<string, unknown>),
  'message.complete': (ctx, e) => handleMessageComplete(ctx, e.data as Record<string, unknown>),
  'history.updated': (ctx, e) => handleHistoryUpdated(ctx, e.data as Record<string, unknown>),
  'messages.catchup': (ctx, e) => handleMessagesCatchup(ctx, e.data as Record<string, unknown>),
  attachments: (ctx, e) => handleAttachments(ctx, e),
  'tool.executing': (ctx, e) => handleToolExecuting(ctx, e.data as Record<string, unknown>),
  'tool.result': (ctx, e) => handleToolResult(ctx, e.data as Record<string, unknown>),
  'subagent.result': (ctx, e) => handleSubagentResult(ctx, e.data as Record<string, unknown>),
  'approval.request': (ctx, e) => handleApprovalRequest(ctx, e),
  'approve.result': (ctx, e) => handleApproveResult(ctx, e),
  'cancel.ack': (ctx, e) => handleCancelAck(ctx, e.data as Record<string, unknown>),
  'subscribe.ack': (ctx, e) => handleSubscribeAck(ctx, e.data as Record<string, unknown>),
  'message.error': (ctx, e) => handleMessageError(ctx, e.data as Record<string, unknown>),
  'stream.error': (ctx, e) => handleStreamError(ctx, e.data as Record<string, unknown>),
}

/**
 * Dispatches a WebSocket client event to the appropriate handler.
 * Each event type maps to a focused, single-responsibility function.
 */
export function dispatchMessageEvent(ctx: MessageEventContext, event: ClientEvent): void {
  const handler = HANDLERS[event.event]
  handler?.(ctx, event)
}
