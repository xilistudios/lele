import '../../test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, fireEvent, render, within } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { createApiClient } from '../../lib/api'
import type { ChatSession } from '../../lib/types'
import i18n from '../../test/i18n'
import { waitUntil } from '../../test/waitUntil'
import { CHAT_PAGE_SIZE, ChatHistoryPage } from './ChatHistoryPage'

const originalFetch = globalThis.fetch
const originalIntersectionObserver = globalThis.IntersectionObserver

const TOTAL_SESSIONS = 120

const ALL_SESSIONS: ChatSession[] = Array.from({ length: TOTAL_SESSIONS }, (_, i) => ({
  key: `native:client-1:s${String(i).padStart(3, '0')}`,
  name: `Chat ${String(i).padStart(3, '0')}`,
  kind: 'chat' as const,
  created: '2026-01-01T00:00:00.000Z',
  updated: new Date(2026, 0, 1, 0, 0, i).toISOString(),
}))

type PageRequest = { offset: number; limit: number }

function installSessionServer(requests: PageRequest[], sessions = ALL_SESSIONS) {
  globalThis.fetch = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.split('?')[0].endsWith('/api/v1/chat/sessions')) {
      return new Response(JSON.stringify({ error: `unexpected fetch: ${url}` }), { status: 404 })
    }
    const params = new URL(url).searchParams
    const offset = Number(params.get('offset') ?? '0')
    const limit = Number(params.get('limit') ?? String(CHAT_PAGE_SIZE))
    requests.push({ offset, limit })
    return new Response(
      JSON.stringify({
        sessions: sessions.slice(offset, offset + limit),
        total: sessions.length,
        has_more: offset + limit < sessions.length,
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )
  }) as unknown as typeof fetch
}

type ObserverRecord = {
  callback: IntersectionObserverCallback
  targets: Set<Element>
}

let observers: ObserverRecord[] = []

function installIntersectionObserver() {
  observers = []
  class FakeIntersectionObserver {
    private record: ObserverRecord

    constructor(callback: IntersectionObserverCallback) {
      this.record = { callback, targets: new Set() }
      observers.push(this.record)
    }

    observe(target: Element) {
      this.record.targets.add(target)
    }

    unobserve(target: Element) {
      this.record.targets.delete(target)
    }

    disconnect() {
      this.record.targets.clear()
    }

    takeRecords() {
      return []
    }
  }
  globalThis.IntersectionObserver =
    FakeIntersectionObserver as unknown as typeof IntersectionObserver
}

function hasLiveSentinel(): boolean {
  return observers.some((o) => o.targets.size > 0)
}

function scrollSentinelIntoView() {
  const live = observers.filter((o) => o.targets.size > 0)
  for (const record of live) {
    act(() => {
      record.callback(
        [{ isIntersecting: true } as IntersectionObserverEntry],
        record as unknown as IntersectionObserver,
      )
    })
  }
}

const apiClient = createApiClient('http://127.0.0.1:18793')
apiClient.setToken('token', 'refresh')

const authValue = {
  api: apiClient,
  apiUrl: 'http://127.0.0.1:18793',
  session: { token: 'token', client_id: 'client-1' },
} as unknown as AuthContextValue

function makeLogicValue(sessions: ChatSession[]) {
  return {
    sessions,
    currentSessionKey: null,
    parentSessionKey: null,
    sidebarOpen: true,
    mobileSidebarOpen: false,
    processingSessions: new Set<string>(),
    chatMode: 'agent',
    groupsEnabled: false,
    onCloseMobileSidebar: () => undefined,
    onOpenMobileSidebar: () => undefined,
    onSelectSession: () => undefined,
    onDeleteSession: async () => undefined,
    onCreateSession: async () => undefined,
    onToggleSidebar: () => undefined,
    onSelectMode: () => undefined,
    onLogout: async () => undefined,
  } as unknown as AppLogicContextValue
}

function renderPage(sessions: ChatSession[]) {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: makeLogicValue(sessions) },
        createElement(MemoryRouter, { initialEntries: ['/chats'] }, createElement(ChatHistoryPage)),
      ),
    ),
  )
}

type RenderedView = ReturnType<typeof renderPage>

const settle = (predicate: () => boolean, what: string) => waitUntil(predicate, what, 20_000)

function sessionCount(view: RenderedView, name: RegExp): number {
  return within(view.getByRole('main')).queryAllByRole('button', { name }).length
}

function renderedChatCount(view: RenderedView): number {
  return sessionCount(view, /^Chat \d{3}/)
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
  globalThis.IntersectionObserver = originalIntersectionObserver
})

