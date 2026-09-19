/**
 * Regression tests for the duplicate-content-hash message id bug.
 *
 * Root cause (verified end-to-end against a live gateway + browser):
 * `pkg/channels/rest_chat.go:residentMessageID` derives a history message id
 * from `sha256(role|content|tool_calls)`. It is POSITION-INDEPENDENT by
 * design, so two genuinely different messages that happen to have identical
 * content (a repeated assistant answer, the same user text sent twice)
 * receive the SAME id.
 *
 * `toChatMessages` then treats the repeated id as "the backend re-emitted the
 * same message" and DROPS the second occurrence — a real message disappears
 * from the WebUI (observed: 4 server messages → 3 rendered bubbles, so the
 * answer to the 2nd question renders above the 2nd question).
 *
 * Observed wire evidence (session e2e-S_MULTI-mu7p7dav):
 *   [1] assistant id=e2e-S_MULTI-mu7p7dav:f65b4b58614615af  "C01 … C12"
 *   [3] assistant id=e2e-S_MULTI-mu7p7dav:f65b4b58614615af  "C01 … C12"
 */
import { describe, expect, test } from 'bun:test'
import { toChatMessages } from '../lib/chatMessageBuilder'
import type { ChatMessage, RawHistoryMessage } from '../lib/types'
import { clearStableIdRegistry, registerStableId } from './stableIdRegistry'
import { mergeMessages } from './useChatHistory'

const SESSION = 'e2e-S_MULTI-mu7p7dav'
const ANSWER = 'C01 C02 C03 C04 C05 C06 C07 C08 C09 C10 C11 C12 '
// The exact ids the server returned for both identical answers.
const SHARED_ID = `${SESSION}:f65b4b58614615af`

const rawHistory: RawHistoryMessage[] = [
  { id: `${SESSION}:f7db6f802c42fa6b`, role: 'user', content: '[S_TEXT] first message' },
  { id: SHARED_ID, role: 'assistant', content: ANSWER },
  { id: `${SESSION}:3b7cdceeb7f614d2`, role: 'user', content: '[S_TEXT] second message' },
  { id: SHARED_ID, role: 'assistant', content: ANSWER },
]

/** The key `MessageList` actually uses for React reconciliation. */
const renderKey = (m: ChatMessage) => m.stableId ?? m.id

describe('duplicate content-hash ids', () => {
  test('toChatMessages keeps every server message (no silent drop)', () => {
    const built = toChatMessages(rawHistory, SESSION)
    expect(built.length).toBe(rawHistory.length)
    expect(built.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  test('render keys are unique even when two messages share a content-hash id', () => {
    const keys = toChatMessages(rawHistory, SESSION).map(renderKey)
    expect(new Set(keys).size).toBe(keys.length)
  })

  test('mergeMessages preserves server order and unique keys', () => {
    const base = toChatMessages(rawHistory, SESSION)
    const merged = mergeMessages(base, [])
    expect(merged.length).toBe(rawHistory.length)
    const keys = merged.map(renderKey)
    expect(new Set(keys).size).toBe(keys.length)
  })

  test('registry keeps one entry per occurrence so keys stay unique', () => {
    clearStableIdRegistry()
    registerStableId('assistant', ANSWER, 'a1-ws')
    registerStableId('assistant', ANSWER, 'a2-ws')
    const built = toChatMessages(rawHistory, SESSION)
    const keys = built.map(renderKey)
    expect(new Set(keys).size).toBe(keys.length)
    // occurrence order: the first copy keeps the first recorded ephemeral id
    expect(built[1].stableId).toBe('a1-ws')
    expect(built[3].stableId).toBe('a2-ws')
  })

  test('re-registering the same ephemeral id does not duplicate it', () => {
    clearStableIdRegistry()
    registerStableId('assistant', ANSWER, 'a2-ws')
    registerStableId('assistant', ANSWER, 'a2-ws')
    const built = toChatMessages(rawHistory, SESSION)
    expect(built[1].stableId).toBe('a2-ws')
    expect(built[3].stableId).toBeUndefined()
    const keys = built.map(renderKey)
    expect(new Set(keys).size).toBe(keys.length)
  })
})

function msg(
  id: string,
  role: 'user' | 'assistant' | 'tool',
  content: string,
  options: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role,
    content,
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey: SESSION,
    ...options,
  }
}

describe('two rapid sends: completed assistant must not be rendered twice', () => {
  test('base already holds turn 1 while turn 2 is in flight', () => {
    // Base (HTTP refetch) has turn 1 confirmed plus the optimistic user #2 that
    // the backend echoed; streaming still holds every in-flight copy.
    const base: ChatMessage[] = [
      msg(`${SESSION}:u1`, 'user', 'first'),
      msg(SHARED_ID, 'assistant', ANSWER),
      msg(`${SESSION}:u2`, 'user', 'second'),
    ]
    const streaming: ChatMessage[] = [
      msg('temp-user-1', 'user', 'first', { optimistic: true, optimisticBaseCount: 0 }),
      msg('a1-ws', 'assistant', ANSWER, { streaming: false }),
      msg('temp-user-2', 'user', 'second', { optimistic: true, optimisticBaseCount: 1 }),
      msg('a2-ws', 'assistant', '', { streaming: true }),
    ]

    const merged = mergeMessages(base, streaming)
    const assistants = merged.filter((m) => m.role === 'assistant')
    // a1 (completed, already in base) must appear ONCE — not as a duplicate
    // appended after the second user message.
    expect(assistants.length).toBe(2)
    expect(assistants[0].content).toBe(ANSWER) // turn 1 answer
    expect(assistants[1].content).toBe('') // turn 2 still streaming
    // And the duplicated copy must not sit after the second user message.
    expect(merged.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  test('a completed assistant with NEW content is never paired into an older base slot', () => {
    // Base is behind by one turn: streaming holds the finished turn-1 copy
    // (already in base) AND the completed answer to the newest user message
    // (not in base yet).
    const base: ChatMessage[] = [
      msg(`${SESSION}:u1`, 'user', 'first'),
      msg(`${SESSION}:a1`, 'assistant', 'answer one'),
      msg(`${SESSION}:u2`, 'user', 'second'),
    ]
    const streaming: ChatMessage[] = [
      msg('temp-user-1', 'user', 'first', { optimistic: true, optimisticBaseCount: 0 }),
      msg('a1-ws', 'assistant', 'answer one', { streaming: false }),
      msg('temp-user-2', 'user', 'second', { optimistic: true, optimisticBaseCount: 1 }),
      msg('a2-ws', 'assistant', 'answer two', { streaming: false }),
    ]

    const merged = mergeMessages(base, streaming)
    // Turn 1 is deduped in place; the NEW answer is appended after its user
    // message — it must NOT take over turn 1's slot (that was the
    // "response above the user message" bug).
    expect(merged.map((m) => m.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    expect(merged.map((m) => m.content)).toEqual(['first', 'answer one', 'second', 'answer two'])
  })
})
