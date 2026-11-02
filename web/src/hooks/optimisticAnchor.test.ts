/**
 * Focused unit tests for the window-independent confirmation of optimistic
 * user bubbles ("anchor id").
 *
 * Background: the backend serves chat history as a sliding window of the last
 * 50 messages (pkg/channels/rest_chat.go: `resultStartIdx := endIdx - limit`)
 * and useChatHistory's queryFn REPLACES the react-query cache with that window
 * whenever `cachedData.messages.length <= DEFAULT_LIMIT` (50). Past 50
 * messages the cached confirmed-user count SATURATES, so the legacy rule
 * `baseUserCount > optimisticBaseCount` is false forever and the optimistic
 * bubble became immortal — mergeMessages then stranded it at the END of the
 * list, after newer answers (the "messages lose their order until I refresh"
 * bug, pinned red by messageOrderingWindow.test.ts).
 *
 * The fix anchors confirmation on the send-time id of the LAST confirmed base
 * user message (`optimisticAnchorId`): base ids are content-derived and
 * position-independent (residentMessageID in pkg/channels/rest_chat.go), so
 * "a confirmed copy of my text exists AFTER the anchor" stays monotonic no
 * matter how the window slides.
 *
 * Covered here (see messageOrderingWindow.test.ts for the end-to-end gate):
 *   1. anchor rule confirms an after-anchor match with a SATURATED count,
 *   2. no confirmation when the only match is BEFORE the anchor,
 *   3. anchor slid out of the window → confirm against the newest match,
 *   4. no anchor (empty cache at send) → exactly the legacy count rule,
 *   5. claim tracking: two identical in-flight sends never share a base copy,
 *   6. empty content never confirms,
 *   7. 200-char prefix tolerance (streaming copy shorter than the persisted one),
 *   8. the FINAL positional fallback for a turn the server persisted under a
 *      DIFFERENT text (or whose anchor slid out of the window):
 *      `findUnclaimedBaseUserForStaleBubble` — sound for user turns only, see
 *      its doc comment.
 */
import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import {
  findConfirmingBaseUserIndex,
  findUnclaimedBaseUserForStaleBubble,
  mergeMessages,
} from './messageMerge'

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

const optUser = (content: string, opts: MsgOpts = {}) =>
  makeUser(opts.id ?? autoId('opt-u'), content, { ...opts, optimistic: true })

const completedAssistant = (content: string, opts: MsgOpts = {}) =>
  makeAssistant(opts.id ?? autoId('a'), content, { ...opts, streaming: false })

/** Convenience: a fresh claim set (one per merge pass / test). */
const noClaims = () => new Set<number>()

// ── 1–4, 6, 7: findConfirmingBaseUserIndex ─────────────────────────────────

