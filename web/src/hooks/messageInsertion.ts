/**
 * Pure insertion-ordering rules for streaming messages.
 *
 * As WebSocket events arrive out of order (acks delayed, tools before
 * stream chunks, multiple assistants per turn), we need deterministic rules
 * for WHERE a new message lands so the rendered list stays chronological.
 *
 * These rules are shared by the stream-queue (assistant placeholders) and the
 * tool handlers, and are unit-tested directly in messageOrdering.test.ts.
 */
import type { ChatMessage } from '../lib/types'

/**
 * Compute the index at which a new assistant message should be inserted into
 * the current streaming message array so it lands in the correct chronological
 * position relative to user messages, prior assistants, and tools.
 *
 * Case 1 — user message is AFTER the last assistant:
 *   e.g. [a1-completed, u2-opt] — a1 was retained from a previous turn (HTTP
 *   cache hasn't caught up yet). The new assistant responds to u2, so it must
 *   go AFTER u2, not after a1.
 *
 * Case 2 — last assistant is AFTER the last user (or no user exists):
 *   e.g. [u1-opt, a1, t1] — the new assistant is a continuation of the same turn
 *   (post-tool-call response). Insert after the last assistant and its trailing
 *   tools.
 *
 * Case 3 — no assistant exists:
 *   e.g. [t1, t2] — tools arrived before message.stream (ack delayed). Insert
 *   BEFORE the first tool so order is: assistant → tool calls.
 */
export function computeAssistantInsertIndex(current: ChatMessage[]): number {
  const lastUserRevIdx = [...current].reverse().findIndex((m) => m.role === 'user')
  const lastAsstRevIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  const lastUserPos = lastUserRevIdx >= 0 ? current.length - 1 - lastUserRevIdx : -1
  const lastAsstPos = lastAsstRevIdx >= 0 ? current.length - 1 - lastAsstRevIdx : -1

  if (lastUserPos > lastAsstPos) {
    // User message is after the last assistant — insert right after it
    return lastUserPos + 1
  }

  if (lastAsstPos >= 0) {
    // Continuation of the current turn — insert after assistant + its tools
    let insertIdx = lastAsstPos + 1
    while (insertIdx < current.length && current[insertIdx].role === 'tool') {
      insertIdx++
    }
    return insertIdx
  }

  // No assistant exists — insert before all tool messages
  const firstToolIdx = current.findIndex((m) => m.role === 'tool')
  return firstToolIdx >= 0 ? firstToolIdx : current.length
}

/**
 * Compute the index at which a new tool message should be inserted.
 *
 * Tools belong to the current LLM iteration, so they go AFTER the last
 * assistant message AND any tool messages already trailing it. Inserting
 * right after the assistant (before existing tools) would produce
 * reverse-chronological order when multiple tools fire in one iteration.
 *
 * When no assistant exists yet, append at the end.
 */
export function computeToolInsertIndex(current: ChatMessage[]): number {
  const lastAsstRevIdx = [...current].reverse().findIndex((m) => m.role === 'assistant')
  if (lastAsstRevIdx < 0) return current.length

  const assistantIdx = current.length - 1 - lastAsstRevIdx
  let insertIdx = assistantIdx + 1
  while (insertIdx < current.length && current[insertIdx].role === 'tool') {
    insertIdx++
  }
  return insertIdx
}
