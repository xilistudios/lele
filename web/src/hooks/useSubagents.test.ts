import { afterEach, beforeEach, describe, expect, jest, mock, test } from 'bun:test'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { AuthProvider } from '../contexts/AuthContext'
import type { SubagentTaskInfo } from '../lib/types'
import { MIN_POLL_MS, NO_POLL_MS, notifySubagentsChanged, useSubagents } from './useSubagents'

const originalFetch = globalThis.fetch

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('lele.session', JSON.stringify({ token: 'token', refresh_token: 'refresh' }))
})

afterEach(() => {
  // Cleanup FIRST so useEffect cleanups (clearInterval, listener removal) run
  // while the mock is still active, preventing leaked async fetches from
  // contaminating later test files.
  cleanup()
  globalThis.fetch = originalFetch
  localStorage.clear()
  // Always leave the file on real timers, even if a fake-timer test threw.
  jest.useRealTimers()
  // And always visible: a hidden state left behind would silently disable the
  // pollers of the following tests.
  setVisibility('visible')
})

function subagent(overrides: Partial<SubagentTaskInfo> = {}): SubagentTaskInfo {
  return {
    task_id: 'subagent-1',
    session_key: 'session-1:subagent-1',
    label: 'worker',
    agent_id: 'main',
    status: 'running',
    summary: '',
    created: 0,
    updated: 0,
    iterations: 0,
    ...overrides,
  }
}

function mockSubagentsResponse(subagents: SubagentTaskInfo[]) {
  return { session_key: 'session-1', subagents }
}

/**
 * Let effects, promises and re-renders settle without real waits.
 *
 * The polling tests run on fake timers (`jest.useFakeTimers()`), where
 * `waitFor` cannot observe an interval tick: its own timeout never elapses.
 * Yielding a handful of microtasks inside `act` flushes the mocked fetch
 * chain plus the resulting state updates instead.
 */
async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 10; i += 1) await Promise.resolve()
  })
}

/** Number of subagent-list requests the mock saw for `sessionKey`. */
function subagentFetchCalls(fetchMock: ReturnType<typeof mock>, sessionKey = 'session-1'): number {
  const path = `/api/v1/chat/sessions/${sessionKey}/subagents`
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes(path)).length
}

/**
 * jsdom never fires `visibilitychange` and `document.visibilityState` is a
 * getter on `Document.prototype`, so both are simulated the way the browser
 * does (same approach as `services/ws/client.test.ts`).
 */
function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

/** Fetch mock that always answers the subagent list with one running task. */
function runningSubagentFetchMock() {
  const fetchMock = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.includes('/subagents')) {
      return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
    }
    return new Response(JSON.stringify(mockSubagentsResponse([subagent({ status: 'running' })])), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  globalThis.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

function wrapper({ children }: { children: ReactNode }) {
  return createElement(
    AuthProvider,
    { defaultApiUrl: 'http://127.0.0.1:18793', children },
    children,
  )
}

describe('useSubagents', () => {
  test('polls every 5s while a subagent is running', async () => {
    let running = true
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/subagents')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      return new Response(
        JSON.stringify(
          mockSubagentsResponse([subagent({ status: running ? 'running' : 'completed' })]),
        ),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    jest.useFakeTimers()
    const { result } = renderHook(() => useSubagents('session-1', 5000), { wrapper })

    // Initial fetch on mount
    await flushEffects()
    expect(result.current.subagents.length).toBe(1)

    const callsAfterMount = fetchMock.mock.calls.length

    // Advance one 5 s window: the polling interval must fire a request.
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)

    // Now mark the subagent completed; the next poll returns it, and polling stops.
    running = false
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(result.current.subagents[0].status).toBe('completed')

    const callsAtComplete = fetchMock.mock.calls.length
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    // No more polling once nothing is running.
    expect(fetchMock.mock.calls.length).toBe(callsAtComplete)
  })

  test('does not poll when there are no running subagents', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/subagents')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      return new Response(
        JSON.stringify(mockSubagentsResponse([subagent({ status: 'completed' })])),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    jest.useFakeTimers()
    const { result } = renderHook(() => useSubagents('session-1', 5000), { wrapper })

    await flushEffects()
    expect(result.current.subagents.length).toBe(1)

    const callsAfterMount = fetchMock.mock.calls.length
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(fetchMock.mock.calls.length).toBe(callsAfterMount)
  })

  test('polls while a subagent is pending (live work, like running)', async () => {
    let pending = true
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/subagents')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      return new Response(
        JSON.stringify(
          mockSubagentsResponse([subagent({ status: pending ? 'pending' : 'completed' })]),
        ),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    jest.useFakeTimers()
    const { result } = renderHook(() => useSubagents('session-1', 5000), { wrapper })

    await flushEffects()
    expect(result.current.subagents.length).toBe(1)

    const callsAfterMount = fetchMock.mock.calls.length
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    // Pending tasks are live: polling must continue.
    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)

    pending = false
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(result.current.subagents[0].status).toBe('completed')

    const callsAtComplete = fetchMock.mock.calls.length
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(fetchMock.mock.calls.length).toBe(callsAtComplete)
  })

  test('refetches immediately when notifySubagentsChanged fires (spawn without refresh)', async () => {
    let list: SubagentTaskInfo[] = []
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/subagents')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      return new Response(JSON.stringify(mockSubagentsResponse(list)), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const { result } = renderHook(() => useSubagents('session-1', 60_000), { wrapper })

    await waitFor(() => {
      expect(result.current.subagents.length).toBe(0)
    })
    const callsAfterMount = fetchMock.mock.calls.length

    // Simulate a spawn landing on the backend, then the WS event notifying us.
    list = [subagent({ status: 'running' })]
    await act(async () => {
      notifySubagentsChanged()
      await Promise.resolve()
    })

    await waitFor(() => {
      expect(result.current.subagents.length).toBe(1)
      expect(result.current.subagents[0].status).toBe('running')
    })
    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)
  })

  test('polls while the parent turn is active even with an empty list', async () => {
    const fetchMock = mock(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (!url.includes('/api/v1/chat/sessions/session-1/subagents')) {
        return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
      }
      return new Response(JSON.stringify(mockSubagentsResponse([])), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    jest.useFakeTimers()
    const { result } = renderHook(() => useSubagents('session-1', 5000, true), { wrapper })

    await flushEffects()
    expect(result.current.loading).toBe(false)

    const callsAfterMount = fetchMock.mock.calls.length
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    // Empty list + active turn: keep polling so a mid-turn spawn is picked up.
    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)
  })
})

