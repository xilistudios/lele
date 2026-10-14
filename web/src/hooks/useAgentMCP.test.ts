import { afterEach, describe, expect, jest, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import React from 'react'
import { AuthContext, type AuthContextValue } from '../contexts/AuthContext'
import type {
  MCPAuthStart,
  MCPAuthStatus,
  MCPInventoryResponse,
  MCPQueryLayer,
  MCPRawFileResponse,
  MCPToggleResponse,
} from '../lib/mcpTypes'
import { endpoints } from '../services/http/endpoints'
import { parseApiError } from '../services/http/errors'
import {
  MCP_AUTH_POLL_INTERVAL_MS,
  agentMCPInventoryQueryKey,
  mcpAuthStatusQueryKey,
  mcpErrorCode,
  mcpRawFileQueryKey,
  useMCPAuthCancel,
  useMCPAuthComplete,
  useMCPAuthFlow,
  useMCPAuthStart,
  useMCPDriveToggle,
  useMCPLogout,
} from './useAgentMCP'

const originalFetch = globalThis.fetch

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
  // Always leave the file on real timers and visible: a hidden state (or a
  // pending fake timer) left behind would silently change the next test.
  jest.useRealTimers()
  setVisibility('visible')
})

/**
 * jsdom neither fires `visibilitychange` nor lets `visibilityState` be set, so
 * both are simulated the way a browser does (same helper as
 * useChatHistory.test.ts / services/ws/client.test.ts).
 */
function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { value: state, configurable: true })
  document.dispatchEvent(new window.Event('visibilitychange'))
}

/**
 * Settle the mocked api chain, React Query's notifyManager (a `setTimeout(0)`
 * scheduler, so a timer tick is required) and the resulting re-renders.
 *
 * `waitFor` cannot observe anything here: on fake timers its own timeout never
 * elapses. Advances are 0 ms, so they never push a query past its 1500 ms OAuth
 * poll by accident.
 */
async function flushEffects() {
  await act(async () => {
    for (let i = 0; i < 5; i += 1) {
      await Promise.resolve()
      jest.advanceTimersByTime(0)
      await Promise.resolve()
    }
  })
}

/**
 * Record every `invalidateQueries` call by key family: the inventory entries
 * keep the AGENT id, the flow-status entries keep the FLOW id — i.e. the
 * segment that identifies the query. The hooks' contract is "invalidate
 * exactly these keys, never patch", so counts matter (the flow poll must
 * invalidate the inventory ONCE, not once per re-render).
 */
function spyInvalidations(queryClient: QueryClient) {
  const inventory: unknown[] = []
  const status: unknown[] = []
  const original = queryClient.invalidateQueries.bind(queryClient)
  queryClient.invalidateQueries = ((filters?: Parameters<QueryClient['invalidateQueries']>[0]) => {
    const key = filters?.queryKey as readonly unknown[] | undefined
    if (key?.[0] === 'agentMCPInventory') inventory.push(key[1])
    if (key?.[0] === 'agentMCPAuthStatus') status.push(key[2])
    return original(filters)
  }) as QueryClient['invalidateQueries']
  return { inventory, status }
}

/** Harness style from useAvailableModels/useCronJobs: renderHook + providers. */
function createWrapper(queryClient: QueryClient, api: Partial<AuthContextValue['api']>) {
  const authValue: AuthContextValue = {
    api: api as AuthContextValue['api'],
    apiUrl: 'http://127.0.0.1:18793',
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => {
      throw new Error('unused')
    },
    ensureSession: async () => null,
    isLoading: false,
  }
  return function Wrapper({ children }: { children: ReactNode }) {
    return React.createElement(
      AuthContext.Provider,
      { value: authValue },
      React.createElement(QueryClientProvider, { client: queryClient }, children),
    )
  }
}

/**
 * The hooks must not talk to `fetch` directly (that would bypass the
 * ApiClient's 401 → single-flight refresh → replay machinery): record raw
 * fetch calls so tests can assert there were none.
 */