describe('findConfirmingBaseUserIndex', () => {
  test('1: anchor rule confirms an after-anchor match even when the count is SATURATED', () => {
    // Saturated window: 3 confirmed users in cache AND at send time
    // (baseUserCount === optimisticBaseCount → legacy rule is false forever).
    const base = [
      makeUser('u1', 'hello there'),
      makeUser('u2', 'how are you'),
      makeUser('u3', 'new question'),
    ]
    const msg = optUser('new question', {
      id: 'opt-1',
      optimisticBaseCount: 3,
      optimisticAnchorId: 'u2', // send-time last confirmed user
    })

    // Legacy count rule alone: 3 > 3 = false.
    expect(3 > (msg.optimisticBaseCount ?? 0)).toBe(false)
    // Anchor rule: confirmed copy 'u3' sits AFTER anchor 'u2' → index 2.
    expect(findConfirmingBaseUserIndex(msg, base, 3, noClaims())).toBe(2)
  })

  test('2: does NOT confirm when the only content match is BEFORE the anchor', () => {
    // An older identical message legitimately exists earlier in the
    // conversation; the send-time anchor proves nothing new landed after it.
    const base = [
      makeUser('old-ping', 'ping'), // older identical message (index 0)
      makeUser('anchor-id', 'something I asked in between'), // anchor (index 1)
      makeUser('u3', 'unrelated'), // newest confirmed user (index 2)
    ]
    const msg = optUser('ping', {
      id: 'opt-ping',
      optimisticBaseCount: 3, // saturated: 3 > 3 = false
      optimisticAnchorId: 'anchor-id',
    })

    expect(findConfirmingBaseUserIndex(msg, base, 3, noClaims())).toBe(-1)
  })

  test('3: anchor slid out of the window → confirms against the NEWEST match', () => {
    // The sliding window dropped the anchor entirely: everything still cached
    // postdates the send, so the newest content match is the confirmed copy.
    const base = [
      makeUser('b1', 'ping'), // would be an older identical message, but the
      makeUser('b2', 'other'), // anchor (absent) proves nothing about it —
      makeUser('b3', 'ping'), // the LAST match wins when the anchor is gone.
    ]
    const msg = optUser('ping', {
      id: 'opt-slide',
      optimisticBaseCount: 3, // saturated
      optimisticAnchorId: 'anchor-not-in-window',
    })

    expect(findConfirmingBaseUserIndex(msg, base, 3, noClaims())).toBe(2)
  })

  test('4: optimisticAnchorId === undefined (empty cache at send) behaves as the legacy count rule', () => {
    // (a) empty cache at send → snapshot 0; content match implies at least one
    // cached user (1 > 0) → confirmed, exactly like the legacy rule.
    const baseA = [makeUser('u1', 'first question'), makeUser('u2', 'second question')]
    const fresh = optUser('second question', { id: 'opt-fresh', optimisticBaseCount: 0 })
    expect(fresh.optimisticAnchorId).toBeUndefined()
    expect(findConfirmingBaseUserIndex(fresh, baseA, baseA.length, noClaims())).toBe(1)

    // (b) count grew but there is NO content match → not confirmed (count
    // alone was never enough, legacy or otherwise).
    const noMatch = optUser('never said this', { id: 'opt-nomatch', optimisticBaseCount: 0 })
    expect(findConfirmingBaseUserIndex(noMatch, baseA, baseA.length, noClaims())).toBe(-1)

    // (c) content match but the count did NOT grow past the snapshot → not
    // confirmed: with no anchor, ONLY the legacy count rule decides.
    const stale = optUser('second question', { id: 'opt-stale', optimisticBaseCount: 2 })
    expect(findConfirmingBaseUserIndex(stale, baseA, 2, noClaims())).toBe(-1)

    // (d) empty base → nothing to confirm.
    const emptyBase = optUser('second question', { id: 'opt-empty', optimisticBaseCount: 0 })
    expect(findConfirmingBaseUserIndex(emptyBase, [], 0, noClaims())).toBe(-1)
  })

  test('6: empty content never confirms', () => {
    const base = [makeUser('u1', ''), makeUser('u2', 'real text')]
    const msg = optUser('', { id: 'opt-blank', optimisticBaseCount: 0 })
    // Even with a grown count and an actual empty base user to match.
    expect(findConfirmingBaseUserIndex(msg, base, 2, noClaims())).toBe(-1)
  })

  test('7: prefix tolerance — 200+-char streaming content matching a longer persisted copy', () => {
    const typed = 'A'.repeat(220)
    // The persisted copy gained a server-side suffix (e.g. attachments) after
    // the turn ended, so the streaming content is a >200-char prefix of it.
    const persisted = `${typed}\n## Attachments\n- /tmp/file.png`
    const base = [makeUser('anchor-id', 'unrelated opener'), makeUser('copy-id', persisted)]
    const msg = optUser(typed, {
      id: 'opt-prefix',
      optimisticBaseCount: 2, // saturated: 2 > 2 = false
      optimisticAnchorId: 'anchor-id',
    })

    expect(findConfirmingBaseUserIndex(msg, base, 2, noClaims())).toBe(1)
  })
})

