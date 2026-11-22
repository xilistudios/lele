import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'

import { mergeMessages } from './messageMerge'

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

function makeSaturatedBase(from: number, to: number): ChatMessage[] {
  const msgs: ChatMessage[] = []
  for (let n = from; n <= to; n++) {
    msgs.push(makeUser(`b_q${n}`, `question number ${n}`))
    msgs.push(makeAssistant(`b_a${n}`, `answer number ${n}`))
  }
  return msgs
}

const renderKey = (m: ChatMessage) => m.stableId ?? m.id

const compact = (list: ChatMessage[]) => list.map((m) => `${m.role}:${m.content}`)

function dbg(label: string, merged: ChatMessage[]): void {
  if (process.env.DEBUG_ORDER) {
    console.log(`[${label}] ${compact(merged).join(' → ')}`)
  }
}

function expectSaneOrder(merged: ChatMessage[]) {
  const keyCounts = new Map<string, number>()
  for (const m of merged) {
    const k = renderKey(m)
    keyCounts.set(k, (keyCounts.get(k) ?? 0) + 1)
  }
  const dupKeys = [...keyCounts.entries()].filter(([, n]) => n > 1).map(([k, n]) => `${k} (x${n})`)
  expect(dupKeys).toEqual([])

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

describe('Message ordering window regression (saturated 50-message sliding window)', () => {
  test('R1: saturated 50-message window — pending bubble keeps its chronological slot, not stranded at the end', () => {
    const base = makeSaturatedBase(1, 25)
    expect(base.length).toBe(50)

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

    expect(merged.filter((m) => m.content === 'question number 26').length).toBe(1)
    const iQ = merged.findIndex((m) => m.content === 'question number 26')
    const iA = merged.findIndex((m) => m.content === 'answer number 26')
    expect(iQ).toBeGreaterThanOrEqual(0)
    expect(iA).toBe(iQ + 1)
    expect(merged[iQ]?.optimistic).toBe(true)
    expect(merged[merged.length - 1]?.content).toBe('answer number 26')
    expectSaneOrder(merged)
  })

  test('R2: saturation accumulates one immortal optimistic user per turn', () => {
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

    expect(compact(merged.filter((m) => m.optimistic === true))).toEqual([])
    for (const n of [26, 27, 28]) {
      expect(merged.filter((m) => m.content === `question number ${n}`).length).toBe(1)
    }
    expectSaneOrder(merged)
  })

  test('R2b: anchored confirmation survives the window slide (pending → confirmed)', () => {
    const streaming = [
      optimisticUser('question number 29', {
        id: 's_q29',
        optimisticBaseCount: 25,
        optimisticAnchorId: 'b_q28',
      }),
      completedAssistant('answer number 29', { id: 's_a29' }),
    ]

    const baseA = makeSaturatedBase(4, 28)
    expect(baseA.length).toBe(50)
    expect(baseA.filter((m) => m.role === 'user').length).toBe(25)

    const mergedA = mergeMessages(baseA, streaming)
    dbg('R2b-A', mergedA)

    expect(mergedA.filter((m) => m.content === 'question number 29').length).toBe(1)
    const iQA = mergedA.findIndex((m) => m.content === 'question number 29')
    const iAA = mergedA.findIndex((m) => m.content === 'answer number 29')
    expect(iQA).toBeGreaterThanOrEqual(0)
    expect(iAA).toBe(iQA + 1)
    expect(mergedA[iQA]?.optimistic).toBe(true)
    expect(mergedA[mergedA.length - 1]?.content).toBe('answer number 29')
    expectSaneOrder(mergedA)

    const baseB = makeSaturatedBase(5, 29)
    expect(baseB.length).toBe(50)
    expect(baseB.filter((m) => m.role === 'user').length).toBe(25)

    const mergedB = mergeMessages(baseB, streaming)
    dbg('R2b-B', mergedB)

    expect(compact(mergedB.filter((m) => m.optimistic === true))).toEqual([])
    expect(mergedB.filter((m) => m.content === 'question number 29').length).toBe(1)
    expect(mergedB.filter((m) => m.content === 'answer number 29').length).toBe(1)
    expect(mergedB[mergedB.length - 1]?.content).toBe('answer number 29')
    expectSaneOrder(mergedB)
  })

  test('R3 (D2): a single streaming assistant blocks positional matching for every later base assistant', () => {
    const base = [u('q1'), a('a1'), u('q2'), a('a2'), u('q3'), a('a3')]
    const streaming = [assistant('partial', { streaming: true }), assistant('a3')]

    const merged = mergeMessages(base, streaming)
    dbg('R3', merged)

    expect(merged.filter((m) => m.content === 'a3').length).toBe(1)
    expectSaneOrder(merged)
  })

  test('R4 (D3): positional matching must not let a stale leftover steal an unrelated base assistant key', () => {
    const base = [u('q1'), a('a1'), u('q2'), a('a2'), u('q3'), a('a3')]
    const streaming = [assistant('a1', { stableId: 'stale-1' })]

    const merged = mergeMessages(base, streaming)
    dbg('R4', merged)

    const a3Entry = merged.find((m) => m.content === 'a3')
    expect(a3Entry?.stableId).not.toBe('stale-1')
    expect(merged.filter((m) => m.content === 'a1').length).toBe(1)
    expectSaneOrder(merged)
  })

  test('R5 (D4): merged output never contains duplicate React render keys', () => {
    const base = [
      user('first question', { stableId: 'S0' }),
      assistant('answer'),
      user('second question', { stableId: 'S0' }),
    ]
    const streaming: ChatMessage[] = []

    const merged = mergeMessages(base, streaming)
    dbg('R5', merged)

    expect(compact(merged)).toEqual([
      'user:first question',
      'assistant:answer',
      'user:second question',
    ])
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

    expect(merged.filter((m) => m.role === 'tool' && m.toolCallId === 'call_1').length).toBe(1)
    expect(merged.filter((m) => m.content === 'final answer').length).toBe(1)
    expect(merged.map((m) => m.role)).toEqual(['user', 'assistant', 'tool', 'assistant'])
    expect(merged[0]?.content).toBe('run a command')
    expect(merged[1]?.reasoningContent).toBe('thinking about it')
    expect(merged[2]?.toolCallId).toBe('call_1')
    expect(merged[3]?.content).toBe('final answer')
    expectSaneOrder(merged)
  })

  test('R7: stale leftover from an old turn must not be stranded below newer turns', () => {
    const base = [u('q1'), a('a1'), u('q2'), a('a2'), u('q3'), a('a3')]
    const streaming = [assistant('a1', { id: 's_old' })]

    const merged = mergeMessages(base, streaming)
    dbg('R7', merged)

    expect(merged.filter((m) => m.content === 'a1').length).toBe(1)
    expectSaneOrder(merged)
  })

  test('R8: a reasoning-only iteration is not duplicated once its twin is persisted', () => {
    const base = [
      ...makeSaturatedBase(1, 24),
      u('question number 25', { id: 'b_q25' }),
      assistant('', { id: 'b_i25', reasoningContent: 'thinking' }),
      assistant('answer number 25', { id: 'b_a25' }),
    ]
    const streaming = [
      completedAssistant('', { id: 's_stale', reasoningContent: 'thinking' }),
      completedAssistant('', { id: 's_live', reasoningContent: 'thinking' }),
      optimisticUser('question number 26', { id: 'u_pending', optimisticBaseCount: 25 }),
    ]

    const merged = mergeMessages(base, streaming)
    dbg('R8', merged)

    const thinking = merged.filter((m) => m.reasoningContent === 'thinking')
    expect(thinking.length).toBe(1)
    expect(merged.indexOf(thinking[0])).toBeLessThan(
      merged.findIndex((m) => m.content === 'answer number 25'),
    )
    expectSaneOrder(merged)
  })

  test('R9: the persisted copy of a reasoning-only iteration reuses the live React key', () => {
    const base = [
      ...makeSaturatedBase(1, 24),
      u('question number 25', { id: 'b_q25' }),
      assistant('', { id: 'b_i25', reasoningContent: 'thinking about 25' }),
      assistant('answer number 25', { id: 'b_a25' }),
    ]
    const streaming = [
      completedAssistant('', {
        id: 's_i25',
        stableId: 's_i25',
        reasoningContent: 'thinking about 25',
      }),
      optimisticUser('question number 26', { id: 'u_pending', optimisticBaseCount: 25 }),
    ]

    const merged = mergeMessages(base, streaming)
    dbg('R9', merged)

    const iters = merged.filter((m) => m.reasoningContent === 'thinking about 25')
    expect(iters.length).toBe(1)
    expect(renderKey(iters[0])).toBe('s_i25')
    expectSaneOrder(merged)
  })
})
