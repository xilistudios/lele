import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import {
  findConfirmingBaseUserIndex,
  findUnclaimedBaseUserForStaleBubble,
  mergeMessages,
} from './messageMerge'

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

const noClaims = () => new Set<number>()

describe('findConfirmingBaseUserIndex', () => {
  test('1: anchor rule confirms an after-anchor match even when the count is SATURATED', () => {
    const base = [
      makeUser('u1', 'hello there'),
      makeUser('u2', 'how are you'),
      makeUser('u3', 'new question'),
    ]
    const msg = optUser('new question', {
      id: 'opt-1',
      optimisticBaseCount: 3,
      optimisticAnchorId: 'u2',
    })

    expect(3 > (msg.optimisticBaseCount ?? 0)).toBe(false)
    expect(findConfirmingBaseUserIndex(msg, base, 3, noClaims())).toBe(2)
  })

  test('2: does NOT confirm when the only content match is BEFORE the anchor', () => {
    const base = [
      makeUser('old-ping', 'ping'),
      makeUser('anchor-id', 'something I asked in between'),
      makeUser('u3', 'unrelated'),
    ]
    const msg = optUser('ping', {
      id: 'opt-ping',
      optimisticBaseCount: 3,
      optimisticAnchorId: 'anchor-id',
    })

    expect(findConfirmingBaseUserIndex(msg, base, 3, noClaims())).toBe(-1)
  })

  test('3: anchor slid out of the window → confirms against the NEWEST match', () => {
    const base = [makeUser('b1', 'ping'), makeUser('b2', 'other'), makeUser('b3', 'ping')]
    const msg = optUser('ping', {
      id: 'opt-slide',
      optimisticBaseCount: 3,
      optimisticAnchorId: 'anchor-not-in-window',
    })

    expect(findConfirmingBaseUserIndex(msg, base, 3, noClaims())).toBe(2)
  })

  test('4: optimisticAnchorId === undefined (empty cache at send) behaves as the legacy count rule', () => {
    const baseA = [makeUser('u1', 'first question'), makeUser('u2', 'second question')]
    const fresh = optUser('second question', { id: 'opt-fresh', optimisticBaseCount: 0 })
    expect(fresh.optimisticAnchorId).toBeUndefined()
    expect(findConfirmingBaseUserIndex(fresh, baseA, baseA.length, noClaims())).toBe(1)

    const noMatch = optUser('never said this', { id: 'opt-nomatch', optimisticBaseCount: 0 })
    expect(findConfirmingBaseUserIndex(noMatch, baseA, baseA.length, noClaims())).toBe(-1)

    const stale = optUser('second question', { id: 'opt-stale', optimisticBaseCount: 2 })
    expect(findConfirmingBaseUserIndex(stale, baseA, 2, noClaims())).toBe(-1)

    const emptyBase = optUser('second question', { id: 'opt-empty', optimisticBaseCount: 0 })
    expect(findConfirmingBaseUserIndex(emptyBase, [], 0, noClaims())).toBe(-1)
  })

  test('6: empty content never confirms', () => {
    const base = [makeUser('u1', ''), makeUser('u2', 'real text')]
    const msg = optUser('', { id: 'opt-blank', optimisticBaseCount: 0 })
    expect(findConfirmingBaseUserIndex(msg, base, 2, noClaims())).toBe(-1)
  })

  test('7: prefix tolerance — 200+-char streaming content matching a longer persisted copy', () => {
    const typed = 'A'.repeat(220)
    const persisted = `${typed}\n## Attachments\n- /tmp/file.png`
    const base = [makeUser('anchor-id', 'unrelated opener'), makeUser('copy-id', persisted)]
    const msg = optUser(typed, {
      id: 'opt-prefix',
      optimisticBaseCount: 2,
      optimisticAnchorId: 'anchor-id',
    })

    expect(findConfirmingBaseUserIndex(msg, base, 2, noClaims())).toBe(1)
  })
})

