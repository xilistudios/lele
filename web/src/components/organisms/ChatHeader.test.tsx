import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeAll, beforeEach, describe, expect, jest, mock, test } from 'bun:test'
import { act, cleanup, render } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthProvider } from '../../contexts/AuthContext'
import type { SubagentTaskInfo } from '../../lib/types'

/**
 * Regression guard for the `/subagents` request storm.
 *
 * `ChatHeader` renders two `useSubagents` instances: the primary one for the
 * current session (5 s poll) and a second one for the *parent* session of a
 * subagent chat, which only needs a one-shot read. That second call used to
 * pass `0` as the interval meaning "never poll", but the hook forwarded the 0
 * to `setInterval`; browsers clamp that to ~4 ms, so while any subagent was
 * running the header alone issued ~250 requests per second against an endpoint
 * that takes the server's write lock. Counting requests per URL proves the
 * parent instance performs exactly one fetch.
 *
 * ChatPageContext is mocked (the technique ChatComposer.test.tsx uses) because
 * its provider can only be built from AppLogicProvider (real socket + app
 * logic). `bun test --isolate` runs each file in its own process, which
 * contains the process-wide module mock.
 */
const mockChatPageCtx = {
  canCancel: false,
  hasConversation: false,
  availableModels: [],
  groupedModels: undefined,
  selectedModel: 'mock-model',
  thinkLevel: 'off',
  currentSession: null,
  parentSession: null,
} as unknown as ReturnType<typeof import('../../contexts/ChatPageContext').useChatPageContext>

let ChatHeader: typeof import('./ChatHeader').ChatHeader

beforeAll(() => {
  mock.module('../../contexts/ChatPageContext', () => ({
    useChatPageContext: () => mockChatPageCtx,
  }))
  ChatHeader = require('./ChatHeader').ChatHeader
})

/** Subagent-chat keys: the child is displayed, its parent is only resolved. */
const PARENT_KEY = 'parent-session'
const CHILD_KEY = 'child-session'

/** Advancing this much would be ~1250 requests for a browser-clamped 4 ms interval. */
const FLOOD_WINDOW_MS = 5000

const appLogicValue = {
  currentAgent: { name: 'main' },
  wsStatus: 'connected',
  currentSessionKey: CHILD_KEY,
  parentSessionKey: PARENT_KEY,
  agents: [],
  chatMode: 'agent',
  isProcessing: false,
  onOpenMobileSidebar: () => undefined,
} as unknown as AppLogicContextValue

function subagent(overrides: Partial<SubagentTaskInfo> = {}): SubagentTaskInfo {
  return {
    task_id: 'subagent-1',
    session_key: `${PARENT_KEY}:subagent-1`,
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

/** Fetch mock: every `/subagents` request answers one running task. */
function subagentFetchMock() {
  const fetchMock = mock(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (!url.includes('/subagents')) {
      return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 })
    }
    return new Response(JSON.stringify({ session_key: 'any', subagents: [subagent()] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  })
  globalThis.fetch = fetchMock as unknown as typeof fetch
  return fetchMock
}

/** Requests the header made to one specific session's subagent endpoint. */
function subagentFetchCalls(fetchMock: ReturnType<typeof mock>, sessionKey: string): number {
  const path = `/api/v1/chat/sessions/${sessionKey}/subagents`
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes(path)).length
}

/** Settle effects, mocked fetch chains and re-renders without real waits. */
async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 10; i += 1) await Promise.resolve()
  })
}

function renderHeader() {
  // `children` is passed both in the props object and as createElement's
  // variadic child (the latter wins). AuthProvider types `children` as
  // required, so the props object needs it to satisfy TypeScript — the same
  // pattern useSubagents.test.ts uses for its wrapper.
  const children: ReactNode = createElement(
    AppLogicContext.Provider,
    { value: appLogicValue },
    createElement(MemoryRouter, { initialEntries: ['/'] }, createElement(ChatHeader)),
  )

  return render(
    createElement(AuthProvider, { defaultApiUrl: 'http://127.0.0.1:18793', children }, children),
  )
}

beforeEach(() => {
  localStorage.clear()
  localStorage.setItem('lele.session', JSON.stringify({ token: 'token', refresh_token: 'refresh' }))
})

afterEach(() => {
  // Cleanup before swapping the mock back so effect cleanups (clearInterval)
  // still run against the mocked fetch.
  cleanup()
  jest.useRealTimers()
  localStorage.clear()
})

describe('ChatHeader — parent subagent list is never polled', () => {
  test('fetches the parent list once on mount and keeps it at 1 request after 5 s', async () => {
    jest.useFakeTimers()
    const fetchMock = subagentFetchMock()

    renderHeader()
    await flushEffects()

    // One read of the parent's subagent list (plus the primary instance's own
    // mount fetch, counted separately per URL).
    expect(subagentFetchCalls(fetchMock, PARENT_KEY)).toBe(1)
    expect(subagentFetchCalls(fetchMock, CHILD_KEY)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

    // The guard: the parent instance passed `0` and must not poll at all.
    expect(subagentFetchCalls(fetchMock, PARENT_KEY)).toBe(1)

    // Sanity check that the window really elapsed: the primary instance polls
    // every 5 s, so a frozen clock cannot be what keeps the parent at 1.
    expect(subagentFetchCalls(fetchMock, CHILD_KEY)).toBe(2)
  })
})