// ── 5: claim tracking across two in-flight sends ────────────────────────────

describe('claim tracking (findConfirmingBaseUserIndex)', () => {
  test('5: two identical in-flight sends, ONE base copy → exactly one confirms', () => {
    const base = [
      makeUser('anchor-id', 'unrelated opener'), // anchor (index 0)
      makeUser('copy-id', 'identical text'), // the ONLY confirmed copy (index 1)
    ]
    const first = optUser('identical text', {
      id: 'opt-first',
      optimisticBaseCount: 2, // saturated: 2 > 2 = false → anchor path only
      optimisticAnchorId: 'anchor-id',
    })
    const second = optUser('identical text', {
      id: 'opt-second',
      optimisticBaseCount: 2,
      optimisticAnchorId: 'anchor-id',
    })

    const claimed = noClaims()
    const firstIdx = findConfirmingBaseUserIndex(first, base, 2, claimed)
    expect(firstIdx).toBe(1)
    claimed.add(firstIdx)

    // The copy is already consumed — the second send must NOT confirm again
    // (without claims both would confirm against index 1 and the second
    // bubble would vanish while still unconfirmed).
    expect(findConfirmingBaseUserIndex(second, base, 2, claimed)).toBe(-1)
  })

  test('5b: claims are handed out in streaming order through mergeMessages', () => {
    // Same shape end-to-end: saturated window, anchor, two identical sends,
    // ONE confirmed copy. Exactly one optimistic bubble must remain — the one
    // whose claim could not be satisfied.
    const base = [
      makeUser('anchor-id', 'unrelated opener'),
      makeUser('copy-id', 'identical text'),
      makeAssistant('ans-id', 'some answer'),
    ]
    const first = optUser('identical text', {
      id: 'opt-first',
      optimisticBaseCount: 3, // saturated: 3 > 3 = false
      optimisticAnchorId: 'anchor-id',
    })
    const second = optUser('identical text', {
      id: 'opt-second',
      optimisticBaseCount: 3,
      optimisticAnchorId: 'anchor-id',
    })

    const merged = mergeMessages(base, [first, second])

    const optimistic = merged.filter((m) => m.optimistic === true)
    expect(optimistic.map((m) => m.id)).toEqual(['opt-second'])
    // The confirmed copy renders exactly once (the first send was absorbed).
    expect(merged.filter((m) => m.content === 'identical text' && !m.optimistic).length).toBe(1)
  })
})

// ── mergeMessages integration: the primary regression, window-independent ───

describe('mergeMessages confirmation with a saturated sliding window', () => {
  /**
   * Alternating user/assistant tail of a long conversation:
   * `question number n` / `answer number n` for n in [from, to].
   */
  function makeSaturatedBase(from: number, to: number): ChatMessage[] {
    const msgs: ChatMessage[] = []
    for (let n = from; n <= to; n++) {
      msgs.push(makeUser(`b_q${n}`, `question number ${n}`))
      msgs.push(makeAssistant(`b_a${n}`, `answer number ${n}`))
    }
    return msgs
  }

  test('1b: optimistic users with anchors confirm after the window saturates', () => {
    // Last-50-style window scaled down: turns 4..8 → 5 confirmed users,
    // count saturated at 5 (every send-time snapshot is 5 as well).
    const base = makeSaturatedBase(4, 8)
    expect(base.filter((m) => m.role === 'user').length).toBe(5)

    // Sends recorded the anchor = last confirmed user at send time (b_q5 is
    // still inside the window; the copies of q6..q8 sit AFTER it).
    const optQ = (n: number) =>
      optUser(`question number ${n}`, {
        id: `s_q${n}`,
        optimisticBaseCount: 5,
        optimisticAnchorId: 'b_q5',
      })
    const streaming = [
      optQ(6),
      completedAssistant('answer number 6', { id: 's_a6' }),
      optQ(7),
      completedAssistant('answer number 7', { id: 's_a7' }),
      optQ(8),
      completedAssistant('answer number 8', { id: 's_a8' }),
    ]

    const merged = mergeMessages(base, streaming)

    // No optimistic bubble survives — the anchor confirmed them all even
    // though the legacy count rule (5 > 5) is false forever.
    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    for (const n of [6, 7, 8]) {
      expect(merged.filter((m) => m.content === `question number ${n}`).length).toBe(1)
    }
    // Nothing stranded at the END after the newest answer.
    expect(merged[merged.length - 1]?.content).toBe('answer number 8')
  })
})

