import { describe, expect, test } from 'bun:test'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { ChatMessage, RawHistoryMessage } from '../lib/types'
import { clearStableIdRegistry, lookupStableIdFromEnd, registerStableId } from './stableIdRegistry'

const SESSION = 'window-session'

const renderKey = (m: ChatMessage) => m.stableId ?? m.id

const contentOf = (m: ChatMessage) => m.content.trim() || m.reasoningContent?.trim() || ''

const TAIL = { bindStableIds: true }

function keysOf(history: RawHistoryMessage[], tail = true): string[] {
  return toChatMessages(history, SESSION, tail ? TAIL : {}).map(renderKey)
}

function renderOf(history: RawHistoryMessage[], tail = true): Array<[string, string]> {
  return toChatMessages(history, SESSION, tail ? TAIL : {}).map((m) => [renderKey(m), contentOf(m)])
}

function expectStableKeys(pages: RawHistoryMessage[][], seen: Map<string, string>): void {
  for (const [pageIndex, page] of pages.entries()) {
    const rendered = renderOf(page)
    const keysInPage = new Set<string>()
    for (const [key, text] of rendered) {
      expect(keysInPage.has(key)).toBe(false)
      keysInPage.add(key)
      const previous = seen.get(key)
      if (previous !== undefined) {
        expect({ key, page: pageIndex, text }).toEqual({ key, page: pageIndex, text: previous })
      }
      seen.set(key, text)
    }
  }
}

