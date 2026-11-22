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
  availableModels: [{ value: 'gpt-4', label: 'gpt-4' }],
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

const hasClass = (el: Element, token: string) => classesOf(el).includes(token)

const sendButtonOf = (container: HTMLElement) =>
  (container.querySelector('button[type="submit"]') as HTMLElement | null) ?? null

const controlsRowOf = (container: HTMLElement) => sendButtonOf(container)?.parentElement ?? null

const controlGroupOf = (container: HTMLElement) => {
  const row = controlsRowOf(container)
  const group = Array.from(row?.children ?? []).find((el) => hasClass(el, 'flex-wrap'))
  return (group as HTMLElement | undefined) ?? null
}

const selectRootOf = (container: HTMLElement, ariaLabel: string) =>
  (container.querySelector(`button[aria-label="${ariaLabel}"]`)
    ?.parentElement as HTMLElement | null) ?? null

const tierWrapperOf = (container: HTMLElement) =>
  (Array.from(container.querySelectorAll('div')).find(
    (el) => hasClass(el, 'max-md:basis-full') && hasClass(el, 'md:contents'),
  ) as HTMLElement | undefined) ?? null

const toolbarRowOf = (container: HTMLElement) =>
  (Array.from(container.querySelectorAll('div')).find((el) => hasClass(el, 'max-md:hidden')) as
    | HTMLElement
    | undefined) ?? null

const folderChipsOf = (container: HTMLElement) =>
  Array.from(container.querySelectorAll('[data-testid="folder-chip"]')) as HTMLElement[]

const toolbarChipOf = (container: HTMLElement) =>
  folderChipsOf(container).find((chip) => toolbarRowOf(container)?.contains(chip)) ?? null

const tierChipBoxOf = (container: HTMLElement) =>
  (Array.from(container.querySelectorAll('div')).find(
    (el) => hasClass(el, 'max-md:basis-full') && hasClass(el, 'md:hidden'),
  ) as HTMLElement | undefined) ?? null

const tierChipOf = (container: HTMLElement) =>
  (tierChipBoxOf(container)?.querySelector('[data-testid="folder-chip"]') as HTMLElement | null) ??
  null

beforeEach(() => {
  mockAppCtxReturn = baseAppCtx()
  mockAuthCtxReturn = baseAuthCtx()
  mockChatPageCtxReturn = baseChatPageCtx()
  mockSlashCommandsReturn = { commands: [], loading: false, error: null }
})

