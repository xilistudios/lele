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
    // Offset-aware: `has_more` must say whether ANY session is left after this
    // page, otherwise the client would ask for one extra (empty) page.
    has_more: offset + sessions.length < total,
  }
}

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

/**
 * Installs a fetch mock serving the paginated metadata endpoint: it slices the
 * given sessions by `offset`/`limit` and reports `has_more` while pages are
 * left. Returns the mock so tests can inspect the requested URLs.
 */
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

/** Meta-endpoint URLs (with offsets) requested through the mock, in order. */
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
    // Backend paginates at 200; create 250 sessions so there are 2 pages.
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

    // Verify we requested a second page with offset=200 (pagination works).
    // The lightweight meta endpoint is used; fall back to any sessions call.
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
    // The meta endpoint must be the one used (not the heavy /sessions).
    expect(allSessionCalls.length).toBe(0)

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
    // Every page request must include include_system=true
    for (const [input] of sessionCalls) {
      const url = new URL(String(input))
      expect(url.searchParams.get('include_system')).toBe('true')
    }

    // All 300 sessions present in state (pagination across pages works)
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
    // Regression for the request storm: with 250 sessions (2 pages, offset 0
    // and offset 200) a burst of triggers produced one full pagination walk
    // each, which is why `?include_system=true&offset=200&limit=200` showed up
    // 5+ times in the network panel.
    const allSessions = Array.from({ length: 250 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )
    const fetchMock = installPaginatedMetaFetch(allSessions)

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result } = renderHook(() => useChatSessions(api, 'token', 'client-1'))

    // No await between the calls: exactly the burst shape of the bug
    // (bootstrap + WS connected + debounced session events firing together).
    let promises: Promise<string | null>[] = []
    let keys: (string | null)[] = []
    act(() => {
      promises = Array.from({ length: 5 }, () => result.current.refreshSessions())
    })
    await act(async () => {
      keys = await Promise.all(promises)
    })

    // ONE pass per offset. Threshold note: the coordinator's hard ceiling is
    // 2 requests per offset (one leading pass + at most one trailing pass), and
    // that ceiling is only reached when a concurrent call carries a DIFFERENT
    // task (e.g. after token/clientId changed). Here the 5 calls are
    // synchronous and share the same refresh task (stable useCallback identity
    // from a single render), so the coordinator joins all of them into one pass
    // and schedules NO trailing pass — re-running an identical task would be
    // pure duplication. Hence exactly 1.
    const offsets = metaOffsets(fetchMock)
    expect(offsets.filter((offset) => offset === '0').length).toBe(1)
    expect(offsets.filter((offset) => offset === '200').length).toBe(1)
    expect(offsets.length).toBe(2)

    // Every awaiter resolved with the same key, and the state holds all pages.
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
    // Page 1 hangs until the test releases it, so the pass is in flight while
    // the reset happens.
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

    // Logout / reset while the pass is still fetching its first page.
    act(() => {
      result.current.reset()
    })
    expect(result.current.sessions).toEqual([])
    expect(result.current.currentSessionKey).toBeNull()

    // A FULL page arrives after the reset (has_more=true): the invalidated pass
    // must neither request page 2 nor write the sessions back into the state.
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
    // The first page hangs until this test releases it by hand, so the pass is
    // still fetching when the hook goes away.
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

    // The tree goes away WITHOUT `reset()` (the forced-logout path, where an
    // unrecoverable 401 unmounts the app), so the generation guard is untouched
    // and only the mount guard can stop the write.
    unmount()

    pageOne.resolve(jsonResponse(mockSessionsResponse(lateSessions, lateSessions.length, 0)))
    let resolvedKey: string | null = 'unset'
    await act(async () => {
      resolvedKey = await refresh
    })

    // Dropped entirely: the pass resolves without applying its result, so the
    // stale session key is never persisted (its write is the observable side
    // effect of the `setSessions`/`persistCurrentSessionKey` pair) and the next
    // mount cannot reopen the previous user's chat.
    expect(resolvedKey).toBeNull()
    expect(localStorage.getItem('lele.currentSessionKey')).toBeNull()
  })

  test('a backend that ignores offset cannot spin the walk: it stops on the repeated page', async () => {
    // Same page on every request (`offset` ignored) with `has_more: true`
    // forever: the exact contract violation that used to leave the pagination
    // loop spinning — and, with the coordinator allowing only ONE pass in
    // flight, one spin froze the session list for the whole life of the tab.
    const repeatedPage = Array.from({ length: 200 }, (_, i) =>
      makeSession(`session-${i}`, new Date(2026, 0, 1, 0, 0, i).toISOString()),
    )
    // Safety valve of the TEST, not of production: after this many requests the
    // mock reports `has_more: false`, so a regression fails on the assertions
    // below instead of hanging the runner.
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

    // TWO requests: the page, plus the one that proved the offset is ignored.
    // Without the repeated-page guard the walk would run all the way to
    // `requestCap`.
    expect(requests).toBe(2)
    // Only the first page was accumulated; the duplicate was never kept.
    expect(result.current.sessions.length).toBe(200)
  })

  test('the walk is capped at 50 pages even when the backend keeps paginating', async () => {
    // 60 full pages, all of them `has_more: true` except the last: a backend
    // (or proxy) that paginates far more than the cap. 50 pages x 200 sessions
    // = 10 000 chats, far above real usage, so the cap itself is the only thing
    // that ends the walk here.
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

    // Without the page cap the walk would request page 61 (offset 12 000)
    // before the backend finally answered `has_more: false`.
    expect(requests).toBe(50)
    expect(result.current.sessions.length).toBe(50 * pageSize)
  })

  test('refreshSessions keeps its identity across renders (the coalescing ceiling depends on it)', () => {
    // WHY this matters: the coordinator collapses a burst of refresh triggers by
    // comparing the TASK IDENTITY — the same function reference neither
    // replaces the pending coalescing nor schedules a trailing pass, so a burst
    // costs ONE paginated pass. If any dependency of this callback became
    // unstable (`api`, `token`, `clientId`, the pass itself, or a coordinator
    // recreated per render), every render would hand the coordinator a
    // "different" task and each burst would cost 2 passes instead of 1 — a
    // silent doubling of the very request storm this work removes, with every
    // other test in this file still green (they exercise one render, not a
    // burst that spans a re-render).
    installPaginatedMetaFetch([])

    const api = createApiClient('http://127.0.0.1:18793')
    api.setToken('token', 'refresh')

    const { result, rerender } = renderHook(() => useChatSessions(api, 'token', 'client-1'))
    const firstRender = result.current.refreshSessions

    // Same props → same credential, same api client, same pass: the identity
    // must survive the re-render.
    rerender()
    expect(result.current.refreshSessions).toBe(firstRender)

    rerender()
    expect(result.current.refreshSessions).toBe(firstRender)
  })
})
