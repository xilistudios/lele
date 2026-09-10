/**
 * Custom slash-command event handler.
 *
 * `command.applied` is emitted by the backend (pkg/harness) when a user-defined
 * slash command — e.g. "/review" from ~/.lele/commands/review.md — is expanded
 * into the prompt that is actually sent to the LLM. The frontend has no other
 * way to know the turn was command-driven, so it renders a tool-like card.
 *
 * The card is a normal `role: 'tool'` streaming message with `toolName:
 * 'command'` and `toolStatus: 'completed'` (the command already ran; there is
 * no "executing" phase and no matching result event), which is exactly how tool
 * events persist in the stream. Message construction is delegated to the pure
 * helpers below so the payload→message mapping is unit-testable without a DOM.
 *
 * The card builder lives in lib/chatMessageBuilder (buildCommandChipToolArgs)
 * because the SAME card is re-rendered from chat history (the persisted
 * `harness_command` field) once this ephemeral streaming copy is gone — one
 * serializer keeps the live chip and the history chip pixel-identical.
 */
import {
  COMMAND_TOOL_NAME,
  buildCommandChipToolArgs,
  createToolMessage,
  createToolMessageId,
  normalizeCommandArgs,
} from '../../lib/chatMessageBuilder'
import type { ChatMessage, WSCommandAppliedPayload } from '../../lib/types'
import { computeToolInsertIndex } from '../messageInsertion'
import { getSessionKey, isSessionMismatch } from './helpers'
import type { MessageEventContext } from './types'

/** Tool name used for the card; ToolCallDisplay maps it to the ⚡ icon/label. */
export { COMMAND_TOOL_NAME }

/**
 * Normalize the raw WS payload (all fields optional on the wire) into the
 * `arguments` object rendered by ToolCallDisplay. The command name gains its
 * leading slash here because the backend sends it bare.
 */
export function buildCommandAppliedArgs(
  data: Partial<WSCommandAppliedPayload>,
): Record<string, string> {
  return normalizeCommandArgs({ command: data.command ?? '' })
}

/**
 * Serialized `toolArgs` string for the card, following the `"toolName {json}"`
 * convention used by handleToolExecuting and formatToolCallArgs. The WS payload
 * is structurally a HistoryCommandInfo (all string fields), so it feeds the
 * shared builder directly.
 */
export function buildCommandAppliedToolArgs(data: Partial<WSCommandAppliedPayload>): string {
  return buildCommandChipToolArgs({
    command: data.command ?? '',
    args: data.args,
    agent: data.agent,
    model: data.model,
    source: data.source,
    description: data.description,
  })
}

/** Pure payload → ChatMessage mapping (exported for tests). */
export function buildCommandAppliedMessage(
  data: Partial<WSCommandAppliedPayload>,
  sessionKey: string,
): ChatMessage {
  return createToolMessage({
    id: createToolMessageId(COMMAND_TOOL_NAME),
    sessionKey,
    toolName: COMMAND_TOOL_NAME,
    toolArgs: buildCommandAppliedToolArgs(data),
    toolStatus: 'completed',
  })
}

export function handleCommandApplied(ctx: MessageEventContext, data: Record<string, unknown>) {
  const eventSessionKey = getSessionKey(data)
  if (isSessionMismatch(eventSessionKey, ctx.currentSessionKeyRef.current, 'command.applied'))
    return

  const toolMsg = buildCommandAppliedMessage(
    data as unknown as WSCommandAppliedPayload,
    (eventSessionKey ?? ctx.currentSessionKeyRef.current ?? undefined) as string,
  )

  ctx.setStreamingMessages((current) => {
    // Same rule as tool.executing: the assistant turn this command triggered is
    // over (or about to be), so it must not stay marked as streaming.
    const updated = current.map((m) =>
      m.role === 'assistant' && m.streaming && m.sessionKey === toolMsg.sessionKey
        ? { ...m, streaming: false }
        : m,
    )

    // Append after the last assistant and any tools already trailing it so the
    // card keeps chronological order inside the iteration.
    const arr = [...updated]
    arr.splice(computeToolInsertIndex(updated), 0, toolMsg)
    return arr
  })
}
