/**
 * Shared harness for tests that need the REAL `useAppLogic` mounted.
 *
 * `useAppLogic` is the only place where the composer's contract lives (chips,
 * queueing, upload, error banner), so tests about "what the user sees after a
 * send/upload" have to render it for real rather than re-implement its logic.
 * Everything it talks to over the network is stubbed here; the hooks under it
 * are the production ones.
 *
 * Two gotchas, both learned the hard way in this repo:
 *  - `waitFor` from testing-library never settles under bun+jsdom → poll with
 *    `waitUntil` below;
 *  - the api stub and the `wsSend` callback must keep a STABLE identity across
 *    renders, otherwise `useAppLogic`'s effects re-arm on every render and the
 *    test hangs forever instead of failing.
 */
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

/**
 * Minimal API stub: only the calls awaited on mount need a real shape, the rest
 * resolve to `{}` (a Proxy), which is enough for hooks that guard their reads.
 */
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

/** Mount the real `useAppLogic` and record everything it sends over the socket. */
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

  // Built ONCE, outside the render callback (see the file header).
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

/** `waitFor` does not settle under bun+jsdom → flush a macrotask and look again. */
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
