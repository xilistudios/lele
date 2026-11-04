/**
 * REGRESSION SUITE — React keys must survive the sliding history window.
 *
 * The backend serves chat history as a sliding window of the last `limit`
 * messages (pkg/channels/rest_chat.go), so as a conversation grows the OLDEST
 * messages fall off the page while the durable stableId registry
 * (hooks/stableIdRegistry.ts) keeps one entry per copy this page confirmed.
 *
 * `toChatMessages` used to look the recorded id up by the occurrence index of
 * the message INSIDE the page. That index is not stable: once an older copy of
 * a repeated identity slid out, every remaining copy read its PREDECESSOR's
 * id, so the React key of a bubble moved to a different message. React and
 * Virtuoso then reconcile the wrong DOM node into the slot — the transcript
 * looks reordered, an old bubble shows up where the newest message belongs,
 * and thinking blocks pile up as a run of copies. A reload cleared the
 * registry and "fixed" it.
 *
 * Tools made this systematic rather than rare: a tool message carries its
 * RESULT in `content`, which is identical across calls (`ls`, `git status`, a
 * failing test), and every thinking model repeats reasoning verbatim. Both are
 * covered here, plus repeated user texts and repeated answers.
 *
 * The invariant under test (the one the user sees): RENDER KEY → MESSAGE TEXT
 * must be a stable mapping across successive windows. A key may disappear from
 * the page (its message scrolled out) but it must never be re-pointed at a
 * different message, and two messages must never share one.
 */
import { describe, expect, test } from 'bun:test'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { ChatMessage, RawHistoryMessage } from '../lib/types'
import { clearStableIdRegistry, lookupStableIdFromEnd, registerStableId } from './stableIdRegistry'

const SESSION = 'window-session'

/** The key `MessageList` / Virtuoso actually reconcile on. */
const renderKey = (m: ChatMessage) => m.stableId ?? m.id

/** What the bubble visibly is: enough to tell two entries apart. */
const contentOf = (m: ChatMessage) => m.content.trim() || m.reasoningContent?.trim() || ''

/** Render keys of a converted page, in document order. */
function keysOf(history: RawHistoryMessage[]): string[] {
  return toChatMessages(history, SESSION).map(renderKey)
}

/** Render keys and their visible text, in document order. */
function renderOf(history: RawHistoryMessage[]): Array<[string, string]> {
  return toChatMessages(history, SESSION).map((m) => [renderKey(m), contentOf(m)])
}

/**
 * Assert the user-visible invariant over every page of a slide sequence:
 * a key always denotes the same text, and no key is used twice in one page.
 *
 * `seen` accumulates key → text across the pages, in the order they render.
 */
function expectStableKeys(pages: RawHistoryMessage[][], seen: Map<string, string>): void {
  for (const [pageIndex, page] of pages.entries()) {
    const rendered = renderOf(page)
    const keysInPage = new Set<string>()
    for (const [key, text] of rendered) {
      expect(keysInPage.has(key)).toBe(false)
      keysInPage.add(key)
      const previous = seen.get(key)
      if (previous !== undefined) {
        // A key that now denotes different text means the registry handed the
        // bubble the id of ANOTHER message — the reordering bug.
        expect({ key, page: pageIndex, text }).toEqual({ key, page: pageIndex, text: previous })
      }
      seen.set(key, text)
    }
  }
}

describe('durable stableIds across the sliding history window', () => {
  test('lookupStableIdFromEnd addresses the newest registered copies', () => {
    clearStableIdRegistry()
    registerStableId('tool', 'same result', 'ws-1')
    registerStableId('tool', 'same result', 'ws-2')
    registerStableId('tool', 'same result', 'ws-3')

    expect(lookupStableIdFromEnd('tool', 'same result', 0)).toBe('ws-3')
    expect(lookupStableIdFromEnd('tool', 'same result', 1)).toBe('ws-2')
    expect(lookupStableIdFromEnd('tool', 'same result', 2)).toBe('ws-1')
    // More copies asked for than recorded: no id, never a wrong one.
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

    // Three calls confirmed by the page, in chronological order.
    registerStableId('tool', 'id:call_1', 'ws-call-1')
    registerStableId('tool', 'note content\n', 'ws-call-1')
    registerStableId('tool', 'id:call_2', 'ws-call-2')
    registerStableId('tool', 'note content\n', 'ws-call-2')
    registerStableId('tool', 'id:call_3', 'ws-call-3')
    registerStableId('tool', 'note content\n', 'ws-call-3')

    expect(keysOf([tool(1), tool(2), tool(3)])).toEqual(['ws-call-1', 'ws-call-2', 'ws-call-3'])
    // The window slides: call_1 falls off the page. Before the fix this page
    // read occurrence 0 → 'ws-call-1', so call_2's card silently took call_1's
    // key and the list reordered itself.
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

    // Every page holds just the newest copy: it must keep ITS key, not the
    // previous copy's (which is what counting from the start of the page did).
    const pages = [1, 2, 3].map((n) => {
      registerStableId('assistant', ANSWER, `ws-answer-${n}`)
      // Converted immediately, while only this copy is registered.
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
    // thinking(1) slid out: thinking(2) must keep ws-think-2 (it used to fall
    // back to ws-think-1 and render one Think block on top of another).
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
    // The FIRST message of the conversation slides out of the window: the copy
    // that stays is the new one and must not inherit the first one's key (the
    // "my first message shows up as the newest one" report).
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
      // Optimistic user bubble, then its persisted copy (same text).
      registerStableId('user', `question ${n}`, `temp-user-${n}`)
      log.push({ role: 'user', content: `question ${n}` })

      // Thinking iteration that emits no text, then a tool with a REPEATED
      // result — the two identities that collide in real sessions.
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

    // Every page alone, and every page against the previous one.
    expectStableKeys(pages, seen)
  })
})