describe('ChatHistoryPage pagination', () => {
  test('default page size is 20 chats', () => {
    expect(CHAT_PAGE_SIZE).toBe(20)
  })

  test('renders a single page even when the app already holds every session', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)

    const view = renderPage(ALL_SESSIONS)

    await settle(() => renderedChatCount(view) === CHAT_PAGE_SIZE, 'the first page renders')
    expect(requests).toEqual([{ offset: 0, limit: CHAT_PAGE_SIZE }])
    expect(renderedChatCount(view)).toBeLessThan(ALL_SESSIONS.length)
  })

  test('scrolling the sentinel reveals the next page', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)
    installIntersectionObserver()

    const view = renderPage(ALL_SESSIONS)
    await settle(() => renderedChatCount(view) === CHAT_PAGE_SIZE, 'the first page renders')

    scrollSentinelIntoView()

    await settle(
      () => renderedChatCount(view) === CHAT_PAGE_SIZE * 2,
      'the second page is revealed',
    )
  })

  test('loads the next server page when the revealed window runs out of sessions', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)
    installIntersectionObserver()

    const view = renderPage([])

    await settle(
      () => requests.some((request) => request.offset === 0),
      'the first page is requested',
    )
    await settle(
      () => requests.some((request) => request.offset === CHAT_PAGE_SIZE),
      'the next page is prefetched',
    )

    await settle(hasLiveSentinel, 'the sentinel is observed')
    scrollSentinelIntoView()

    await settle(() => renderedChatCount(view) === CHAT_PAGE_SIZE * 2, 'both pages are rendered')
  })

  test('keeps a load-more action available while searching', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests, ALL_SESSIONS.slice(0, 45))
    installIntersectionObserver()

    const view = renderPage([])
    await settle(() => renderedChatCount(view) === CHAT_PAGE_SIZE, 'the first page renders')

    fireEvent.change(view.getByRole('textbox'), { target: { value: 'Chat 04' } })
    expect(renderedChatCount(view)).toBe(0)

    const loadMore = view.getByRole('button', {
      name: i18n.t('chat.loadMore', { loaded: 40, total: 45 }),
    })
    fireEvent.click(loadMore)

    await settle(() => renderedChatCount(view) === 5, 'the last page is loaded')
    expect(requests).toContainEqual({ offset: 40, limit: CHAT_PAGE_SIZE })
  })

  test('ignores a page that was requested for a previous kind', async () => {
    const requests: PageRequest[] = []
    let releaseStalePage = () => {}
    const stalePage = new Promise<void>((resolve) => {
      releaseStalePage = resolve
    })

    globalThis.fetch = mock(async (input: RequestInfo | URL) => {
      const url = new URL(String(input))
      const offset = Number(url.searchParams.get('offset') ?? '0')
      const kind = url.searchParams.get('kind')
      requests.push({ offset, limit: Number(url.searchParams.get('limit') ?? CHAT_PAGE_SIZE) })

      if (kind === 'heartbeat') {
        return new Response(JSON.stringify({ sessions: [], total: 0, has_more: false }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      }

      if (offset > 0) await stalePage
      const sessions = ALL_SESSIONS.slice(offset, offset + CHAT_PAGE_SIZE)
      return new Response(
        JSON.stringify({ sessions, total: ALL_SESSIONS.length, has_more: true }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }) as unknown as typeof fetch

    installIntersectionObserver()
    const view = renderPage([])
    await settle(
      () => requests.some((request) => request.offset === CHAT_PAGE_SIZE),
      'the prefetch is in flight',
    )

    fireEvent.click(view.getByRole('button', { name: i18n.t('chat.filterKind.heartbeat') }))
    await settle(() => renderedChatCount(view) === 0, 'the kind switch clears the list')

    releaseStalePage()

    await settle(() => requests.length >= 3, 'the stale page resolved')
    expect(renderedChatCount(view)).toBe(0)
    expect(view.getByText(i18n.t('chat.totalSessions', { count: 0 }))).not.toBeNull()
  })

  test('never renders a fetched row that belongs to another kind', async () => {
    const requests: PageRequest[] = []
    const mixed: ChatSession[] = Array.from({ length: 10 }, (_, i) => ({
      key: `native:client-1:b${String(i).padStart(3, '0')}`,
      name: `Beat ${String(i).padStart(3, '0')}`,
      kind: 'heartbeat' as const,
      created: '2026-01-01T00:00:00.000Z',
      updated: new Date(2026, 0, 2, 0, 0, i).toISOString(),
    }))
    installSessionServer(requests, [...ALL_SESSIONS.slice(0, 10), ...mixed])
    installIntersectionObserver()

    const view = renderPage([])
    await settle(() => renderedChatCount(view) === 10, 'the chat rows render')
    expect(sessionCount(view, /^Beat \d{3}/)).toBe(10)

    fireEvent.click(view.getByRole('button', { name: i18n.t('chat.filterKind.heartbeat') }))

    await settle(
      () => sessionCount(view, /^Beat \d{3}/) === 10,
      'the heartbeat rows survive the kind switch',
    )
    expect(renderedChatCount(view)).toBe(0)
  })

  test('stops requesting pages once every session is loaded', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests, ALL_SESSIONS.slice(0, 25))
    installIntersectionObserver()

    const view = renderPage([])

    await settle(() => requests.length === 2, 'both pages are requested')
    expect(requests).toContainEqual({ offset: 0, limit: CHAT_PAGE_SIZE })
    expect(requests).toContainEqual({ offset: CHAT_PAGE_SIZE, limit: CHAT_PAGE_SIZE })

    await settle(hasLiveSentinel, 'the sentinel is observed')
    scrollSentinelIntoView()

    await settle(() => renderedChatCount(view) === 25, 'every session renders')
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(requests).toHaveLength(2)
  })
})
