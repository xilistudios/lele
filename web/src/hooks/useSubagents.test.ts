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
  cleanup()
  globalThis.fetch = originalFetch
  localStorage.clear()
  jest.useRealTimers()
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

async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 10; i += 1) await Promise.resolve()
  })
}

function subagentFetchCalls(fetchMock: ReturnType<typeof mock>, sessionKey = 'session-1'): number {
  const path = `/api/v1/chat/sessions/${sessionKey}/subagents`
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes(path)).length
}

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

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

    await flushEffects()
    expect(result.current.subagents.length).toBe(1)

    const callsAfterMount = fetchMock.mock.calls.length

    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)

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
    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)
  })
})

const FLOOD_WINDOW_MS = 5000

describe('poll interval guard', () => {
  test('pollIntervalMs = 0 means "do not poll": one fetch on mount, none after 5 s', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 0), { wrapper })

    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

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

    expect(subagentFetchCalls(fetchMock)).toBe(1)
  })

  test('pollIntervalMs = 50 is raised to MIN_POLL_MS: ~1 request per second, not ~250', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', 50), { wrapper })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    for (let second = 1; second <= 3; second += 1) {
      act(() => {
        jest.advanceTimersByTime(MIN_POLL_MS)
      })
      await flushEffects()
      expect(subagentFetchCalls(fetchMock)).toBe(1 + second)
    }

    expect(subagentFetchCalls(fetchMock)).toBeLessThan(10)
  })

  test('pollIntervalMs = NaN falls back to MIN_POLL_MS: ~1 request per second, not ~1250', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    renderHook(() => useSubagents('session-1', Number.NaN), { wrapper })

    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

    expect(subagentFetchCalls(fetchMock)).toBe(1 + FLOOD_WINDOW_MS / MIN_POLL_MS)

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

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

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

    act(() => {
      jest.advanceTimersByTime(4999)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

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

    await act(async () => {
      notifySubagentsChanged()
      await Promise.resolve()
    })
    await flushEffects()

    expect(subagentFetchCalls(fetchMock)).toBeGreaterThan(callsAfterMount)
  })
})
describe('visibility gating', () => {
  test('hidden from mount: one mount fetch, then nothing until shown again', async () => {
    jest.useFakeTimers()
    const fetchMock = runningSubagentFetchMock()

    setVisibility('hidden')
    renderHook(() => useSubagents('session-1', 5000), { wrapper })

    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(30_000)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(1)

    act(() => setVisibility('visible'))
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)

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

    act(() => {
      jest.advanceTimersByTime(5000)
    })
    await flushEffects()
    expect(subagentFetchCalls(fetchMock)).toBe(2)

    act(() => setVisibility('hidden'))
    await flushEffects()
    const callsWhenHidden = subagentFetchCalls(fetchMock)

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

    await act(async () => {
      notifySubagentsChanged()
      await Promise.resolve()
    })
    await flushEffects()

    expect(subagentFetchCalls(fetchMock)).toBe(callsAfterMount + 1)
  })
})
