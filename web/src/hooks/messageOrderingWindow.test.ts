/**
 * REGRESSION SUITE — WebUI message ordering under a saturated sliding window.
 *
 * This suite pins the user-reported WebUI bug: once a conversation exceeds 50
 * messages, the history shown is a *sliding window of the last 50* and the
 * merge of HTTP base history with live WebSocket state strands an ever-growing
 * tail of "immortal" optimistic user bubbles AFTER newer assistant answers.
 * Only a page refresh (which clears `streamingMessages`) restores order.
 *
 * The mechanism involves three files working together:
 *
 *  - `pkg/channels/rest_chat.go` serves history as a sliding window of the
 *    last `limit` messages (`resultStartIdx := endIdx - limit`, default 50).
 *  - `web/src/hooks/useChatHistory.ts` `queryFn` only merges the new window
 *    with previously-cached older messages when
 *    `cachedData.messages.length > DEFAULT_LIMIT` (DEFAULT_LIMIT = 50);
 *    otherwise it REPLACES the cache with the new window. Once the
 *    conversation exceeds 50 messages the cached count sits at exactly 50,
 *    the window slides on every poll and the cached NON-OPTIMISTIC user count
 *    SATURATES (e.g. it stays at 25 forever).
 *  - `web/src/hooks/messageMerge.ts` `filterStreamingLeftovers` drops an
 *    optimistic user only when BOTH hold: (1) its content matches a
 *    non-optimistic base user, AND (2) `baseUserCount > optimisticBaseCount`.
 *    `optimisticBaseCount` is snapshotted at send time (see `sendMessage` in
 *    `useMessages`) via `getHistoryUserCount()`; under saturation it is also
 *    25, so `25 > 25` is false FOREVER → the optimistic bubble is immortal.
 *    `mergeMessages` then appends every unmatched streaming leftover at the
 *    END of the list — i.e. after newer answers.
 *
 * Secondary defects pinned here (D2–D4, all proven against current
 * `mergeMessages` behavior):
 *
 *   D2 — in `buildFilteredBase`, when the candidate at `streamAsstIdx` IS
 *        streaming it is not matched AND `streamAsstIdx` is NOT advanced, so
 *        that one candidate blocks positional matching for EVERY later base
 *        assistant. All later completed streaming assistants become
 *        leftovers appended at the end → duplicated answers at the bottom.
 *   D3 — positional matching never verifies content, so a stale leftover from
 *        an old turn can be paired with an unrelated recent base assistant
 *        and STEAL its React key (`stableId`).
 *   D4 — `mergeMessages` could emit two entries with the same render key
 *        (`stableId ?? id`), which makes React/Virtuoso mis-render or drop a
 *        bubble. Pinned with DISTINCT content sharing one `stableId` (key
 *        uniqueness only — repeated identical content is legitimate and must
 *        NOT be deduplicated); the `enforceUniqueRenderKeys` guard on the
 *        final merged list now normalizes any collision.
 *
 * This suite was written RED as a regression pin before each fix landed; all
 * defects D2–D4 are now fixed and this suite must stay green as their guard.
 * Do NOT weaken the assertions. The companion suite of already-fixed bugs
 * lives in `messageOrdering.test.ts`.
 *
 * Debug aid: run `DEBUG_ORDER=1 bun test src/hooks/messageOrderingWindow.test.ts`
 * to print each merged list as a compact `role:content` sequence.
 */
import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { mergeMessages } from './messageMerge'

// ── Factories (real ChatMessage field names only — see lib/types.ts) ────────

type MsgOpts = Partial<ChatMessage>

const SESSION_KEY = 'test-session'
const NOW = '2026-01-01T00:00:00.000Z'

let autoSeq = 0
const autoId = (prefix: string) => `${prefix}${++autoSeq}`