describe('durable stableIds across the sliding history window', () => {
  test('an OLDER page (loadMore) never binds by rank, even when the counts coincide', () => {
    clearStableIdRegistry()
    const ANSWER = 'Done — nothing else to do.'
    const answer = (n: number): RawHistoryMessage =>
      ({
        id: `${SESSION}:old-${n}`,
        role: 'assistant',
        content: ANSWER,
      }) as unknown as RawHistoryMessage

    registerStableId('assistant', ANSWER, 'ws-tail-1')
    registerStableId('assistant', ANSWER, 'ws-tail-2')

    const olderPage = [answer(1), answer(2)]
    const bound = keysOf(olderPage, false)
    expect(bound).toEqual([`${SESSION}:old-1`, `${SESSION}:old-2`])
    for (const key of bound) expect(key).not.toContain('ws-tail')
    expect(keysOf(olderPage, true)).toEqual(['ws-tail-1', 'ws-tail-2'])
  })

  test('a refetch that beats the confirmation keeps every copy on its own id', () => {
    clearStableIdRegistry()
    const RESULT = 'note content\n'
    const tool = (n: number): RawHistoryMessage =>
      ({
        id: `${SESSION}:tool-${n}`,
        role: 'tool',
        content: RESULT,
        tool_call_id: `call_${n}`,
        tool_name: 'read_file',
      }) as unknown as RawHistoryMessage

    registerStableId('tool', 'id:call_1', 'ws-call-1')
    registerStableId('tool', 'id:call_2', 'ws-call-2')

    const page = [tool(1), tool(2), tool(3)]
    expect(keysOf(page)).toEqual(['ws-call-1', 'ws-call-2', `${SESSION}:tool-3`])

    clearStableIdRegistry()
    const ANSWER = 'same answer'
    const answer = (n: number): RawHistoryMessage =>
      ({
        id: `${SESSION}:a-${n}`,
        role: 'assistant',
        content: ANSWER,
      }) as unknown as RawHistoryMessage
    registerStableId('assistant', ANSWER, 'ws-a1')
    registerStableId('assistant', ANSWER, 'ws-a2')
    expect(keysOf([answer(1), answer(2)])).toEqual(['ws-a1', 'ws-a2'])
    expect(keysOf([answer(1), answer(2), answer(3)])).toEqual([
      `${SESSION}:a-1`,
      `${SESSION}:a-2`,
      `${SESSION}:a-3`,
    ])
  })

  test('lookupStableIdFromEnd addresses the newest registered copies', () => {
    clearStableIdRegistry()
    registerStableId('tool', 'same result', 'ws-1')
    registerStableId('tool', 'same result', 'ws-2')
    registerStableId('tool', 'same result', 'ws-3')

    expect(lookupStableIdFromEnd('tool', 'same result', 0)).toBe('ws-3')
    expect(lookupStableIdFromEnd('tool', 'same result', 1)).toBe('ws-2')
    expect(lookupStableIdFromEnd('tool', 'same result', 2)).toBe('ws-1')
    expect(lookupStableIdFromEnd('tool', 'same result', 3)).toBeUndefined()
    expect(lookupStableIdFromEnd('tool', 'never registered', 0)).toBeUndefined()
  })

  test('tool cards keep their key when an older identical result slides out', () => {
    clearStableIdRegistry()
    const RESULTS = 'note content\n'
    const tool = (n: number): RawHistoryMessage =>
      ({
        id: `${SESSION}:tool-${n}`,
        role: 'tool',
        content: RESULTS,
        tool_call_id: `call_${n}`,
        tool_name: 'read_file',
      }) as unknown as RawHistoryMessage

    registerStableId('tool', 'id:call_1', 'ws-call-1')
    registerStableId('tool', 'note content\n', 'ws-call-1')
    registerStableId('tool', 'id:call_2', 'ws-call-2')
    registerStableId('tool', 'note content\n', 'ws-call-2')
    registerStableId('tool', 'id:call_3', 'ws-call-3')
    registerStableId('tool', 'note content\n', 'ws-call-3')

    expect(keysOf([tool(1), tool(2), tool(3)])).toEqual(['ws-call-1', 'ws-call-2', 'ws-call-3'])
    expect(keysOf([tool(2), tool(3)])).toEqual(['ws-call-2', 'ws-call-3'])
    expect(keysOf([tool(3)])).toEqual(['ws-call-3'])
  })

  test('repeated answers keep their key while older copies slide out', () => {
    clearStableIdRegistry()
    const ANSWER = 'Done — nothing else to do.'
    const answer = (n: number): RawHistoryMessage =>
      ({
        id: `${SESSION}:answer-${n}`,
        role: 'assistant',
        content: ANSWER,
      }) as unknown as RawHistoryMessage

    const pages = [1, 2, 3].map((n) => {
      registerStableId('assistant', ANSWER, `ws-answer-${n}`)
      return keysOf([answer(n)])
    })

    expect(pages).toEqual([['ws-answer-1'], ['ws-answer-2'], ['ws-answer-3']])
  })

  test('repeated reasoning (identical thinking text) keeps each iteration distinct', () => {
    clearStableIdRegistry()
    const REASONING = 'Let me read the file to check.'
    const thinking = (n: number): RawHistoryMessage =>
      ({
        id: `${SESSION}:thinking-${n}`,
        role: 'assistant',
        content: '',
        reasoning_content: REASONING,
      }) as unknown as RawHistoryMessage

    registerStableId('assistant', REASONING, 'ws-think-1')
    registerStableId('assistant', REASONING, 'ws-think-2')

    expect(keysOf([thinking(1), thinking(2)])).toEqual(['ws-think-1', 'ws-think-2'])
    expect(keysOf([thinking(2)])).toEqual(['ws-think-2'])
  })

  test('a repeated user text keeps its key as the window slides', () => {
    clearStableIdRegistry()
    const TEXT = 'continúa'
    const user = (n: number): RawHistoryMessage =>
      ({ id: `${SESSION}:user-${n}`, role: 'user', content: TEXT }) as unknown as RawHistoryMessage

    registerStableId('user', TEXT, 'temp-user-1')
    expect(keysOf([user(1)])).toEqual(['temp-user-1'])

    registerStableId('user', TEXT, 'temp-user-2')
    expect(keysOf([user(1), user(2)])).toEqual(['temp-user-1', 'temp-user-2'])
    expect(keysOf([user(2)])).toEqual(['temp-user-2'])
  })

  test('a long session: no key is ever re-pointed at another message', () => {
    clearStableIdRegistry()
    const WINDOW = 50
    const TURNS = 24
    const log: Array<{
      role: ChatMessage['role']
      content: string
      reasoning?: string
      toolCallId?: string
    }> = []
    const pages: RawHistoryMessage[][] = []
    const seen = new Map<string, string>()

    for (let n = 1; n <= TURNS; n++) {
      registerStableId('user', `question ${n}`, `temp-user-${n}`)
      log.push({ role: 'user', content: `question ${n}` })

      registerStableId('assistant', 'same reasoning', `ws-think-${n}`)
      log.push({ role: 'assistant', content: '', reasoning: 'same reasoning' })

      registerStableId('tool', `id:call_${n}`, `ws-call-${n}`)
      registerStableId('tool', 'same result', `ws-call-${n}`)
      log.push({ role: 'tool', content: 'same result', toolCallId: `call_${n}` })

      registerStableId('assistant', `answer ${n}`, `ws-answer-${n}`)
      log.push({ role: 'assistant', content: `answer ${n}` })

      const window = log.slice(Math.max(0, log.length - WINDOW))
      const page = window.map(
        (m, i) =>
          ({
            id: `${SESSION}:m${n}-${i}`,
            role: m.role,
            content: m.content,
            ...(m.reasoning ? { reasoning_content: m.reasoning } : {}),
            ...(m.toolCallId ? { tool_call_id: m.toolCallId, tool_name: 'read_file' } : {}),
          }) as unknown as RawHistoryMessage,
      )
      pages.push(page)
    }

    expectStableKeys(pages, seen)
  })
})
