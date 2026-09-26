import '../test/setup'
import '../test/i18n'

import { afterEach, beforeAll, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, render } from '@testing-library/react'
import type { ReactNode } from 'react'
import { useSyncExternalStore } from 'react'
import type { ChatMessage, ChatSession } from '../lib/types'

/**
 * Render-count tests for the two hot contexts. The REAL providers are mounted —
 * only the two hook modules `AppLogicContext` depends on are replaced:
 *
 *  - `useAppLogic` → a tiny external store, so a test can push exactly the
 *    updates a 32 ms typewriter tick produces (a new `messages` array, same
 *    length, one streaming message whose content grew).
 *  - `useSocket`  → a no-op (jsdom must not open a real WebSocket).
 *
 * Each spy component counts its OWN renders, which is what matters: when a
 * provider re-renders, React bails out of the unchanged `children` element, so
 * a child only re-renders when a context it consumes actually changed.
 */

// ── Store standing in for useAppLogic's state ───────────────────────────────
let appState: Record<string, unknown> = {}

const appListeners = new Set<() => void>()
const subscribeApp = (listener: () => void) => {
  appListeners.add(listener)
  return () => {
    appListeners.delete(listener)
  }
}
const getAppSnapshot = () => appState

const noop = () => {}

/** Every field AppLogicContext/`ChatPageContext` read, with stable identities. */
function baseApp(): Record<string, unknown> {
  return {
    error: null,
    agents: [],
    currentAgent: null,
    diagnostics: { status: null, channels: [], tools: [], config: null, agentInfo: null },
    diagnosticsOpen: false,
    sidebarOpen: false,
    mobileSidebarOpen: false,
    chatMode: 'agent',
    onSelectMode: noop,
    modelState: { available: ['test/model'], groups: [], current: 'test/model' },
    thinkLevel: 'default',
    sessionFolder: '',
    isProcessing: false,
    processingSessions: new Set<string>(),
    sessions: [] as ChatSession[],
    currentSessionKey: null,
    parentSessionKey: null,
    approvalRequest: null,
    approvalResult: null,
    pendingAttachments: [],
    queuedMessages: [],
    groups: [],
    groupsEnabled: false,
    sendTyping: noop,
    handleEvent: noop,
    onSend: noop,
    removeQueuedMessage: noop,
    sendNowQueuedMessage: noop,
    clearQueue: noop,
    queueCount: 0,
    retryMessage: noop,
    onApprove: noop,
    onCancel: noop,
    onSelectSession: noop,
    onCreateSession: noop,
    createSession: noop,
    onDeleteSession: noop,
    onClearSession: noop,
    onSelectAgent: noop,
    onSelectModel: noop,
    onSelectThinkLevel: noop,
    onSelectFolder: noop,
    onClearFolder: noop,
    onUploadAttachments: noop,
    onAttachmentsChange: noop,
    onLogout: noop,
    onToggleDiagnostics: noop,
    onToggleSidebar: noop,
    onOpenMobileSidebar: noop,
    onCloseMobileSidebar: noop,
    loadMore: noop,
    hasMore: false,
    isLoadingMore: false,
    isHistoryLoading: false,
    // hot fields
    messages: [] as ChatMessage[],
    toolStatus: null,
    typingIndicator: false,
  }
}

/** Replace the app state (patch) and notify the provider. Call inside act(). */
function pushApp(patch: Record<string, unknown> = {}) {
  appState = { ...baseApp(), ...appState, ...patch }
  for (const listener of appListeners) listener()
}

/** Reset the store to pristine state + patch, before mounting. */
function seedApp(patch: Record<string, unknown> = {}) {
  appState = { ...baseApp(), ...patch }
}

mock.module('../hooks/useAppLogic', () => ({
  useAppLogic: () => useSyncExternalStore(subscribeApp, getAppSnapshot),
}))

