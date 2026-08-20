import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, renderHook, waitFor } from '@testing-library/react'
import { createApiClient } from '../lib/api'
import type { ChatSession } from '../lib/types'
import { useChatSessions } from './useChatSessions'

const originalFetch = globalThis.fetch

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  globalThis.fetch = originalFetch
  localStorage.clear()
})

function makeSession(key: string, updated: string): ChatSession {
  return {
    key,
    created: '2026-01-01T00:00:00.000Z',
    updated,
  }
}

function mockSessionsResponse(sessions: ChatSession[], total: number) {
  return {
    sessions,
    total,
    has_more: sessions.length > 0 && sessions.length < total,
  }
}

describe('useChatSessions', () => {
  test('refreshSessions loads all pages when the backend paginates', async () => {
    // Backend paginates at 200; create 250 sessions so there are 2 pages.
    const allSessions = Array.from({ length: 250 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )

    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions?')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      const parsed = new URL(url)
      const offset = Number(parsed.searchParams.get('offset') ?? '0')
      const limit = Number(parsed.searchParams.get('limit') ?? '50')
      const page = allSessions.slice(offset, offset + limit)
      return new Response(JSON.stringify(mockSessionsResponse(page, allSessions.length)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    await act(async () => {
      await result.current.refreshSessions()
    })

    // Verify we requested a second page with offset=200 (pagination works)
    const sessionCalls = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes('/api/v1/chat/sessions?'),
    )
    expect(sessionCalls.length).toBeGreaterThanOrEqual(2)
    const offsets = sessionCalls.map(([input]) => new URL(String(input)).searchParams.get('offset'))
    expect(offsets).toContain('0')
    expect(offsets).toContain('200')

    // State holds all pages
    expect(result.current.sessions.length).toBe(allSessions.length)
  })

  test('refreshSessions requests include_system=true so all persisted chats appear', async () => {
    // Backend tracks only a handful of client session keys (~30), but the
    // session manager persists hundreds. include_system=true is what merges
    // the persisted sessions into the response; without it the sidebar would
    // silently drop most chats.
    const allSessions = Array.from({ length: 300 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )

    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions?')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      const parsed = new URL(url)
      const offset = Number(parsed.searchParams.get('offset') ?? '0')
      const limit = Number(parsed.searchParams.get('limit') ?? '50')
      const page = allSessions.slice(offset, offset + limit)
      return new Response(JSON.stringify(mockSessionsResponse(page, allSessions.length)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    await act(async () => {
      await result.current.refreshSessions()
    })

    const sessionCalls = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes('/api/v1/chat/sessions?'),
    )
    expect(sessionCalls.length).toBeGreaterThanOrEqual(1)
    // Every page request must include include_system=true
    for (const [input] of sessionCalls) {
      const url = new URL(String(input))
      expect(url.searchParams.get('include_system')).toBe('true')
    }

    // All 300 sessions present in state (pagination across pages works)
    expect(result.current.sessions.length).toBe(allSessions.length)
  })

  test('refreshSessions keeps current session when it is not on the backend yet', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions?')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      return new Response(JSON.stringify(mockSessionsResponse([], 0)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })

    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    act(() => {
      result.current.selectSession('local-uuid')
    })

    await act(async () => {
      await result.current.refreshSessions()
    })

    await waitFor(() => {
      expect(result.current.sessions.some((s) => s.key === 'local-uuid')).toBe(true)
    })
    expect(result.current.currentSessionKey).toBe('local-uuid')
  })
})
