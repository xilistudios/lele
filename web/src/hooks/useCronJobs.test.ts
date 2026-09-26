/**
 * useCronJobs tests — polling cadence and visibility gating.
 *
 * `GET /api/v1/cron?include_disabled=true` used to be requested every 5 s from
 * every open tab, hidden or not. These tests pin the new contract:
 *   1. visible: one poll per 5 s window (unchanged),
 *   2. hidden: no interval, no request,
 *   3. becoming visible: exactly one immediate refresh, then the same cadence.
 *
 * Plain `setInterval` (no React Query), so fake timers fully control the clock
 * and `waitFor` is unusable — effects/promises are flushed by hand.
 */

import { afterEach, beforeEach, describe, expect, jest, mock, test } from 'bun:test'
import { act, cleanup, renderHook } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { AuthProvider } from '../contexts/AuthContext'
import { useCronJobs } from './useCronJobs'

const originalFetch = globalThis.fetch

const CRON_JOB = {
  id: 'job-1',
  name: 'nightly',
  schedule: '0 3 * * *',
  enabled: true,
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('lele.session', JSON.stringify({ token: 'token', refresh_token: 'refresh' }))
})

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
  localStorage.clear()
  jest.useRealTimers()
  setVisibility('visible')
})

/** jsdom never fires `visibilitychange`; `visibilityState` is a prototype
 *  getter, so both are simulated the way a browser does (see
 *  `services/ws/client.test.ts`). */
function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

/** Number of cron-list requests the mock saw. */
function cronFetchCalls(fetchMock: ReturnType<typeof mock>): number {
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes('/api/v1/cron')).length
}

/** Fetch mock answering the cron list (and 404 for anything else). */
function cronFetchMock() {
  const fetchMock = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.includes('/api/v1/cron')) {
      return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
    }
    return new Response(JSON.stringify({ jobs: [CRON_JOB], status: { running: true } }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  globalThis.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 10; i += 1) await Promise.resolve()
  })
}

function wrapper({ children }: { children: ReactNode }) {
  return createElement(
    AuthProvider,
    { defaultApiUrl: 'http://127.0.0.1:18793', children },
    children,
  )
}

describe('useCronJobs polling', () => {
  test('polls once per 5 s window while the page is visible', async () => {
    jest.useFakeTimers()
    const fetchMock = cronFetchMock()

    const { result } = renderHook(() => useCronJobs(), { wrapper })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(1)
    expect(result.current.jobs.length).toBe(1)

    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(2)

    // 4999 ms later the next window has not started yet: no extra poll.
    act(() => {
      jest.advanceTimersByTime(4999)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(2)

    act(() => {
      jest.advanceTimersByTime(1)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(3)
  })

  test('does not poll while hidden and refreshes once when shown again', async () => {
    jest.useFakeTimers()
    const fetchMock = cronFetchMock()

    renderHook(() => useCronJobs(), { wrapper })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(2)

    act(() => setVisibility('hidden'))
    await flushEffects()

    // A minute hidden: 12 ticks would have been ~12 requests per tab.
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(2)

    // Shown again: one immediate refresh, no wait for the next tick.
    act(() => setVisibility('visible'))
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(3)

    // ...and the 5 s cadence resumes.
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(4)
  })

  test('a tab mounted while hidden loads once and then stays silent', async () => {
    jest.useFakeTimers()
    const fetchMock = cronFetchMock()

    setVisibility('hidden')
    renderHook(() => useCronJobs(), { wrapper })

    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(30_000)
    })
    await flushEffects()
    expect(cronFetchCalls(fetchMock)).toBe(1)
  })
})
