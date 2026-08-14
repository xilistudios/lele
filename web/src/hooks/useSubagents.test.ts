import { afterEach, beforeEach, describe, expect, mock, test } from 'bun:test'
import { act, renderHook, waitFor } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { AuthProvider } from '../contexts/AuthContext'
import type { SubagentTaskInfo } from '../lib/types'
import { useSubagents } from './useSubagents'

const originalFetch = globalThis.fetch

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('lele.session', JSON.stringify({ token: 'token', refresh_token: 'refresh' }))
})

afterEach(() => {
  globalThis.fetch = originalFetch
  localStorage.clear()
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

    const { result } = renderHook(() => useSubagents('session-1', 50), { wrapper })

    // Initial fetch on mount
    await waitFor(() => {
      expect(result.current.subagents.length).toBe(1)
    })

    const callsAfterMount = fetchMock.mock.calls.length

    // Wait for the 50ms polling interval to fire another request.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200))
    })

    expect(fetchMock.mock.calls.length).toBeGreaterThan(callsAfterMount)

    // Now mark the subagent completed; the next poll returns it, and polling stops.
    running = false
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200))
    })
    expect(result.current.subagents[0].status).toBe('completed')

    const callsAtComplete = fetchMock.mock.calls.length
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200))
    })
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

    const { result } = renderHook(() => useSubagents('session-1', 50), { wrapper })

    await waitFor(() => {
      expect(result.current.subagents.length).toBe(1)
    })

    const callsAfterMount = fetchMock.mock.calls.length
    await act(async () => {
      await new Promise((r) => setTimeout(r, 200))
    })
    expect(fetchMock.mock.calls.length).toBe(callsAfterMount)
  })
})
