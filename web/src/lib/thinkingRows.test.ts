/**
 * Unit tests for the render-level collapse of adjacent thinking cards
 * (lib/thinkingRows.ts).
 *
 * The contract under test is visible behaviour, not the implementation: a run
 * of 2+ rows that each render as a bare "Thinking…" card must become ONE card
 * whose body is the concatenation of the run's reasoning, and every row that
 * shows anything else (prose, a tool card, a user bubble, an attachment, the
 * archived-from-context banner) must break the run.
 *
 * The fixtures are built through `msg()` so each case states only the fields it
 * cares about; `ChatMessage` requires `id`, `role`, `content`, `streaming`,
 * `createdAt`, which is exactly what the helper defaults.
 */
import { describe, expect, test } from 'bun:test'
import { collapseThinkingRuns, isThinkingOnlyRow } from './thinkingRows'
import type { ChatMessage } from './types'

/** Minimal ChatMessage builder — override only what the case is about. */
function msg(overrides: Partial<ChatMessage> & { id: string }): ChatMessage {
  return {
    role: 'assistant',
    content: '',
    streaming: false,
    createdAt: '2026-05-28T10:00:00.000Z',
    ...overrides,
  }
}

/** An assistant iteration that carries reasoning and no visible text. */
function thinking(
  id: string,
  reasoning: string,
  overrides: Partial<ChatMessage> = {},
): ChatMessage {
  return msg({ id, reasoningContent: reasoning, ...overrides })
}

describe('isThinkingOnlyRow — mirrors the MessageBubble visibility rules', () => {
  test('assistant + no text + reasoning is a thinking-only row', () => {
    expect(isThinkingOnlyRow(thinking('a', 'let me think'))).toBe(true)
  })

  test('whitespace-only content still counts as "no text"', () => {
    expect(isThinkingOnlyRow(msg({ id: 'a', content: ' \n\n ', reasoningContent: 'hmm' }))).toBe(
      true,
    )
  })

  test('assistant WITH content is not thinking-only', () => {
    expect(isThinkingOnlyRow(msg({ id: 'a', content: 'answer', reasoningContent: 'hmm' }))).toBe(
      false,
    )
  })

  test('assistant with no reasoning is not thinking-only', () => {
    expect(isThinkingOnlyRow(msg({ id: 'a', content: '' }))).toBe(false)
  })

  test('whitespace-only reasoning is not thinking-only', () => {
    expect(isThinkingOnlyRow(msg({ id: 'a', content: '', reasoningContent: '  \n ' }))).toBe(false)
  })

  test('user and tool rows are never thinking-only', () => {
    expect(isThinkingOnlyRow(msg({ id: 'u', role: 'user', reasoningContent: 'hmm' }))).toBe(false)
    expect(isThinkingOnlyRow(msg({ id: 't', role: 'tool', reasoningContent: 'hmm' }))).toBe(false)
  })

  test('an attachments-carrying assistant is not thinking-only', () => {
    const m = msg({
      id: 'a',
      reasoningContent: 'hmm',
      attachments: [{ name: 'report.pdf' }],
    })
    expect(isThinkingOnlyRow(m)).toBe(false)
  })

  test('an excludeFromContext assistant is not thinking-only (it renders a banner)', () => {
    expect(
      isThinkingOnlyRow(msg({ id: 'a', reasoningContent: 'hmm', excludeFromContext: true })),
    ).toBe(false)
  })
})

describe('collapseThinkingRuns — collapsing', () => {
  test('two adjacent reasoning-only assistants collapse into one, texts joined by a blank line', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'first thought'),
      thinking('b', 'second thought'),
    ])
    expect(out).toHaveLength(1)
    expect(out[0].reasoningContent).toBe('first thought\n\nsecond thought')
  })

  test('the collapsed row keeps the FIRST row id / createdAt / role', () => {
    const first = thinking('a', 'one')
    const second = thinking('b', 'two', { createdAt: '2026-05-28T10:00:05.000Z' })
    const out = collapseThinkingRuns([first, second])
    expect(out[0].id).toBe('a')
    expect(out[0].createdAt).toBe(first.createdAt)
    expect(out[0].role).toBe('assistant')
  })

  test('three in a row collapse into one, in order', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      thinking('b', 'two'),
      thinking('c', 'three'),
    ])
    expect(out).toHaveLength(1)
    expect(out[0].reasoningContent).toBe('one\n\ntwo\n\nthree')
  })

  test('streaming is true when ANY row of the run was streaming', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      thinking('b', 'two', { streaming: true }),
      thinking('c', 'three'),
    ])
    expect(out[0].streaming).toBe(true)
  })

  test('streaming is false when no row of the run was streaming', () => {
    const out = collapseThinkingRuns([thinking('a', 'one'), thinking('b', 'two')])
    expect(out[0].streaming).toBe(false)
  })

  test('identical consecutive reasoning is printed once (duplicate live+persisted copy)', () => {
    const out = collapseThinkingRuns([
      thinking('live', 'same paragraph'),
      thinking('persisted', 'same paragraph'),
    ])
    expect(out).toHaveLength(1)
    expect(out[0].reasoningContent).toBe('same paragraph')
  })

  test('non-adjacent identical reasoning is kept (only CONSECUTIVE copies collapse)', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'same'),
      thinking('b', 'other'),
      thinking('c', 'same'),
    ])
    expect(out[0].reasoningContent).toBe('same\n\nother\n\nsame')
  })

  test('texts are trimmed before joining', () => {
    const out = collapseThinkingRuns([thinking('a', '\n  one  \n'), thinking('b', '\ttwo ')])
    expect(out[0].reasoningContent).toBe('one\n\ntwo')
  })

  test('stableId of the collapsed row is the first row stableId', () => {
    const out = collapseThinkingRuns([
      thinking('live-1', 'one', { stableId: 'stable-1' }),
      thinking('live-2', 'two', { stableId: 'stable-2' }),
    ])
    expect(out[0].stableId).toBe('stable-1')
  })

  test('stableId of the collapsed row falls back to the first row id', () => {
    const out = collapseThinkingRuns([thinking('a', 'one'), thinking('b', 'two')])
    expect(out[0].stableId).toBe('a')
  })

  test('two separate runs collapse independently, prose between them survives', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      thinking('b', 'two'),
      msg({ id: 'answer', content: 'the answer' }),
      thinking('c', 'three'),
      thinking('d', 'four'),
    ])
    expect(out.map((m) => m.id)).toEqual(['a', 'answer', 'c'])
    expect(out[0].reasoningContent).toBe('one\n\ntwo')
    expect(out[2].reasoningContent).toBe('three\n\nfour')
  })
})

