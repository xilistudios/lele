import {
  createDeterministicToolMessageId,
  createToolMessage,
  createToolMessageId,
  parseSubagentSessionKey,
} from '../../lib/chatMessageBuilder'
import type { ToolStatus } from '../../lib/types'
import { computeToolInsertIndex } from '../messageInsertion'
import { notifySubagentsChanged } from '../useSubagents'
import {
  effectiveSessionKey,
  findToolMessageIndex,
  getSessionKey,
  isSessionMismatch,
} from './helpers'
import type { MessageEventContext } from './types'

export function handleToolExecuting(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'tool.executing')) return

  // Re-tag aliased events with the current key (see effectiveSessionKey in
  // ./helpers): the backend resolves the conversation alias (`base:chat:N`)
  // before publishing tool events too, and useChatHistory filters streaming
  // messages strictly by session key — an aliased key here hides the tool
  // card live until a reload restores it from REST history.
  const sessionKey = effectiveSessionKey(ctx, eventSessionKey)

  ctx.setToolStatus(data as unknown as ToolStatus)

  // A spawn is starting: wake the subagents panel so the new task appears
  // without a chat refresh (the list endpoint is the source of truth).
  if ((data.tool as string) === 'spawn') {
    notifySubagentsChanged()
  }

  upsertToolExecuting(ctx, sessionKey, {
    tool: data.tool as string,
    action: data.action as string | undefined,
    args: data.arguments,
    toolCallId: data.tool_call_id as string | undefined,
    subagentSessionKey: data.subagent_session_key as string | undefined,
  })
}

/**
 * Restores the tool card of a session that is still running a tool, from the
 * `in_progress_tool` payload carried by the welcome / reconnected /
 * subscribe.ack events.
 *
 * The in-flight tool call exists only in the live event stream — it never
 * reaches the message history — so without this the "running tool" card
 * disappeared on every chat reload (page refresh, chat switch, re-subscribe)
 * while the tool kept running (a long sleep, wait_for_subagent, exec...).
 * Idempotent, same as the live path: re-applying the same call updates the
 * existing card instead of duplicating it.
 */
export function restoreInProgressTool(
  ctx: MessageEventContext,
  sessionKey: string,
  data: Record<string, unknown> | null | undefined,
) {
  if (!sessionKey || !data || !(data.tool as string)) return

  ctx.setToolStatus(data as unknown as ToolStatus)

  if ((data.tool as string) === 'spawn') {
    notifySubagentsChanged()
  }

  upsertToolExecuting(ctx, sessionKey, {
    tool: data.tool as string,
    action: data.action as string | undefined,
    args: data.arguments,
    toolCallId: data.tool_call_id as string | undefined,
    subagentSessionKey: data.subagent_session_key as string | undefined,
  })
}

/** Fields shared by the live tool.executing event and the restore payload. */
type ExecutingToolInput = {
  tool: string
  action?: string
  args?: unknown
  toolCallId?: string
  subagentSessionKey?: string
}

/**
 * Upserts the card of a currently executing tool for `sessionKey`. Shared by
 * the live tool.executing handler and the reload restore path so both produce
 * byte-identical cards (same deterministic ids, same insertion point).
 */
function upsertToolExecuting(
  ctx: MessageEventContext,
  sessionKey: string,
  input: ExecutingToolInput,
) {
  const toolCallId = input.toolCallId
  const toolArgsStr = input.args
    ? `${input.tool} ${JSON.stringify(input.args)}`
    : (input.action ?? '')

  const toolMsg = createToolMessage({
    id: toolCallId
      ? createDeterministicToolMessageId('ws', toolCallId)
      : createToolMessageId(input.tool),
    sessionKey,
    toolName: input.tool,
    toolArgs: toolArgsStr,
    toolStatus: 'executing',
    toolCallId,
    subagentSessionKey: input.subagentSessionKey,
  })

  ctx.setStreamingMessages((current) => {
    // When a tool starts executing, the preceding assistant for this session
    // has completed its generation. Mark it as non-streaming.
    const updated = current.map((m) =>
      m.role === 'assistant' && m.streaming && m.sessionKey === toolMsg.sessionKey
        ? { ...m, streaming: false }
        : m,
    )

    if (toolCallId) {
      const existingIdx = updated.findIndex((m) => m.role === 'tool' && m.toolCallId === toolCallId)
      if (existingIdx >= 0) {
        return updated.map((m, i) =>
          i === existingIdx ? { ...m, toolArgs: toolArgsStr, toolStatus: 'executing' as const } : m,
        )
      }
    }
    // Insert tool messages after the last assistant message AND any existing
    // tool messages that follow it, to preserve chronological order within
    // the current LLM iteration (see computeToolInsertIndex).
    const arr = [...updated]
    arr.splice(computeToolInsertIndex(updated), 0, toolMsg)
    return arr
  })
}

export function handleToolResult(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'tool.result')) return

  ctx.setToolStatus(null)

  // Spawn finished registering (or failed): refresh so the panel picks up
  // status / session_key without waiting for the poll interval.
  if ((data.tool as string) === 'spawn') {
    notifySubagentsChanged()
  }

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

export function handleSubagentResult(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'subagent.result'))
    return

  notifySubagentsChanged()

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