function makeUser(id: string, content: string, opts: MsgOpts = {}): ChatMessage {
  return {
    id,
    role: 'user',
    content,
    streaming: false,
    createdAt: NOW,
    sessionKey: SESSION_KEY,
    ...opts,
  }
}

function makeAssistant(id: string, content: string, opts: MsgOpts = {}): ChatMessage {
  return {
    id,
    role: 'assistant',
    content,
    streaming: false,
    createdAt: NOW,
    sessionKey: SESSION_KEY,
    ...opts,
  }
}

function makeTool(id: string, toolCallId: string, opts: MsgOpts = {}): ChatMessage {
  return {
    id,
    role: 'tool',
    content: '',
    streaming: false,
    createdAt: NOW,
    sessionKey: SESSION_KEY,
    toolCallId,
    ...opts,
  }
}

/** Short hands used across the scenarios (id auto-generated unless given). */
const user = (content: string, opts: MsgOpts = {}) =>
  makeUser(opts.id ?? autoId('u'), content, opts)
const assistant = (content: string, opts: MsgOpts = {}) =>
  makeAssistant(opts.id ?? autoId('a'), content, opts)
const tool = (toolCallId: string, opts: MsgOpts = {}) =>
  makeTool(opts.id ?? autoId('t'), toolCallId, opts)
const u = user
const a = assistant
const optimisticUser = (content: string, opts: MsgOpts = {}) =>
  user(content, { ...opts, optimistic: true })
const completedAssistant = (content: string, opts: MsgOpts = {}) =>
  assistant(content, { ...opts, streaming: false })

/**
 * Alternating user/assistant tail of a long conversation:
 * `question number n` / `answer number n` for n in [from, to] (2 msgs per n).
 */
function makeSaturatedBase(from: number, to: number): ChatMessage[] {
  const msgs: ChatMessage[] = []
  for (let n = from; n <= to; n++) {
    msgs.push(makeUser(`b_q${n}`, `question number ${n}`))
    msgs.push(makeAssistant(`b_a${n}`, `answer number ${n}`))
  }
  return msgs
}

// ── Shared invariant ────────────────────────────────────────────────────────

/** Render key React/Virtuoso will use (mirrors MessageList.messageKey). */
const renderKey = (m: ChatMessage) => m.stableId ?? m.id

/** Compact `role:content` rendering used in assertion messages / debug. */
const compact = (list: ChatMessage[]) => list.map((m) => `${m.role}:${m.content}`)

/** Print the merged sequence only when DEBUG_ORDER is set (kept lint-clean). */
function dbg(label: string, merged: ChatMessage[]): void {
  if (process.env.DEBUG_ORDER) {
    console.log(`[${label}] ${compact(merged).join(' → ')}`)
  }
}

/**
 * Assert the merged list has no duplicated logical message and no duplicated
 * React key:
 *
 *   (a) no two entries share the same `renderKey`;
 *   (b) no two entries share the same logical signature
 *       `${role}|${content}|${toolCallId ?? ''}` for NON-EMPTY content
 *       (empty-content tool/placeholder entries are exempt);
 *   (c) no optimistic user message remains when the base already contains a
 *       confirmed (non-optimistic) copy of its content.
 *
 * Note for (c): `filteredBase` is `baseMessages` passed through, so a
 * confirmed copy visible inside `merged` implies the base contained it.
 */
