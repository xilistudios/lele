import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { enforceUniqueRenderKeys } from './messageMerge'

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

    expect(out).toBe(input)
  })

  test('2: colliding stableId, distinct ids → first holder keeps its key, second re-keyed onto its own id', () => {
    const input = [
      makeUser('id-1', 'first question', { stableId: 'S0' }),
      makeUser('id-2', 'neutral message'),
      makeUser('id-3', 'second question', { stableId: 'S0' }),
    ]

    const out = enforceUniqueRenderKeys(input)

    expect(keysOf(out)).toEqual(['S0', 'id-2', 'id-3'])
    expect(out.map((m) => m.content)).toEqual([
      'first question',
      'neutral message',
      'second question',
    ])
    expect(out.length).toBe(input.length)
  })

  test('3: colliding key where the fallback id is ALSO taken → __dup1 suffix, id never rewritten', () => {
    const input = [makeUser('X', 'first', { stableId: 'S' }), makeUser('S', 'second')]

    const out = enforceUniqueRenderKeys(input)

    const keys = keysOf(out)
    expect(keys).toEqual(['S', 'S__dup1'])
    expect(new Set(keys).size).toBe(keys.length)
    expect(out[1]?.id).toBe('S')
    expect(out[1]?.stableId).toBe('S__dup1')
    expect(out[0]?.stableId).toBe('S')
  })

  test('4: three-way collision → all keys distinct, suffixes deterministic (__dup1, __dup2)', () => {
    const input = [
      makeUser('S', 'one', { stableId: 'S' }),
      makeUser('S', 'two', { stableId: 'S' }),
      makeUser('S', 'three', { stableId: 'S' }),
    ]

    const out = enforceUniqueRenderKeys(input)

    const keys = keysOf(out)
    expect(keys).toEqual(['S', 'S__dup1', 'S__dup2'])
    expect(new Set(keys).size).toBe(keys.length)
    expect(out.map((m) => m.content)).toEqual(['one', 'two', 'three'])
  })

  test('5: id is never mutated in any branch', () => {
    const scenarios: ChatMessage[][] = [
      [makeUser('a', 'x', { stableId: 'k' }), makeUser('b', 'y')],
      [makeUser('a', 'x', { stableId: 'k' }), makeUser('b', 'y', { stableId: 'k' })],
      [makeUser('X', 'x', { stableId: 'S' }), makeUser('S', 'y')],
    ]

    for (const input of scenarios) {
      const out = enforceUniqueRenderKeys(input)
      expect(idsOf(out)).toEqual(idsOf(input))
      const keys = keysOf(out)
      expect(new Set(keys).size).toBe(keys.length)
    }
  })
})
