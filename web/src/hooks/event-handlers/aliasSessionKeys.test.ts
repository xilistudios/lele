/**
 * Alias session-key matching tests.
 *
 * Regression cover for the stuck-loading bug: after /new or /agent the backend
 * (pkg/agent/loop.go) maps `base` -> `base:chat:N` and emits stream/complete/
 * history events with the ALIASED key, while message.ack — and therefore the
 * whole frontend state — is keyed by the BASE key. Strict comparison made the
 * client discard those events, leaving the assistant placeholder with
 * `streaming: true` and the loading dots/spinner stuck forever.
 */
import { describe, expect, mock, test } from 'bun:test'
import { QueryClient } from '@tanstack/react-query'
import type { ChatMessage } from '../../lib/types'
import { buildChatHistoryQueryKey } from '../useChatHistory'
import { isSessionMismatch, sessionKeysLooselyMatch } from './helpers'
import {
  handleHistoryUpdated,
  handleMessageAck,
  handleMessageComplete,
  handleMessageStream,
  handleStreamError,
} from './streaming'
import type { MessageEventContext } from './types'

// ── sessionKeysLooselyMatch ─────────────────────────────────────────────────

describe('sessionKeysLooselyMatch', () => {
  test('equal keys match', () => {
    expect(sessionKeysLooselyMatch('native:A', 'native:A')).toBe(true)
    expect(sessionKeysLooselyMatch('native:A:chat:2', 'native:A:chat:2')).toBe(true)
  })

  test('base key matches its conversation alias', () => {
    expect(sessionKeysLooselyMatch('native:A', 'native:A:chat:2')).toBe(true)
    expect(sessionKeysLooselyMatch('native:A:chat:2', 'native:A')).toBe(true)
  })

  test('two aliases of the same base match (same UI client)', () => {
    expect(sessionKeysLooselyMatch('native:A:chat:1', 'native:A:chat:2')).toBe(true)
  })

  test('unrelated keys do not match', () => {
    expect(sessionKeysLooselyMatch('native:A', 'native:B')).toBe(false)
    expect(sessionKeysLooselyMatch('native:A:chat:1', 'native:B:chat:1')).toBe(false)
  })

  test('undefined / null / empty never match', () => {
    expect(sessionKeysLooselyMatch(undefined, 'native:A')).toBe(false)
    expect(sessionKeysLooselyMatch('native:A', undefined)).toBe(false)
    expect(sessionKeysLooselyMatch(null, 'native:A')).toBe(false)
    expect(sessionKeysLooselyMatch('', 'native:A')).toBe(false)
    expect(sessionKeysLooselyMatch(undefined, undefined)).toBe(false)
  })

  test('non-numeric :chat: suffix is not a conversation alias', () => {
    expect(sessionKeysLooselyMatch('native:A', 'native:A:chat:x')).toBe(false)
    expect(sessionKeysLooselyMatch('native:A:chat:x', 'native:A:chat:2')).toBe(false)
  })

  test('different channel prefixes do not match', () => {
    expect(sessionKeysLooselyMatch('native:A', 'telegram:A')).toBe(false)
    expect(sessionKeysLooselyMatch('native:A:chat:2', 'telegram:A:chat:2')).toBe(false)
  })

  test('suffix must be at the end of the key', () => {
    // ':chat:2' embedded mid-key is not the alias suffix
    expect(sessionKeysLooselyMatch('native:A:chat:2:sub', 'native:A')).toBe(false)
  })
})

// ── isSessionMismatch ───────────────────────────────────────────────────────

describe('isSessionMismatch with conversation aliases', () => {
  test('aliased event key no longer drops the event', () => {
    expect(isSessionMismatch('native:A:chat:2', 'native:A', 'message.complete')).toBe(false)
    expect(isSessionMismatch('native:A', 'native:A:chat:2', 'message.stream')).toBe(false)
  })

  test('foreign session key still drops the event', () => {
    expect(isSessionMismatch('native:B:chat:1', 'native:A', 'message.stream')).toBe(true)
  })

  test('null current session never drops (nothing to compare against)', () => {
    expect(isSessionMismatch('native:A:chat:2', null, 'message.stream')).toBe(false)
    expect(isSessionMismatch('native:A', null, 'message.stream')).toBe(false)
  })

  test('event without session key is not dropped', () => {
    expect(isSessionMismatch(undefined, 'native:A', 'message.stream')).toBe(false)
  })
})

