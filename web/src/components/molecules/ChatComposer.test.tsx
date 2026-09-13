import '../../test/setup'
import { beforeEach, describe, expect, mock, test } from 'bun:test'
import '../../test/i18n'

// Mutable mock returns — tests swap these before each render.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
let mockAppCtxReturn: any = {}
let mockAuthCtxReturn: any = {}
let mockChatPageCtxReturn: any = {}
let mockSlashCommandsReturn: any = { commands: [], loading: false, error: null }

mock.module('../../contexts/AppLogicContext', () => ({
  useAppLogicContext: () => mockAppCtxReturn,
}))
mock.module('../../contexts/AuthContext', () => ({
  useAuthContext: () => mockAuthCtxReturn,
}))
mock.module('../../contexts/ChatPageContext', () => ({
  useChatPageContext: () => mockChatPageCtxReturn,
}))
mock.module('../../hooks/useSlashCommands', () => ({
  useSlashCommands: () => mockSlashCommandsReturn,
}))

// Imports come AFTER mock.module so the mocks are in place.
import { fireEvent, render, waitFor } from '@testing-library/react'
import { ChatComposer } from './ChatComposer'

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

function baseAppCtx(onSend: (...args: unknown[]) => unknown) {
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

function baseAuthCtx() {
  return {
    apiUrl: 'http://localhost:18793',
    api: { chatCommands: () => Promise.resolve({ commands: [] }) },
  }
}

function baseChatPageCtx() {
  return {
    canCancel: false,
    hasConversation: false,
    availableModels: [],
    groupedModels: undefined,
    selectedModel: '',
    thinkLevel: 'default',
  }
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

    const { container } = render(<ChatComposer />)
    const textarea = container.querySelector('textarea')!

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

    const { container } = render(<ChatComposer />)
    const textarea = container.querySelector('textarea')!

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

    const { container } = render(<ChatComposer />)
    const textarea = container.querySelector('textarea')!

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

    const { container } = render(<ChatComposer />)
    const textarea = container.querySelector('textarea')!

    // Enter with no content — no call, no blocking
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(0)

    // Type and submit — works fine
    fireEvent.change(textarea, { target: { value: 'hi' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })
    expect(callCount).toBe(1)
  })
})
