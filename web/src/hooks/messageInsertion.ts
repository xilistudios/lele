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

/**
 * How many items were inserted *before* the previously-first message.
 *
 * Used by MessageList to shift Virtuoso's `firstItemIndex` after a loadMore
 * prepend. Detecting prepends by "first id changed AND length grew" misfires
 * when a history rebuild rewrites ids or when a stream append coincides with
 * an id change — Virtuoso then shifts by the full length delta and the
 * viewport jumps / old messages flicker.
 *
 * The correct signal is the new position of the old first item: if it moved
 * from index 0 to index N, exactly N items were prepended. Returns 0 when the
 * previous first key is unknown or no longer present (id rewrite without a
 * real prepend).
 */
export function countPrependedItems(prevFirstKey: string | undefined, nextKeys: string[]): number {
  if (!prevFirstKey) return 0
  const idx = nextKeys.indexOf(prevFirstKey)
  return idx > 0 ? idx : 0
}

/**
 * Next value of Virtuoso's `firstItemIndex` after a list change — the guarded
 * form of `firstItemIndex - countPrependedItems(...)`.
 *
 * WHY this exists (the contract it protects): Virtuoso's virtual list is a
 * monotonic counter. `firstItemIndex + data.length` is the number of items
 * EVER rendered, and to prepend items without the viewport jumping you move
 * `firstItemIndex` BACKWARD — exactly as far back as the number of items that
 * were inserted BEFORE the previously-first item, and no further:
 *
 *   - Over-shifting (decrementing by more than was prepended) breaks the
 *     counter: the items ever rendered no longer adds up, so scroll offsets
 *     drift and rows land in the wrong place after a loadMore.
 *   - Under-shifting (decrementing by less) leaves the anchor pointing at the
 *     wrong row, so the viewport visibly jumps once the prepended page paints.
 *   - Shifting forward (a positive delta) is never allowed: `firstItemIndex`
 *     may only move into the PAST.
 *
 * That is why the shift is measured by `countPrependedItems` on the RENDERED
 * list (`displayMessages`, i.e. the post-collapse output of
 * `lib/thinkingRows.ts`) and NOT on the merge output: a prepended page that
 * holds collapsible thinking rows grows the merge output by MORE items than
 * the rendered list (a run of 3 bare thinking rows renders as 1), so counting
 * merge items would over-shift by one per collapsed run.
 *
 * Returns `firstItemIndex` unchanged when nothing was prepended (0 delta) —
 * going through this helper guarantees the index only ever moves backward. When
 * a shift IS due, a `firstItemIndex` already at (or below) zero is clamped to 0
 * instead of handing Virtuoso a negative index; that clamp can only trigger on
 * an input already outside the contract, so it cannot mask a real bug.
 */
export function nextFirstItemIndex(
  firstItemIndex: number,
  prevFirstKey: string | undefined,
  nextKeys: string[],
): number {
  const prepended = countPrependedItems(prevFirstKey, nextKeys)
  if (prepended <= 0) return firstItemIndex
  return Math.max(0, firstItemIndex - prepended)
}