// ── ctx fixture ─────────────────────────────────────────────────────────────

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
    sessionKey: 'native:A',
    ...extra,
  }
}

type CtxFixture = {
  ctx: MessageEventContext
  getStreaming: () => ChatMessage[]
  calls: {
    clearQueue: ReturnType<typeof mock>
    removeProcessingSession: ReturnType<typeof mock>
    setToolStatus: ReturnType<typeof mock>
    setPendingAttachments: ReturnType<typeof mock>
    debouncedSessionRefresh: ReturnType<typeof mock>
    invalidateQueries: ReturnType<typeof mock>
    enqueueChunk: ReturnType<typeof mock>
    ensureAssistantPlaceholder: ReturnType<typeof mock>
    addProcessingSession: ReturnType<typeof mock>
  }
}

function makeCtx(
  initialStreaming: ChatMessage[],
  opts: {
    currentSessionKey?: string | null
    cachedHistory?: { messages: ChatMessage[] }
  } = {},
): CtxFixture {
  const queryClient = new QueryClient()
  const currentKey = opts.currentSessionKey === undefined ? 'native:A' : opts.currentSessionKey
  if (opts.cachedHistory && currentKey) {
    queryClient.setQueryData(buildChatHistoryQueryKey(currentKey), {
      sessionKey: currentKey,
      ...opts.cachedHistory,
      rawMessages: [],
      processing: false,
    })
  }

  let streaming = initialStreaming
  const setStreamingMessages: MessageEventContext['setStreamingMessages'] = (updater) => {
    streaming =
      typeof updater === 'function'
        ? (updater as (p: ChatMessage[]) => ChatMessage[])(streaming)
        : updater
  }

  const calls = {
    clearQueue: mock(() => {}),
    removeProcessingSession: mock(() => {}),
    setToolStatus: mock(() => {}),
    setPendingAttachments: mock(() => {}),
    debouncedSessionRefresh: mock(() => {}),
    invalidateQueries: mock(() => {}),
    enqueueChunk: mock(() => {}),
    ensureAssistantPlaceholder: mock(() => {}),
    addProcessingSession: mock(() => {}),
  }

  const ctx = {
    currentSessionKeyRef: { current: currentKey },
    parentSessionKeyRef: { current: null },
    queryClient: {
      invalidateQueries: calls.invalidateQueries,
      getQueryData: (key: unknown) => queryClient.getQueryData(key as never),
      setQueryData: (key: unknown, data: unknown) =>
        queryClient.setQueryData(key as never, data as never),
    } as unknown as QueryClient,
    debouncedSessionRefresh: calls.debouncedSessionRefresh,
    setStreamingMessages,
    setToolStatus: calls.setToolStatus,
    setPendingAttachments: calls.setPendingAttachments,
    setApprovalRequest: () => {},
    showApprovalResult: () => {},
    enqueueChunk: calls.enqueueChunk,
    clearQueue: calls.clearQueue,
    clearAllQueues: () => {},
    ensureAssistantPlaceholder: calls.ensureAssistantPlaceholder,
    addProcessingSession: calls.addProcessingSession,
    removeProcessingSession: calls.removeProcessingSession,
    syncProcessingSession: () => {},
    processingSessionKeyRef: { current: 'native:A' },
    upsertGroup: () => {},
    hydrateGroups: () => {},
    markActiveGroupsStopped: () => {},
    setGroupsEnabled: () => {},
    setTypingIndicator: () => {},
  } as unknown as MessageEventContext

  return { ctx, getStreaming: () => streaming, calls }
}

// ── handleMessageComplete ───────────────────────────────────────────────────

