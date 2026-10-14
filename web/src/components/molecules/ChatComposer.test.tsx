import '../../test/setup'
import { afterAll, beforeAll, beforeEach, describe, expect, mock, test } from 'bun:test'
import '../../test/i18n'
import { fireEvent, render, waitFor } from '@testing-library/react'
import {
  AppLogicContext,
  type AppLogicContextValue,
  StreamMetricsContext,
} from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { StreamMetrics } from '../../hooks/useStreamMetrics'

// Mutable mock returns — tests swap these before each render.
type MockCtx = Record<string, unknown>
let mockAppCtxReturn: MockCtx = {}
let mockAuthCtxReturn: MockCtx = {}
let mockChatPageCtxReturn: MockCtx = {}
let mockSlashCommandsReturn: MockCtx = { commands: [], loading: false, error: null }

// Deferred import — resolved in beforeAll AFTER mock.module registration so the
// component picks up the mocked ChatPageContext and useSlashCommands.
let ChatComposer: typeof import('./ChatComposer').ChatComposer

beforeAll(() => {
  // Only mock the two modules that are NOT imported by other test files.
  // AppLogicContext and AuthContext are provided via real context Providers so
  // that AgentEntityLayout.test.tsx (and any other file importing them) is
  // never affected.
  mock.module('../../contexts/ChatPageContext', () => ({
    useChatPageContext: () => mockChatPageCtxReturn,
  }))
  mock.module('../../hooks/useSlashCommands', () => ({
    useSlashCommands: () => mockSlashCommandsReturn,
  }))

  ChatComposer = require('./ChatComposer').ChatComposer
})

afterAll(() => {
  // Restore any mock() spies created in this file.
  // NOTE: mock.restore() does NOT revert mock.module() in Bun — module mocks are
  // process-wide and cannot be undone from within a file. The two mock.module()
  // targets above (ChatPageContext, useSlashCommands) are therefore contained by
  // running each test file in its own process via `bun test --isolate` (the
  // canonical command, see BASELINE.md / CI). Do not rely on this call to stop a
  // module-mock leak under a non-isolated run.
  mock.restore()
})

/** Find the composer textarea; throws instead of non-null asserting. */
function getTextarea(container: HTMLElement): HTMLTextAreaElement {
  const el = container.querySelector('textarea')
  if (!el) throw new Error('textarea not found')
  return el
}

/** Create a deferred promise the test can resolve/reject manually. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function baseAppCtx(onSend: (...args: unknown[]) => unknown): MockCtx {
  return {
    onSend,
    onCancel: () => {},
    onUploadAttachments: async () => [] as string[],
    onAttachmentsChange: () => {},
    onSelectAgent: async () => {},
    onSelectModel: () => {},
    onSelectThinkLevel: async () => {},
    onSelectFolder: async () => {},
    onClearFolder: async () => {},
    sendTyping: () => {},
    agents: [] as unknown[],
    currentAgent: null,
    pendingAttachments: [] as string[],
    sessionFolder: '',
    chatMode: 'agent' as const,
    currentSessionKey: 'sess-1',
    queuedMessages: [] as unknown[],
    isProcessing: false,
    removeQueuedMessage: () => {},
    sendNowQueuedMessage: () => {},
    clearQueue: () => {},
  }
}

function baseAuthCtx(): MockCtx {
  return {
    apiUrl: 'http://localhost:18793',
    api: { chatCommands: () => Promise.resolve({ commands: [] }) },
  }
}

function baseChatPageCtx(): MockCtx {
  return {
    canCancel: false,
    hasConversation: false,
    availableModels: [],
    groupedModels: undefined,
    selectedModel: '',
    thinkLevel: 'default',
  }
}

/** Wrap ChatComposer in real context Providers so we avoid mock.module for
 *  AppLogicContext and AuthContext (which are also imported by other test files).
 *  `metrics` seeds the StreamMetricsContext (default null → chip hidden). */
function composerTree(metrics: StreamMetrics | null = null) {
  return (
    <AuthContext.Provider value={mockAuthCtxReturn as unknown as AuthContextValue}>
      <AppLogicContext.Provider value={mockAppCtxReturn as unknown as AppLogicContextValue}>
        <StreamMetricsContext.Provider value={metrics}>
          <ChatComposer />
        </StreamMetricsContext.Provider>
      </AppLogicContext.Provider>
    </AuthContext.Provider>
  )
}

function renderComposer(metrics: StreamMetrics | null = null) {
  return render(composerTree(metrics))
}

