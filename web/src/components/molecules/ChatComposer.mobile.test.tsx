import '../../test/setup'
import { afterAll, beforeAll, beforeEach, describe, expect, mock, test } from 'bun:test'
import { render } from '@testing-library/react'
import {
  AppLogicContext,
  type AppLogicContextValue,
  StreamMetricsContext,
} from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { StreamMetrics } from '../../hooks/useStreamMetrics'
import i18n from '../../test/i18n'

type MockCtx = Record<string, unknown>
let mockAppCtxReturn: MockCtx = {}
let mockAuthCtxReturn: MockCtx = {}
let mockChatPageCtxReturn: MockCtx = {}
let mockSlashCommandsReturn: MockCtx = { commands: [], loading: false, error: null }

let ChatComposer: typeof import('./ChatComposer').ChatComposer

const label = (key: string) => i18n.t(`chat.${key}`) as string

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

function baseAppCtx(): MockCtx {
  return {
    onSend: async () => true,
    onCancel: () => {},
    onUploadAttachments: async () => [] as string[],
    onAttachmentsChange: () => {},
    onSelectAgent: () => {},
    onSelectModel: () => {},
    onSelectThinkLevel: () => {},
    onSelectFolder: async () => {},
    onClearFolder: () => {},
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

function classesOf(el: Element | null): string[] {
  if (!el) throw new Error('element not rendered')
  return (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean)
}

function contains(el: Element | null, ...tokens: string[]) {
  const classes = classesOf(el)
  for (const token of tokens) expect(classes).toContain(token)
}

const capsuleOf = (container: HTMLElement) =>
  (container.querySelector('[data-testid="composer-capsule"]') as HTMLElement | null) ?? null

const chipsOf = (container: HTMLElement) =>
  (container.querySelector('[data-testid="composer-chips"]') as HTMLElement | null) ?? null

const textareaOf = (container: HTMLElement) =>
  (container.querySelector('textarea') as HTMLTextAreaElement | null) ?? null

const sendButtonOf = (container: HTMLElement) =>
  (container.querySelector('button[type="submit"]') as HTMLElement | null) ?? null

const folderButtonOf = (container: HTMLElement) =>
  (container.querySelector(`button[aria-label="${label('addFolder')}"]`) as HTMLElement | null) ??
  null

const attachButtonOf = (container: HTMLElement) =>
  (container.querySelector(`button[aria-label="${label('attachFiles')}"]`) as HTMLElement | null) ??
  null

const metricChipOf = (container: HTMLElement) =>
  (Array.from(container.querySelectorAll('span')).find((el) =>
    (el.textContent ?? '').includes('tok/s'),
  ) as HTMLElement | undefined) ?? null

const selectRootOf = (container: HTMLElement, ariaLabel: string) =>
  (container.querySelector(`button[aria-label="${ariaLabel}"]`)
    ?.parentElement as HTMLElement | null) ?? null

const folderChipsOf = (container: HTMLElement) =>
  Array.from(container.querySelectorAll('[data-testid="folder-chip"]')) as HTMLElement[]

beforeEach(() => {
  mockAppCtxReturn = baseAppCtx()
  mockAuthCtxReturn = baseAuthCtx()
  mockChatPageCtxReturn = baseChatPageCtx()
  mockSlashCommandsReturn = { commands: [], loading: false, error: null }
})

describe('ChatComposer — capsule input', () => {
  test('the input row is a single non-wrapping capsule holding the textarea and the send action', () => {
    const { container } = renderComposer()
    const capsule = capsuleOf(container)
    const textarea = textareaOf(container)
    const send = sendButtonOf(container)

    expect(capsule).not.toBeNull()
    expect(textarea).not.toBeNull()
    expect(send).not.toBeNull()

    contains(capsule, 'flex', 'items-center', 'rounded-full')
    expect(classesOf(capsule)).not.toContain('flex-wrap')
    expect(classesOf(capsule)).not.toContain('max-md:flex-nowrap')

    expect(capsule?.contains(textarea ?? null)).toBe(true)
    expect(capsule?.contains(send ?? null)).toBe(true)
    expect(send?.parentElement).toBe(capsule)
    expect(textarea?.parentElement).toBe(capsule)
  })

  test('the textarea grows inside the capsule and stays capped against the mobile viewport', () => {
    const { container } = renderComposer()
    const textarea = textareaOf(container)

    contains(textarea, 'min-w-0', 'flex-1', 'max-h-[200px]', 'max-md:max-h-[min(200px,30dvh)]')
  })

  test('every capsule action is a centered circle capped to the input height', () => {
    const { container } = renderComposer()
    const capsule = capsuleOf(container)

    for (const control of [
      folderButtonOf(container),
      attachButtonOf(container),
      sendButtonOf(container),
    ]) {
      expect(control).not.toBeNull()
      contains(control, 'h-9', 'w-9', 'flex', 'items-center', 'justify-center')
      expect(classesOf(control)).toContain('rounded-full')
      expect(capsule?.contains(control ?? null)).toBe(true)
      for (const oversized of ['h-11', 'w-11', 'h-10', 'w-10', 'h-12', 'w-12']) {
        expect(classesOf(control)).not.toContain(oversized)
      }
    }
  })

  test('the input line sets the height of the row, not the actions', () => {
    const { container } = renderComposer()
    const textarea = textareaOf(container)
    const capsule = capsuleOf(container)

    contains(textarea, 'min-h-[36px]', 'py-[9px]')
    expect(classesOf(capsule)).not.toContain('min-h')
    for (const control of [
      folderButtonOf(container),
      attachButtonOf(container),
      sendButtonOf(container),
    ]) {
      expect(classesOf(control ?? null)).not.toContain('min-h-[36px]')
    }
  })

  test('no element mixes a max-md utility with an sm boundary', () => {
    mockAppCtxReturn = { ...TWO_AGENTS, sessionFolder: SESSION_FOLDER }
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const { container } = renderComposer()
    for (const el of container.querySelectorAll('*')) {
      const classes = classesOf(el)
      const tiered = classes.some((token) => token.startsWith('max-md:'))
      if (tiered) expect(classes).not.toContain('sm:contents')
      expect(classes).not.toContain('sm:basis-full')
    }
  })
})

describe('ChatComposer — selector chips', () => {
  test('the chips strip sits under the capsule and wraps instead of scrolling', () => {
    const { container } = renderComposer()
    const form = container.querySelector('form')
    const chips = chipsOf(container)
    const capsule = capsuleOf(container)

    expect(chips).not.toBeNull()
    expect(chips?.parentElement).toBe(form)
    contains(chips, 'flex', 'flex-wrap', 'items-center', 'gap-1.5')
    expect(classesOf(chips)).not.toContain('flex-nowrap')
    expect(chips?.contains(capsule ?? null)).toBe(false)
  })

  test('the model select is a direct child of the strip and takes the leftover width', () => {
    const { container } = renderComposer()
    const chips = chipsOf(container)
    const model = selectRootOf(container, label('model'))

    expect(model).not.toBeNull()
    expect(model?.parentElement).toBe(chips)
    contains(model, 'min-w-0', 'flex-1', 'basis-[132px]')
  })

  test('only the model select renders when no reasoning, agent or folder is set', () => {
    const { container } = renderComposer()
    const chips = chipsOf(container)

    expect(selectRootOf(container, label('thinking'))).toBeNull()
    expect(selectRootOf(container, label('agent'))).toBeNull()
    expect(folderChipsOf(container)).toHaveLength(0)
    expect(chips?.querySelectorAll('button[aria-label]').length).toBe(1)
  })

  test('thinking and agent are content-width pills that never squeeze the model', () => {
    mockAppCtxReturn = TWO_AGENTS
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const { container } = renderComposer()
    const chips = chipsOf(container)

    for (const ariaLabel of [label('thinking'), label('agent')]) {
      const root = selectRootOf(container, ariaLabel)
      expect(root).not.toBeNull()
      expect(root?.parentElement).toBe(chips)
      contains(root, 'min-w-0', 'flex-none')
      expect(classesOf(root)).not.toContain('flex-1')
      contains(root?.querySelector('button') ?? null, 'rounded-full')
    }
  })
})

describe('ChatComposer — folder chip', () => {
  test('a session folder renders one chip in the strip and none inside the capsule', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: SESSION_FOLDER }
    const { container } = renderComposer()
    const chips = chipsOf(container)
    const capsule = capsuleOf(container)

    const [chip] = folderChipsOf(container)
    expect(folderChipsOf(container)).toHaveLength(1)
    expect(chip).toBeDefined()
    expect(chip.parentElement).toBe(chips)
    expect(capsule?.contains(chip)).toBe(false)
    expect(chips?.children[0]).toBe(chip)
  })

  test('the chip takes a line of its own on mobile and caps its width on desktop', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: SESSION_FOLDER }
    const { container } = renderComposer()
    const [chip] = folderChipsOf(container)

    contains(chip, 'min-w-0', 'max-w-[200px]', 'max-md:basis-full')
    for (const root of [selectRootOf(container, label('model'))]) {
      expect(classesOf(root ?? null)).not.toContain('max-md:basis-full')
    }
  })

  test('the folder button stays in the capsule next to the attach action', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: SESSION_FOLDER }
    const { container } = renderComposer()
    const capsule = capsuleOf(container)
    const folderButton = folderButtonOf(container)

    expect(folderButton?.parentElement).toBe(capsule)
    expect(folderButton?.nextElementSibling?.contains(attachButtonOf(container) ?? null)).toBe(true)
  })
})

describe('ChatComposer — metrics chip', () => {
  test('the rate rides the end of the chips strip', () => {
    const { container } = renderComposer(visibleMetrics)
    const chips = chipsOf(container)
    const chip = metricChipOf(container)

    expect(chip).not.toBeNull()
    contains(chip, 'ml-auto')
    expect(chips?.contains(chip ?? null)).toBe(true)
    expect(chips?.querySelectorAll('.ml-auto').length).toBe(1)
  })

  test('no chip is rendered before the first token of a turn', () => {
    const { container } = renderComposer()
    expect(metricChipOf(container)).toBeNull()
    expect(sendButtonOf(container)).not.toBeNull()
  })
})
