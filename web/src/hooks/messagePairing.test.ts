/**
 * REGRESSION SUITE — live↔history pairing inside `mergeMessages`.
 *
 * Reproduced in a REAL browser (Chromium + the real gateway + a scripted
 * streaming provider) with `lib/orderingMonitor.ts` attached: from turn 3 of a
 * tool-using conversation onwards, EVERY merge re-assigned the React render
 * keys of already-rendered base messages to their neighbour's live copy, which
 * moves a bubble across the list (the user-reported "the order breaks after a
 * while, only a reload fixes it").
 *
 * The recorded shape (violation `stable-order`, turn 3, tool running):
 *
 *   base      q1 t1 tool1 a1 | q2 t2 tool2 a2          (canonical history)
 *   streaming  q2* t2 tool2 a2 | q3* t3 tool3          (* = optimistic user)
 *
 * Two things are simultaneously true in that snapshot, and each of them used to
 * shift the positional pairing by one:
 *
 *  1. the PREVIOUS turn's live copies (t2, a2) are still in streaming state —
 *     `handleHistoryUpdated` cannot strip them until the async refetch lands,
 *     so they duplicate messages base already holds;
 *  2. the CURRENT turn's first iteration already finished (its stream ended
 *     when the tool call was emitted, `streaming: false`) while the turn keeps
 *     going, so streaming holds a COMPLETED assistant that base does not have.
 *
 * `computeMatchOffset` assumed "the last N completed streaming assistants are
 * the last N base assistants" — false as soon as (1) and (2) coexist. With the
 * off-by-one, `buildFilteredBase` paired a1↔t2, t2↔a2, a2↔t3 and transferred
 * each one's `stableId`, so the bubbles swapped places (and `filterStreamingLeftovers`
 * marked the wrong live copies as used, stranding q3 and the turn's tool).
 *
 * Keep this suite green: it is the exact production shape, not a simplification.
 */
import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'
import { mergeMessages } from './messageMerge'

const SESSION_KEY = 'sess-1'
const NOW = '2026-01-01T00:00:00.000Z'

/** History id shape used by the backend: `<session key>:<content hash>`. */
const baseId = (hash: string) => `${SESSION_KEY}:${hash}`

