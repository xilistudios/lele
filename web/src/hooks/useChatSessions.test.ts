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

function mockSessionsResponse(sessions: ChatSession[], total: number, offset = 0) {
  return {
    sessions,
    total,
    has_more: offset + sessions.length < total,
  }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function installPaginatedMetaFetch(allSessions: ChatSession[]) {
  const fetchMock = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions/meta?')) {
      return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
    }
    const parsed = new URL(url)
    const offset = Number(parsed.searchParams.get('offset') ?? '0')
    const limit = Number(parsed.searchParams.get('limit') ?? '50')
    const page = allSessions.slice(offset, offset + limit)
    return jsonResponse(mockSessionsResponse(page, allSessions.length, offset))
  })
  globalThis.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

function metaOffsets(fetchMock: ReturnType<typeof installPaginatedMetaFetch>): string[] {
  return fetchMock.mock.calls
    .map(([input]) => String(input))
    .filter((url) => url.includes('/api/v1/chat/sessions/meta?'))
    .map((url) => new URL(url).searchParams.get('offset') ?? '')
}

type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void }

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => {
    resolve = res
  })
  return { promise, resolve }
}

describe('useChatSessions', () => {
  test('refreshSessions loads all pages when the backend paginates', async () => {
    const allSessions = Array.from({ length: 250 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )

    const fetchMock = installPaginatedMetaFetch(allSessions)

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    await act(async () => {
      await result.current.refreshSessions()
    })

    const sessionCalls = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes('/api/v1/chat/sessions/meta?'),
    )
    const allSessionCalls = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes('/api/v1/chat/sessions?'),
    )
    expect(sessionCalls.length).toBeGreaterThanOrEqual(2)
    const offsets = sessionCalls.map(([input]) => new URL(String(input)).searchParams.get('offset'))
    expect(offsets).toContain('0')
    expect(offsets).toContain('200')
    expect(allSessionCalls.length).toBe(0)

    expect(result.current.sessions.length).toBe(allSessions.length)
  })

  test('refreshSessions requests include_system=true so all persisted chats appear', async () => {
    const allSessions = Array.from({ length: 300 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )

    const fetchMock = installPaginatedMetaFetch(allSessions)

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    await act(async () => {
      await result.current.refreshSessions()
    })

    const sessionCalls = fetchMock.mock.calls.filter(([input]) =>
      String(input).includes('/api/v1/chat/sessions/meta?'),
    )
    expect(sessionCalls.length).toBeGreaterThanOrEqual(1)
    for (const [input] of sessionCalls) {
      const url = new URL(String(input))
      expect(url.searchParams.get('include_system')).toBe('true')
    }

    expect(result.current.sessions.length).toBe(allSessions.length)
  })

  test('refreshSessions keeps current session when it is not on the backend yet', async () => {
    installPaginatedMetaFetch([])

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

  test('a burst of concurrent refreshSessions() calls emits a SINGLE paginated pass', async () => {
    const allSessions = Array.from({ length: 250 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )
    const fetchMock = installPaginatedMetaFetch(allSessions)

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    let promises: Promise<string | null>[] = []
    let keys: (string | null)[] = []
    act(() => {
      promises = Array.from({ length: 5 }, () => result.current.refreshSessions())
    })
    await act(async () => {
      keys = await Promise.all(promises)
    })

    const offsets = metaOffsets(fetchMock)
    expect(offsets.filter((offset) => offset === '0').length).toBe(1)
    expect(offsets.filter((offset) => offset === '200').length).toBe(1)
    expect(offsets.length).toBe(2)

    expect(keys.length).toBe(5)
    expect(new Set(keys).size).toBe(1)
    expect(keys[0]).not.toBeNull()
    expect(result.current.sessions.length).toBe(250)
    expect(result.current.currentSessionKey).toBe(keys[0])
    expect(result.current.sessions.some((s) => s.key === keys[0])).toBe(true)
  })

  test('reset() during an in-flight refresh cancels the pass: no page 2, no revived list', async () => {
    const allSessions = Array.from({ length: 250 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )
    const pageOne = deferred<Response>()
    let pageRequests = 0
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions/meta?')) {
        return jsonResponse({ error: 'unexpected' })
      }
      pageRequests += 1
      const offset = Number(new URL(url).searchParams.get('offset') ?? '0')
      if (offset === 0) return pageOne.promise
      return jsonResponse(
        mockSessionsResponse(allSessions.slice(offset, offset + 200), allSessions.length, offset),
      )
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    let refresh: Promise<string | null> = Promise.resolve(null)
    act(() => {
      refresh = result.current.refreshSessions()
    })
    expect(pageRequests).toBe(1)

    act(() => {
      result.current.reset()
    })
    expect(result.current.sessions).toEqual([])
    expect(result.current.currentSessionKey).toBeNull()

    pageOne.resolve(
      jsonResponse(mockSessionsResponse(allSessions.slice(0, 200), allSessions.length, 0)),
    )
    let resolvedKey: string | null = 'unset'
    await act(async () => {
      resolvedKey = await refresh
    })

    expect(pageRequests).toBe(1)
    expect(resolvedKey).toBeNull()
    expect(result.current.sessions).toEqual([])
    expect(result.current.currentSessionKey).toBeNull()
  })

  test('a pass that lands after unmount writes neither state nor the session key', async () => {
    const lateSessions = [makeSession('session-late', '2026-01-01T00:00:00.000Z')]
    const pageOne = deferred<Response>()
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions/meta?')) {
        return jsonResponse({ error: 'unexpected' })
      }
      return pageOne.promise
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result, unmount } = renderHook(() => useChatSessions(api, 'token', 'client-1'))
    expect(localStorage.getItem('lele.currentSessionKey')).toBeNull()

    let refresh: Promise<string | null> = Promise.resolve(null)
    act(() => {
      refresh = result.current.refreshSessions()
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)

    unmount()

    pageOne.resolve(jsonResponse(mockSessionsResponse(lateSessions, lateSessions.length, 0)))
    let resolvedKey: string | null = 'unset'
    await act(async () => {
      resolvedKey = await refresh
    })

    expect(resolvedKey).toBeNull()
    expect(localStorage.getItem('lele.currentSessionKey')).toBeNull()
  })

  test('a backend that ignores offset cannot spin the walk: it stops on the repeated page', async () => {
    const repeatedPage = Array.from({ length: 200 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )
    const requestCap = 5
    let requests = 0
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions/meta?')) {
        return jsonResponse({ error: 'unexpected' })
      }
      requests += 1
      return jsonResponse({
        sessions: repeatedPage,
        total: 4_000,
        has_more: requests < requestCap,
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))
    await act(async () => {
      await result.current.refreshSessions()
    })

    expect(requests).toBe(2)
    expect(result.current.sessions.length).toBe(200)
  })

  test('the walk is capped at 50 pages even when the backend keeps paginating', async () => {
    const pageSize = 200
    const availablePages = 60
    const pages = Array.from({ length: availablePages }, (_, page) =>
      Array.from({ length: pageSize }, (_, i) =>
        makeSession(`session-${page}-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
      ),
    )
    let requests = 0
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.startsWith('http://127.0.0.1:18793/api/v1/chat/sessions/meta?')) {
        return jsonResponse({ error: 'unexpected' })
      }
      requests += 1
      const offset = Number(new URL(url).searchParams.get('offset') ?? '0')
      const page = pages[offset / pageSize]
      return jsonResponse({
        sessions: page ?? [],
        total: availablePages * pageSize,
        has_more: page !== undefined,
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))
    await act(async () => {
      await result.current.refreshSessions()
    })

    expect(requests).toBe(50)
    expect(result.current.sessions.length).toBe(50 * pageSize)
  })

  test('refreshSessions keeps its identity across renders (the coalescing ceiling depends on it)', () => {
    installPaginatedMetaFetch([])

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result, rerender } = renderHook(() => useChatSessions(api, 'token', 'client-1'))
    const firstRender = result.current.refreshSessions

    rerender()
    expect(result.current.refreshSessions).toBe(firstRender)

    rerender()
    expect(result.current.refreshSessions).toBe(firstRender)
  })
})
