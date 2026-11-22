import { beforeEach, describe, expect, test } from 'bun:test'
import {
  isOrderingDebugEnabled,
  noteHistorySnapshot,
  noteWsEvent,
  observeMergedOrder,
  orderingReport,
  resetOrderingMonitor,
} from '../lib/orderingMonitor'
import type { ChatMessage } from '../lib/types'

const store = new Map<string, string>()

beforeEach(() => {
  store.clear()
  ;(globalThis as unknown as { window: unknown }).window = {
    localStorage: {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    },
    location: { search: '' },
  }
  resetOrderingMonitor()
  store.set('lele.orderingDebug', '1')
})

function user(id: string, content: string): ChatMessage {
  return { id, role: 'user', content, sessionKey: 'base:chat:1' } as ChatMessage
}

function assistant(id: string, content: string, reasoning?: string): ChatMessage {
  return {
    id,
    role: 'assistant',
    content,
    reasoningContent: reasoning,
    sessionKey: 'base:chat:1',
  } as ChatMessage
}

function tool(id: string, toolCallId: string, options: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id,
    role: 'tool',
    content: '',
    toolCallId,
    toolName: 'read_file',
    sessionKey: 'base:chat:1',
    ...options,
  } as ChatMessage
}

function kinds(): string[] {
  return orderingReport().violations.map((v) => v.kind)
}

describe('isOrderingDebugEnabled', () => {
  test('off by default', () => {
    store.clear()
    expect(isOrderingDebugEnabled()).toBe(false)
  })

  test('on via localStorage', () => {
    expect(isOrderingDebugEnabled()).toBe(true)
  })

  test('on via URL parameter', () => {
    store.clear()
    ;(globalThis as unknown as { window: { location: { search: string } } }).window.location = {
      search: '?orderingDebug=1',
    }
    expect(isOrderingDebugEnabled()).toBe(true)
  })
})

