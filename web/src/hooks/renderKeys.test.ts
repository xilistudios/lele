/**
 * Focused unit tests for `enforceUniqueRenderKeys` — the last-resort guard
 * that keeps the merged message list free of duplicate React render keys
 * (`stableId ?? id`), pinned as defect D4 in messageOrderingWindow.test.ts.
 *
 * Why this guard exists (two asymmetrical facts):
 *   - The BACKEND never emits duplicate ids: `residentMessageID` in
 *     pkg/channels/rest_chat.go occurrence-indexes repeated content, so a
 *     repeated message gets a `-<n>` suffix — duplicate keys are NOT
 *     reachable from base data.
 *   - The FRONTEND can still create them, because `buildFilteredBase`
 *     ASSIGNS `stableId` while merging (transferring a streaming message's
 *     key onto a base message to prevent a remount — defect D3 was exactly
 *     such a key steal). A future merge bug that collides two render keys
 *     would otherwise make the virtualized list mis-render or silently drop
 *     a bubble; the guard keeps that cosmetic instead of structural.
 *
 * Covered here (both branches + the no-op fast path):
 *   1. no collision → identical array reference (fast path does not allocate),
 *   2. colliding `stableId`, distinct ids → first holder keeps its key,
 *      second is re-keyed onto its own `id`,
 *   3. colliding key whose `id` fallback is ALSO taken → `__dup1` suffix,
 *   4. three-way collision → pairwise-distinct, deterministic suffixes,
 *   5. `id` is NEVER mutated in any branch (retry and attachment logic key
 *      off it).
 */
import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { enforceUniqueRenderKeys } from './messageMerge'

// ── Factories (real ChatMessage field names only — see lib/types.ts) ────────

type MsgOpts = Partial<ChatMessage>

const SESSION_KEY = 'test-session'
const NOW = '2026-01-01T00:00:00.000Z'

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

/** The render key React/Virtuoso will use (mirrors MessageList.messageKey). */
const renderKey = (m: ChatMessage) => m.stableId ?? m.id

const keysOf = (list: ChatMessage[]) => list.map(renderKey)
const idsOf = (list: ChatMessage[]) => list.map((m) => m.id)

describe('enforceUniqueRenderKeys', () => {
  test('1: no collision → returns the input array untouched (no allocation)', () => {
    const input = [
      makeUser('u1', 'first question', { stableId: 'S1' }),
      makeUser('u2', 'second question'),
      makeUser('u3', 'third question', { stableId: 'S3' }),
    ]

    const out = enforceUniqueRenderKeys(input)

    // Same reference: the common path does not allocate a new array.
    expect(out).toBe(input)
  })

  test('2: colliding stableId, distinct ids → first holder keeps its key, second re-keyed onto its own id', () => {
    const input = [
      makeUser('id-1', 'first question', { stableId: 'S0' }),
      makeUser('id-2', 'neutral message'),
      makeUser('id-3', 'second question', { stableId: 'S0' }),
    ]

    const out = enforceUniqueRenderKeys(input)

    // Exact `stableId ?? id` sequence: the FIRST holder keeps S0 (the bubble
    // already on screen — preserving its key avoids a remount); the later
    // colliding entry is re-keyed onto its own id.
    expect(keysOf(out)).toEqual(['S0', 'id-2', 'id-3'])
    // No message dropped, none reordered.
    expect(out.map((m) => m.content)).toEqual([
      'first question',
      'neutral message',
      'second question',
    ])
    expect(out.length).toBe(input.length)
  })

  test('3: colliding key where the fallback id is ALSO taken → __dup1 suffix, id never rewritten', () => {
    // The second entry renders under 'S' (its own id, no stableId), which the
    // first entry's stableId already occupies → key AND id fallback collide.
    const input = [makeUser('X', 'first', { stableId: 'S' }), makeUser('S', 'second')]

    const out = enforceUniqueRenderKeys(input)

    const keys = keysOf(out)
    expect(keys).toEqual(['S', 'S__dup1'])
    // Pairwise distinct.
    expect(new Set(keys).size).toBe(keys.length)
    // The second entry's `id` field is still 'S' — only stableId is re-keyed.
    expect(out[1]?.id).toBe('S')
    expect(out[1]?.stableId).toBe('S__dup1')
    expect(out[0]?.stableId).toBe('S')
  })

  test('4: three-way collision → all keys distinct, suffixes deterministic (__dup1, __dup2)', () => {
    // Deliberately pathological input: the guard is a last-resort net, so the
    // test feeds it a key that ALL entries share, with id fallbacks that are
    // taken as well.
    const input = [
      makeUser('S', 'one', { stableId: 'S' }),
      makeUser('S', 'two', { stableId: 'S' }),
      makeUser('S', 'three', { stableId: 'S' }),
    ]

    const out = enforceUniqueRenderKeys(input)

    const keys = keysOf(out)
    expect(keys).toEqual(['S', 'S__dup1', 'S__dup2'])
    expect(new Set(keys).size).toBe(keys.length)
    // No message dropped, none reordered.
    expect(out.map((m) => m.content)).toEqual(['one', 'two', 'three'])
  })

  test('5: id is never mutated in any branch', () => {
    const scenarios: ChatMessage[][] = [
      // Fast path (no collision).
      [makeUser('a', 'x', { stableId: 'k' }), makeUser('b', 'y')],
      // Re-keyed onto the entry's own id.
      [makeUser('a', 'x', { stableId: 'k' }), makeUser('b', 'y', { stableId: 'k' })],
      // Re-keyed onto a __dup1 variant.
      [makeUser('X', 'x', { stableId: 'S' }), makeUser('S', 'y')],
    ]

    for (const input of scenarios) {
      const out = enforceUniqueRenderKeys(input)
      // `id` itself is never rewritten — retry/attachment logic keys off it.
      expect(idsOf(out)).toEqual(idsOf(input))
      // And every branch leaves render keys pairwise distinct.
      const keys = keysOf(out)
      expect(new Set(keys).size).toBe(keys.length)
    }
  })
})