describe('ChatComposer — double-submit guard', () => {
  beforeEach(() => {
    mockAuthCtxReturn = baseAuthCtx()
    mockChatPageCtxReturn = baseChatPageCtx()
    mockSlashCommandsReturn = { commands: [], loading: false, error: null }
  })

  test('second rapid submit is ignored while the first is in-flight', async () => {
    const { promise: sendPromise, resolve: resolveSend } = deferred<boolean>()
    let callCount = 0
    const onSend = mock((..._args: unknown[]) => {
      callCount++
      return sendPromise
    })
    mockAppCtxReturn = baseAppCtx(onSend)

    const { container } = renderComposer()
    const textarea = getTextarea(container)

    // Type a message
    fireEvent.change(textarea, { target: { value: 'hello' } })

    // First Enter — fires onSend, promise is pending
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(onSend).toHaveBeenCalledTimes(1)
    expect(callCount).toBe(1)

    // Second Enter while still in-flight — must be swallowed
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    // Third Enter — also swallowed
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    // Resolve the first send
    resolveSend(true)
    await waitFor(() => expect(callCount).toBe(1))

    // After resolution, submit works again
    fireEvent.change(textarea, { target: { value: 'world' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(2)
  })

  test('submit is blocked for same content while in-flight but allows different content', async () => {
    const { promise: sendPromise, resolve: resolveSend } = deferred<boolean>()
    let callCount = 0
    const onSend = mock((..._args: unknown[]) => {
      callCount++
      return sendPromise
    })
    mockAppCtxReturn = baseAppCtx(onSend)

    const { container } = renderComposer()
    const textarea = getTextarea(container)

    // First submit with "hello"
    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    // Same content again — blocked by content guard (double-click scenario)
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    // Different content — allowed through (queue scenario)
    fireEvent.change(textarea, { target: { value: 'world' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(2)

    // Resolve both and verify fresh submit works
    resolveSend(true)
    await waitFor(() => expect(onSend).toHaveBeenCalledTimes(2))

    fireEvent.change(textarea, { target: { value: 'third' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(3)
  })

  test('submit works again after onSend returns false (queue full)', async () => {
    const { promise: sendPromise, resolve: resolveSend } = deferred<boolean>()
    let callCount = 0
    const onSend = mock((..._args: unknown[]) => {
      callCount++
      return sendPromise
    })
    mockAppCtxReturn = baseAppCtx(onSend)

    const { container } = renderComposer()
    const textarea = getTextarea(container)

    fireEvent.change(textarea, { target: { value: 'hello' } })

    // First Enter
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    // Resolve with false (queue full)
    resolveSend(false)
    await waitFor(() => expect(callCount).toBe(1))

    // Submit works again after queue-full response
    fireEvent.change(textarea, { target: { value: 'retry' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(2)
  })

  test('empty content does not trigger onSend and does not block', async () => {
    let callCount = 0
    const onSend = mock((..._args: unknown[]) => {
      callCount++
      return Promise.resolve(true)
    })
    mockAppCtxReturn = baseAppCtx(onSend)

    const { container } = renderComposer()
    const textarea = getTextarea(container)

    // Enter with no content — no call, no blocking
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(0)

    // Type and submit — works fine
    fireEvent.change(textarea, { target: { value: 'hi' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)
  })
})

describe('ChatComposer — token speed chip', () => {
  beforeEach(() => {
    mockAuthCtxReturn = baseAuthCtx()
    mockChatPageCtxReturn = baseChatPageCtx()
    mockSlashCommandsReturn = { commands: [], loading: false, error: null }
    mockAppCtxReturn = baseAppCtx(mock(() => Promise.resolve(true)))
  })

  const liveMetrics: StreamMetrics = {
    tokens: 100,
    elapsedMs: 2000,
    tokPerSec: 50.0,
    estimated: false,
  }

  test('shows the rate with one decimal when metrics are seeded', () => {
    const { getByText, getByLabelText } = renderComposer(liveMetrics)

    expect(getByText('50.0 tok/s')).not.toBeNull()
    expect(getByLabelText('Velocidad de generación del modelo')).not.toBeNull()
  })

  test('is absent with no metrics, and the send button stays accessible', () => {
    const { queryByText, container } = renderComposer()

    expect(queryByText(/tok\/s/)).toBeNull()
    expect(container.querySelector('button[type="submit"]')).not.toBeNull()
  })

  test('never renders 0.0 tok/s (zero-rate metrics stay hidden)', () => {
    const { queryByText, container } = renderComposer({
      tokens: 0,
      elapsedMs: 0,
      tokPerSec: 0,
      estimated: false,
    })

    expect(queryByText(/tok\/s/)).toBeNull()
    expect(container.querySelector('button[type="submit"]')).not.toBeNull()
  })

  test('keeps the final figure after the stream ends and clears on a new turn/session', () => {
    const view = render(composerTree())

    // Before any stream: hidden.
    expect(view.queryByText(/tok\/s/)).toBeNull()

    // Streaming: the live rate appears.
    view.rerender(composerTree(liveMetrics))
    expect(view.getByText('50.0 tok/s')).not.toBeNull()

    // Segment done: the same (retained) figure is still shown.
    view.rerender(composerTree(liveMetrics))
    expect(view.getByText('50.0 tok/s')).not.toBeNull()

    // A new send / session switch clears it.
    view.rerender(composerTree(null))
    expect(view.queryByText(/tok\/s/)).toBeNull()
  })
})
