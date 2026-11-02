/**
 * `message.discard` — the backend streamed an LLM iteration to this client and
 * then THREW IT AWAY.
 *
 * Why this needs its own cover: the discarded iteration is never persisted, so
 * `mergeMessages` has no twin to pair the live bubble with and appends it AFTER
 * the canonical history. That is the stray "Thinking…" card rendered below the
 * final answer (or two thinking cards in a row). `message.discard` is the only
 * signal that retracts it, and it must do so WITHOUT touching anything else: the
 * turn is still running, and the sibling bubbles (and their order) must survive.
 *
 * The handler is driven directly against a `MessageEventContext` fake, the same
 * way `historyUpdated.test.ts` / `aliasSessionKeys.test.ts` do it, so the pure
 * transition and the session guard are exercised without React or a socket.
 */
import { describe, expect, mock, test } from 'bun:test'
import type { QueryClient } from '@tanstack/react-query'
import type { ChatMessage } from '../lib/types'
import { handleMessageDiscard } from './event-handlers/streaming'
import type { MessageEventContext } from './event-handlers/types'
import { removeDiscardedMessage } from './streamingOps'

const SESSION = 'native:discard'

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
    sessionKey: SESSION,
    ...extra,
  }
}

type CtxFixture = {
  ctx: MessageEventContext
  getStreaming: () => ChatMessage[]
  calls: {
    setStreamingMessages: ReturnType<typeof mock>
    clearQueue: ReturnType<typeof mock>
    setToolStatus: ReturnType<typeof mock>
    setError: ReturnType<typeof mock>
    setPendingAttachments: ReturnType<typeof mock>
    addProcessingSession: ReturnType<typeof mock>
    removeProcessingSession: ReturnType<typeof mock>
    debouncedSessionRefresh: ReturnType<typeof mock>
    invalidateQueries: ReturnType<typeof mock>
  }
}

function makeCtx(
  initialStreaming: ChatMessage[],
  opts: { currentSessionKey?: string | null } = {},
): CtxFixture {
  const currentKey =
    opts.currentSessionKey === undefined ? SESSION : (opts.currentSessionKey as string | null)

  let streaming = initialStreaming
  const calls = {
    setStreamingMessages: mock((_: unknown) => {}),
    clearQueue: mock(() => {}),
    setToolStatus: mock(() => {}),
    setError: mock(() => {}),
    setPendingAttachments: mock(() => {}),
    addProcessingSession: mock(() => {}),
    removeProcessingSession: mock(() => {}),
    debouncedSessionRefresh: mock(() => {}),
    invalidateQueries: mock(() => {}),
  }

  const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
    calls.setStreamingMessages(updater)
    streaming =
      typeof updater === 'function'
        ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
        : updater
  }

  const ctx = {
    currentSessionKeyRef: { current: currentKey },
    parentSessionKeyRef: { current: null },
    queryClient: {
      invalidateQueries: calls.invalidateQueries,
      getQueryData: () => undefined,
      setQueryData: () => {},
    } as unknown as QueryClient,
    debouncedSessionRefresh: calls.debouncedSessionRefresh,
    setStreamingMessages,
    streamingMessagesRef: { current: streaming },
    setToolStatus: calls.setToolStatus,
    setPendingAttachments: calls.setPendingAttachments,
    setError: calls.setError,
    setApprovalRequest: () => {},
    showApprovalResult: () => {},
    enqueueChunk: () => {},
    clearQueue: calls.clearQueue,
    clearAllQueues: () => {},
    setStreamMetrics: () => {},
    clearStreamMetrics: () => {},
    ensureAssistantPlaceholder: () => {},
    addProcessingSession: calls.addProcessingSession,
    removeProcessingSession: calls.removeProcessingSession,
    syncProcessingSession: () => {},
    processingSessionKeyRef: { current: SESSION },
    upsertGroup: () => {},
    hydrateGroups: () => {},
    markActiveGroupsStopped: () => {},
    setGroupsEnabled: () => {},
    setTypingIndicator: () => {},
  } as unknown as MessageEventContext

  return { ctx, getStreaming: () => streaming, calls }
}

// ── removeDiscardedMessage (pure transition) ────────────────────────────────