// ── Positional fallback: the server persisted the turn under a DIFFERENT text ─

/**
 * Every content-based rule requires a CONTENT match (`content.startsWith(prefix)`)
 * or the attachments-only marker. When the server rewrites the turn (trimming,
 * rendered attachment context, a system notice) — or when the anchor id has
 * already slid out of the sliding window — `collectConfirmCandidates` returns
 * `[]` and the bubble can NEVER be confirmed: `mergeMessages` appends it after
 * canonical history, so it renders BELOW newer answers ("an old user message
 * shows up as the newest") until a page reload.
 *
 * `findUnclaimedBaseUserForStaleBubble` is the final POSITIONAL rule. It is
 * sound for USER turns only (a send and its persisted copy are 1:1 and ordered
 * inside the window), unlike the assistant case where the backend persists one
 * message per LLM iteration.
 */
describe('stranded optimistic bubble (positional fallback)', () => {
  test('1: server-normalized persisted copy — bubble dropped, base copy keeps its slot', () => {
    const base = [
      makeUser('u-opener', 'first question'),
      makeAssistant('a-1', 'answer one'),
      // The server persisted the turn as 'Second question?' while the client
      // sent 'second question' → the 200-char prefix identity never matches.
      makeUser('u-normalized', 'Second question?'),
    ]
    const msg = optUser('second question', {
      id: 'opt-second',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-opener',
    })

    const merged = mergeMessages(base, [msg])

    // No stranded bubble at the tail...
    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    // ...and the persisted copy keeps its canonical slot in server order.
    expect(merged.map((m) => m.id)).toEqual(['u-opener', 'a-1', 'u-normalized'])
    expect(merged[merged.length - 1]?.content).toBe('Second question?')
  })

  test('2: FIRST message of a session (no anchor, empty cache) is dropped too', () => {
    // At send time the history cache was empty, so `optimisticAnchorId` is
    // undefined and only the send-time ordinal (0) can place the bubble.
    const base = [makeUser('u-first', 'Hello, world!')]
    const msg = optUser('hello world', { id: 'opt-first', optimisticBaseCount: 0 })
    expect(msg.optimisticAnchorId).toBeUndefined()

    const merged = mergeMessages(base, [msg])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.map((m) => m.id)).toEqual(['u-first'])
  })

  test('3: anchor slid out of the window → bubble dropped', () => {
    // Saturated count (1 > 1 is false), so only the anchor rule could help —
    // and the anchor is gone, meaning everything still cached postdates the
    // send. The newest confirmed user is the persisted copy of this turn.
    const base = [makeUser('u-window', 'a question from before the window slid')]
    const msg = optUser('my lost draft', {
      id: 'opt-slid',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'anchor-gone',
    })

    const merged = mergeMessages(base, [msg])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.map((m) => m.id)).toEqual(['u-window'])
  })

  test('4: nothing new in base (only up to the anchor) → bubble KEPT at the tail', () => {
    // The normal in-flight case: the turn has NOT been persisted yet. No rule
    // may claim a base user here, or the draft would vanish while it is still
    // being answered.
    const base = [makeUser('u-anchor', 'opener'), makeAssistant('a-1', 'an answer')]
    const msg = optUser('in flight question', {
      id: 'opt-inflight',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-anchor',
    })

    const merged = mergeMessages(base, [msg])

    expect(merged.filter((m) => m.optimistic === true).map((m) => m.id)).toEqual(['opt-inflight'])
    expect(merged[merged.length - 1]?.id).toBe('opt-inflight')
  })

  test('5: two rapid sends, ONE persisted copy → exactly one bubble left (the newest)', () => {
    // Two devices (or two fast sends) both unconfirmed by content, and the
    // server persisted only ONE copy — under a different text. The accepted
    // trade-off: the earlier bubble is dropped rather than duplicated below
    // the persisted copy.
    const base = [
      makeUser('u-opener', 'opener'),
      makeUser('u-persisted', 'First of the two (normalized)'),
    ]
    const first = optUser('first send', {
      id: 'opt-1',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-opener',
    })
    const second = optUser('second send', {
      id: 'opt-2',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-opener',
    })

    const merged = mergeMessages(base, [first, second])

    const optimistic = merged.filter((m) => m.optimistic === true)
    expect(optimistic.map((m) => m.id)).toEqual(['opt-2'])
    expect(merged.map((m) => m.id)).toEqual(['u-opener', 'u-persisted', 'opt-2'])
  })

  test('6: a failed (refused) bubble is NEVER dropped by the fallback', () => {
    // A refused send never got a persisted copy: it must stay visible AND
    // retryable, even though the count/anchor say base moved past the send.
    const base = [
      makeUser('u-opener', 'opener'),
      makeUser('u-notice', '[System: subagent] background task completed'),
    ]
    const msg = optUser('my refused send', {
      id: 'opt-failed',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-opener',
      failed: true,
    })

    const merged = mergeMessages(base, [msg])

    expect(merged.filter((m) => m.optimistic === true).map((m) => m.id)).toEqual(['opt-failed'])
    expect(merged[merged.length - 1]?.id).toBe('opt-failed')
  })
})

