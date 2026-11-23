import '../../test/setup'
import { afterAll, beforeEach, describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, within } from '@testing-library/react'
import {
  AppLogicContext,
  type AppLogicContextValue,
  StreamMetricsContext,
} from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { StreamMetrics } from '../../hooks/useStreamMetrics'
import i18n from '../../test/i18n'
import { waitUntil } from '../../test/waitUntil'

type MockCtx = Record<string, unknown>
let mockAppCtxReturn: MockCtx = {}
let mockAuthCtxReturn: MockCtx = {}
let mockChatPageCtxReturn: MockCtx = {}
let mockSlashCommandsReturn: MockCtx = { commands: [], loading: false, error: null }

const label = (key: string) => i18n.t(`chat.${key}`) as string

mock.module('../../contexts/ChatPageContext', () => ({
  useChatPageContext: () => mockChatPageCtxReturn,
}))
mock.module('../../hooks/useSlashCommands', () => ({
  useSlashCommands: () => mockSlashCommandsReturn,
}))

const { ChatComposer } = require('./ChatComposer') as typeof import('./ChatComposer')

afterAll(() => {
  mock.restore()
})

const sendCalls: Array<[string, string[]]> = []
const cancelCalls: string[] = []
const clearFolderCalls: string[] = []

function baseAppCtx(): MockCtx {
  return {
    onSend: async (content: string, attachments: string[]) => {
      sendCalls.push([content, attachments])
      return true
    },
    onCancel: () => {
      cancelCalls.push('cancel')
    },
    onUploadAttachments: async () => [] as string[],
    onAttachmentsChange: () => {},
    onSelectAgent: () => {},
    onSelectModel: () => {},
    onSelectThinkLevel: () => {},
    onSelectFolder: async () => {},
    onClearFolder: () => {
      clearFolderCalls.push('clear')
    },
    sendTyping: () => {},
    agents: [] as unknown[],
    currentAgent: null,
    pendingAttachments: [] as string[],
    sessionFolder: '',
    chatMode: 'agent' as const,
    currentSessionKey: null,
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
    availableModels: [{ value: 'gpt-4', label: 'gpt-4' }],
    groupedModels: undefined,
    selectedModel: 'gpt-4',
    thinkLevel: 'default',
  }
}

const REASONING_CHAT_PAGE_CTX: MockCtx = {
  ...baseChatPageCtx(),
  groupedModels: [
    {
      label: 'Reasoning',
      options: [{ value: 'gpt-4', label: 'gpt-4', reasoning: { enable: true } }],
    },
  ],
}

const TWO_AGENTS: MockCtx = {
  ...baseAppCtx(),
  agents: [
    { id: 'main', name: 'Main Agent' },
    { id: 'ui-engineer', name: 'UI Engineer' },
  ],
  currentAgent: { id: 'main', name: 'Main Agent' },
}

const SESSION_FOLDER = '/home/user/projects/lele'

const visibleMetrics: StreamMetrics = {
  tokens: 120,
  elapsedMs: 3000,
  tokPerSec: 40,
  estimated: false,
}

type ComposerView = ReturnType<typeof renderComposer>

function renderComposer(metrics: StreamMetrics | null = null) {
  return render(
    <AuthContext.Provider value={mockAuthCtxReturn as unknown as AuthContextValue}>
      <AppLogicContext.Provider value={mockAppCtxReturn as unknown as AppLogicContextValue}>
        <StreamMetricsContext.Provider value={metrics}>
          <ChatComposer />
        </StreamMetricsContext.Provider>
      </AppLogicContext.Provider>
    </AuthContext.Provider>,
  )
}

const textareaOf = (view: ComposerView) => view.getByRole('textbox') as HTMLTextAreaElement

const capsuleOf = (view: ComposerView) => {
  const capsule = textareaOf(view).parentElement
  if (!capsule) throw new Error('the composer input is not wrapped in a capsule')
  return capsule
}

const chipsOf = (view: ComposerView) => {
  const chips = capsuleOf(view).nextElementSibling
  if (!(chips instanceof HTMLElement)) throw new Error('the chips strip is not rendered')
  return chips
}

const buttonOf = (view: ComposerView, name: string) => view.getByRole('button', { name })

beforeEach(() => {
  sendCalls.length = 0
  cancelCalls.length = 0
  clearFolderCalls.length = 0
  mockAppCtxReturn = baseAppCtx()
  mockAuthCtxReturn = baseAuthCtx()
  mockChatPageCtxReturn = baseChatPageCtx()
  mockSlashCommandsReturn = { commands: [], loading: false, error: null }
})

