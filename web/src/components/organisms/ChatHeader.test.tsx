import '../../test/setup'
import '../../test/i18n'
import { afterEach, beforeAll, beforeEach, describe, expect, jest, mock, test } from 'bun:test'
import { act, cleanup, render } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthProvider } from '../../contexts/AuthContext'
import type { SubagentTaskInfo } from '../../lib/types'

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

const PARENT_KEY = 'parent-session'
const CHILD_KEY = 'child-session'

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

function subagentFetchCalls(fetchMock: ReturnType<typeof mock>, sessionKey: string): number {
  const path = `/api/v1/chat/sessions/${sessionKey}/subagents`
  return fetchMock.mock.calls.filter((call) => String(call[0]).includes(path)).length
}

async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 10; i += 1) await Promise.resolve()
  })
}

function renderHeader() {
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

    expect(subagentFetchCalls(fetchMock, PARENT_KEY)).toBe(1)
    expect(subagentFetchCalls(fetchMock, CHILD_KEY)).toBe(1)

    act(() => {
      jest.advanceTimersByTime(FLOOD_WINDOW_MS)
    })
    await flushEffects()

    expect(subagentFetchCalls(fetchMock, PARENT_KEY)).toBe(1)

    expect(subagentFetchCalls(fetchMock, CHILD_KEY)).toBe(2)
  })
})
