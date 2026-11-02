import type { ChatMessage } from './types'

/**
 * Collapsing of adjacent "Thinking…" cards in the chat log — a RENDER-level
 * concern, reversible and lossless.
 *
 * WHY: `MessageBubble` draws ONE bordered thinking card per assistant message
 * that carries `reasoningContent` (the `hasThinking` block). The backend
 * persists EVERY LLM iteration as its own assistant message
 * (`pkg/agent/llm_runner.go` → `AddFullMessage`), and several of those
 * iterations legitimately carry reasoning but no visible text: discarded
 * "empty response" retries, the malformed-tool-call guidance path, and the
 * tool-call step of any model that emits no text alongside the call. Each one
 * renders as a bare card, so a single turn can stack several "Thinking…" boxes
 * with nothing between them — the reported "los bloques de thinking aparecen
 * consecutivos".
 *
 * The same shape also shows up transiently, when the live copy and its
 * persisted twin coexist for one render (or a retry segment is re-streamed
 * after a reconnect). This module deliberately does not care about the reason
 * for the adjacency: it only sees rows, so every cause is covered by the same
 * rule and the fix keeps working if the merge changes again.
 *
 * WHAT IT IS NOT: a merge/dedup fix. Nothing here mutates the message list,
 * the merge or the WS handlers. The collapsed row keeps the FIRST row's `id`,
 * `createdAt` and every other field; only `reasoningContent`, `streaming` and
 * `stableId` are derived. Remounting `MessageList` without this pass (or
 * feeding it `visibleMessages` directly) reproduces today's output exactly.
 */

/**
 * True when a row renders as a bare thinking card — nothing else.
 *
 * This MIRRORS the visibility rules of `MessageBubble` on purpose: collapsing
 * is only safe for rows that would otherwise show a single thinking card and
 * nothing besides it. If the bubble grows another visible branch (a banner, a
 * status chip), this predicate must be revisited instead of silently joining
 * rows that did have visible content.
 *
 * - `role === 'assistant'`: only assistant rows render the thinking card.
 * - `content.trim() === ''`: any visible text means the row is prose, not just
 *   activity (raw truthiness is not enough — ' ' and '\n' are truthy yet
 *   invisible, the same trap `MessageBubble` documents).
 * - `reasoningContent.trim() !== ''`: no card without reasoning.
 * - no attachments: an attachment chip is visible content.
 * - `!excludeFromContext`: that flag renders an "archived from context" banner,
 *   so the row is not a bare card.
 */
export function isThinkingOnlyRow(m: ChatMessage): boolean {
  if (m.role !== 'assistant') return false
  if (m.content.trim() !== '') return false
  if ((m.reasoningContent ?? '').trim() === '') return false
  if ((m.attachments?.length ?? 0) !== 0) return false
  if (m.excludeFromContext) return false
  return true
}

/**
 * Merge the rows `messages[start, end)` — a run of 2+ thinking-only rows — into
 * the single card they will render as.
 *
 * Consecutive BYTE-IDENTICAL texts collapse to a single copy. That is the
 * duplicate-live-twin case: when the live copy and the persisted copy of one
 * iteration are both rendered, joining them verbatim would print the same
 * paragraph twice — worse than the stacked cards this module removes.
 *
 * Texts are trimmed before comparing and joining (the bubble renders
 * `whitespace-pre-wrap`, so stray leading/trailing blank lines would show up as
 * real vertical space), and texts that are empty after trimming are dropped —
 * they can only exist on the *first* row, since `isThinkingOnlyRow` already
 * required the others to be non-empty.
 */
function collapseRun(messages: ChatMessage[], start: number, end: number): ChatMessage {
  const first = messages[start]
  const texts: string[] = []
  let streaming = false
  for (let i = start; i < end; i++) {
    if (messages[i].streaming === true) streaming = true
    const text = (messages[i].reasoningContent ?? '').trim()
    if (text === '') continue
    if (texts.length > 0 && texts[texts.length - 1] === text) continue
    texts.push(text)
  }
  return {
    ...first,
    reasoningContent: texts.join('\n\n'),
    streaming,
    stableId: first.stableId ?? first.id,
  }
}

/**
 * Replace every maximal run of 2+ adjacent thinking-only rows with a single
 * row, and return the input unchanged when nothing needs collapsing.
 *
 * Rules, all chosen so the pass is invisible to every other part of the list:
 *
 * - Only rows for which `isThinkingOnlyRow` is true can enter a run, so a run
 *   never spans a user bubble, a tool card or an assistant row with prose.
 * - The collapsed row STARTS FROM THE FIRST row of the run: its `id`,
 *   `createdAt`, `role` and any other field survive verbatim, so anything keyed
 *   on message identity (stableId registry, subagent links, retry) still sees
 *   the message it saw before.
 * - `stableId` is pinned to `first.stableId ?? first.id`, so the React and
 *   Virtuoso render keys stay identical pre/post collapse and the row is not
 *   remounted (which would replay the enter animation mid-scroll).
 * - `streaming` is OR-ed over the run: a turn whose last reasoning iteration
 *   has not finished yet must still show the pulsing dot and keep its card
 *   auto-open.
 * - A run of length 1 returns the SAME object, and a list with no run returns
 *   the SAME array, so `useMemo` memoisation downstream is not defeated by
 *   fresh identities that would re-render every bubble on each stream tick.
 *
 * O(n) in a single pass with one allocation of the filtered array.
 */
export function collapseThinkingRuns(messages: ChatMessage[]): ChatMessage[] {
  let collapsed: ChatMessage[] | null = null
  let i = 0
  while (i < messages.length) {
    // Not a thinking-only row (prose, tool card, user bubble, attachment, …):
    // pass it through. It is also the wall that ends a run on either side.
    if (!isThinkingOnlyRow(messages[i])) {
      if (collapsed) collapsed.push(messages[i])
      i++
      continue
    }

    // Maximal run of thinking-only rows starting at `i`: `end` is the index of
    // the first row that does not belong to it.
    let end = i + 1
    while (end < messages.length && isThinkingOnlyRow(messages[end])) end++

    if (end - i === 1) {
      // A lone thinking row is not a run: pass it through (same object) and do
      // NOT materialise the output array, so a list without a run of 2+ keeps
      // its original identity all the way through and downstream `useMemo`s
      // stay valid.
      if (collapsed) collapsed.push(messages[i])
      i = end
      continue
    }

    // Materialise the output array lazily: the first real run copies the rows
    // before it, and everything after that is pushed in order below.
    if (!collapsed) collapsed = messages.slice(0, i)

    collapsed.push(collapseRun(messages, i, end))

    i = end
  }

  return collapsed ?? messages
}