function guardRawFetch() {
  const spy = mock(
    async () =>
      new Response(JSON.stringify({}), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
  )
  globalThis.fetch = spy as unknown as typeof fetch
  return spy
}

/** Seed the cache with the inventory and the raw query of every layer. */
function seedMCPQueries(queryClient: QueryClient, agentId: string) {
  const inventory: MCPInventoryResponse = { agent_id: agentId }
  const raw = (layer: string): MCPRawFileResponse => ({ layer, exists: false, content: '' })
  queryClient.setQueryData(agentMCPInventoryQueryKey(agentId), inventory)
  for (const layer of ['global', 'agent', 'project'] as const) {
    queryClient.setQueryData(mcpRawFileQueryKey(agentId, layer), raw(layer))
  }
}

describe('endpoints.agents.mcp URL builders', () => {
  test('produce the exact paths with every dynamic segment encoded', () => {
    expect(endpoints.agents.mcp.inventory('main')).toBe('/api/v1/mcp?agent_id=main')
    // unicode agent id (ñ, space) is percent-encoded
    expect(endpoints.agents.mcp.inventory('agente ñ')).toBe('/api/v1/mcp?agent_id=agente%20%C3%B1')
    expect(endpoints.agents.mcp.raw('main', 'global')).toBe('/api/v1/mcp/global/raw?agent_id=main')
    expect(endpoints.agents.mcp.raw('main', 'project')).toBe(
      '/api/v1/mcp/project/raw?agent_id=main',
    )
    // putRaw shares the raw URL
    expect(endpoints.agents.mcp.putRaw('main', 'agent')).toBe(
      endpoints.agents.mcp.raw('main', 'agent'),
    )
    // a server name containing "/" is encoded
    expect(endpoints.agents.mcp.toggle('main', 'agent', 'srv/ice')).toBe(
      '/api/v1/mcp/agent/servers/srv%2Fice/toggle?agent_id=main',
    )
    expect(endpoints.agents.mcp.toggle('main', 'auto', 'srv/ice')).toBe(
      '/api/v1/mcp/auto/servers/srv%2Fice/toggle?agent_id=main',
    )
    expect(endpoints.agents.mcp.toggle('main', 'agent', 'srv/ice', false)).toBe(
      '/api/v1/mcp/agent/servers/srv%2Fice/toggle?agent_id=main',
    )
    // force appends &force=true only when asked
    expect(endpoints.agents.mcp.toggle('main', 'agent', 'srv/ice', true)).toBe(
      '/api/v1/mcp/agent/servers/srv%2Fice/toggle?agent_id=main&force=true',
    )
    expect(endpoints.agents.mcp.toggle('agente ñ', 'project', 'a/b', true)).toBe(
      '/api/v1/mcp/project/servers/a%2Fb/toggle?agent_id=agente%20%C3%B1&force=true',
    )
    expect(endpoints.agents.mcp.validate('main')).toBe('/api/v1/mcp/validate?agent_id=main')
  })
})

describe('useMCPDriveToggle invalidation', () => {
  test('invalidates inventory + the written layer raw key, not the other layers', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')

    const fetchSpy = guardRawFetch()
    const mcpToggle = mock(
      async (
        _agentId: string,
        _layer: MCPQueryLayer,
        _name: string,
        _enabled: boolean,
        _force?: boolean,
      ): Promise<MCPToggleResponse> => ({
        name: 'srv/ice',
        enabled: false,
        changed: true,
        removed: false,
        created: false,
        layer: 'agent',
        path: '/w/agent/mcp.json',
        effective: 'disabled',
        effective_layer: 'agent',
      }),
    )

    const { result } = renderHook(() => useMCPDriveToggle('main'), {
      wrapper: createWrapper(queryClient, { mcpToggle }),
    })

    await act(async () => {
      await result.current.mutateAsync({ layer: 'agent', name: 'srv/ice', enabled: false })
    })

    // The mutation reaches the ApiClient seam with the exact route args…
    expect(mcpToggle).toHaveBeenCalledTimes(1)
    expect(mcpToggle).toHaveBeenCalledWith('main', 'agent', 'srv/ice', false, undefined)
    // …and those args address the exact URL the request used to hit
    // (base join + verb/body live in the client, pinned in client.test.ts).
    const [agentId, layer, name, , force] = mcpToggle.mock.calls[0]
    expect(
      `http://127.0.0.1:18793${endpoints.agents.mcp.toggle(agentId, layer, name, force)}`,
    ).toBe('http://127.0.0.1:18793/api/v1/mcp/agent/servers/srv%2Fice/toggle?agent_id=main')
    // …and never through a hand-rolled fetch (that would bypass the client's
    // 401 → refresh → replay machinery).
    expect(fetchSpy.mock.calls).toHaveLength(0)

    // inventory + written layer invalidated…
    expect(queryClient.getQueryState(agentMCPInventoryQueryKey('main'))?.isInvalidated).toBe(true)
    expect(queryClient.getQueryState(mcpRawFileQueryKey('main', 'agent'))?.isInvalidated).toBe(true)
    // …and ONLY that layer's raw key
    expect(queryClient.getQueryState(mcpRawFileQueryKey('main', 'global'))?.isInvalidated).toBe(
      false,
    )
    expect(queryClient.getQueryState(mcpRawFileQueryKey('main', 'project'))?.isInvalidated).toBe(
      false,
    )
  })

  test('with layer auto invalidates the raw key of the layer the server wrote', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')

    const fetchSpy = guardRawFetch()
    const mcpToggle = mock(
      async (
        _agentId: string,
        _layer: MCPQueryLayer,
        _name: string,
        _enabled: boolean,
        _force?: boolean,
      ): Promise<MCPToggleResponse> => ({
        name: 'srv',
        enabled: true,
        changed: true,
        removed: false,
        created: false,
        layer: 'project', // server resolved auto → project
        path: '/w/project/mcp.json',
        effective: 'enabled',
        effective_layer: 'project',
      }),
    )

    const { result } = renderHook(() => useMCPDriveToggle('main'), {
      wrapper: createWrapper(queryClient, { mcpToggle }),
    })

    await act(async () => {
      await result.current.mutateAsync({ layer: 'auto', name: 'srv', enabled: true })
    })

    // `auto` is forwarded verbatim: the server resolves the winning layer.
    expect(mcpToggle).toHaveBeenCalledWith('main', 'auto', 'srv', true, undefined)
    expect(fetchSpy.mock.calls).toHaveLength(0)

    expect(queryClient.getQueryState(mcpRawFileQueryKey('main', 'project'))?.isInvalidated).toBe(
      true,
    )
    expect(queryClient.getQueryState(mcpRawFileQueryKey('main', 'global'))?.isInvalidated).toBe(
      false,
    )
    expect(queryClient.getQueryState(mcpRawFileQueryKey('main', 'agent'))?.isInvalidated).toBe(
      false,
    )
  })
})