describe('ChatComposer — capsule input', () => {
  test('keeps the draft, the folder picker, the attach action and send in one capsule', () => {
    const view = renderComposer()
    const capsule = capsuleOf(view)

    const controls = [
      buttonOf(view, label('addFolder')),
      buttonOf(view, label('attachFiles')),
      buttonOf(view, label('send')),
    ]

    for (const control of controls) expect(capsule.contains(control)).toBe(true)
    expect(within(capsule).getAllByRole('button')).toHaveLength(3)
    expect(capsule.contains(textareaOf(view))).toBe(true)
    expect(capsule.closest('form')).not.toBeNull()
  })

  test('sends the typed draft and clears the field afterwards', async () => {
    const view = renderComposer()
    const textarea = textareaOf(view)

    fireEvent.change(textarea, { target: { value: '  hola  ' } })
    expect(textarea.value).toBe('  hola  ')

    fireEvent.click(buttonOf(view, label('send')))

    await waitUntil(() => sendCalls.length === 1)
    expect(sendCalls[0]).toEqual(['hola', []])
    await waitUntil(() => textarea.value === '')
  })

  test('sends on Enter without a modifier', async () => {
    const view = renderComposer()
    const textarea = textareaOf(view)

    fireEvent.change(textarea, { target: { value: 'enter' } })
    fireEvent.keyDown(textarea, { key: 'Enter' })

    await waitUntil(() => sendCalls.length === 1)
    expect(sendCalls[0]).toEqual(['enter', []])
  })

  test('offers cancel instead of send while a response is streaming', () => {
    mockChatPageCtxReturn = { ...baseChatPageCtx(), canCancel: true }
    const view = renderComposer()

    const cancel = buttonOf(view, label('cancel'))
    expect(capsuleOf(view).contains(cancel)).toBe(true)
    expect(view.queryByRole('button', { name: label('send') })).toBeNull()

    fireEvent.click(cancel)
    expect(cancelCalls).toEqual(['cancel'])
    expect(sendCalls).toHaveLength(0)
  })
})

describe('ChatComposer — selector chips', () => {
  test('renders only the model selector when no reasoning, agent or folder is set', () => {
    const view = renderComposer()
    const chips = chipsOf(view)

    expect(within(chips).getAllByRole('button')).toHaveLength(1)
    expect(chips.contains(buttonOf(view, label('model')))).toBe(true)
    expect(view.queryByRole('button', { name: label('thinking') })).toBeNull()
    expect(view.queryByRole('button', { name: label('agent') })).toBeNull()
  })

  test('adds the thinking and agent selectors to the strip when they apply', () => {
    mockAppCtxReturn = TWO_AGENTS
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const view = renderComposer()
    const chips = chipsOf(view)

    for (const name of [label('model'), label('thinking'), label('agent')]) {
      expect(chips.contains(buttonOf(view, name))).toBe(true)
    }
    expect(within(chips).getAllByRole('button')).toHaveLength(3)
    expect(buttonOf(view, label('agent')).textContent).toContain('Main Agent')
  })

  test('keeps the selectors outside the capsule', () => {
    mockAppCtxReturn = TWO_AGENTS
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const view = renderComposer()
    const capsule = capsuleOf(view)

    for (const name of [label('model'), label('thinking'), label('agent')]) {
      expect(capsule.contains(buttonOf(view, name))).toBe(false)
    }
  })
})

describe('ChatComposer — folder chip', () => {
  test('shows the picked folder as a clearable chip in the strip', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: SESSION_FOLDER }
    const view = renderComposer()
    const chips = chipsOf(view)

    const removeFolder = buttonOf(view, label('removeFolder'))
    expect(chips.contains(removeFolder)).toBe(true)
    expect(capsuleOf(view).contains(removeFolder)).toBe(false)
    expect(chips.textContent).toContain('lele')
    expect(chips.children[0]?.contains(removeFolder)).toBe(true)

    fireEvent.click(removeFolder)
    expect(clearFolderCalls).toEqual(['clear'])
  })

  test('renders no chip when no folder is selected', () => {
    const view = renderComposer()
    expect(view.queryByRole('button', { name: label('removeFolder') })).toBeNull()
  })
})

describe('ChatComposer — metrics chip', () => {
  test('reports the rate in the strip once a turn is streaming', () => {
    const view = renderComposer(visibleMetrics)
    const chips = chipsOf(view)
    const rate = within(chips).getByLabelText(i18n.t('chat.tokensPerSecondTitle'))

    expect(rate.textContent).toBe(i18n.t('chat.tokensPerSecond', { rate: '40.0' }))
  })

  test('renders no rate before the first token of a turn', () => {
    const view = renderComposer()
    expect(view.queryByLabelText(i18n.t('chat.tokensPerSecondTitle'))).toBeNull()
    expect(buttonOf(view, label('send'))).not.toBeNull()
  })
})
