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

export { COMMAND_TOOL_NAME }

export function buildCommandAppliedArgs(
  data: Partial<WSCommandAppliedPayload>,
): Record<string, string> {
  return normalizeCommandArgs({ command: data.command ?? '' })
}

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
    const updated = current.map((m) =>
      m.role === 'assistant' && m.streaming && m.sessionKey === toolMsg.sessionKey
        ? { ...m, streaming: false }
        : m,
    )

    const arr = [...updated]
    arr.splice(computeToolInsertIndex(updated), 0, toolMsg)
    return arr
  })
}
