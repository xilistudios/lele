import type { QueryClient } from '@tanstack/react-query'
import {
  createAssistantMessage,
  createDeterministicToolMessageId,
  createToolMessage,
  createToolMessageId,
  parseSubagentSessionKey,
} from '../lib/chatMessageBuilder'
import { wsDebug } from '../lib/debug'
import type {
  ChatMessage,
  GroupInfo,
  GroupSnapshot,
  GroupToolCall,
  GroupTurn,
  HistoryToolCall,
  ToolStatus,
} from '../lib/types'
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
  upsertGroup: (groupId: string, updater: (existing: GroupInfo | undefined) => GroupInfo) => void
  hydrateGroups: (infos: GroupInfo[]) => void
  markActiveGroupsStopped: () => void
  setGroupsEnabled: (enabled: boolean) => void
}

// ── Helpers ──────────────────────────────────────────────────────────────────

/** Convert a GroupSnapshot (from WS/HTTP) into the internal GroupInfo shape. */
export function snapshotToGroupInfo(s: GroupSnapshot): GroupInfo {
  return {
    groupID: s.group_id,
    status: s.status,
    strategy: s.strategy,
    participants: s.participants,
    layers: s.layers,
    totalTokens: s.total_tokens,
    createdAt: s.created_at || new Date().toISOString(),
    synthesis: s.synthesis || undefined,
    turns: s.turns.map((t) => ({
      groupID: s.group_id,
      speaker: t.speaker,
      label: t.label,
      role: t.role,
      layer: t.layer,
      turnIndex: t.turn_index,
      content: t.content,
      toolCalls: t.tool_calls,
    })),
  }
}

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

  // Mark any active groups as stopped — without this, groups with status 'started'
  // would remain 'started' forever and the processing indicator would never clear.
  ctx.markActiveGroupsStopped()
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

// ── Group Chat (MoA) Handlers ───────────────────────────────────────────────

function handleGroupStatus(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.status')) return

  const groupId = data.group_id as string
  const status = data.status as GroupInfo['status']
  const participants = (data.participants as string) ?? ''

  if (eventSessionKey) {
    if (status === 'started') {
      ctx.addProcessingSession(eventSessionKey)
    } else if (status === 'done' || status === 'error' || status === 'stopped') {
      ctx.removeProcessingSession(eventSessionKey)
    }
  }

  ctx.upsertGroup(groupId, (existing) => ({
    groupID: groupId,
    status,
    strategy: existing?.strategy ?? '',
    participants,
    layers: existing?.layers ?? 0,
    totalTokens: existing?.totalTokens ?? 0,
    createdAt: existing?.createdAt ?? new Date().toISOString(),
    turns: existing?.turns ?? [],
    synthesis: existing?.synthesis,
  }))
}

function handleGroupTurn(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.turn')) return

  const groupId = data.group_id as string
  const incomingTurn: GroupTurn = {
    groupID: groupId,
    speaker: data.speaker as string,
    label: data.label as string,
    role: data.role as GroupTurn['role'],
    layer: data.layer as number,
    turnIndex: data.turn_index as number,
    content: data.content as string,
  }

  ctx.upsertGroup(groupId, (existing) => {
    const turns = existing?.turns ? [...existing.turns] : []
    // Deduplicate by turn_index — replace if same index exists, preserving existing toolCalls
    const existingIdx = turns.findIndex((t) => t.turnIndex === incomingTurn.turnIndex)
    if (existingIdx >= 0) {
      turns[existingIdx] = {
        ...incomingTurn,
        toolCalls: turns[existingIdx].toolCalls ?? incomingTurn.toolCalls,
      }
    } else {
      turns.push(incomingTurn)
    }
    return {
      groupID: groupId,
      status: existing?.status ?? 'started',
      strategy: existing?.strategy ?? '',
      participants: existing?.participants ?? '',
      layers: Math.max(existing?.layers ?? 0, incomingTurn.layer + 1),
      totalTokens: existing?.totalTokens ?? 0,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
      turns,
      synthesis: existing?.synthesis,
    }
  })
}

function handleGroupTool(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.tool')) return

  const groupId = data.group_id as string
  const turnIndex = data.turn_index as number
  const toolCallId = data.tool_call_id as string
  const toolName = data.tool as string
  const status = data.status as GroupToolCall['status']
  const args = data.arguments as string | undefined
  const result = data.result as string | undefined

  ctx.upsertGroup(groupId, (existing) => {
    const turns = existing?.turns ? [...existing.turns] : []
    let targetIdx = turns.findIndex((t) => t.turnIndex === turnIndex)

    // If turn not found, push a placeholder
    if (targetIdx < 0) {
      turns.push({
        groupID: groupId,
        speaker: data.speaker as string,
        label: (data.label as string) || (data.speaker as string),
        role: 'proposer',
        layer: data.layer as number,
        turnIndex,
        content: '',
        toolCalls: [],
      })
      targetIdx = turns.length - 1
    }

    const turn = turns[targetIdx]
    const toolCalls = turn.toolCalls ? [...turn.toolCalls] : []
    const existingTcIdx = toolCalls.findIndex((tc) => tc.tool_call_id === toolCallId)

    const updatedTc: GroupToolCall = {
      tool_call_id: toolCallId,
      tool: toolName,
      status,
      // Preserve prior arguments if incoming event has none
      arguments: args ?? (existingTcIdx >= 0 ? toolCalls[existingTcIdx].arguments : undefined),
      result,
    }

    if (existingTcIdx >= 0) {
      toolCalls[existingTcIdx] = updatedTc
    } else {
      toolCalls.push(updatedTc)
    }

    turns[targetIdx] = { ...turn, toolCalls }

    return {
      groupID: groupId,
      status: existing?.status ?? 'started',
      strategy: existing?.strategy ?? '',
      participants: existing?.participants ?? '',
      layers: existing?.layers ?? 0,
      totalTokens: existing?.totalTokens ?? 0,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
      turns,
      synthesis: existing?.synthesis,
    }
  })
}

function handleGroupComplete(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'group.complete')) return

  if (eventSessionKey) {
    ctx.removeProcessingSession(eventSessionKey)
  }

  const groupId = data.group_id as string
  const content = data.content as string

  ctx.upsertGroup(groupId, (existing) => ({
    groupID: groupId,
    status: 'done',
    strategy: (data.strategy as string) ?? existing?.strategy ?? '',
    participants: existing?.participants ?? '',
    layers: (data.layers as number) ?? existing?.layers ?? 0,
    totalTokens: (data.total_tokens as number) ?? existing?.totalTokens ?? 0,
    createdAt: existing?.createdAt ?? new Date().toISOString(),
    turns: existing?.turns ?? [],
    synthesis: content,
  }))
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
  'group.status': (ctx, e) => handleGroupStatus(ctx, e.data as Record<string, unknown>),
  'group.turn': (ctx, e) => handleGroupTurn(ctx, e.data as Record<string, unknown>),
  'group.tool': (ctx, e) => handleGroupTool(ctx, e.data as Record<string, unknown>),
  'group.complete': (ctx, e) => handleGroupComplete(ctx, e.data as Record<string, unknown>),
}

/**
 * Dispatches a WebSocket client event to the appropriate handler.
 * Each event type maps to a focused, single-responsibility function.
 */
export function dispatchMessageEvent(ctx: MessageEventContext, event: ClientEvent): void {
  const handler = HANDLERS[event.event]
  handler?.(ctx, event)
}