describe('observeMergedOrder', () => {
  test('returns the SAME array identity when disabled (hot path contract)', () => {
    store.clear()
    const merged = [user('u1', 'hi')]
    expect(observeMergedOrder(merged, [], [])).toBe(merged)
    expect(orderingReport().trace).toHaveLength(0)
  })

  test('records a trace entry per merge and keeps the array identity', () => {
    const merged = [user('u1', 'hi'), assistant('a1', 'hello')]
    expect(observeMergedOrder(merged, merged, [])).toBe(merged)
    expect(orderingReport().mergeCount).toBe(1)
    expect(orderingReport().trace.at(-1)?.kind).toBe('merge')
  })

  test('duplicate-key: two entries sharing stableId ?? id', () => {
    const base = [user('u1', 'hi')]
    const merged = [user('u1', 'hi'), { ...user('u1', 'hi'), id: 'other', stableId: 'u1' }]
    observeMergedOrder(merged as ChatMessage[], base, [])
    expect(kinds()).toContain('duplicate-key')
  })

  test('base-order: merge reorders canonical history', () => {
    const base = [user('u1', 'q1'), assistant('a1', 'a1'), user('u2', 'q2')]
    const merged = [user('u2', 'q2'), user('u1', 'q1'), assistant('a1', 'a1')]
    observeMergedOrder(merged as ChatMessage[], base, [])
    expect(kinds()).toContain('base-order')
  })

  test('stranded-copy: a live copy renders beside its persisted twin', () => {
    const base = [user('u1', 'q1'), assistant('a1', 'the answer')]
    const merged = [...base, assistant('temp-a', 'the answer')]
    observeMergedOrder(merged as ChatMessage[], base, [assistant('temp-a', 'the answer')])
    expect(kinds()).toEqual(['stranded-copy'])
  })

  test('stranded-copy: reasoning-only iteration is identified by its reasoning', () => {
    const base = [user('u1', 'q1'), assistant('a1', '', 'Thinking hard')]
    const merged = [...base, assistant('temp-a', '', 'Thinking hard')]
    observeMergedOrder(merged as ChatMessage[], base, [])
    expect(kinds()).toEqual(['stranded-copy'])
  })

  test('stranded-copy is NOT tripped by the streaming tail being a prefix of an older copy', () => {
    const base = [user('u1', 'q1'), assistant('h-a1', 'answer 1')]
    const tail = { ...assistant('temp-a16', 'answer 1'), streaming: true }
    observeMergedOrder([...base, user('u2', 'q2'), tail], base, [tail])
    expect(kinds()).toEqual([])
  })

  test('stable-order: a message moves above one that preceded it', () => {
    const first = [user('u1', 'q1'), assistant('a1', 'a1'), user('u2', 'q2')]
    observeMergedOrder(first, first, [])
    const second = [user('u1', 'q1'), user('u2', 'q2'), assistant('a1', 'a1')]
    observeMergedOrder(second, first, [])
    expect(kinds()).toContain('stable-order')
    expect(
      orderingReport()
        .violations.map((v) => v.detail)
        .join(' '),
    ).toContain('is now after')
  })

  test('stable-order is NOT tripped by confirmation, growth or window sliding', () => {
    const live1 = [user('u1', 'q1'), assistant('temp-a', 'a1')]
    observeMergedOrder(live1, [], live1)

    const base2 = [user('h-u1', 'q1'), assistant('h-a1', 'a1')]
    observeMergedOrder(base2, base2, [])

    const live2 = [user('u2', 'q2'), assistant('temp-a2', 'a2')]
    observeMergedOrder([...base2, ...live2], base2, live2)
    const base3 = [...base2, user('h-u2', 'q2'), assistant('h-a2', 'a2')]
    observeMergedOrder(base3, base3, [])

    expect(kinds()).toEqual([])
  })

  test('stable-order is NOT tripped when the sliding window drops the oldest turn', () => {
    const base = [user('u1', 'q1'), assistant('a1', 'a1'), user('u2', 'q2')]
    observeMergedOrder(base, base, [])
    const slid = [user('u2', 'q2')]
    observeMergedOrder(slid, slid, [])
    expect(kinds()).toEqual([])
  })

  test('key-identity: a render key that moves to ANOTHER message is reported', () => {
    observeMergedOrder([user('k1', 'q1'), assistant('k2', 'answer one')], [], [])
    observeMergedOrder([user('k1', 'q2'), assistant('k2', 'answer two')], [], [])
    expect(kinds()).toEqual(['key-identity'])
    const detail = orderingReport().violations[0].detail
    expect(detail).toContain('k1')
    expect(detail).toContain('q1')
    expect(detail).toContain('q2')
  })

  test('key-identity reports a re-pointed key ONCE, not on every following merge', () => {
    observeMergedOrder([user('k1', 'q1')], [], [])
    const moved = [user('k1', 'q2')]
    observeMergedOrder(moved, [], [])
    observeMergedOrder(moved, [], [])
    expect(kinds().filter((k) => k === 'key-identity')).toHaveLength(1)
  })

  test('key-identity keeps scanning after a mismatch', () => {
    observeMergedOrder([user('k1', 'q1'), user('k2', 'q2')], [], [])
    observeMergedOrder([user('k1', 'zz'), user('k2', 'yy')], [], [])
    observeMergedOrder([user('k1', 'zz'), user('k2', 'xx')], [], [])
    expect(kinds().filter((k) => k === 'key-identity')).toHaveLength(2)
    expect(orderingReport().violations.at(-1)?.detail).toContain('k2')
  })

  test('key-identity is NOT tripped when a tool card receives its result', () => {
    const executing = tool('ws-call-1', 'call_1', { toolStatus: 'executing' })
    observeMergedOrder([executing], [], [executing])
    const done = { ...executing, toolResult: 'note content', toolStatus: 'completed' as const }
    observeMergedOrder([done], [], [done])
    expect(kinds()).toEqual([])
  })

  test("key-identity still catches one tool card taking ANOTHER call's key", () => {
    const first = tool('ws-call-1', 'call_1', { toolResult: 'note content' })
    observeMergedOrder([first], [], [first])
    const stolen = tool('ws-call-1', 'call_5', { toolResult: 'note content' })
    observeMergedOrder([stolen], [], [stolen])
    expect(kinds()).toContain('key-identity')
  })

  test('key-identity is NOT tripped by a message re-keyed onto its own id', () => {
    observeMergedOrder([user('live-u1', 'q1'), assistant('live-a1', 'a1')], [], [])
    observeMergedOrder([user('hash-u1', 'q1'), assistant('hash-a1', 'a1')], [], [])
    expect(kinds()).toEqual([])
  })

  test('key-identity is NOT tripped by indistinguishable messages swapping keys', () => {
    const base = [user('u1', 'yes'), assistant('a1', 'ok'), user('u2', 'yes')]
    observeMergedOrder(base, base, [])
    const swapped = [user('u2', 'yes'), assistant('a1', 'ok'), user('u1', 'yes')]
    observeMergedOrder(swapped, base, [])
    expect(kinds()).not.toContain('key-identity')
  })

  test('legitimate duplicate text inside history is not a stranded copy', () => {
    const base = [user('u1', 'yes'), assistant('a1', 'ok'), user('u2', 'yes')]
    observeMergedOrder(base, base, [])
    expect(kinds()).toEqual([])
  })

  test('a persisted copy and a NEW live answer for the next turn coexist cleanly', () => {
    const base = [user('h-u1', 'q1'), assistant('h-a1', 'a1')]
    const live = [user('u2', 'q2'), assistant('temp-a2', 'a2')]
    observeMergedOrder([...base, ...live], base, live)
    expect(kinds()).toEqual([])
  })

  test('violations are persisted to localStorage as soon as they happen', () => {
    const base = [user('u1', 'q1')]
    observeMergedOrder([user('u1', 'q1'), { ...user('u1', 'q1'), stableId: 'u1' }], base, [])
    const saved = JSON.parse(store.get('lele.orderingReport') ?? '{}')
    expect(saved.violations.length).toBeGreaterThan(0)
    expect(saved.violations[0].kind).toBe('duplicate-key')
  })

  test('trace records WS events and HTTP snapshots in arrival order', () => {
    noteWsEvent('message.stream', { message_id: 'm1', chunk: 'hi' })
    noteHistorySnapshot('base:chat:1', [user('u1', 'q1')])
    const kinds = orderingReport().trace.map((t) => t.kind)
    expect(kinds).toEqual(['ws', 'http'])
  })

  test('trace is bounded (ring buffer) so a long session cannot grow forever', () => {
    for (let i = 0; i < 700; i++) {
      noteWsEvent('message.stream', { message_id: `m${i}` })
    }
    expect(orderingReport().trace.length).toBeLessThanOrEqual(600)
  })
})