mock.module('../hooks/useSocket', () => ({
  useSocket: () => ({ status: 'connected', send: noop, close: noop }),
}))

// Deferred imports: resolved AFTER mock.module() above.
let AppLogicProvider: typeof import('./AppLogicContext').AppLogicProvider
let useAppLogicContext: typeof import('./AppLogicContext').useAppLogicContext
let useAppStreamingContext: typeof import('./AppLogicContext').useAppStreamingContext
let AuthContext: typeof import('./AuthContext').AuthContext
let ChatPageProvider: typeof import('./ChatPageContext').ChatPageProvider
let useChatPageContext: typeof import('./ChatPageContext').useChatPageContext

beforeAll(() => {
  const appLogic = require('./AppLogicContext')
  AppLogicProvider = appLogic.AppLogicProvider
  useAppLogicContext = appLogic.useAppLogicContext
  useAppStreamingContext = appLogic.useAppStreamingContext
  AuthContext = require('./AuthContext').AuthContext
  const chatPage = require('./ChatPageContext')
  ChatPageProvider = chatPage.ChatPageProvider
  useChatPageContext = chatPage.useChatPageContext
})

const AUTH = {
  api: {},
  apiUrl: 'http://localhost/',
  session: { token: 'test-token', client_id: 'test-client' },
  persistSession: noop,
}

let coldRenders = 0
let streamingRenders = 0
let chatPageRenders = 0

function ColdSpy() {
  coldRenders += 1
  const { sidebarOpen, sessions } = useAppLogicContext()
  return (
    <span data-testid="cold">
      {String(sidebarOpen)}:{sessions.length}
    </span>
  )
}

function StreamingSpy() {
  streamingRenders += 1
  const { messages, toolStatus } = useAppStreamingContext()
  return (
    <span data-testid="streaming">
      {messages.length}:{toolStatus ? 'active' : 'idle'}
    </span>
  )
}

function ChatPageSpy() {
  chatPageRenders += 1
  const { currentSession, parentSession, canCancel, hasConversation } = useChatPageContext()
  return (
    <span data-testid="chat-page">
      {currentSession ? `${currentSession.key}=${currentSession.name ?? ''}` : 'none'}|
      {canCancel ? 'cancel' : 'idle'}|{hasConversation ? 'conv' : 'empty'}|
      {parentSession ? parentSession.key : '-'}
    </span>
  )
}

function mountApp(children: ReactNode) {
  return render(
    <AuthContext.Provider value={AUTH as never}>
      <AppLogicProvider>{children}</AppLogicProvider>
    </AuthContext.Provider>,
  )
}

function mountChatPage() {
  return mountApp(
    <ChatPageProvider>
      <StreamingSpy />
      <ChatPageSpy />
    </ChatPageProvider>,
  )
}

/** One typewriter tick: same message list length, one grown content. */
function withStreamedContent(content: string, sessionKey = 's1'): ChatMessage[] {
  return [chatMessage({ id: 'm1', content, sessionKey })]
}

function chatMessage(overrides: Partial<ChatMessage> & { id: string }): ChatMessage {
  return {
    role: 'assistant',
    content: '',
    streaming: true,
    createdAt: '2026-01-01T00:00:00.000Z',
    ...overrides,
  }
}

function chatSession(key: string, name: string): ChatSession {
  return {
    key,
    name,
    created: '2026-01-01T00:00:00.000Z',
    updated: '2026-01-01T00:00:00.000Z',
  }
}

beforeEach(() => {
  cleanup()
  appState = baseApp()
  appListeners.clear()
  coldRenders = 0
  streamingRenders = 0
  chatPageRenders = 0
})

afterEach(cleanup)