describe('handleMessageComplete with aliased session key', () => {
  test('aliased complete clears processing for BOTH alias and base and finalizes streaming', () => {
    const placeholder = msg('a1', 'assistant', 'partial answer', {
      streaming: true,
      sessionKey: 'native:A',
    })
    const { ctx, getStreaming, calls } = makeCtx([placeholder])

    handleMessageComplete(ctx, {
      message_id: 'a1',
      session_key: 'native:A:chat:2',
      content: 'final answer',
    })

    // Both sides of the aliased pair cleared (ack registers the base key).
    const cleared = calls.removeProcessingSession.mock.calls.map((c) => c[0])
    expect(cleared).toContain('native:A:chat:2')
    expect(cleared).toContain('native:A')

    // No early-return: the placeholder must be finalized under the CURRENT key.
    const assistant = getStreaming().find((m) => m.id === 'a1')
    expect(assistant).toBeDefined()
    expect(assistant?.streaming).toBe(false)
    expect(assistant?.content).toBe('final answer')

    expect(calls.setToolStatus.mock.calls.length).toBe(1)
    expect(calls.setPendingAttachments.mock.calls.length).toBe(1)
    expect(ctx.processingSessionKeyRef.current).toBeNull()
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(1)
  })

  test('base-keyed complete still finalizes when the frontend key is aliased', () => {
    const placeholder = msg('a1', 'assistant', 'x', {
      streaming: true,
      sessionKey: 'native:A:chat:3',
    })
    const { ctx, getStreaming, calls } = makeCtx([placeholder], {
      currentSessionKey: 'native:A:chat:3',
    })

    handleMessageComplete(ctx, { message_id: 'a1', session_key: 'native:A', content: 'y' })

    const cleared = calls.removeProcessingSession.mock.calls.map((c) => c[0])
    expect(cleared).toContain('native:A')
    expect(cleared).toContain('native:A:chat:3')
    expect(getStreaming().find((m) => m.id === 'a1')?.streaming).toBe(false)
  })

  test('complete for an unrelated session clears only its own key and leaves current state untouched', () => {
    const placeholder = msg('a1', 'assistant', 'streaming now', {
      streaming: true,
      sessionKey: 'native:A',
    })
    const { ctx, getStreaming, calls } = makeCtx([placeholder])

    handleMessageComplete(ctx, {
      message_id: 'other',
      session_key: 'native:B:chat:1',
      content: 'elsewhere',
    })

    expect(calls.clearQueue.mock.calls.length).toBe(1)
    expect(calls.removeProcessingSession.mock.calls.map((c) => c[0])).toEqual(['native:B:chat:1'])

    // Hard requirements: no side effects on the session currently on screen.
    expect(calls.setToolStatus.mock.calls.length).toBe(0)
    expect(calls.setPendingAttachments.mock.calls.length).toBe(0)
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(0)
    expect(ctx.processingSessionKeyRef.current).toBe('native:A')

    // setStreamingMessages must NOT have been invoked for the foreign event.
    const assistant = getStreaming().find((m) => m.id === 'a1')
    expect(assistant?.streaming).toBe(true)
    expect(assistant?.content).toBe('streaming now')
    expect(getStreaming().some((m) => m.id === 'other')).toBe(false)
  })

  test('event without session_key falls back to the current session', () => {
    const placeholder = msg('a1', 'assistant', 'x', { streaming: true })
    const { ctx, getStreaming, calls } = makeCtx([placeholder])

    handleMessageComplete(ctx, { message_id: 'a1', content: 'done' })

    expect(calls.removeProcessingSession.mock.calls.map((c) => c[0])).toEqual(['native:A'])
    expect(getStreaming().find((m) => m.id === 'a1')?.streaming).toBe(false)
    expect(ctx.processingSessionKeyRef.current).toBeNull()
  })
})

// ── handleMessageStream ─────────────────────────────────────────────────────

