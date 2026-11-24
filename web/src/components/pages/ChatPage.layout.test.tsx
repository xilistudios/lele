import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { type ReactElement, createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import {
  AppLogicContext,
  type AppLogicContextValue,
  AppStreamingContext,
  type AppStreamingContextValue,
  StreamMetricsContext,
} from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { ChatPage } from './ChatPage'

const authValue = {
  session: { device_name: 'Phone' },
  apiUrl: 'http://localhost',
  api: null,
} as unknown as AuthContextValue

function logicValue(overrides: Partial<AppLogicContextValue> = {}): AppLogicContextValue {
  return {
    wsStatus: 'connected',
    diagnostics: { status: null, agentInfo: null, channels: [], tools: [] },
    error: null,
    diagnosticsOpen: false,
    sidebarOpen: true,
    mobileSidebarOpen: false,
    chatMode: 'agent',
    parentSessionKey: null,
    onCloseMobileSidebar: () => undefined,
    currentSessionKey: null,
    sessions: [],
    agents: [],
    currentAgent: null,
    pendingAttachments: [],
    queuedMessages: [],
    groups: new Map(),
    modelState: { available: [], groups: [], current: '' },
    thinkLevel: 'default',
    isProcessing: false,
    groupsEnabled: false,
    processingSessions: new Set<string>(),
    approvalRequest: null,
    approvalResult: null,
    isHistoryLoading: false,
    hasMore: false,
    isLoadingMore: false,
    loadMore: () => undefined,
    onSend: async () => true,
    onCancel: () => undefined,
    onUploadAttachments: async () => undefined,
    onAttachmentsChange: () => undefined,
    onSelectAgent: () => undefined,
    onSelectModel: () => undefined,
    onSelectThinkLevel: () => undefined,
    onSelectFolder: () => undefined,
    onClearFolder: () => undefined,
    onSelectMode: () => undefined,
    onCreateSession: () => undefined,
    onDeleteSession: () => undefined,
    onToggleSidebar: () => undefined,
    onSelectSession: () => undefined,
    onApprove: () => undefined,
    onRetry: () => undefined,
    onLogout: async () => undefined,
    sendTyping: () => undefined,
    removeQueuedMessage: () => undefined,
    sendNowQueuedMessage: () => undefined,
    clearQueue: () => undefined,
    ...overrides,
  } as unknown as AppLogicContextValue
}

const streamingValue = {
  messages: [],
  typingIndicator: null,
  toolStatus: null,
} as unknown as AppStreamingContextValue

function renderChatPage(overrides: Partial<AppLogicContextValue> = {}): HTMLElement {
  const tree: ReactElement = createElement(
    AuthContext.Provider,
    { value: authValue },
    createElement(
      AppLogicContext.Provider,
      { value: logicValue(overrides) },
      createElement(
        AppStreamingContext.Provider,
        { value: streamingValue },
        createElement(
          StreamMetricsContext.Provider,
          { value: null },
          createElement(MemoryRouter, { initialEntries: ['/'] }, createElement(ChatPage)),
        ),
      ),
    ),
  )
  return render(tree).container
}

function classesOf(el: Element | null): string[] {
  if (!el) throw new Error('element not rendered')
  return (el.getAttribute('class') ?? '').split(/\s+/).filter(Boolean)
}

function contains(el: Element | null, ...tokens: string[]) {
  const classes = classesOf(el)
  for (const token of tokens) expect(classes).toContain(token)
}

const shellOf = (container: HTMLElement) => container.querySelector('.app-shell')
const mainOf = (container: HTMLElement) => container.querySelector('.app-shell > main')
const listWrapperOf = (container: HTMLElement) =>
  mainOf(container)?.querySelector(':scope > div.flex-1') ?? null
const composerWrapperOf = (container: HTMLElement) =>
  mainOf(container)?.querySelector(':scope > div.border-t') ?? null
const headerOf = (container: HTMLElement) =>
  mainOf(container)?.querySelector(':scope > div.border-b') ?? null

afterEach(() => {
  cleanup()
})

describe('ChatPage layout contract', () => {
  test('the shell owns the viewport height and keeps flex', () => {
    const container = renderChatPage()
    const shell = shellOf(container)
    expect(shell).not.toBeNull()
    contains(shell, 'app-shell', 'flex')
    expect(classesOf(shell)).not.toContain('h-screen')
  })

  test('the message list wrapper absorbs the leftover height and cannot overflow', () => {
    const container = renderChatPage()
    contains(listWrapperOf(container), 'flex-1', 'min-h-0', 'overflow-hidden')
  })

  test('the message list wrapper is capped at 60% and anchored to the column bottom', () => {
    const container = renderChatPage()
    contains(listWrapperOf(container), 'max-h-[60%]', 'mt-auto')
  })

  test('the message list wrapper drops the height cap on mobile', () => {
    const container = renderChatPage()
    contains(listWrapperOf(container), 'max-md:max-h-none')
  })

  test('the composer wrapper is pinned', () => {
    const container = renderChatPage()
    const wrapper = composerWrapperOf(container)
    contains(wrapper, 'shrink-0', 'border-t')
    expect(classesOf(wrapper)).not.toContain('mt-auto')
  })

  test('the composer is available while reading a subagent session', () => {
    const container = renderChatPage({ parentSessionKey: 'parent-session' })
    const wrapper = composerWrapperOf(container)

    expect(wrapper).not.toBeNull()
    expect(wrapper?.querySelector('textarea')).not.toBeNull()
  })

  test('the chat header is pinned', () => {
    const container = renderChatPage()
    contains(headerOf(container), 'shrink-0', 'border-b')
  })

  test('the error banner is wrapped in a non-shrinking box', () => {
    const container = renderChatPage({ error: 'boom' })
    const banner = container.querySelector('[role="alert"]')
    expect(banner).not.toBeNull()
    const wrapper = banner?.parentElement ?? null
    expect(wrapper?.tagName).toBe('DIV')
    contains(wrapper, 'shrink-0')
  })

  test('the diagnostics panel is wrapped in a non-shrinking box', () => {
    const container = renderChatPage({ diagnosticsOpen: true })
    const panel = container.querySelector('section')
    expect(panel).not.toBeNull()
    const wrapper = panel?.parentElement ?? null
    expect(wrapper?.tagName).toBe('DIV')
    contains(wrapper, 'shrink-0')
  })

  test('the shell carries no mobile-only layout', () => {
    const container = renderChatPage()
    const suspects = [shellOf(container), mainOf(container), composerWrapperOf(container)]
    for (const el of suspects) {
      for (const token of classesOf(el)) expect(token.startsWith('max-md:')).toBe(false)
    }
  })
})