describe('mcpErrorCode', () => {
  test('returns mcp_entry_shadowed from a parsed 409 body', async () => {
    const err = await parseApiError(
      new Response(
        JSON.stringify({
          code: 'mcp_entry_shadowed',
          message: 'shadowed by layer "project"; retry with force=true',
          error: 'shadowed by layer "project"; retry with force=true',
        }),
        { status: 409, statusText: 'Conflict', headers: { 'Content-Type': 'application/json' } },
      ),
    )
    expect(err.status).toBe(409)
    expect(mcpErrorCode(err)).toBe('mcp_entry_shadowed')
    // not an ApiError → no code
    expect(mcpErrorCode(new Error('boom'))).toBeUndefined()
  })
})
describe('endpoints.agents.mcp.auth URL builders', () => {
  test('produce the exact five OAuth paths with every dynamic segment encoded', () => {
    expect(endpoints.agents.mcp.auth.start('main', 'srv/ice')).toBe(
      '/api/v1/mcp/servers/srv%2Fice/auth?agent_id=main',
    )
    expect(endpoints.agents.mcp.auth.status('main', 'f/1')).toBe(
      '/api/v1/mcp/auth/f%2F1?agent_id=main',
    )
    expect(endpoints.agents.mcp.auth.complete('main', 'f/1')).toBe(
      '/api/v1/mcp/auth/f%2F1/complete?agent_id=main',
    )
    expect(endpoints.agents.mcp.auth.cancel('agente ñ', 'f/1')).toBe(
      '/api/v1/mcp/auth/f%2F1?agent_id=agente%20%C3%B1',
    )
    expect(endpoints.agents.mcp.auth.logout('agente ñ', 'a/b')).toBe(
      '/api/v1/mcp/servers/a%2Fb/auth?agent_id=agente%20%C3%B1',
    )
    // The two read/write pairs share a URL by design: the HTTP verb lives at
    // the call site (GET status vs DELETE cancel, POST start vs DELETE logout).
    expect(endpoints.agents.mcp.auth.cancel('main', 'f1')).toBe(
      endpoints.agents.mcp.auth.status('main', 'f1'),
    )
    expect(endpoints.agents.mcp.auth.logout('main', 'srv')).toBe(
      endpoints.agents.mcp.auth.start('main', 'srv'),
    )
  })
})