describe('collapseThinkingRuns — run boundaries', () => {
  test('reasoning-only followed by an assistant WITH content does not collapse', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      msg({ id: 'b', content: 'answer', reasoningContent: 'two' }),
    ])
    expect(out).toHaveLength(2)
    expect(out[0].reasoningContent).toBe('one')
    expect(out[1].content).toBe('answer')
  })

  test('a tool card between two reasoning-only rows prevents collapsing', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      msg({ id: 'tool', role: 'tool', toolName: 'shell', toolStatus: 'completed' }),
      thinking('b', 'two'),
    ])
    expect(out).toHaveLength(3)
    expect(out.map((m) => m.id)).toEqual(['a', 'tool', 'b'])
  })

  test('a user message between two reasoning-only rows prevents collapsing', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      msg({ id: 'u', role: 'user', content: 'go on' }),
      thinking('b', 'two'),
    ])
    expect(out).toHaveLength(3)
    expect(out.map((m) => m.id)).toEqual(['a', 'u', 'b'])
  })

  test('an attachments-carrying assistant breaks the run', () => {
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      msg({ id: 'att', reasoningContent: 'two', attachments: [{ name: 'x.png' }] }),
      thinking('b', 'three'),
    ])
    expect(out.map((m) => m.id)).toEqual(['a', 'att', 'b'])
  })
})

describe('collapseThinkingRuns — identity preservation', () => {
  test('a single thinking row keeps object identity', () => {
    const only = thinking('a', 'one')
    const input = [msg({ id: 'u', role: 'user', content: 'hi' }), only]
    const out = collapseThinkingRuns(input)
    expect(out[1]).toBe(only)
  })

  test('a list with no runs returns the SAME array', () => {
    const input = [
      msg({ id: 'u', role: 'user', content: 'hi' }),
      msg({ id: 'a', content: 'answer', reasoningContent: 'thought' }),
      msg({ id: 't', role: 'tool', toolName: 'shell' }),
    ]
    expect(collapseThinkingRuns(input) as ChatMessage[]).toBe(input)
  })

  test('an empty list returns the SAME array', () => {
    const input: ChatMessage[] = []
    expect(collapseThinkingRuns(input)).toBe(input)
  })

  test('a lone thinking row next to a collapsed run survives verbatim', () => {
    const lone = thinking('lone', 'alone')
    const out = collapseThinkingRuns([
      thinking('a', 'one'),
      thinking('b', 'two'),
      msg({ id: 'x', content: 'text' }),
      lone,
    ])
    // Only the pair collapses; `lone` is a run of 1 and keeps its identity.
    expect(out.map((m) => m.id)).toEqual(['a', 'x', 'lone'])
    expect(out[2]).toBe(lone)
  })

  test('the input list is never mutated', () => {
    const input = [thinking('a', 'one'), thinking('b', 'two')]
    const snapshot = input.map((m) => ({ ...m }))
    collapseThinkingRuns(input)
    expect(input).toHaveLength(2)
    expect(input.map((m) => ({ ...m }))).toEqual(snapshot)
  })
})

describe('collapseThinkingRuns — a realistic turn', () => {
  test('discarded retry + guidance + tool step collapse to one card, tool cards kept', () => {
    // Shape produced by one turn: iteration 1 thinks and calls a tool, the
    // retry thinks again with no text, the tool result lands, then the answer.
    const out = collapseThinkingRuns([
      msg({ id: 'u', role: 'user', content: 'check the disk' }),
      thinking('i1', 'I should run df'),
      msg({ id: 't1', role: 'tool', toolName: 'shell', toolStatus: 'completed' }),
      thinking('i2', 'empty response, retrying'),
      thinking('i3', 'still no text'),
      msg({ id: 't2', role: 'tool', toolName: 'shell', toolStatus: 'completed' }),
      thinking('i4', 'now summarize'),
      msg({ id: 'a', content: 'Disk is 40% full', reasoningContent: 'summarizing' }),
    ])
    expect(out.map((m) => m.id)).toEqual(['u', 'i1', 't1', 'i2', 't2', 'i4', 'a'])
    expect(out[3].reasoningContent).toBe('empty response, retrying\n\nstill no text')
    // The assistant WITH text kept its own reasoning card untouched.
    expect(out[6].reasoningContent).toBe('summarizing')
  })
})

describe('collapseThinkingRuns — array identity without a run', () => {
  test('a list holding ONE thinking-only row still returns the SAME array', () => {
    // Documented contract: only a run of 2+ may allocate a new array, otherwise
    // every `useMemo` downstream of the collapse is defeated on each stream tick.
    const lone = thinking('a', 'one')
    const input = [msg({ id: 'u', role: 'user', content: 'hi' }), lone]
    expect(collapseThinkingRuns(input) as ChatMessage[]).toBe(input)
  })
})