describe('AppLogicContext — hot value identity', () => {
  test('30 cold state changes do not re-publish the streaming value', () => {
    seedApp()

    const { container } = mountApp(
      <>
        <ColdSpy />
        <StreamingSpy />
      </>,
    )

    expect(coldRenders).toBe(1)
    expect(streamingRenders).toBe(1)

    for (let i = 1; i <= 30; i += 1) {
      act(() => pushApp({ sidebarOpen: i % 2 === 0 }))
    }

    // The cold value legitimately changes on every real update: 29 of the 30
    // patches flip `sidebarOpen`, the first one is a no-op (false → false), so
    // 30 renders total including the initial mount.
    expect(coldRenders).toBe(30)
    // …but the hot value was never re-created, so its consumers stay put.
    expect(streamingRenders).toBe(1)
    expect(container.textContent).toContain('true:0')
  })

  test('message updates still reach the hot consumers (nothing dropped)', () => {
    seedApp()

    const { container } = mountApp(<StreamingSpy />)

    for (let i = 1; i <= 30; i += 1) {
      act(() => pushApp({ messages: withStreamedContent('x'.repeat(i)) }))
    }

    expect(streamingRenders).toBe(31)
    expect(container.textContent).toBe('1:idle')
  })
})

describe('ChatPageContext — consumer render stability', () => {
  test('30 streaming ticks re-render the ChatPage consumer at most twice', () => {
    seedApp({
      sessions: [chatSession('s1', 'Named session')],
      currentSessionKey: 's1',
      messages: withStreamedContent('a'),
    })

    const { container } = mountChatPage()

    expect(chatPageRenders).toBe(1)
    expect(container.textContent).toContain('s1=Named session|idle|conv')

    for (let i = 1; i <= 30; i += 1) {
      act(() => pushApp({ messages: withStreamedContent('a'.repeat(i)) }))
    }

    // The updates DID flow (the hot consumer moved on every tick)…
    expect(streamingRenders).toBe(31)
    // …while the ChatPage consumer — which reads none of the grown content —
    // did not re-render. (Pre-fix this was 31.)
    expect(chatPageRenders).toBeLessThanOrEqual(2)
    expect(chatPageRenders).toBe(1)
    expect(container.textContent).toContain('s1=Named session|idle|conv')
  })

  test('a new session name derived from the first user message still shows up', () => {
    seedApp({ currentSessionKey: 's-new', sessions: [], messages: [] })

    const { container } = mountChatPage()

    expect(container.textContent).toContain('s-new=s-new|idle|empty')

    act(() =>
      pushApp({
        messages: [
          chatMessage({
            id: 'u1',
            role: 'user',
            streaming: false,
            content: 'Plan the release\nsecond line',
            sessionKey: 's-new',
          }),
        ],
      }),
    )

    // The name is derived from the message → the consumer MUST re-render.
    expect(chatPageRenders).toBe(2)
    expect(container.textContent).toContain('s-new=Plan the release|idle|conv')

    // The assistant reply then streams in: the name stops changing, so the
    // consumer must go quiet again (the content it does not read keeps growing).
    for (let i = 1; i <= 30; i += 1) {
      act(() =>
        pushApp({
          messages: [
            chatMessage({
              id: 'u1',
              role: 'user',
              streaming: false,
              content: 'Plan the release\nsecond line',
              sessionKey: 's-new',
            }),
            chatMessage({ id: 'm1', content: 'y'.repeat(i), sessionKey: 's-new' }),
          ],
        }),
      )
    }

    expect(streamingRenders).toBe(32)
    expect(chatPageRenders).toBe(2)
    expect(container.textContent).toContain('s-new=Plan the release')
  })

  test('a rename pushed through `sessions` still shows up', () => {
    seedApp({
      sessions: [chatSession('s1', 'Old name')],
      currentSessionKey: 's1',
      messages: withStreamedContent('a'),
    })

    const { container } = mountChatPage()

    expect(container.textContent).toContain('s1=Old name')

    act(() => pushApp({ sessions: [chatSession('s1', 'New name')] }))

    expect(chatPageRenders).toBe(2)
    expect(container.textContent).toContain('s1=New name')
  })

  test('the synthesized session is replaced by the persisted entry when it lands', () => {
    seedApp({
      sessions: [],
      currentSessionKey: 's1',
      messages: [
        chatMessage({
          id: 'u1',
          role: 'user',
          streaming: false,
          content: 'From messages',
          sessionKey: 's1',
        }),
      ],
    })

    const { container } = mountChatPage()

    expect(container.textContent).toContain('s1=From messages')

    act(() => pushApp({ sessions: [chatSession('s1', 'Persisted')] }))

    expect(chatPageRenders).toBe(2)
    expect(container.textContent).toContain('s1=Persisted')
  })

  test('derived names keep being truncated at 80 chars', () => {
    seedApp({
      currentSessionKey: 's-long',
      sessions: [],
      messages: [
        chatMessage({
          id: 'u1',
          role: 'user',
          streaming: false,
          content: 'x'.repeat(120),
          sessionKey: 's-long',
        }),
      ],
    })

    const { container } = mountChatPage()

    expect(container.textContent).toContain(`s-long=${'x'.repeat(77)}...`)
  })

  test('tool status still flips `canCancel` for the composer', () => {
    seedApp({
      sessions: [chatSession('s1', 'Named session')],
      currentSessionKey: 's1',
      messages: withStreamedContent('a'),
    })

    const { container } = mountChatPage()

    expect(container.textContent).toContain('|idle|')

    act(() => pushApp({ toolStatus: 'running' }))

    expect(chatPageRenders).toBe(2)
    expect(container.textContent).toContain('|cancel|')
  })

  test('a first message flips `hasConversation` on its own', () => {
    seedApp({
      sessions: [chatSession('s1', 'Named session')],
      currentSessionKey: 's1',
      messages: [],
    })

    const { container } = mountChatPage()

    expect(container.textContent).toContain('s1=Named session|idle|empty')

    act(() => pushApp({ messages: withStreamedContent('a') }))

    expect(chatPageRenders).toBe(2)
    expect(container.textContent).toContain('s1=Named session|idle|conv')

    // …and the list length then stays put while the content grows.
    for (let i = 1; i <= 30; i += 1) {
      act(() => pushApp({ messages: withStreamedContent('a'.repeat(i)) }))
    }

    expect(chatPageRenders).toBe(2)
  })

  test('model selection and parent session updates still reach the consumer', () => {
    const available = ['test/model']
    const groups: never[] = []
    seedApp({
      sessions: [chatSession('s1', 'Named session')],
      currentSessionKey: 's1',
      messages: withStreamedContent('a'),
      modelState: { available, groups, current: 'test/model' },
    })

    const { container } = mountChatPage()

    // Only `modelState.current` changes (same `available` / `groups` identities).
    act(() => pushApp({ modelState: { available, groups, current: 'test/other' } }))

    expect(chatPageRenders).toBe(2)

    act(() =>
      pushApp({
        parentSessionKey: 'p1',
        sessions: [chatSession('s1', 'Named session'), chatSession('p1', 'Parent')],
      }),
    )

    expect(chatPageRenders).toBe(3)
    expect(container.textContent).toContain('s1=Named session|idle|conv|p1')
  })

  test('switching session re-derives the name (no stale memoized object)', () => {
    seedApp({
      sessions: [],
      currentSessionKey: 'a',
      messages: [
        chatMessage({
          id: 'u-a',
          role: 'user',
          streaming: false,
          content: 'Session A title',
          sessionKey: 'a',
        }),
      ],
    })

    const { container } = mountChatPage()

    expect(container.textContent).toContain('a=Session A title')

    act(() =>
      pushApp({
        currentSessionKey: 'b',
        messages: [
          chatMessage({
            id: 'u-b',
            role: 'user',
            streaming: false,
            content: 'Session B title',
            sessionKey: 'b',
          }),
        ],
      }),
    )

    expect(container.textContent).toContain('b=Session B title')
  })
})