// ── Direct unit tests of the positional fallback ────────────────────────────

describe('findUnclaimedBaseUserForStaleBubble', () => {
  test('7a: rule 1 — first confirmed user at/after the send-time ordinal', () => {
    const base = [
      makeUser('u1', 'one'),
      makeAssistant('a1', 'answer'),
      makeUser('u2', 'two'),
      makeUser('u3', 'three'),
    ]
    const msg = optUser('typed this', { id: 'opt-ord', optimisticBaseCount: 2 })

    // base grew to 3 confirmed users; ordinal 2 (index 3) is the newcomer.
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, noClaims())).toBe(3)
    // With that index already claimed by an earlier bubble, nothing is left.
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([3]))).toBe(-1)
    // No growth past the snapshot → rule 1 declines (and there is no anchor).
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 2, noClaims())).toBe(-1)
  })

  test('7b: rule 2 — first unclaimed confirmed user after the anchor', () => {
    const base = [
      makeUser('u-anchor', 'opener'),
      makeAssistant('a1', 'answer'),
      makeUser('u-after', 'rewritten copy'),
    ]
    const msg = optUser('draft', {
      id: 'opt-anchor',
      optimisticBaseCount: 1, // saturated → rule 1 cannot decide
      optimisticAnchorId: 'u-anchor',
    })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 1, noClaims())).toBe(2)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 1, new Set([2]))).toBe(-1)
  })

  test('7c: rule 2 — absent anchor → the LAST unclaimed confirmed user', () => {
    const base = [makeUser('u1', 'one'), makeUser('u2', 'two'), makeUser('u3', 'three')]
    const msg = optUser('draft', {
      id: 'opt-gone',
      optimisticBaseCount: 3, // saturated
      optimisticAnchorId: 'not-in-window',
    })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, noClaims())).toBe(2)
    // As specified: the LAST *unclaimed* confirmed user, so a second stranded
    // bubble hands out the next-newest slot instead of vanishing while an
    // earlier one might still be unmatched.
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([2]))).toBe(1)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([1, 2]))).toBe(0)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([0, 1, 2]))).toBe(-1)
  })

  test('7d: never returns an optimistic index and skips claimed ones', () => {
    // An optimistic copy can itself live in base (a cache that still holds it);
    // it is not a confirmed user and must never be handed out.
    const base = [
      makeUser('b-opt', 'stale optimistic copy', { optimistic: true }),
      makeUser('u1', 'one'),
      makeUser('u2', 'two'),
    ]
    const msg = optUser('draft', { id: 'opt-skip', optimisticBaseCount: 0 })

    const idx = findUnclaimedBaseUserForStaleBubble(msg, base, 2, noClaims())
    expect(idx).toBe(1)
    expect(base[idx].optimistic).toBeUndefined()
    // Index 0 is never a candidate even when rules could reach it.
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 2, new Set([1]))).toBe(2)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 2, new Set([1, 2]))).toBe(-1)
  })

  test('7e: nothing newer than the send → -1 (still in flight, keep the bubble)', () => {
    const base = [makeUser('u-anchor', 'opener'), makeAssistant('a1', 'answer')]
    const msg = optUser('draft', {
      id: 'opt-flight',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-anchor',
    })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 1, noClaims())).toBe(-1)
    // Empty base, no anchor, no count growth: nothing to pair with.
    expect(
      findUnclaimedBaseUserForStaleBubble(
        optUser('draft', { optimisticBaseCount: 0 }),
        [],
        0,
        noClaims(),
      ),
    ).toBe(-1)
  })

  test('7f: an attachments-carrying bubble is never paired positionally', () => {
    // Its identity is the attachment marker, not its text (the persisted path
    // is chosen by the server), so position must not vouch for it — see
    // attachmentsOnlySend.test.ts (5, 5b, 5c).
    const base = [makeUser('u-opener', 'opener'), makeUser('u-other', 'an unrelated turn')]
    const attachment = { path: '/lele/tmp/uploads/aa_a.jpg', name: 'a.jpg', kind: 'file' as const }
    const msg = optUser('look at this', {
      id: 'opt-attach',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-opener',
      attachments: [attachment],
    })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 2, noClaims())).toBe(-1)
  })
})

