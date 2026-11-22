import '../test/setup'
import '../test/i18n'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, renderHook } from '@testing-library/react'
import { type ReactNode, createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { useAppLogic } from '../hooks/useAppLogic'
import type { ApiClient } from '../lib/api'
import { saveCurrentSessionKey } from '../lib/storage'

export type SentEvent = { event: string; data: Record<string, unknown> }

const DEFAULT_AGENT = 'main'

export function stubApi(overrides: Record<string, unknown> = {}): ApiClient {
  const impl: Record<string, unknown> = {
    agents: async () => ({ agents: [{ id: DEFAULT_AGENT, name: 'Main' }] }),
    sessionAgent: async () => ({ agent_id: DEFAULT_AGENT }),
    sessionsMeta: async () => ({ sessions: [], has_more: false, next_cursor: null }),
    models: async () => ({ model: 'test/model', models: ['test/model'], model_groups: [] }),
    sessionModel: async () => ({ model: 'test/model' }),
    history: async (sessionKey: string) => ({
      session_key: sessionKey,
      messages: [],
      processing: false,
      has_more: false,
    }),
    uploadFiles: async () => ({ files: [] }),
    agentInfo: async () => ({}),
    systemStatus: async () => ({}),
    channels: async () => ({ channels: [] }),
    tools: async () => ({ tools: [] }),
    config: async () => ({}),
    subagents: async () => ({ subagents: [] }),
    approvals: async () => ({ approvals: [] }),
    cronJobs: async () => ({ jobs: [] }),
    ...overrides,
  }
  return new Proxy(impl, {
    get: (target, prop: string) => (prop in target ? target[prop] : async () => ({})),
  }) as unknown as ApiClient
}

export type RenderAppLogicOptions = {
  sessionKey?: string
  token?: string | null
  api?: ApiClient
  clientId?: string | null
}

export function renderAppLogic(options: RenderAppLogicOptions = {}) {
  const sessionKey = options.sessionKey ?? 'native:test-session'
  const token = options.token === undefined ? 'tok' : options.token

  localStorage.setItem('lele.session', JSON.stringify({ token: 'tok', clientId: 'cli' }))
  saveCurrentSessionKey(sessionKey)

  const sent: SentEvent[] = []
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(MemoryRouter, null, children),
    )

  const api = options.api ?? stubApi()
  const wsSend = (event: string, data: Record<string, unknown>) => sent.push({ event, data })

  const rendered = renderHook(
    () =>
      useAppLogic(
        api,
        token,
        options.clientId ?? 'cli',
        'connected',
        wsSend,
        () => {},
        () => {},
      ),
    { wrapper },
  )

  const waitUntilReady = () =>
    waitUntil(() => rendered.result.current.agents.length === 1, 'agents loaded')

  return { ...rendered, sent, waitUntilReady, sessionKey }
}

export async function waitUntil(
  predicate: () => boolean,
  what = 'condition',
  timeoutMs = 4000,
): Promise<void> {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) {
    if (predicate()) return
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 10))
    })
  }
  throw new Error(`waitUntil: ${what} never became true`)
}
