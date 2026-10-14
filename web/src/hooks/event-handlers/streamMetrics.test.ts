/**
 * `message.stream` metric propagation tests (handler level).
 *
 * The backend adds four fields to every `message.stream` event — `tokens`,
 * `elapsed_ms`, `tok_per_sec`, `estimated`. The handler must parse them
 * tolerantly (an older backend without them degrades to zeros, never NaN) and
 * forward the result to the metrics state, on EVERY chunk including the
 * terminal one so the final figure is retained downstream.
 */
import { describe, expect, mock, test } from 'bun:test'
import type { StreamMetrics } from '../useStreamMetrics'
import { handleMessageStream, handleWelcome } from './streaming'
import type { MessageEventContext } from './types'

function makeCtx(currentSessionKey: string | null = 'native:A') {
  const noop = () => {}
  const setStreamMetrics = mock((_metrics: StreamMetrics) => {})
  const clearStreamMetrics = mock(() => {})
  const enqueueChunk = mock(() => {})
  const ensureAssistantPlaceholder = mock(() => {})

  const ctx = {
    currentSessionKeyRef: { current: currentSessionKey },
    parentSessionKeyRef: { current: null },
    queryClient: {},
    debouncedSessionRefresh: noop,
    setStreamingMessages: noop,
    setToolStatus: noop,
    setPendingAttachments: noop,
    setApprovalRequest: noop,
    showApprovalResult: noop,
    enqueueChunk,
    clearQueue: noop,
    clearAllQueues: noop,
    setStreamMetrics,
    clearStreamMetrics,
    ensureAssistantPlaceholder,
    addProcessingSession: noop,
    removeProcessingSession: noop,
    syncProcessingSession: noop,
    processingSessionKeyRef: { current: null },
    upsertGroup: noop,
    hydrateGroups: noop,
    markActiveGroupsStopped: noop,
    setGroupsEnabled: noop,
    setTypingIndicator: noop,
  } as unknown as MessageEventContext

  return { ctx, setStreamMetrics, clearStreamMetrics, enqueueChunk }
}

describe('handleMessageStream — token metrics', () => {
  test('propagates the four new fields', () => {
    const { ctx, setStreamMetrics } = makeCtx()

    handleMessageStream(ctx, {
      message_id: 'm1',
      session_key: 'native:A',
      chunk: 'hello',
      done: false,
      tokens: 120,
      elapsed_ms: 1350,
      tok_per_sec: 88.33333333333333,
      estimated: true,
    })

    expect(setStreamMetrics.mock.calls.length).toBe(1)
    expect(setStreamMetrics.mock.calls[0][0]).toEqual({
      tokens: 120,
      elapsedMs: 1350,
      tokPerSec: 88.33333333333333,
      estimated: true,
    })
  })

  test('legacy payload without the fields → safe defaults, no crash', () => {
    const { ctx, setStreamMetrics, enqueueChunk } = makeCtx()

    handleMessageStream(ctx, {
      message_id: 'm1',
      session_key: 'native:A',
      chunk: 'hello',
    })

    expect(setStreamMetrics.mock.calls[0][0]).toEqual({
      tokens: 0,
      elapsedMs: 0,
      tokPerSec: 0,
      estimated: false,
    })
    // The chunk itself is still queued as before — metrics are additive.
    expect(enqueueChunk.mock.calls.length).toBe(1)
  })

  test('terminal chunk (done + chunk) also reports the final metrics', () => {
    const { ctx, setStreamMetrics } = makeCtx()

    handleMessageStream(ctx, {
      message_id: 'm1',
      session_key: 'native:A',
      chunk: 'the end',
      done: true,
      tokens: 42,
      elapsed_ms: 1000,
      tok_per_sec: 42,
      estimated: false,
    })

    expect(setStreamMetrics.mock.calls[0][0]).toEqual({
      tokens: 42,
      elapsedMs: 1000,
      tokPerSec: 42,
      estimated: false,
    })
  })

  test('foreign-session chunk is dropped before any metrics side effect', () => {
    const { ctx, setStreamMetrics } = makeCtx('native:A')

    handleMessageStream(ctx, {
      message_id: 'm1',
      session_key: 'native:B',
      chunk: 'elsewhere',
      tokens: 10,
      elapsed_ms: 100,
      tok_per_sec: 100,
    })

    expect(setStreamMetrics.mock.calls.length).toBe(0)
  })
})

describe('handleWelcome — reconnect clears stale metrics', () => {
  test('welcome/reconnected clears the previously shown figures', () => {
    const { ctx, clearStreamMetrics } = makeCtx()

    handleWelcome(ctx, { session_key: 'native:A', processing: true })

    expect(clearStreamMetrics.mock.calls.length).toBe(1)
  })
})