function expectSaneOrder(merged: ChatMessage[]) {
  // (a) unique React render keys.
  const keyCounts = new Map<string, number>()
  for (const m of merged) {
    const k = renderKey(m)
    keyCounts.set(k, (keyCounts.get(k) ?? 0) + 1)
  }
  const dupKeys = [...keyCounts.entries()].filter(([, n]) => n > 1).map(([k, n]) => `${k} (x${n})`)
  expect(dupKeys).toEqual([])

  // (b) unique logical signatures for non-empty content.
  const sigCounts = new Map<string, number>()
  for (const m of merged) {
    if (m.content.trim() === '') continue
    const sig = `${m.role}|${m.content}|${m.toolCallId ?? ''}`
    sigCounts.set(sig, (sigCounts.get(sig) ?? 0) + 1)
  }
  const dupSigs = [...sigCounts.entries()]
    .filter(([, n]) => n > 1)
    .map(([sig, n]) => `${sig} (x${n})`)
  expect(dupSigs).toEqual([])

  // (c) no immortal optimistic user whose confirmed twin is already rendered.
  const confirmedContents = merged
    .filter((m) => m.role === 'user' && !m.optimistic)
    .map((m) => m.content)
  const immortal = merged.filter(
    (m) =>
      m.role === 'user' &&
      m.optimistic === true &&
      m.content.length > 0 &&
      confirmedContents.some((c) => c.startsWith(m.content.slice(0, 200))),
  )
  expect(compact(immortal)).toEqual([])
}

// ── Scenarios ───────────────────────────────────────────────────────────────