/** A pending/poll status row, base for the OAuth flow fixtures. */
function authStatus(state: string, authenticated = false): MCPAuthStatus {
  return { flow_id: 'f1', server: 'srv', state, authenticated }
}

describe('MCP OAuth mutations', () => {
  test('useMCPAuthStart calls the client and invalidates NOTHING', async () => {
    // Starting a flow only registers a loopback listener server-side: the
    // inventory row (and its `auth` report) is unchanged until a poll sees
    // `done`, which is where the inventory gets invalidated.
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    const { inventory } = spyInvalidations(queryClient)
    const fetchSpy = guardRawFetch()
    const started: MCPAuthStart = {
      flow_id: 'f1',
      auth_url: 'https://idp.example/authorize?x=1',
      redirect_uri: 'http://127.0.0.1:18794/callback',
      expires_at: '2026-01-01T00:05:00Z',
    }
    const mcpAuthStart = mock(async (): Promise<MCPAuthStart> => started)

    const { result } = renderHook(() => useMCPAuthStart('main'), {
      wrapper: createWrapper(queryClient, { mcpAuthStart }),
    })

    await act(async () => {
      const data = await result.current.mutateAsync('srv/ice')
      expect(data.auth_url).toBe(started.auth_url)
    })

    expect(mcpAuthStart).toHaveBeenCalledWith('main', 'srv/ice')
    expect(fetchSpy.mock.calls).toHaveLength(0)
    expect(inventory).toHaveLength(0)
    expect(queryClient.getQueryState(agentMCPInventoryQueryKey('main'))?.isInvalidated).toBe(false)
  })

  test('useMCPAuthComplete passes the body through and invalidates the flow key', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    queryClient.setQueryData(mcpAuthStatusQueryKey('main', 'f1'), authStatus('pending'))
    const { inventory, status } = spyInvalidations(queryClient)
    const fetchSpy = guardRawFetch()
    const mcpAuthComplete = mock(async (): Promise<MCPAuthStatus> => authStatus('done', true))

    const { result } = renderHook(() => useMCPAuthComplete('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthComplete }),
    })

    await act(async () => {
      await result.current.mutateAsync({ code: 'c0de', state: 'st4te' })
    })

    expect(mcpAuthComplete).toHaveBeenCalledWith('main', 'f1', { code: 'c0de', state: 'st4te' })
    expect(fetchSpy.mock.calls).toHaveLength(0)
    // The flow key is re-read (the server owns the authoritative state)…
    expect(queryClient.getQueryState(mcpAuthStatusQueryKey('main', 'f1'))?.isInvalidated).toBe(true)
    // …and the inventory is NOT touched here: only the flow poll observing
    // `done` invalidates it, so the "once" latch stays single-sourced.
    expect(inventory).toHaveLength(0)
    expect(status).toEqual(['f1'])
  })

  test('useMCPAuthCancel invalidates the flow key', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    queryClient.setQueryData(mcpAuthStatusQueryKey('main', 'f1'), authStatus('pending'))
    const { inventory, status } = spyInvalidations(queryClient)
    const fetchSpy = guardRawFetch()
    const mcpAuthCancel = mock(async (): Promise<void> => undefined)

    const { result } = renderHook(() => useMCPAuthCancel('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthCancel }),
    })

    await act(async () => {
      await result.current.mutateAsync()
    })

    expect(mcpAuthCancel).toHaveBeenCalledWith('main', 'f1')
    expect(fetchSpy.mock.calls).toHaveLength(0)
    expect(queryClient.getQueryState(mcpAuthStatusQueryKey('main', 'f1'))?.isInvalidated).toBe(true)
    expect(inventory).toHaveLength(0)
    expect(status).toEqual(['f1'])
  })

  test('useMCPLogout invalidates the inventory of the right agent only', async () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    seedMCPQueries(queryClient, 'other')
    const { inventory } = spyInvalidations(queryClient)
    const fetchSpy = guardRawFetch()
    const mcpLogout = mock(async (): Promise<void> => undefined)

    const { result } = renderHook(() => useMCPLogout('main'), {
      wrapper: createWrapper(queryClient, { mcpLogout }),
    })

    await act(async () => {
      await result.current.mutateAsync('srv/ice')
    })

    expect(mcpLogout).toHaveBeenCalledWith('main', 'srv/ice')
    expect(fetchSpy.mock.calls).toHaveLength(0)
    expect(queryClient.getQueryState(agentMCPInventoryQueryKey('main'))?.isInvalidated).toBe(true)
    expect(queryClient.getQueryState(agentMCPInventoryQueryKey('other'))?.isInvalidated).toBe(false)
    expect(inventory).toEqual(['main'])
  })
})