describe('removeDiscardedMessage', () => {
  test('removes the named assistant bubble and preserves the order of the rest', () => {
    const before = [
      msg('u1', 'user', 'why?'),
      msg('a1', 'assistant', '', { reasoningContent: 'thinking', streaming: true }),
      msg('t1', 'tool', '', { toolCallId: 'call_1', toolStatus: 'executing' }),
      msg('a2', 'assistant', 'the answer', { streaming: true }),
    ]

    const after = removeDiscardedMessage(before, 'a1', SESSION)

    expect(after.map((m) => m.id)).toEqual(['u1', 't1', 'a2'])
    // Same instances survive untouched (no accidental cloning/shuffling).
    expect(after[0]).toBe(before[0])
    expect(after[1]).toBe(before[2])
    expect(after[2]).toBe(before[3])
  })

  test('returns the SAME array instance when nothing matches (late/duplicate event)', () => {
    const before = [msg('u1', 'user', 'why?'), msg('a2', 'assistant', 'answer')]

    expect(removeDiscardedMessage(before, 'gone', SESSION)).toBe(before)
  })

  test('never removes a user bubble whose id happens to match', () => {
    const user = msg('same-id', 'user', 'hello')
    const before = [user]

    expect(removeDiscardedMessage(before, 'same-id', SESSION)).toBe(before)
  })

  test('never removes a tool card whose id happens to match', () => {
    const tool = msg('same-id', 'tool', 'result', { toolCallId: 'call_9' })
    const before = [msg('a1', 'assistant', 'answer'), tool]

    expect(removeDiscardedMessage(before, 'same-id', SESSION)).toBe(before)
  })

  test('ignores a bubble tagged with a different session', () => {
    const before = [msg('a1', 'assistant', 'other pane', { sessionKey: 'native:other' })]

    expect(removeDiscardedMessage(before, 'a1', SESSION)).toBe(before)
  })

  test('matches the base key against its conversation alias', () => {
    // Events after /new or /agent carry `base:chat:N`: the same conversation.
    const before = [
      msg('a1', 'assistant', 'discarded', { sessionKey: SESSION }),
      msg('a2', 'assistant', 'kept'),
    ]

    const after = removeDiscardedMessage(before, 'a1', `${SESSION}:chat:3`)

    expect(after.map((m) => m.id)).toEqual(['a2'])
  })

  test('an empty message id is a no-op', () => {
    const before = [msg('a1', 'assistant', 'kept')]

    expect(removeDiscardedMessage(before, '', SESSION)).toBe(before)
  })
})

// ── handleMessageDiscard (handler) ──────────────────────────────────────────

describe('handleMessageDiscard — current session', () => {
  test('(a) retracts only the discarded iteration, keeping the others in order', () => {
    const before = [
      msg('u1', 'user', 'why?'),
      msg('ghost-think', 'assistant', '', {
        reasoningContent: 'plain-text tool call guidance',
        streaming: true,
      }),
      msg('t1', 'tool', '', { toolCallId: 'call_1', toolStatus: 'executing' }),
      msg('answer', 'assistant', 'the final answer', { streaming: true }),
    ]
    const { ctx, getStreaming, calls } = makeCtx(before)

    handleMessageDiscard(ctx, {
      event_session_key_ignored: true,
      message_id: 'ghost-think',
      session_key: SESSION,
      reason: 'plain_text_tool_call',
    })

    expect(getStreaming().map((m) => m.id)).toEqual(['u1', 't1', 'answer'])
    expect(calls.setStreamingMessages.mock.calls.length).toBe(1)
  })

  test('drains the typewriter queue of the discarded id first', () => {
    const { ctx, calls } = makeCtx([msg('ghost', 'assistant', 'partial', { streaming: true })])

    handleMessageDiscard(ctx, { message_id: 'ghost', session_key: SESSION })

    // Queued chunks for that id would otherwise re-create the bubble on the
    // next tick of the shared animation loop.
    expect(calls.clearQueue.mock.calls.map((c) => c[0])).toEqual(['ghost'])
  })

  test('leaves the running turn alone: no processing/tool/banner/history side effects', () => {
    const { ctx, calls } = makeCtx([msg('ghost', 'assistant', 'partial', { streaming: true })])

    handleMessageDiscard(ctx, {
      message_id: 'ghost',
      session_key: SESSION,
      reason: 'empty_response',
    })

    expect(calls.setToolStatus.mock.calls.length).toBe(0)
    expect(calls.setError.mock.calls.length).toBe(0)
    expect(calls.setPendingAttachments.mock.calls.length).toBe(0)
    expect(calls.addProcessingSession.mock.calls.length).toBe(0)
    expect(calls.removeProcessingSession.mock.calls.length).toBe(0)
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(0)
    expect(calls.invalidateQueries.mock.calls.length).toBe(0)
    // Still processing: the retry iteration is on its way.
    expect(ctx.processingSessionKeyRef.current).toBe(SESSION)
  })

  test('re-tags an aliased event key to the session on screen', () => {
    // Backend resolves `base` -> `base:chat:N` before emitting; UI state is
    // keyed by the base key, so the bubble must still be found.
    const { ctx, getStreaming } = makeCtx([
      msg('ghost', 'assistant', 'partial', { streaming: true }),
      msg('kept', 'assistant', 'final'),
    ])

    handleMessageDiscard(ctx, { message_id: 'ghost', session_key: `${SESSION}:chat:2` })

    expect(getStreaming().map((m) => m.id)).toEqual(['kept'])
  })
})