// ── The fallback must never STRAND a later, properly-matched send ────────────

/**
 * The fallback is the last resort, and claims are handed out in send order. That
 * makes one specific mis-pairing possible and it is the reason the caller passes
 * the RESERVED set: a bubble the server never persisted (a built-in slash
 * command such as `/model` or `/status` is executed and answered WITHOUT being
 * written to the session) sits in the streaming list forever. Without the
 * reservation it would claim the persisted row of the NEXT send, and that next
 * bubble — whose copy IS in base — would have nothing left to pair with. It then
 * renders below newer answers until a reload, i.e. exactly the ordering symptom
 * this fallback exists to remove.
 */
describe('stale bubble vs. a later, properly-persisted send', () => {
  test('8: a never-persisted command bubble cannot steal the next send’s row', () => {
    // `/model gpt-5` was answered by the backend's command handler and never
    // written to the session; the following question WAS persisted verbatim.
    const base = [
      makeUser('u1', 'opener'),
      makeAssistant('a1', 'answer'),
      makeUser('u-q2', 'Question B'),
    ]
    const command = optUser('/model gpt-5', {
      id: 'opt-cmd',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u1',
    })
    const question = optUser('Question B', {
      id: 'opt-q2',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u1',
    })

    const merged = mergeMessages(base, [command, question])

    // The next send is confirmed by its own copy and must NOT be stranded at the
    // end of the list (that is the reported bug).
    expect(merged.map((m) => m.id)).toEqual(['u1', 'a1', 'u-q2'])
    expect(merged.filter((m) => m.optimistic)).toEqual([])
  })

  test('8b: with the reservation, the row is off limits to the fallback', () => {
    const base = [makeUser('u1', 'opener'), makeUser('u-q2', 'Question B')]
    const command = optUser('/model gpt-5', {
      id: 'opt-cmd',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u1',
    })

    // Index 1 belongs to `Question B` by content, so it is reserved.
    expect(findUnclaimedBaseUserForStaleBubble(command, base, 2, noClaims(), new Set([1]))).toBe(-1)
    // Unreserved, the positional rule would have handed it out (the bug).
    expect(findUnclaimedBaseUserForStaleBubble(command, base, 2, noClaims())).toBe(1)
  })

  test('8c: a row the chat log never renders is never claimable', () => {
    // Compaction summaries and loop-detector guidance are persisted with role
    // "user" but filtered out of the log. Claiming one would delete the user's
    // bubble in exchange for something invisible.
    const base = [
      makeUser('u1', 'opener'),
      makeUser('u-summary', '## Summary of Previous Conversation\n\nwe talked'),
      makeUser('u-guidance', '⚠️ GUIDANCE: stop repeating the same tool calls'),
    ]
    const msg = optUser('a fresh question', {
      id: 'opt-fresh',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u1',
    })

    // Ordinal rule: rows 1 and 2 are hidden, so there is no claimable slot.
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, noClaims())).toBe(-1)
    expect(mergedTails(base, msg)).toEqual(['u1', 'u-summary', 'u-guidance', 'opt-fresh'])
  })

  test('8d: a REFUSED bubble survives BOTH fallback exits', () => {
    // `msg.failed` is checked before either exit (claim or drop), so a refusal
    // the server already reported can never be swallowed by the positional
    // rules — that would remove the only UI the user can retry from.
    const base = [makeUser('u1', 'opener'), makeUser('u-q2', 'Question B')]
    const refused = optUser('refused send', {
      id: 'opt-refused',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u1',
      failed: true,
    })
    const question = optUser('Question B', {
      id: 'opt-q2',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u1',
    })

    const merged = mergeMessages(base, [refused, question])

    // The refusal stays (visible + retryable); the later send is confirmed.
    expect(merged.filter((m) => m.optimistic).map((m) => m.id)).toEqual(['opt-refused'])
    expect(merged[merged.length - 1]?.id).toBe('opt-refused')
  })

  test('8e: a later send that merely repeats OLDER text keeps an in-flight bubble', () => {
    // The reservation is computed from CONTENT candidates, which ignore
    // position: sending the same short text twice ("ok", "continue", "sí")
    // makes the later bubble reserve the OLD row carrying that text. Reading
    // that as "history already moved past me" would drop a bubble that is
    // legitimately still in flight (it re-appears from base on the next poll,
    // but the user sees it vanish). Only a row POSTDATING the send position
    // proves the turn was answered without ever being persisted.
    const base = [makeUser('u1', 'opener'), makeAssistant('a1', 'answer'), makeUser('u-old', 'ok')]
    const inFlight = optUser('a brand new question', {
      id: 'opt-a',
      optimisticBaseCount: 2,
      optimisticAnchorId: 'u-old',
    })
    // Repeats the text of `u-old`, which sits BEFORE the anchor.
    const repeated = optUser('ok', {
      id: 'opt-b',
      optimisticBaseCount: 2,
      optimisticAnchorId: 'u-old',
    })

    const merged = mergeMessages(base, [inFlight, repeated])

    // Both bubbles are still in flight: neither may be swallowed, and the send
    // order is preserved at the tail.
    expect(merged.map((m) => m.id)).toEqual(['u1', 'a1', 'u-old', 'opt-a', 'opt-b'])
  })
})

/** Render order of the merged list, for the "bubble survived" assertions. */
function mergedTails(base: ChatMessage[], msg: ChatMessage): string[] {
  return mergeMessages(base, [msg]).map((m) => m.id)
}
