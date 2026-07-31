/**
 * Tool execution event handlers.
 *
 * Handles tool lifecycle events: execution start, result delivery,
 * and subagent result integration into the streaming message list.
 */
import {
  createDeterministicToolMessageId,
  createToolMessage,
  createToolMessageId,
  parseSubagentSessionKey,
} from '../../lib/chatMessageBuilder'
import type { ToolStatus } from '../../lib/types'
import { findToolMessageIndex, getSessionKey, isSessionMismatch } from './helpers'
import type { MessageEventContext } from './types'

export function handleToolExecuting(ctx: MessageEventContext, data: Record<string, unknown>) {
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

export function handleToolResult(ctx: MessageEventContext, data: Record<string, unknown>) {
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

export function handleSubagentResult(ctx: MessageEventContext, data: Record<string, unknown>) {
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