describe('Message ordering window regression (saturated 50-message sliding window)', () => {
  test('R1: saturated 50-message window — pending bubble keeps its chronological slot, not stranded at the end', () => {
    // Base = exact last-50 window of a 50-message conversation: 25 non-opt
    // users (q1..q25) + 25 assistants — user count saturated at 25.
    const base = makeSaturatedBase(1, 25)
    expect(base.length).toBe(50)

    // Turn 26: optimistic send (snapshot = 25; production sendMessage records
    // the last confirmed base user at send time → b_q25) + completed answer
    // via WS. The base does NOT yet contain question number 26.
    const streaming = [
      optimisticUser('question number 26', {
        id: 's_q26',
        optimisticBaseCount: 25,
        optimisticAnchorId: 'b_q25',
      }),
      completedAssistant('answer number 26', { id: 's_a26' }),
    ]

    const merged = mergeMessages(base, streaming)
    dbg('R1', merged)

    // The sent question must be rendered exactly once…
    expect(merged.filter((m) => m.content === 'question number 26').length).toBe(1)
    // The pending bubble keeps its chronological slot: immediately before its
    // own answer, never stranded after it.
    const iQ = merged.findIndex((m) => m.content === 'question number 26')
    const iA = merged.findIndex((m) => m.content === 'answer number 26')
    expect(iQ).toBeGreaterThanOrEqual(0)
    expect(iA).toBe(iQ + 1)
    // Still unconfirmed (base has no copy yet) → still carries the pending flag.
    expect(merged[iQ]?.optimistic).toBe(true)
    // Nothing may be stranded after the newest answer.
    expect(merged[merged.length - 1]?.content).toBe('answer number 26')
    expectSaneOrder(merged)
  })

  test('R2: saturation accumulates one immortal optimistic user per turn', () => {
    // True last-50 window after turns 26/27/28 landed: turns q4..q28 →
    // exactly 25 non-optimistic users (saturated at 25) including the
    // confirmed copies of q26, q27, q28 and their answers, while every
    // send-time snapshot optimisticBaseCount is also 25 (`25 > 25` = false).
    const base = makeSaturatedBase(4, 28)
    expect(base.length).toBe(50)
    expect(base.filter((m) => m.role === 'user').length).toBe(25)

    const optQ = (n: number, anchorId: string) =>
      optimisticUser(`question number ${n}`, {
        id: `s_q${n}`,
        optimisticBaseCount: 25,
        optimisticAnchorId: anchorId,
      })
    const doneA = (n: number) => completedAssistant(`answer number ${n}`, { id: `s_a${n}` })
    // Each send records the last confirmed base user at ITS send moment:
    // turn 26 saw b_q25, turn 27 saw b_q26, turn 28 saw b_q27 — all three
    // anchors are still inside the current window.
    const streaming = [
      optQ(26, 'b_q25'),
      doneA(26),
      optQ(27, 'b_q26'),
      doneA(27),
      optQ(28, 'b_q27'),
      doneA(28),
    ]

    const merged = mergeMessages(base, streaming)
    dbg('R2', merged)

    // No optimistic bubble may survive: base already holds the confirmed
    // copies of all three questions.
    expect(compact(merged.filter((m) => m.optimistic === true))).toEqual([])
    // Each question appears exactly once (the base copy — not base + leftover).
    for (const n of [26, 27, 28]) {
      expect(merged.filter((m) => m.content === `question number ${n}`).length).toBe(1)
    }
    expectSaneOrder(merged)
  })

  test('R2b: anchored confirmation survives the window slide (pending → confirmed)', () => {
    // Turn 29 sent against the saturated window of turns 4..28 (anchor =
    // last confirmed user at send time, b_q28 — still inside the window).
    const streaming = [
      optimisticUser('question number 29', {
        id: 's_q29',
        optimisticBaseCount: 25,
        optimisticAnchorId: 'b_q28',
      }),
      completedAssistant('answer number 29', { id: 's_a29' }),
    ]

    // Phase A — HTTP history has NOT caught up: the base still ends at turn
    // 28, so question number 29 is genuinely pending.
    const baseA = makeSaturatedBase(4, 28)
    expect(baseA.length).toBe(50)
    expect(baseA.filter((m) => m.role === 'user').length).toBe(25)

    const mergedA = mergeMessages(baseA, streaming)
    dbg('R2b-A', mergedA)

    // Rendered exactly once, still pending, immediately before its own
    // answer, nothing stranded at the end.
    expect(mergedA.filter((m) => m.content === 'question number 29').length).toBe(1)
    const iQA = mergedA.findIndex((m) => m.content === 'question number 29')
    const iAA = mergedA.findIndex((m) => m.content === 'answer number 29')
    expect(iQA).toBeGreaterThanOrEqual(0)
    expect(iAA).toBe(iQA + 1)
    expect(mergedA[iQA]?.optimistic).toBe(true)
    expect(mergedA[mergedA.length - 1]?.content).toBe('answer number 29')
    expectSaneOrder(mergedA)

    // Phase B — the window slid by one turn (turns 5..29): still exactly 50
    // messages and still 25 non-optimistic users (the count is saturated,
    // so the legacy rule `25 > 25` can never confirm), but the base now
    // contains the confirmed b_q29/b_a29 — the anchor rule must confirm.
    const baseB = makeSaturatedBase(5, 29)
    expect(baseB.length).toBe(50)
    expect(baseB.filter((m) => m.role === 'user').length).toBe(25)

    const mergedB = mergeMessages(baseB, streaming)
    dbg('R2b-B', mergedB)

    // No optimistic survivor (base HAS the confirmed copy now)…
    expect(compact(mergedB.filter((m) => m.optimistic === true))).toEqual([])
    // …each message rendered exactly once, list ends with the newest answer.
    expect(mergedB.filter((m) => m.content === 'question number 29').length).toBe(1)
    expect(mergedB.filter((m) => m.content === 'answer number 29').length).toBe(1)
    expect(mergedB[mergedB.length - 1]?.content).toBe('answer number 29')
    expectSaneOrder(mergedB)
  })

  test('R3 (D2): a single streaming assistant blocks positional matching for every later base assistant', () => {
    const base = [u('q1'), a('a1'), u('q2'), a('a2'), u('q3'), a('a3')]
    // First entry is an actively-streaming placeholder; second is the
    // completed copy of base a3. The stuck candidate must not block it.
    const streaming = [assistant('partial', { streaming: true }), assistant('a3')]

    const merged = mergeMessages(base, streaming)
    dbg('R3', merged)

    expect(merged.filter((m) => m.content === 'a3').length).toBe(1)
    expectSaneOrder(merged)
  })

  test('R4 (D3): positional matching must not let a stale leftover steal an unrelated base assistant key', () => {
    const base = [u('q1'), a('a1'), u('q2'), a('a2'), u('q3'), a('a3')]
    // Stale leftover copy of the FIRST answer, retained from an earlier turn.
    const streaming = [assistant('a1', { stableId: 'stale-1' })]

    const merged = mergeMessages(base, streaming)
    dbg('R4', merged)

    // The recent answer a3 must keep its own key — not inherit the stale one.
    const a3Entry = merged.find((m) => m.content === 'a3')
    expect(a3Entry?.stableId).not.toBe('stale-1')
    // a1 itself must appear exactly once (base copy only).
    expect(merged.filter((m) => m.content === 'a1').length).toBe(1)
    expectSaneOrder(merged)
  })

  test('R5 (D4): merged output never contains duplicate React render keys', () => {
    // Two DIFFERENT logical messages that ended up sharing ONE render key.
    //
    // This is not reachable from base data: residentMessageID occurrence-indexes
    // repeated content (pkg/channels/rest_chat.go), so the server never emits two
    // equal ids. It IS reachable from the merge itself, because buildFilteredBase
    // ASSIGNS stableId while pairing streaming messages onto base messages —
    // defect D3 was exactly such a key steal. This guard keeps a future merge bug
    // cosmetic instead of structural.
    //
    // Content is deliberately DISTINCT: repeated user text is legitimate (asking
    // the same question twice) and must NOT be deduplicated, so this test pins key
    // uniqueness only.
    const base = [
      user('first question', { stableId: 'S0' }),
      assistant('answer'),
      user('second question', { stableId: 'S0' }),
    ]
    const streaming: ChatMessage[] = []

    const merged = mergeMessages(base, streaming)
    dbg('R5', merged)

    // All three logical messages survive, in base order — the guard renames keys,
    // it never drops a message.
    expect(compact(merged)).toEqual([
      'user:first question',
      'assistant:answer',
      'user:second question',
    ])
    // …and each renders under a UNIQUE React key.
    expectSaneOrder(merged)
  })

  test('R6: tool turn — base order is preserved and no duplicate cards', () => {
    const base: ChatMessage[] = [
      user('run a command', { id: 'b_u1' }),
      assistant('', { id: 'b_a1', reasoningContent: 'thinking about it' }),
      tool('call_1', { id: 'b_t1', toolName: 'exec', toolStatus: 'completed' }),
      assistant('final answer', { id: 'b_a2' }),
    ]
    const streaming: ChatMessage[] = [
      assistant('', { id: 's_i1', reasoningContent: 'thinking about it' }),
      tool('call_1', { id: 's_t1', toolName: 'exec', toolStatus: 'completed' }),
      assistant('final answer', { id: 's_i2' }),
    ]

    const merged = mergeMessages(base, streaming)
    dbg('R6', merged)

    // Exactly one tool card for call_1 and one final answer.
    expect(merged.filter((m) => m.role === 'tool' && m.toolCallId === 'call_1').length).toBe(1)
    expect(merged.filter((m) => m.content === 'final answer').length).toBe(1)
    // Relative order: user → reasoning assistant → tool → final answer.
    expect(merged.map((m) => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    expect(merged[0]?.content).toBe('run a command')
    expect(merged[1]?.reasoningContent).toBe('thinking about it')
    expect(merged[2]?.toolCallId).toBe('call_1')
    expect(merged[3]?.content).toBe('final answer')
    expectSaneOrder(merged)
  })

  test('R7: stale leftover from an old turn must not be stranded below newer turns', () => {
    const base = [u('q1'), a('a1'), u('q2'), a('a2'), u('q3'), a('a3')]
    // Stale copy of the first answer that was never garbage-collected.
    const streaming = [assistant('a1', { id: 's_old' })]

    const merged = mergeMessages(base, streaming)
    dbg('R7', merged)

    expect(merged.filter((m) => m.content === 'a1').length).toBe(1)
    expectSaneOrder(merged)
  })
})
