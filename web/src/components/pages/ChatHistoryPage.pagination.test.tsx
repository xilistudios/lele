import '../../test/setup'
import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { createApiClient } from '../../lib/api'
import type { ChatSession } from '../../lib/types'
import { CHAT_PAGE_SIZE, ChatHistoryPage } from './ChatHistoryPage'

// ---------------------------------------------------------------------------
// Contract: the chat list requests 50 chats per page by default.
//
// The backend (parsePagination, pkg/channels/rest_system.go) already defaults
// to 50 and caps a page at 200, but the WebUI used to send limit=200 explicitly
// — the server maximum — so opening the chat history with a large session count
// pulled everything at once. These tests pin the request page size so a
// regression back to 200 fails loudly.
// ---------------------------------------------------------------------------

const originalFetch = globalThis.fetch

const TOTAL_SESSIONS = 120

const ALL_SESSIONS: ChatSession[] = Array.from({ length: TOTAL_SESSIONS }, (_, i) => ({
  key: `native:client-1:s${String(i).padStart(3, '0')}`,
  name: `Chat ${i}`,
  kind: 'chat' as const,
  created: '2026-01-01T00:00:00.000Z',
  updated: new Date(2026, 0, 1, 0, 0, i).toISOString(),
}))

type PageRequest = { offset: number; limit: number }

function installSessionServer(requests: PageRequest[]) {
  globalThis.fetch = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    // Only the paginated list endpoint is served; anything else 404s so an
    // unexpected call fails fast instead of being retried into the next test.
    if (!url.split('?')[0].endsWith('/api/v1/chat/sessions')) {
      return new Response(JSON.stringify({ error: `unexpected fetch: ${url}` }), { status: 404 })
    }
    const params = new URL(url).searchParams
    const offset = Number(params.get('offset') ?? '0')
    const limit = Number(params.get('limit') ?? '50')
    requests.push({ offset, limit })
    const page = ALL_SESSIONS.slice(offset, offset + limit)
    return new Response(
      JSON.stringify({
        sessions: page,
        total: TOTAL_SESSIONS,
        has_more: offset + limit < TOTAL_SESSIONS,
      }),
      { status: 200, headers: { 'Content-Type': 'application/json' } },
    )
  }) as unknown as typeof fetch
}

const apiClient = createApiClient('http://127.0.0.1:18793')
apiClient.setToken('token', 'refresh')

const authValue = {
  api: apiClient,
  apiUrl: 'http://127.0.0.1:18793',
  session: { token: 'token', client_id: 'client-1' },
} as unknown as AuthContextValue

// ChatHistoryPage renders <Sidebar> and reads the cold app state; the fields it
// never touches are cast away so the fixture stays focused on pagination.
const logicValue = {
  sessions: [],
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

function renderPage() {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue },
        createElement(MemoryRouter, { initialEntries: ['/chats'] }, createElement(ChatHistoryPage)),
      ),
    ),
  )
}

/** The "Load more (N of M)" button, or undefined while there is nothing left. */
function loadMoreButton(container: HTMLElement): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find((b) =>
    (b.textContent ?? '').includes('Cargar'),
  )
}

/** How many chat rows are currently rendered. */
function renderedChatCount(container: HTMLElement): number {
  const main = container.querySelector('main')
  return ALL_SESSIONS.filter((s) => (main?.textContent ?? '').includes(s.name ?? '')).length
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
})

describe('ChatHistoryPage pagination', () => {
  test('default page size is 50 chats', () => {
    expect(CHAT_PAGE_SIZE).toBe(50)
  })

  test('first load requests a single page of 50 sessions', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)

    // Mount the page; the assertion is about the outgoing request, not the DOM.
    renderPage()

    await waitFor(() => expect(requests.length).toBeGreaterThan(0))
    expect(requests[0]).toEqual({ offset: 0, limit: CHAT_PAGE_SIZE })
    // Nothing else was fetched on mount — no eager second page.
    expect(requests.length).toBe(1)
  })

  test('only the first 50 chats are rendered while more remain on the server', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)

    const view = renderPage()

    await waitFor(() => expect(loadMoreButton(view.container)).toBeTruthy())
    expect(renderedChatCount(view.container)).toBe(50)
  })

  test('"Load more" fetches the next page of 50 and appends it', async () => {
    const requests: PageRequest[] = []
    installSessionServer(requests)

    const view = renderPage()
    await waitFor(() => expect(loadMoreButton(view.container)).toBeTruthy())

    const button = loadMoreButton(view.container) as HTMLButtonElement
    await act(async () => {
      fireEvent.click(button)
    })

    await waitFor(() => expect(renderedChatCount(view.container)).toBe(100))
    expect(requests).toContainEqual({ offset: 50, limit: CHAT_PAGE_SIZE })

    // Last page is partial and clears has_more, so the button disappears.
    const lastButton = loadMoreButton(view.container) as HTMLButtonElement
    await act(async () => {
      fireEvent.click(lastButton)
    })
    await waitFor(() => expect(renderedChatCount(view.container)).toBe(TOTAL_SESSIONS))
    expect(requests).toContainEqual({ offset: 100, limit: CHAT_PAGE_SIZE })
    await waitFor(() => expect(loadMoreButton(view.container)).toBeUndefined())
  })
})