describe('handleMessageStream with aliased session key', () => {
  test('aliased chunk is tagged with the current (base) session key', () => {
    const { ctx, calls } = makeCtx([])

    handleMessageStream(ctx, {
      message_id: 'a1',
      session_key: 'native:A:chat:2',
      chunk: 'hello',
    })

    expect(calls.enqueueChunk.mock.calls.length).toBe(1)
    const [msgId, sessionKey, chunk, done] = calls.enqueueChunk.mock.calls[0] as [
      string,
      string,
      string,
      boolean,
    ]
    expect(msgId).toBe('a1')
    expect(sessionKey).toBe('native:A')
    expect(chunk).toBe('hello')
    expect(done).toBe(false)
  })

  test('foreign session chunk is dropped', () => {
    const { ctx, calls } = makeCtx([])

    handleMessageStream(ctx, {
      message_id: 'a1',
      session_key: 'native:B:chat:2',
      chunk: 'hello',
    })

    expect(calls.enqueueChunk.mock.calls.length).toBe(0)
  })
})

// ── handleMessageAck ────────────────────────────────────────────────────────

describe('handleMessageAck with aliased session key', () => {
  test('aliased ack re-tags processing, placeholder and restore-cleanup to the current session', () => {
    // Restore placeholder created by welcome under the CURRENT (base) key.
    const restored = msg('restore-native:A', 'assistant', 'accumulated', {
      streaming: true,
      sessionKey: 'native:A',
    })
    const { ctx, getStreaming, calls } = makeCtx([restored])

    // Backend now resolves the alias before emitting message.ack (see
    // pkg/channels/websocket.go / rest_stream.go) -> event carries 'native:A:chat:2'.
    handleMessageAck(ctx, { message_id: 'a1', session_key: 'native:A:chat:2' })

    // Processing must be registered under the key the UI shows ('native:A'),
    // otherwise the loading indicator never appears for the current turn.
    expect(calls.addProcessingSession.mock.calls.map((c) => c[0])).toEqual(['native:A'])
    // Placeholder keyed with the alias would be hidden by the strict
    // per-session filters (sessionStreamingMessages / hasStreamingMessageForSession).
    expect(calls.ensureAssistantPlaceholder.mock.calls.length).toBe(1)
    expect(calls.ensureAssistantPlaceholder.mock.calls[0]).toEqual(['a1', 'native:A'])
    // Restore placeholders live under the base key — cleanup must target it.
    expect(getStreaming().some((m) => m.id === 'restore-native:A')).toBe(false)
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(1)
  })

  test('ack with the current (un-aliased) key behaves exactly as before', () => {
    const restored = msg('restore-native:A', 'assistant', 'accumulated', {
      streaming: true,
      sessionKey: 'native:A',
    })
    const { ctx, getStreaming, calls } = makeCtx([restored])

    handleMessageAck(ctx, { message_id: 'a1', session_key: 'native:A' })

    expect(calls.addProcessingSession.mock.calls.map((c) => c[0])).toEqual(['native:A'])
    expect(calls.ensureAssistantPlaceholder.mock.calls[0]).toEqual(['a1', 'native:A'])
    expect(getStreaming().some((m) => m.id === 'restore-native:A')).toBe(false)
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(1)
  })

  test('ack without session_key keeps the empty-key behaviour untouched', () => {
    const { ctx, calls } = makeCtx([])

    handleMessageAck(ctx, { message_id: 'a1' })

    // No processing registration / refresh for an empty key (as before), but
    // the placeholder is still created with the raw empty key.
    expect(calls.addProcessingSession.mock.calls.length).toBe(0)
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(0)
    expect(calls.ensureAssistantPlaceholder.mock.calls[0]).toEqual(['a1', ''])
  })
})

// ── handleStreamError ───────────────────────────────────────────────────────