describe('ChatComposer — mobile controls row', () => {
  test('the group dissolves at md and never goes nowrap on mobile', () => {
    const { container } = renderComposer()
    const group = controlGroupOf(container)
    expect(group).not.toBeNull()
    contains(group, 'flex-wrap', 'gap-2', 'min-w-0', 'flex-1', 'md:contents')
    expect(classesOf(group)).not.toContain('sm:contents')
    for (const el of container.querySelectorAll('*')) {
      expect(classesOf(el)).not.toContain('max-md:flex-nowrap')
    }
  })

  test('no element mixes a max-md utility with an sm boundary', () => {
    mockAppCtxReturn = { ...TWO_AGENTS, sessionFolder: '/home/user/projects/lele' }
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const { container } = renderComposer()
    for (const el of container.querySelectorAll('*')) {
      const classes = classesOf(el)
      const tiered = classes.some((token) => token.startsWith('max-md:'))
      if (tiered) expect(classes).not.toContain('sm:contents')
      expect(classes).not.toContain('sm:basis-full')
    }
    for (const el of [controlGroupOf(container), tierWrapperOf(container)]) {
      contains(el ?? null, 'md:contents')
    }
  })

  test('the desktop control row keeps the single wrapping row the composer always had', () => {
    const { container } = renderComposer()
    const row = controlsRowOf(container)
    contains(row, 'flex', 'flex-wrap', 'items-center', 'gap-2', 'px-3', 'pb-2', 'pt-1', 'sm:gap-3')
    expect(classesOf(row)).not.toContain('flex-nowrap')
    expect(classesOf(row)).not.toContain('max-md:flex-nowrap')
  })

  test('the send button is not a descendant of the wrapping group', () => {
    const { container } = renderComposer()
    const row = controlsRowOf(container)
    const group = controlGroupOf(container)
    const send = sendButtonOf(container)
    expect(row?.children.length).toBe(2)
    expect(row?.children[0] === group).toBe(true)
    expect(row?.children[1] === send).toBe(true)
    expect(group).not.toBeNull()
    expect(send).not.toBeNull()
    expect(group?.contains(send ?? null)).toBe(false)
    expect(send?.parentElement).toBe(controlsRowOf(container))
  })

  test('the model select is a direct child of the group and takes the leftover width', () => {
    const { container } = renderComposer()
    const group = controlGroupOf(container)
    const model = selectRootOf(container, label('model'))
    expect(model).not.toBeNull()
    expect(model?.parentElement).toBe(group)
    contains(model, 'max-md:min-w-0', 'max-md:flex-1')
  })

  test('thinking and agent share one full-width tier on mobile and dissolve on desktop', () => {
    mockAppCtxReturn = TWO_AGENTS
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const { container } = renderComposer()

    const wrapper = tierWrapperOf(container)
    expect(wrapper).not.toBeNull()
    contains(wrapper, 'max-md:basis-full', 'max-md:flex', 'md:contents')
    expect(classesOf(wrapper)).not.toContain('sm:contents')
    expect(wrapper?.parentElement).toBe(controlGroupOf(container))

    const thinking = selectRootOf(container, label('thinking'))
    const agent = selectRootOf(container, label('agent'))
    expect(thinking?.parentElement).toBe(wrapper)
    expect(agent?.parentElement).toBe(wrapper)

    for (const root of [thinking, agent]) {
      contains(root, 'max-md:min-w-0')
      expect(classesOf(root)).not.toContain('max-md:flex-1')
    }
    contains(tierWrapperOf(container), 'max-md:items-center', 'max-md:gap-2')
  })

  test('the second tier is not rendered when neither secondary selector nor a folder is set', () => {
    const { container } = renderComposer()
    expect(tierWrapperOf(container)).toBeNull()
    expect(selectRootOf(container, label('thinking'))).toBeNull()
    expect(selectRootOf(container, label('agent'))).toBeNull()
  })

  test('a folder alone still gets the second tier', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: '/home/user/projects/lele' }
    const { container } = renderComposer()
    const wrapper = tierWrapperOf(container)
    expect(wrapper).not.toBeNull()
    const chipBox = tierChipBoxOf(container)
    expect(chipBox).not.toBeNull()
    expect(tierChipOf(container)).not.toBeNull()
  })

  test('the send button keeps its desktop size and grows to a touch target on mobile', () => {
    const { container } = renderComposer()
    const send = sendButtonOf(container)
    contains(send, 'h-9', 'w-9', 'max-md:h-11', 'max-md:w-11', 'flex-shrink-0')
  })

  test('the folder controls live in the wrapping group on mobile', () => {
    const { container } = renderComposer()
    expect(classesOf(toolbarRowOf(container))).toContain('max-md:hidden')
    const group = controlGroupOf(container)
    const folderButton = Array.from(group?.querySelectorAll('button') ?? []).find((button) =>
      hasClass(button, 'md:hidden'),
    )
    expect(folderButton).toBeDefined()
    contains(folderButton ?? null, 'md:hidden', 'max-md:h-11', 'max-md:w-11')
  })

  test('the folder chip rides the second tier, ahead of the selectors', () => {
    mockAppCtxReturn = { ...TWO_AGENTS, sessionFolder: '/home/user/projects/lele' }
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const { container } = renderComposer()
    const wrapper = tierWrapperOf(container)
    expect(folderChipsOf(container).length).toBe(2)

    const chipBox = tierChipBoxOf(container)
    expect(chipBox?.parentElement).toBe(wrapper)
    expect(wrapper?.children[0] ?? null).toBe(chipBox)

    const mobileChip = tierChipOf(container)
    expect(mobileChip).not.toBeNull()
    contains(mobileChip, 'min-w-0', 'max-md:max-w-[200px]')

    const desktopChip = toolbarChipOf(container)
    expect(desktopChip).not.toBeNull()
    contains(desktopChip, 'max-w-[180px]')
    expect(desktopChip?.closest('[data-testid="folder-chip"]')).toBe(desktopChip as HTMLElement)
  })

  test('the folder chip takes a line of its own on the second tier', () => {
    mockAppCtxReturn = { ...TWO_AGENTS, sessionFolder: '/home/user/projects/lele' }
    mockChatPageCtxReturn = REASONING_CHAT_PAGE_CTX
    const { container } = renderComposer()
    const wrapper = tierWrapperOf(container)
    contains(wrapper, 'max-md:flex-wrap')

    const chipBox = tierChipBoxOf(container)
    contains(chipBox, 'max-md:basis-full', 'md:hidden')
    for (const root of [
      selectRootOf(container, label('thinking')),
      selectRootOf(container, label('agent')),
    ]) {
      expect(classesOf(root)).not.toContain('max-md:basis-full')
    }
    expect(classesOf(chipBox)).not.toContain('max-md:shrink-[2]')
  })

  test('the folder button stays on the primary line', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: '/home/user/projects/lele' }
    const { container } = renderComposer()
    const group = controlGroupOf(container)
    const wrapper = tierWrapperOf(container)
    const folderButton = group?.querySelector(`button[aria-label="${label('addFolder')}"]`) ?? null
    expect(folderButton).not.toBeNull()
    expect(folderButton?.parentElement).toBe(group)
    expect(wrapper?.contains(folderButton ?? null)).toBe(false)
  })

  test('the folder chip is rendered in both rows when a folder is set', () => {
    mockAppCtxReturn = { ...baseAppCtx(), sessionFolder: '/home/user/projects/lele' }
    const { container } = renderComposer()
    expect(folderChipsOf(container).length).toBe(2)
    const mobileChip = tierChipOf(container)
    expect(mobileChip).not.toBeNull()
    contains(mobileChip, 'max-md:max-w-[200px]')
    expect(mobileChip?.closest('[data-testid="folder-chip"]')).toBe(mobileChip as HTMLElement)
    const desktopChip = toolbarChipOf(container)
    expect(desktopChip).not.toBeNull()
    contains(desktopChip, 'max-w-[180px]')
  })

  test('both ml-auto anchors survive (chip inside the group, send outside it)', () => {
    const { container } = renderComposer(visibleMetrics)
    const send = sendButtonOf(container)
    contains(send, 'ml-auto')
    const group = controlGroupOf(container)
    const chip = Array.from(group?.querySelectorAll('*') ?? []).find((el) =>
      hasClass(el, 'ml-auto'),
    )
    expect(chip).toBeDefined()
    expect(group?.querySelectorAll('.ml-auto').length).toBe(1)
  })

  test('the textarea is capped against the mobile viewport', () => {
    const { container } = renderComposer()
    const textarea = container.querySelector('textarea')
    contains(textarea, 'max-h-[200px]', 'max-md:max-h-[min(200px,30dvh)]')
  })

  test('the attach button keeps a 44px touch target inside the group', () => {
    const { container } = renderComposer()
    const group = controlGroupOf(container)
    const attach = group?.querySelector(`button[aria-label="${label('attachFiles')}"]`) ?? null
    expect(attach).not.toBeNull()
    contains(attach, 'max-md:h-11', 'max-md:w-11')
    expect(attach?.closest('.relative')).not.toBeNull()
    expect(group?.contains(attach ?? null)).toBe(true)
  })
})