function user(id: string, content: string, opts: Partial<ChatMessage> = {}): ChatMessage {
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

function assistant(id: string, content: string, opts: Partial<ChatMessage> = {}): ChatMessage {
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

function tool(toolCallId: string, opts: Partial<ChatMessage> = {}): ChatMessage {
  return {
    id: `tool:ws:${toolCallId}`,
    role: 'tool',
    content: '',
    streaming: false,
    createdAt: NOW,
    sessionKey: SESSION_KEY,
    toolCallId,
    toolStatus: 'completed',
    ...opts,
  }
}

/** Render key React/Virtuoso uses (mirrors MessageList.messageKey). */
const key = (m: ChatMessage) => m.stableId ?? m.id
const label = (m: ChatMessage) => `${m.role}:${m.content || m.reasoningContent || m.toolCallId}`

describe('live↔history pairing (real-browser reproduction, tool turn)', () => {
  /**
   * The recorded snapshot: two finished turns in base, the third turn running
   * its tool, and the previous turn's live copies not yet stripped.
   */
  function recordedScenario() {
    const base: ChatMessage[] = [
      user(baseId('q1'), 'question number 1'),
      assistant(baseId('t1'), '', { reasoningContent: 'think 1' }),
      tool('call_1'),
      assistant(baseId('a1'), 'answer number 1'),
      user(baseId('q2'), 'question number 2'),
      assistant(baseId('t2'), '', { reasoningContent: 'think 2' }),
      tool('call_2'),
      assistant(baseId('a2'), 'answer number 2'),
    ]

    const streaming: ChatMessage[] = [
      // Previous turn's optimistic user bubble, still waiting for the strip
      // that `history.updated` could not do (the refetch had not landed yet).
      user('temp-user-q2', 'question number 2', {
        optimistic: true,
        optimisticBaseCount: 1,
        optimisticAnchorId: baseId('q1'),
      }),
      assistant('live-t2', '', { reasoningContent: 'think 2', stableId: 'live-t2' }),
      tool('call_2'),
      assistant('live-a2', 'answer number 2', { stableId: 'live-a2' }),
      // Current turn: iteration 1 finished (its stream ended at the tool call)
      // and the tool is running.
      user('temp-user-q3', 'question number 3', {
        optimistic: true,
        optimisticBaseCount: 2,
        optimisticAnchorId: baseId('q2'),
      }),
      assistant('live-t3', '', { reasoningContent: 'think 3', stableId: 'live-t3' }),
      tool('call_3', { toolStatus: 'executing' }),
    ]

    return mergeMessages(base, streaming)
  }

  test('keeps the chronological order of the whole conversation', () => {
    const merged = recordedScenario()
    expect(merged.map(label)).toEqual([
      'user:question number 1',
      'assistant:think 1',
      'tool:call_1',
      'assistant:answer number 1',
      'user:question number 2',
      'assistant:think 2',
      'tool:call_2',
      'assistant:answer number 2',
      'user:question number 3',
      'assistant:think 3',
      'tool:call_3',
    ])
  })

  test('never transfers a live copy key to a DIFFERENT message', () => {
    const merged = recordedScenario()
    const stableIdOf = (content: string) =>
      merged.find((m) => m.content === content || m.reasoningContent === content)?.stableId

    // Each base assistant keeps its own identity; a live copy's key belongs to
    // the live copy of the SAME message only.
    expect(stableIdOf('answer number 1')).toBeUndefined()
    expect(stableIdOf('answer number 2')).toBe('live-a2')
    expect(stableIdOf('think 2')).toBe('live-t2')
    expect(stableIdOf('think 3')).toBe('live-t3')
  })

  test('renders every render key at most once', () => {
    const merged = recordedScenario()
    const keys = merged.map(key)
    expect(new Set(keys).size).toBe(keys.length)
  })

  test('does not duplicate the previous turn when base already holds it', () => {
    const merged = recordedScenario()
    expect(merged.filter((m) => m.content === 'answer number 2')).toHaveLength(1)
    expect(merged.filter((m) => m.reasoningContent === 'think 2')).toHaveLength(1)
    expect(merged.filter((m) => m.toolCallId === 'call_2')).toHaveLength(1)
  })

  test('drops the optimistic twin of the confirmed user turn', () => {
    const merged = recordedScenario()
    expect(merged.filter((m) => m.content === 'question number 2')).toHaveLength(1)
    expect(merged.find((m) => m.content === 'question number 2')?.optimistic).toBeFalsy()
    // …and the in-flight one is still a live bubble, in place.
    expect(merged.find((m) => m.content === 'question number 3')?.optimistic).toBe(true)
  })

  test('keeps the pending tool of the running turn', () => {
    const merged = recordedScenario()
    expect(merged.find((m) => m.toolCallId === 'call_3')?.toolStatus).toBe('executing')
  })
})

describe('live↔history pairing (identical reasoning across iterations)', () => {
  /**
   * The same off-by-one WITHOUT any stale optimistic bubble: base holds the
   * previous turn, the current turn's first iteration is done (streaming:
   * false) and the second one is streaming. Nothing may be paired positionally,
   * because streaming's completed assistants are a SUBSET of base's tail.
   */
  test('a completed iteration of the running turn does not steal base keys', () => {
    const base = [
      user(baseId('q1'), 'q1'),
      assistant(baseId('t1'), '', { reasoningContent: 'think 1' }),
      tool('call_1'),
      assistant(baseId('a1'), 'a1'),
    ]
    const streaming = [
      user('temp-q2', 'q2', {
        optimistic: true,
        optimisticBaseCount: 1,
        optimisticAnchorId: baseId('q1'),
      }),
      assistant('live-t2', '', { reasoningContent: 'think 2', stableId: 'live-t2' }),
      tool('call_2', { toolStatus: 'executing' }),
      assistant('live-t3', '', {
        reasoningContent: 'think 3',
        streaming: true,
        stableId: 'live-t3',
      }),
    ]

    const merged = mergeMessages(base, streaming)
    expect(merged.map(label)).toEqual([
      'user:q1',
      'assistant:think 1',
      'tool:call_1',
      'assistant:a1',
      'user:q2',
      'assistant:think 2',
      'tool:call_2',
      'assistant:think 3',
    ])
    // The only live key transfer is t2's own message (base has no such copy).
    expect(merged.find((m) => m.content === 'a1')?.stableId).toBeUndefined()
  })
})