describe('handleStreamError with aliased session key', () => {
  // The backend does not emit stream.error today; the re-tag below is defense
  // for when it does. Streaming state is keyed by the CURRENT (base) session,
  // so an aliased error key must resolve to it or the placeholder would stay
  // streaming:true forever (stuck loading).
  test('aliased stream.error marks the base-keyed streaming assistant as errored', () => {
    const placeholder = msg('a1', 'assistant', 'partial answer', {
      streaming: true,
      sessionKey: 'native:A',
    })
    const { ctx, getStreaming, calls } = makeCtx([placeholder])

    handleStreamError(ctx, { session_key: 'native:A:chat:2', error: 'boom' })

    const assistant = getStreaming().find((m) => m.id === 'a1')
    expect(assistant).toBeDefined()
    expect(assistant?.streaming).toBe(false)
    expect(assistant?.error).toBe('boom')

    // Processing cleared under the current (base) key, not the alias.
    expect(calls.removeProcessingSession.mock.calls.map((c) => c[0])).toEqual(['native:A'])
    expect(ctx.processingSessionKeyRef.current).toBeNull()
  })

  test('stream.error without session_key keeps falling back to the current session', () => {
    const placeholder = msg('a1', 'assistant', 'partial answer', {
      streaming: true,
      sessionKey: 'native:A',
    })
    const { ctx, getStreaming } = makeCtx([placeholder])

    handleStreamError(ctx, { error: 'boom' })

    expect(getStreaming().find((m) => m.id === 'a1')?.streaming).toBe(false)
    expect(getStreaming().find((m) => m.id === 'a1')?.error).toBe('boom')
  })
})

// ── handleHistoryUpdated ────────────────────────────────────────────────────

describe('handleHistoryUpdated with aliased session key', () => {
  test('aliased event invalidates and reconciles the CURRENT session cache', () => {
    const assistant = msg('a1', 'assistant', 'Confirmed answer', { streaming: false })
    const confirmed = msg('hash-1', 'assistant', 'Confirmed answer')
    const { ctx, getStreaming, calls } = makeCtx([assistant], {
      cachedHistory: { messages: [confirmed] },
    })

    handleHistoryUpdated(ctx, { session_key: 'native:A:chat:2' })

    // Invalidated under the key the cache actually lives on (base), not the alias.
    expect(calls.invalidateQueries.mock.calls.length).toBe(1)
    const [arg] = calls.invalidateQueries.mock.calls[0] as [{ queryKey: readonly unknown[] }]
    expect(arg.queryKey).toEqual(['chatHistory', 'native:A'])

    // Stripping block ran for the aliased event → confirmed copy removed.
    expect(getStreaming().some((m) => m.id === 'a1')).toBe(false)
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(1)
  })

  test('foreign session event invalidates but never strips the current session', () => {
    const assistant = msg('a1', 'assistant', 'still streaming', { streaming: false })
    const confirmed = msg('hash-1', 'assistant', 'still streaming')
    const { ctx, getStreaming, calls } = makeCtx([assistant], {
      cachedHistory: { messages: [confirmed] },
    })

    handleHistoryUpdated(ctx, { session_key: 'native:B:chat:2' })

    expect(calls.invalidateQueries.mock.calls.length).toBe(1)
    const [arg] = calls.invalidateQueries.mock.calls[0] as [{ queryKey: readonly unknown[] }]
    expect(arg.queryKey).toEqual(['chatHistory', 'native:B:chat:2'])
    expect(calls.debouncedSessionRefresh.mock.calls.length).toBe(1)
    // Current session untouched (behaviour preserved from before).
    expect(getStreaming().some((m) => m.id === 'a1')).toBe(true)
  })

  test('event without session_key keeps using the current session', () => {
    const assistant = msg('a1', 'assistant', 'Confirmed answer', { streaming: false })
    const confirmed = msg('hash-1', 'assistant', 'Confirmed answer')
    const { ctx, getStreaming, calls } = makeCtx([assistant], {
      cachedHistory: { messages: [confirmed] },
    })

    handleHistoryUpdated(ctx, {})

    const [arg] = calls.invalidateQueries.mock.calls[0] as [{ queryKey: readonly unknown[] }]
    expect(arg.queryKey).toEqual(['chatHistory', 'native:A'])
    expect(getStreaming().some((m) => m.id === 'a1')).toBe(false)
  })
})