describe('claim tracking (findConfirmingBaseUserIndex)', () => {
  test('5: two identical in-flight sends, ONE base copy → exactly one confirms', () => {
    const base = [makeUser('anchor-id', 'unrelated opener'), makeUser('copy-id', 'identical text')]
    const first = optUser('identical text', {
      id: 'opt-first',
      optimisticBaseCount: 2,
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

    expect(findConfirmingBaseUserIndex(second, base, 2, claimed)).toBe(-1)
  })

  test('5b: claims are handed out in streaming order through mergeMessages', () => {
    const base = [
      makeUser('anchor-id', 'unrelated opener'),
      makeUser('copy-id', 'identical text'),
      makeAssistant('ans-id', 'some answer'),
    ]
    const first = optUser('identical text', {
      id: 'opt-first',
      optimisticBaseCount: 3,
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
    expect(merged.filter((m) => m.content === 'identical text' && !m.optimistic).length).toBe(1)
  })
})

describe('mergeMessages confirmation with a saturated sliding window', () => {
  function makeSaturatedBase(from: number, to: number): ChatMessage[] {
    const msgs: ChatMessage[] = []
    for (let n = from; n <= to; n++) {
      msgs.push(makeUser(`b_q${n}`, `question number ${n}`))
      msgs.push(makeAssistant(`b_a${n}`, `answer number ${n}`))
    }
    return msgs
  }

  test('1b: optimistic users with anchors confirm after the window saturates', () => {
    const base = makeSaturatedBase(4, 8)
    expect(base.filter((m) => m.role === 'user').length).toBe(5)

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

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    for (const n of [6, 7, 8]) {
      expect(merged.filter((m) => m.content === `question number ${n}`).length).toBe(1)
    }
    expect(merged[merged.length - 1]?.content).toBe('answer number 8')
  })
})

describe('stranded optimistic bubble (positional fallback)', () => {
  test('1: server-normalized persisted copy — bubble dropped, base copy keeps its slot', () => {
    const base = [
      makeUser('u-opener', 'first question'),
      makeAssistant('a-1', 'answer one'),
      makeUser('u-normalized', 'Second question?'),
    ]
    const msg = optUser('second question', {
      id: 'opt-second',
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-opener',
    })

    const merged = mergeMessages(base, [msg])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.map((m) => m.id)).toEqual(['u-opener', 'a-1', 'u-normalized'])
    expect(merged[merged.length - 1]?.content).toBe('Second question?')
  })

  test('2: FIRST message of a session (no anchor, empty cache) is dropped too', () => {
    const base = [makeUser('u-first', 'Hello, world!')]
    const msg = optUser('hello world', { id: 'opt-first', optimisticBaseCount: 0 })
    expect(msg.optimisticAnchorId).toBeUndefined()

    const merged = mergeMessages(base, [msg])

    expect(merged.filter((m) => m.optimistic === true)).toEqual([])
    expect(merged.map((m) => m.id)).toEqual(['u-first'])
  })

  test('3: anchor slid out of the window → bubble dropped', () => {
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

describe('findUnclaimedBaseUserForStaleBubble', () => {
  test('7a: rule 1 — first confirmed user at/after the send-time ordinal', () => {
    const base = [
      makeUser('u1', 'one'),
      makeAssistant('a1', 'answer'),
      makeUser('u2', 'two'),
      makeUser('u3', 'three'),
    ]
    const msg = optUser('typed this', { id: 'opt-ord', optimisticBaseCount: 2 })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, noClaims())).toBe(3)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([3]))).toBe(-1)
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
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-anchor',
    })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 1, noClaims())).toBe(2)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 1, new Set([2]))).toBe(-1)
  })

  test('7c: rule 2 — absent anchor → the LAST unclaimed confirmed user', () => {
    const base = [makeUser('u1', 'one'), makeUser('u2', 'two'), makeUser('u3', 'three')]
    const msg = optUser('draft', {
      id: 'opt-gone',
      optimisticBaseCount: 3,
      optimisticAnchorId: 'not-in-window',
    })

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, noClaims())).toBe(2)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([2]))).toBe(1)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([1, 2]))).toBe(0)
    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, new Set([0, 1, 2]))).toBe(-1)
  })

  test('7d: never returns an optimistic index and skips claimed ones', () => {
    const base = [
      makeUser('b-opt', 'stale optimistic copy', { optimistic: true }),
      makeUser('u1', 'one'),
      makeUser('u2', 'two'),
    ]
    const msg = optUser('draft', { id: 'opt-skip', optimisticBaseCount: 0 })

    const idx = findUnclaimedBaseUserForStaleBubble(msg, base, 2, noClaims())
    expect(idx).toBe(1)
    expect(base[idx].optimistic).toBeUndefined()
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

describe('stale bubble vs. a later, properly-persisted send', () => {
  test('8: a never-persisted command bubble cannot steal the next send’s row', () => {
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

    expect(findUnclaimedBaseUserForStaleBubble(command, base, 2, noClaims(), new Set([1]))).toBe(-1)
    expect(findUnclaimedBaseUserForStaleBubble(command, base, 2, noClaims())).toBe(1)
  })

  test('8c: a row the chat log never renders is never claimable', () => {
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

    expect(findUnclaimedBaseUserForStaleBubble(msg, base, 3, noClaims())).toBe(-1)
    expect(mergedTails(base, msg)).toEqual(['u1', 'u-summary', 'u-guidance', 'opt-fresh'])
  })

  test('8d: a REFUSED bubble survives BOTH fallback exits', () => {
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

    expect(merged.filter((m) => m.optimistic).map((m) => m.id)).toEqual(['opt-refused'])
    expect(merged[merged.length - 1]?.id).toBe('opt-refused')
  })

  test('8e: a later send that merely repeats OLDER text keeps an in-flight bubble', () => {
    const base = [makeUser('u1', 'opener'), makeAssistant('a1', 'answer'), makeUser('u-old', 'ok')]
    const inFlight = optUser('a brand new question', {
      id: 'opt-a',
      optimisticBaseCount: 2,
      optimisticAnchorId: 'u-old',
    })
    const repeated = optUser('ok', {
      id: 'opt-b',
      optimisticBaseCount: 2,
      optimisticAnchorId: 'u-old',
    })

    const merged = mergeMessages(base, [inFlight, repeated])

    expect(merged.map((m) => m.id)).toEqual(['u1', 'a1', 'u-old', 'opt-a', 'opt-b'])
  })
})

function mergedTails(base: ChatMessage[], msg: ChatMessage): string[] {
  return mergeMessages(base, [msg]).map((m) => m.id)
}
