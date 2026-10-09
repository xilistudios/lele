import { afterEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import React from 'react'
import { AuthContext, type AuthContextValue } from '../contexts/AuthContext'
import type {
  MCPInventoryResponse,
  MCPQueryLayer,
  MCPRawFileResponse,
  MCPToggleResponse,
} from '../lib/mcpTypes'
import { endpoints } from '../services/http/endpoints'
import { parseApiError } from '../services/http/errors'
import {
  agentMCPInventoryQueryKey,
  mcpErrorCode,
  mcpRawFileQueryKey,
  useMCPDriveToggle,
} from './useAgentMCP'

const originalFetch = globalThis.fetch

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
})

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
