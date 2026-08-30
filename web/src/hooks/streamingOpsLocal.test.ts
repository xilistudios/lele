import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'
import {
  finalizeStreamingAssistantsForSession,
  hasStreamingMessageForSession,
} from './streamingOpsLocal'

function msg(
  id: string,
  role: ChatMessage['role'],
  content: string,
  extra: Partial<ChatMessage> = {},
): ChatMessage {
  return {
    id,
    role,
    content,
    streaming: false,
    createdAt: new Date().toISOString(),
    sessionKey: 's1',
    ...extra,
  }
}

describe('finalizeStreamingAssistantsForSession', () => {
  test('finalizes streaming assistants of the target session', () => {
    const current = [
      msg('u1', 'user', 'hi', { streaming: true }),
      msg('a1', 'assistant', 'partial', { streaming: true }),
      msg('a2', 'assistant', '', { streaming: true }),
    ]
    const next = finalizeStreamingAssistantsForSession(current, 's1')

    expect(next.find((m) => m.id === 'a1')?.streaming).toBe(false)
    expect(next.find((m) => m.id === 'a2')?.streaming).toBe(false)
    // Content is preserved, only the flag flips
    expect(next.find((m) => m.id === 'a1')?.content).toBe('partial')
    // Non-assistant roles are untouched (user placeholder keeps its flag)
    expect(next.find((m) => m.id === 'u1')?.streaming).toBe(true)
  })

  test('leaves other sessions untouched', () => {
    const current = [
      msg('a1', 'assistant', 'mine', { streaming: true, sessionKey: 's1' }),
      msg('a2', 'assistant', 'theirs', { streaming: true, sessionKey: 's2' }),
    ]
    const next = finalizeStreamingAssistantsForSession(current, 's1')

    expect(next.find((m) => m.id === 'a1')?.streaming).toBe(false)
    expect(next.find((m) => m.id === 'a2')?.streaming).toBe(true)
  })

  test('returns the SAME reference when nothing changes', () => {
    const current = [
      msg('a1', 'assistant', 'done', { streaming: false }),
      msg('a2', 'assistant', 'other', { streaming: true, sessionKey: 's2' }),
    ]
    expect(finalizeStreamingAssistantsForSession(current, 's1')).toBe(current)
  })

  test('returns the SAME reference for null/undefined/empty sessionKey', () => {
    const current = [msg('a1', 'assistant', 'x', { streaming: true })]
    expect(finalizeStreamingAssistantsForSession(current, null)).toBe(current)
    expect(finalizeStreamingAssistantsForSession(current, undefined)).toBe(current)
    expect(finalizeStreamingAssistantsForSession(current, '')).toBe(current)
  })

  test('does not mutate the input array or its messages', () => {
    const streaming = msg('a1', 'assistant', 'x', { streaming: true })
    const current = [streaming]
    const next = finalizeStreamingAssistantsForSession(current, 's1')

    expect(streaming.streaming).toBe(true)
    expect(current[0]).toBe(streaming)
    expect(next).not.toBe(current)
  })
})

describe('hasStreamingMessageForSession', () => {
  test('matches only streaming messages of the current session', () => {
    const current = [
      msg('a1', 'assistant', 'x', { streaming: true, sessionKey: 's2' }),
      msg('a2', 'assistant', 'y', { streaming: false, sessionKey: 's1' }),
    ]
    expect(hasStreamingMessageForSession(current, 's1')).toBe(false)
    expect(hasStreamingMessageForSession(current, 's2')).toBe(true)
  })

  test('is false when no session is selected', () => {
    const current = [msg('a1', 'assistant', 'x', { streaming: true })]
    expect(hasStreamingMessageForSession(current, null)).toBe(false)
    expect(hasStreamingMessageForSession(current, undefined)).toBe(false)
    expect(hasStreamingMessageForSession(current, '')).toBe(false)
  })

  test('counts any streaming message type (user/tool placeholders included)', () => {
    const current = [msg('u1', 'user', 'hi', { streaming: true, sessionKey: 's1' })]
    expect(hasStreamingMessageForSession(current, 's1')).toBe(true)
  })
})