/**
 * Regression guard for the WebUI request storm.
 *
 * `ChatHeader` used to call `useSubagents(parentSessionKey, 0, false)` meaning
 * "never poll", but the hook handed the 0 straight to `setInterval`. Browsers
 * clamp that to ~4 ms, so while any subagent was running the client issued
 * ~250 GET /api/v1/chat/sessions/{key}/subagents per second — and each of those
 * takes the server's SessionManager write lock plus a full SQLite load, which
 * stalls streaming for every session.
 *
 * Fake timers make the request count over a window exact instead of timing
 * dependent.
 */
const FLOOD_WINDOW_MS = 5000

describe('poll interval guard', () => {
  test('pollIntervalMs = 0 means "do not poll": one fetch on mount, none after 5 s', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 0), { wrapper })

    // Mount fetch still happens: interval 0 disables polling, not loading.
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

    // A 0 ms interval clamped to 4 ms would have produced ~1250 requests here.
    expect(subagentFetchCalls(fetchMock)).toBe(1)
  })

  test('a negative interval also means "do not poll": one fetch on mount, none after 5 s', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', -FLOOD_WINDOW_MS), { wrapper })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

    // Same contract as 0: a non-positive interval disables polling, it is never
    // sanitised into a 1 s poll.
    expect(subagentFetchCalls(fetchMock)).toBe(1)
  })

  test('pollIntervalMs = 50 is raised to MIN_POLL_MS: ~1 request per second, not ~250', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 50), { wrapper })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // Three 1 s windows: with the floor applied exactly one poll per window.
    for (let second = 1; second <= 3; second += 1) {
      act(() => {
        jest.advanceTimersByTime(MIN_POLL_MS)
      })
      await flushEffects()
      expect(subagentFetchCalls(fetchMock)).toBe(1 + second)
    }

    // A 50 ms interval (~60 requests) or the browser-clamped 4 ms variant
    // (~750 requests) would blow past this in the same fake-time window.
    expect(subagentFetchCalls(fetchMock)).toBeLessThan(10)
  })

  test('pollIntervalMs = NaN falls back to MIN_POLL_MS: ~1 request per second, not ~1250', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', Number.NaN), { wrapper })

    // The mount fetch is independent of polling (same as the 0 case).
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

    // `Math.max(NaN, MIN_POLL_MS)` is NaN and `NaN <= 0` is false, so a bare
    // Math.max would hand the NaN to `setInterval`, which browsers clamp to
    // ~4 ms — ~1250 requests in this window. The floor must be applied instead.
    expect(subagentFetchCalls(fetchMock)).toBe(1 + FLOOD_WINDOW_MS / MIN_POLL_MS)

    // A fast poll of any kind would exceed the exact per-second count above.
    expect(subagentFetchCalls(fetchMock)).toBeLessThan(10)
  })

  test('changing the interval restarts polling at the new rate', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    const { rerender } = renderHook(({ intervalMs }) => useSubagents('session-1', intervalMs), {
      wrapper,
      initialProps: { intervalMs: 60_000 },
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // A 60 s interval cannot fire inside a 5 s window.
    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // The interval itself is the effect dependency: switching to the floor must
    // restart the timer at 1 s instead of keeping the stale 60 s schedule.
    rerender({ intervalMs: MIN_POLL_MS })
    await flushEffects()
    act(() => {
      jest.advanceTimersByTime(MIN_POLL_MS)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)
  })

  test('pollIntervalMs = 5000 is unchanged: exactly one poll per 5 s window', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 5000), { wrapper })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // 4999 ms: the 5 s interval has not fired yet.
    act(() => {
      jest.advanceTimersByTime(4999)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // One more millisecond: still exactly one poll per window (no floor drift).
    act(() => {
      jest.advanceTimersByTime(1)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)
  })

  test('a non-polling instance (interval 0) still refreshes on notifySubagentsChanged', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 0), { wrapper })
    await flushEffects()
    const callsAfterMount = subagentFetchCalls(fetchMock)
    expect(callsAfterMount).toBe(1)

    // The WS fan-out is the refresh mechanism for non-polling instances.
    await act(async () => {
      notifySubagentsChanged()
      await Promise.resolve()
    })
    await flushEffects()

    expect(subagentFetchCalls(fetchMock)).toBeGreaterThan(callsAfterMount)
  })
})
/**
 * Visibility gating.
 *
 * A background tab must not keep polling `GET /api/v1/chat/sessions/{key}/subagents`
 * (SessionManager write lock + full SQLite load per request) — six idle lele
 * tabs used to generate a continuous stream of those reads. The contract these
 * tests pin:
 *   1. while hidden: zero interval requests,
 *   2. on becoming visible: exactly ONE immediate refresh (no waiting for the
 *      next tick, no staleness),
 *   3. afterwards: the same cadence as before (5000 ms here),
 *   4. the `NO_POLL_MS` contract is untouched: a non-polling instance does not
 *      gain requests from a visibility round-trip.
 */
