import '../../test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { createApiClient } from '../../lib/api'
import type { ChatSession } from '../../lib/types'
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

function renderedChatCount(container: HTMLElement): number {
  const main = container.querySelector('main')
  const text = main?.textContent ?? ''
  return ALL_SESSIONS.filter((s) => text.includes(s.name ?? '')).length
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

    await waitFor(() => expect(renderedChatCount(view.container)).toBe(CHAT_PAGE_SIZE))
    expect(requests).toEqual([{ offset: 0, limit: CHAT_PAGE_SIZE }])
    expect(renderedChatCount(view.container)).toBeLessThan(ALL_SESSIONS.length)
  })

  test('scrolling the sentinel reveals the next page', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)
    installIntersectionObserver()

    const view = renderPage(ALL_SESSIONS)
    await waitFor(() => expect(renderedChatCount(view.container)).toBe(CHAT_PAGE_SIZE))

    scrollSentinelIntoView()

    await waitFor(() => expect(renderedChatCount(view.container)).toBe(CHAT_PAGE_SIZE * 2))
  })

  test('loads the next server page when the revealed window runs out of sessions', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)
    installIntersectionObserver()

    const view = renderPage([])

    await waitFor(() => expect(requests).toContainEqual({ offset: 0, limit: CHAT_PAGE_SIZE }))
    await waitFor(() =>
      expect(requests).toContainEqual({ offset: CHAT_PAGE_SIZE, limit: CHAT_PAGE_SIZE }),
    )

    await waitFor(() => expect(hasLiveSentinel()).toBe(true))
    scrollSentinelIntoView()

    await waitFor(() => expect(renderedChatCount(view.container)).toBe(CHAT_PAGE_SIZE * 2))
  })

  test('stops requesting pages once every session is loaded', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests, ALL_SESSIONS.slice(0, 25))
    installIntersectionObserver()

    const view = renderPage([])

    await waitFor(() => expect(requests).toHaveLength(2))
    expect(requests).toContainEqual({ offset: 0, limit: CHAT_PAGE_SIZE })
    expect(requests).toContainEqual({ offset: CHAT_PAGE_SIZE, limit: CHAT_PAGE_SIZE })

    await waitFor(() => expect(hasLiveSentinel()).toBe(true))
    scrollSentinelIntoView()

    await waitFor(() => expect(renderedChatCount(view.container)).toBe(25))
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(requests).toHaveLength(2)
  })
})
