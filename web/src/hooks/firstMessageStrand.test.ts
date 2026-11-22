import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../lib/types'
import { mergeMessages } from './messageMerge'

function u(id: string, content: string, extra: Partial<ChatMessage> = {}): ChatMessage {
  return { id, role: 'user', content, sessionKey: 's', createdAt: '', ...extra } as ChatMessage
}
function a(id: string, content: string, extra: Partial<ChatMessage> = {}): ChatMessage {
  return { id, role: 'assistant', content, sessionKey: 's', createdAt: '', ...extra } as ChatMessage
}
function opt(id: string, content: string, extra: Partial<ChatMessage> = {}): ChatMessage {
  return u(id, content, { optimistic: true, streaming: false, ...extra })
}

describe('first message rendered as newest (stranded optimistic bubble)', () => {
  test('A: first send answered without a persisted user row → bubble dropped, not stranded', () => {
    const base = [a('a-cmd', 'Model set to gpt-5')]
    const bubble = opt('opt-cmd', '/model gpt-5', { optimisticBaseCount: 0 })
    const merged = mergeMessages(base, [bubble])
    expect(merged.map((m) => m.id)).toEqual(['a-cmd'])
  })

  test('D: refetch landed the answer but not the user row → bubble dropped', () => {
    const base = [a('a1', 'hi there')]
    const bubble = opt('opt-1', 'hello', { optimisticBaseCount: 0 })
    const merged = mergeMessages(base, [bubble])
    expect(merged.map((m) => m.id)).toEqual(['a1'])
  })

  test('B: normal first message, base has user+assistant → confirmed by content', () => {
    const base = [u('u1', 'hello'), a('a1', 'hi there')]
    const bubble = opt('opt-1', 'hello', { optimisticBaseCount: 0 })
    const merged = mergeMessages(base, [bubble])
    expect(merged.map((m) => m.id)).toEqual(['u1', 'a1'])
  })

  test('C: normal first message in flight (base empty) → bubble kept ABOVE the stream', () => {
    const bubble = opt('opt-1', 'hello', { optimisticBaseCount: 0 })
    const asst = a('a-stream', 'partial...', { streaming: true })
    const merged = mergeMessages([], [bubble, asst])
    expect(merged.map((m) => m.id)).toEqual(['opt-1', 'a-stream'])
  })

  test('E: command first send, nothing persisted → bubble kept (correct order)', () => {
    const bubble = opt('opt-cmd', '/status', { optimisticBaseCount: 0 })
    const merged = mergeMessages([], [bubble])
    expect(merged.map((m) => m.id)).toEqual(['opt-cmd'])
  })

  test('F: a LATER send in flight is NOT dropped (previous answer is not its own)', () => {
    const base = [u('u-anchor', 'opener'), a('a-1', 'an answer')]
    const bubble = opt('opt-inflight', 'in flight question', {
      optimisticBaseCount: 1,
      optimisticAnchorId: 'u-anchor',
    })
    const merged = mergeMessages(base, [bubble])
    expect(merged.map((m) => m.id)).toEqual(['u-anchor', 'a-1', 'opt-inflight'])
  })

  test('G: a failed first send is NEVER dropped by the strand rule', () => {
    const base = [a('a1', 'some answer')]
    const bubble = opt('opt-failed', 'hello', { optimisticBaseCount: 0, failed: true })
    const merged = mergeMessages(base, [bubble])
    expect(merged.map((m) => m.id)).toEqual(['a1', 'opt-failed'])
  })
})