/**
 * Poll-until-terminal of ONE OAuth flow.
 *
 * Approach: FAKE TIMERS plus a countable fake `api` (no fetch mock), driven by
 * `jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS)` with `flushEffects()`
 * between ticks — the same harness useChatHistory.test.ts uses for its 4 s
 * reconciliation poll. The fake api counts its own calls, so "polled twice"
 * and "stopped polling" are exact numbers rather than timing guesses; the
 * inventory invalidations are counted through the query client's
 * `invalidateQueries` (never through fetch), which is what pins the
 * "exactly once" latch.
 */
describe('useMCPAuthFlow poll', () => {
  test('polls while pending, stops on done and invalidates the inventory exactly once', async () => {
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    const { inventory } = spyInvalidations(queryClient)

    let calls = 0
    const mcpAuthStatus = mock(async (): Promise<MCPAuthStatus> => {
      calls += 1
      // pending → pending → done
      const state = calls <= 2 ? 'pending' : 'done'
      return { ...authStatus(state, state === 'done'), flow_id: 'f1' }
    })

    const { result } = renderHook(() => useMCPAuthFlow('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthStatus }),
    })

    // Mount fetch (unconditional for an enabled query)…
    await flushEffects()
    expect(calls).toBe(1)
    expect(result.current.data?.state).toBe('pending')
    // …no invalidation while pending.
    expect(inventory).toHaveLength(0)

    act(() => {
      jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS)
    })
    await flushEffects()
    expect(calls).toBe(2)
    expect(result.current.data?.state).toBe('pending')
    expect(inventory).toHaveLength(0)

    act(() => {
      jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS)
    })
    await flushEffects()
    expect(calls).toBe(3)
    expect(result.current.data?.state).toBe('done')
    expect(result.current.data?.authenticated).toBe(true)
    // The backend reloads the MCP manager on completion, so the cached
    // inventory still shows the pre-auth state: exactly one re-read.
    expect(inventory).toEqual(['main'])
    expect(queryClient.getQueryState(agentMCPInventoryQueryKey('main'))?.isInvalidated).toBe(true)

    // Terminal: the interval is gone, three more minutes change nothing.
    act(() => {
      jest.advanceTimersByTime(180_000)
    })
    await flushEffects()
    expect(calls).toBe(3)
    expect(inventory).toHaveLength(1)
  })

  test('never invalidates the inventory while the flow stays pending', async () => {
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    const { inventory } = spyInvalidations(queryClient)

    let calls = 0
    const mcpAuthStatus = mock(async (): Promise<MCPAuthStatus> => {
      calls += 1
      return authStatus('pending')
    })

    renderHook(() => useMCPAuthFlow('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthStatus }),
    })

    await flushEffects()
    for (let tick = 0; tick < 3; tick += 1) {
      act(() => {
        jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS)
      })
      await flushEffects()
    }

    expect(calls).toBe(4)
    expect(inventory).toHaveLength(0)
  })

  test('stops polling on error and never invalidates the inventory', async () => {
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    const { inventory } = spyInvalidations(queryClient)

    let calls = 0
    const mcpAuthStatus = mock(async (): Promise<MCPAuthStatus> => {
      calls += 1
      if (calls === 1) return authStatus('pending')
      return { ...authStatus('error'), error: 'invalid_grant' }
    })

    const { result } = renderHook(() => useMCPAuthFlow('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthStatus }),
    })

    await flushEffects()
    act(() => {
      jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS)
    })
    await flushEffects()

    expect(calls).toBe(2)
    expect(result.current.data?.state).toBe('error')
    expect(result.current.data?.error).toBe('invalid_grant')

    act(() => {
      jest.advanceTimersByTime(180_000)
    })
    await flushEffects()
    expect(calls).toBe(2)
    expect(inventory).toHaveLength(0)
  })

  test('does not poll while the tab is hidden', async () => {
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')

    let calls = 0
    const mcpAuthStatus = mock(async (): Promise<MCPAuthStatus> => {
      calls += 1
      return authStatus('pending')
    })

    renderHook(() => useMCPAuthFlow('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthStatus }),
    })

    await flushEffects()
    expect(calls).toBe(1)

    act(() => setVisibility('hidden'))
    await flushEffects()

    act(() => {
      jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS * 4)
    })
    await flushEffects()
    // Zero requests for the whole time the user is away: the interval is
    // cleared, not merely skipped.
    expect(calls).toBe(1)
  })

  test('does not request anything while no flow is running (flowId null)', async () => {
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const mcpAuthStatus = mock(async (): Promise<MCPAuthStatus> => authStatus('pending'))

    const { result } = renderHook(() => useMCPAuthFlow('main', null), {
      wrapper: createWrapper(queryClient, { mcpAuthStatus }),
    })

    await flushEffects()
    act(() => {
      jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS * 4)
    })
    await flushEffects()

    expect(mcpAuthStatus).toHaveBeenCalledTimes(0)
    expect(result.current.isFetching).toBe(false)
    // The disabled query still has a stable key ('' stands for "no flow").
    expect(mcpAuthStatusQueryKey('main', null)).toEqual(['agentMCPAuthStatus', 'main', ''])
  })

  test('observing `done` twice for the SAME flow id invalidates only once (latch)', async () => {
    // Synthetic re-observation: pending → done → pending → done for one flow
    // id (e.g. the status query was invalidated/reset and refetched). Without
    // the `flowId`-keyed ref latch, the second `done` would buy a second full
    // inventory read; with it, the read happens once per flow.
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    const { inventory } = spyInvalidations(queryClient)

    let calls = 0
    const mcpAuthStatus = mock(async (): Promise<MCPAuthStatus> => {
      calls += 1
      return calls % 2 === 0 ? authStatus('done', true) : authStatus('pending')
    })

    const { result } = renderHook(() => useMCPAuthFlow('main', 'f1'), {
      wrapper: createWrapper(queryClient, { mcpAuthStatus }),
    })

    await flushEffects() // 1: pending
    act(() => {
      jest.advanceTimersByTime(MCP_AUTH_POLL_INTERVAL_MS)
    })
    await flushEffects() // 2: done
    expect(calls).toBe(2)
    expect(result.current.data?.state).toBe('done')
    expect(inventory).toHaveLength(1)

    // 3: pending again (a refetch the poller itself would no longer schedule).
    await act(async () => {
      await result.current.refetch()
    })
    await flushEffects()
    expect(calls).toBe(3)
    expect(result.current.data?.state).toBe('pending')

    // 4: done again — same flow id, so the latch must hold.
    await act(async () => {
      await result.current.refetch()
    })
    await flushEffects()
    expect(calls).toBe(4)
    expect(result.current.data?.state).toBe('done')
    expect(inventory).toHaveLength(1)
  })

  test('a NEW flow id invalidates again (the latch is keyed per flow)', async () => {
    jest.useFakeTimers()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    seedMCPQueries(queryClient, 'main')
    const { inventory } = spyInvalidations(queryClient)

    const mcpAuthStatus = mock(
      async (_agentId: string, flowId: string): Promise<MCPAuthStatus> => ({
        flow_id: flowId,
        server: 'srv',
        state: 'done',
        authenticated: true,
      }),
    )

    const { rerender } = renderHook(
      ({ flowId }: { flowId: string | null }) => useMCPAuthFlow('main', flowId),
      {
        initialProps: { flowId: 'f1' as string | null },
        wrapper: createWrapper(queryClient, { mcpAuthStatus }),
      },
    )

    await flushEffects()
    expect(inventory).toHaveLength(1)

    // Second flow (e.g. the user authenticates another server): a distinct
    // flow id is a distinct latch entry, so the inventory is re-read again.
    await act(async () => {
      rerender({ flowId: 'f2' })
    })
    await flushEffects()

    expect(inventory).toEqual(['main', 'main'])
    expect(mcpAuthStatus).toHaveBeenCalledTimes(2)
  })
})