describe('visibility gating', () => {
  test('hidden from mount: one mount fetch, then nothing until shown again', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    setVisibility('hidden')
    renderHook(() => useSubagents('session-1', 5000), { wrapper })

    // Mounting is not the interval: a hidden tab still loads its list once.
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // Six 5 s polls worth of time while the user is looking elsewhere.
    act(() => {
      jest.advanceTimersByTime(30_000)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // Becoming visible refreshes ONCE immediately...
    act(() => setVisibility('visible'))
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)

    // ...and the normal 5 s cadence resumes from there (no drift, no backlog).
    act(() => {
      jest.advanceTimersByTime(4999)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)

    act(() => {
      jest.advanceTimersByTime(1)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(3)
  })

  test('hiding stops a live poller; showing it refreshes once and resumes', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 5000), { wrapper })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    // Polling is live while the page is visible.
    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)

    act(() => setVisibility('hidden'))
    await flushEffects()
    const callsWhenHidden = subagentFetchCalls(fetchMock)

    // A full minute hidden: not a single request (12 ticks would be ~12).
    act(() => {
      jest.advanceTimersByTime(60_000)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(callsWhenHidden)

    act(() => setVisibility('visible'))
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(callsWhenHidden + 1)

    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(callsWhenHidden + 2)
  })

  test('a NO_POLL_MS instance gains no request from a visibility round-trip', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    // ChatHeader-style instance: "never poll", refreshed by mount and by
    // notifySubagentsChanged only. Visibility gating must not turn it into a
    // poller (MIN_POLL_MS/NO_POLL_MS contract).
    renderHook(() => useSubagents('session-1', NO_POLL_MS), { wrapper })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => setVisibility('hidden'))
    await flushEffects()
    act(() => {
      jest.advanceTimersByTime(30_000)
    })
    await flushEffects()
    act(() => setVisibility('visible'))
    await flushEffects()

    expect(subagentFetchCalls(fetchMock)).toBe(1)
  })

  test('the WebSocket fan-out still refreshes while the tab is hidden', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    setVisibility('hidden')
    renderHook(() => useSubagents('session-1', 5000), { wrapper })
    await flushEffects()
    const callsAfterMount = subagentFetchCalls(fetchMock)

    // Gating the *interval* must not gate the push path: a spawn that lands
    // while the tab is hidden is still fetched (one read, event-driven).
    await act(async () => {
      notifySubagentsChanged()
      await Promise.resolve()
    })
    await flushEffects()

    expect(subagentFetchCalls(fetchMock)).toBe(callsAfterMount + 1)
  })
})
