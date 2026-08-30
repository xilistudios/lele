import { describe, expect, test } from 'bun:test'
import type { ChatMessage } from '../../lib/types'
import { handleSubscribeAck } from './approvals'
import type { MessageEventContext } from './types'

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

/**
 * Minimal fake MessageEventContext: setStreamingMessages applies updater
 * functions against a local array so we can inspect the result, and every
 * other field is a vi.fn()-style spy (call recorder).
 */
function makeCtx(initialStreaming: ChatMessage[]) {
  let streaming = initialStreaming
  const calls: Array<{ sessionKey: string; processing: boolean }> = []

  const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
    streaming =
      typeof updater === 'function'
        ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
        : updater
  }

  const noop = () => {}

  const ctx = {
    currentSessionKeyRef: { current: 's1' },
    parentSessionKeyRef: { current: null },
    queryClient: {},
    debouncedSessionRefresh: noop,
    setStreamingMessages,
    setToolStatus: noop,
    setPendingAttachments: noop,
    setApprovalRequest: noop,
    showApprovalResult: noop,
    enqueueChunk: noop,
    clearQueue: noop,
    clearAllQueues: noop,
    ensureAssistantPlaceholder: noop,
    addProcessingSession: noop,
    removeProcessingSession: noop,
    syncProcessingSession: (sessionKey: string, processing: boolean) => {
      calls.push({ sessionKey, processing })
    },
    processingSessionKeyRef: { current: null },
    upsertGroup: noop,
    hydrateGroups: noop,
    markActiveGroupsStopped: noop,
    setGroupsEnabled: noop,
    setTypingIndicator: noop,
  } as unknown as MessageEventContext

  return { ctx, getStreaming: () => streaming, syncCalls: calls }
}

describe('handleSubscribeAck', () => {
  test('processing:false does NOT restore in_progress leftovers as streaming', () => {
    const { ctx, getStreaming, syncCalls } = makeCtx([msg('u1', 'user', 'hello')])

    handleSubscribeAck(ctx, {
      session_key: 's1',
      processing: false,
      in_progress_messages: [{ role: 'assistant', content: 'stale partial answer' }],
    })

    expect(syncCalls).toEqual([{ sessionKey: 's1', processing: false }])
    // No placeholder was created: the backend already finished.
    expect(getStreaming().some((m) => m.role === 'assistant')).toBe(false)
    expect(getStreaming().some((m) => m.streaming)).toBe(false)
  })

  test('processing:true restores in_progress assistants as streaming placeholders', () => {
    const { ctx, getStreaming } = makeCtx([msg('u1', 'user', 'hello')])

    handleSubscribeAck(ctx, {
      session_key: 's1',
      processing: true,
      in_progress_messages: [
        { role: 'assistant', content: 'partial answer', reasoning_content: 'thinking' },
      ],
    })

    const restored = getStreaming().find((m) => m.role === 'assistant')
    expect(restored).toBeDefined()
    expect(restored?.streaming).toBe(true)
    expect(restored?.content).toBe('partial answer')
    expect(restored?.reasoningContent).toBe('thinking')
    // Inserted after the session's last user message (chronological position)
    const roles = getStreaming().map((m) => m.role)
    expect(roles).toEqual(['user', 'assistant'])
  })

  test('processing:true with existing streaming content does not duplicate', () => {
    const existing = msg('a-existing', 'assistant', 'already here', { streaming: true })
    const { ctx, getStreaming } = makeCtx([existing])

    handleSubscribeAck(ctx, {
      session_key: 's1',
      processing: true,
      in_progress_messages: [{ role: 'assistant', content: 'partial answer' }],
    })

    expect(getStreaming().filter((m) => m.role === 'assistant')).toHaveLength(1)
    expect(getStreaming()[0].id).toBe('a-existing')
  })

  test('processing:false finalizes stale streaming assistants of that session', () => {
    const stale = msg('a-stale', 'assistant', 'leftover', { streaming: true })
    const other = msg('a-other', 'assistant', 'busy elsewhere', {
      streaming: true,
      sessionKey: 's2',
    })
    const { ctx, getStreaming } = makeCtx([stale, other])

    handleSubscribeAck(ctx, { session_key: 's1', processing: false })

    expect(getStreaming().find((m) => m.id === 'a-stale')?.streaming).toBe(false)
    // Other sessions are untouched
    expect(getStreaming().find((m) => m.id === 'a-other')?.streaming).toBe(true)
  })

  test('processing:false leaves streaming messages of OTHER sessions intact', () => {
    const otherStreaming = msg('a-other', 'assistant', 'still working', {
      streaming: true,
      sessionKey: 's2',
    })
    const done = msg('a-done', 'assistant', 'finished', { streaming: false })
    const { ctx, getStreaming } = makeCtx([otherStreaming, done])

    handleSubscribeAck(ctx, { session_key: 's1', processing: false })

    // Nothing changed → same array reference (no spurious re-render)
    expect(getStreaming()).toHaveLength(2)
    expect(getStreaming().find((m) => m.id === 'a-other')?.streaming).toBe(true)
  })

  test('empty session_key is a no-op', () => {
    const stale = msg('a-stale', 'assistant', 'leftover', { streaming: true })
    const { ctx, getStreaming, syncCalls } = makeCtx([stale])

    handleSubscribeAck(ctx, { session_key: '', processing: false })

    expect(syncCalls).toHaveLength(0)
    expect(getStreaming().find((m) => m.id === 'a-stale')?.streaming).toBe(true)
  })
})