describe('handleMessageDiscard — ignored events', () => {
  test('(b) a discard for ANOTHER session is ignored', () => {
    const before = [
      msg('ghost', 'assistant', 'still mine', { streaming: true }),
      msg('u1', 'user', 'hello'),
    ]
    const { ctx, getStreaming, calls } = makeCtx(before)

    handleMessageDiscard(ctx, { message_id: 'ghost', session_key: 'native:other:chat:1' })

    expect(getStreaming().map((m) => m.id)).toEqual(['ghost', 'u1'])
    // Guard fires before anything is scheduled or drained.
    expect(calls.setStreamingMessages.mock.calls.length).toBe(0)
    expect(calls.clearQueue.mock.calls.length).toBe(0)
  })

  test('(c) an empty message_id is a no-op that never schedules a state update', () => {
    const before = [msg('ghost', 'assistant', 'partial', { streaming: true })]
    const { ctx, getStreaming, calls } = makeCtx(before)

    handleMessageDiscard(ctx, { message_id: '', session_key: SESSION })

    expect(getStreaming()).toBe(before)
    expect(calls.setStreamingMessages.mock.calls.length).toBe(0)
    expect(calls.clearQueue.mock.calls.length).toBe(0)
  })

  test('(c) an UNKNOWN message id leaves the array identity intact', () => {
    const before = [msg('u1', 'user', 'why?'), msg('a1', 'assistant', 'answer')]
    const { ctx, getStreaming, calls } = makeCtx(before)

    handleMessageDiscard(ctx, { message_id: 'never-streamed', session_key: SESSION })

    // The updater ran, but the state value is the very same array: React sees
    // an identical reference and skips the re-render.
    expect(calls.setStreamingMessages.mock.calls.length).toBe(1)
    expect(getStreaming()).toBe(before)
  })

  test('(d) never removes a user bubble whose id matches', () => {
    const before = [msg('shared-id', 'user', 'my question', { optimistic: true })]
    const { ctx, getStreaming } = makeCtx(before)

    handleMessageDiscard(ctx, { message_id: 'shared-id', session_key: SESSION })

    expect(getStreaming()).toBe(before)
    expect(getStreaming().some((m) => m.role === 'user')).toBe(true)
  })

  test('(d) never removes a tool card whose id matches', () => {
    const before = [
      msg('shared-id', 'tool', 'tool output', { toolCallId: 'call_1', toolStatus: 'executing' }),
    ]
    const { ctx, getStreaming } = makeCtx(before)

    handleMessageDiscard(ctx, { message_id: 'shared-id', session_key: SESSION })

    expect(getStreaming()).toBe(before)
  })

  test('(e) a late/duplicate discard is harmless', () => {
    const before = [
      msg('u1', 'user', 'why?'),
      msg('ghost', 'assistant', 'partial', { streaming: true }),
      msg('answer', 'assistant', 'the final answer', { streaming: true }),
    ]
    const { ctx, getStreaming } = makeCtx(before)

    const discard = () =>
      handleMessageDiscard(ctx, {
        message_id: 'ghost',
        session_key: SESSION,
        reason: 'invalid_tool_calls',
      })

    discard()
    const afterFirst = getStreaming()
    expect(afterFirst.map((m) => m.id)).toEqual(['u1', 'answer'])

    // Second copy (or one that arrives after the turn completed): nothing left
    // to remove, so the state keeps the exact same array.
    discard()
    expect(getStreaming()).toBe(afterFirst)
    expect(getStreaming().map((m) => m.id)).toEqual(['u1', 'answer'])
  })
})
