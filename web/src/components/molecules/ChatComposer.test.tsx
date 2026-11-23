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

type MockCtx = Record<string, unknown>
let mockAppCtxReturn: MockCtx = {}
let mockAuthCtxReturn: MockCtx = {}
let mockChatPageCtxReturn: MockCtx = {}
let mockSlashCommandsReturn: MockCtx = { commands: [], loading: false, error: null }

let ChatComposer: typeof import('./ChatComposer').ChatComposer

beforeAll(() => {
  mock.module('../../contexts/ChatPageContext', () => ({
    useChatPageContext: () => mockChatPageCtxReturn,
  }))
  mock.module('../../hooks/useSlashCommands', () => ({
    useSlashCommands: () => mockSlashCommandsReturn,
  }))

  ChatComposer = require('./ChatComposer').ChatComposer
})

afterAll(() => {
  mock.restore()
})

function getTextarea(container: HTMLElement): HTMLTextAreaElement {
  const el = container.querySelector('textarea')
  if (!el) throw new Error('textarea not found')
  return el
}

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

    fireEvent.change(textarea, { target: { value: 'hello' } })

    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(onSend).toHaveBeenCalledTimes(1)
    expect(callCount).toBe(1)

    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    resolveSend(true)
    await waitFor(() => expect(callCount).toBe(1))

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

    fireEvent.change(textarea, { target: { value: 'hello' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    fireEvent.change(textarea, { target: { value: 'world' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(2)

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

    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)

    resolveSend(false)
    await waitFor(() => expect(callCount).toBe(1))

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

    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(0)

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

    expect(view.queryByText(/tok\/s/)).toBeNull()

    view.rerender(composerTree(liveMetrics))
    expect(view.getByText('50.0 tok/s')).not.toBeNull()

    view.rerender(composerTree(liveMetrics))
    expect(view.getByText('50.0 tok/s')).not.toBeNull()

    view.rerender(composerTree(null))
    expect(view.queryByText(/tok\/s/)).toBeNull()
  })
})

describe('ChatComposer — agent pill', () => {
  const AGENTS = [
    { id: 'main', name: 'Main Agent' },
    { id: 'ui-engineer', name: 'UI Engineer' },
  ]

  beforeEach(() => {
    mockAuthCtxReturn = baseAuthCtx()
    mockChatPageCtxReturn = { ...baseChatPageCtx(), hasConversation: true }
    mockSlashCommandsReturn = { commands: [], loading: false, error: null }
  })

  test('lets the user switch agent once the conversation has started', async () => {
    const onSelectAgent = mock(() => {})
    mockAppCtxReturn = {
      ...baseAppCtx(mock(() => Promise.resolve(true))),
      agents: AGENTS,
      currentAgent: AGENTS[0],
      onSelectAgent,
    }

    const { getByLabelText, getByText } = renderComposer()
    const trigger = getByLabelText('Agente') as HTMLButtonElement

    expect(trigger.disabled).toBe(false)

    fireEvent.click(trigger)
    fireEvent.click(await waitFor(() => getByText('UI Engineer')))

    expect(onSelectAgent).toHaveBeenCalledWith('ui-engineer')
  })
})
