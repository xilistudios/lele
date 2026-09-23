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
 *   7. 200-char prefix tolerance (streaming copy shorter than the persisted one).
 */
import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { findConfirmingBaseUserIndex, mergeMessages } from './messageMerge'

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
