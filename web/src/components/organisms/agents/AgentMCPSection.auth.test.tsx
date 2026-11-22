import '../../../test/setup'
import { afterEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../../contexts/AuthContext'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { MCPAuthStart, MCPAuthStatus, MCPInventoryResponse } from '../../../lib/mcpTypes'
import type { EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { endpoints } from '../../../services/http/endpoints'
import { ApiError } from '../../../services/http/errors'
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { AgentMCPSection } from './AgentMCPSection'

afterEach(() => {
  cleanup()
  Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
})

const AUTH_API_URL = 'http://127.0.0.1:18793'

const START: MCPAuthStart = {
  flow_id: 'flow-1',
  auth_url: 'https://as.example.com/authorize?code_challenge=CHALLENGE&state=st-1',
  redirect_uri: 'http://127.0.0.1:41234/callback',
  expires_at: '2030-01-01T00:00:00Z',
  access_token: 'LEAKED_ACCESS_TOKEN',
  client_secret: 'LEAKED_CLIENT_SECRET',
} as MCPAuthStart

const PENDING: MCPAuthStatus = {
  flow_id: 'flow-1',
  server: 'oauth-needed',
  state: 'pending',
  authenticated: false,
}

const DONE: MCPAuthStatus = {
  flow_id: 'flow-1',
  server: 'oauth-needed',
  state: 'done',
  authenticated: true,
}

const COMPLETE_OK: MCPAuthStatus = { ...DONE }

const FLOW_ERROR: MCPAuthStatus = {
  flow_id: 'flow-1',
  server: 'oauth-needed',
  state: 'error',
  error: 'authorization failed: access_denied',
  authenticated: false,
}

const FLOW_CANCELED: MCPAuthStatus = {
  flow_id: 'flow-1',
  server: 'oauth-needed',
  state: 'error',
  error: 'canceled',
  authenticated: false,
}

const FLOW_TIMED_OUT: MCPAuthStatus = {
  flow_id: 'flow-1',
  server: 'oauth-needed',
  state: 'error',
  error: 'authorization timed out after 5m0s',
  authenticated: false,
}

const START_RETRY: MCPAuthStart = {
  ...START,
  flow_id: 'flow-2',
  auth_url: 'https://as.example.com/authorize?code_challenge=SECOND&state=st-2',
}

const INVENTORY: MCPInventoryResponse = {
  agent_id: 'coder',
  layers: [{ layer: 'global', path: '/home/u/.lele/mcp.json', exists: true }],
  servers: [
    {
      name: 'oauth-done',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: {
        url: 'https://a.example.com/sse',
        auth: { required: true, authenticated: true, method: 'oauth' },
      },
    },
    {
      name: 'token-stored',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: {
        url: 'https://e.example.com/sse',
        auth: { required: false, authenticated: true, method: 'oauth' },
      },
    },
    {
      name: 'oauth-needed',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: {
        url: 'https://b.example.com/sse',
        auth: { required: true, authenticated: false, method: 'oauth' },
      },
    },
    {
      name: 'saw-401',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: {
        url: 'https://c.example.com/sse',
        auth: { required: true, authenticated: false, method: '' },
      },
    },
    {
      name: 'stdio',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: { command: 'npx', args: 1 },
    },
    {
      name: 'no-creds',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: {
        url: 'https://d.example.com/sse',
        auth: { required: false, authenticated: false, method: '' },
      },
    },
  ],
}

type FakeCall =
  | { kind: 'inventory'; agentId: string }
  | { kind: 'authStart'; agentId: string; name: string }
  | { kind: 'authStatus'; agentId: string; flowId: string }
  | {
      kind: 'authComplete'
      agentId: string
      flowId: string
      body: { code: string; state?: string }
    }
  | { kind: 'authCancel'; agentId: string; flowId: string }
  | { kind: 'logout'; agentId: string; name: string }

type Options = {
  inventory?: MCPInventoryResponse
  startError?: (attempt: number) => void
  statuses?: MCPAuthStatus[]
  statusError?: () => never
  completeError?: () => never
  logoutError?: () => never
  startResponses?: MCPAuthStart[]
  gateStart?: (attempt: number) => boolean
  gateLogout?: boolean
}

function setup(options: Options = {}) {
  const agent: EditableAgentConfig = { id: 'coder' }
  const writes: Write[] = []
  const state = makeState([agent], { writes })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const inventory = options.inventory ?? INVENTORY
  const statuses = options.statuses ?? [PENDING]

  const calls: FakeCall[] = []
  const startResponses = options.startResponses ?? [START]
  let statusIndex = 0
  let startIndex = 0
  let startAttempt = 0
  let releaseLogout: () => void = () => undefined
  let releaseStart: () => void = () => undefined
  const startGate = options.gateStart
    ? new Promise<void>((resolve) => {
        releaseStart = resolve
      })
    : null
  const logoutGate = options.gateLogout
    ? new Promise<void>((resolve) => {
        releaseLogout = resolve
      })
    : null

  const authApi = {
    getToken: () => 'token-test',
    mcpInventory: async (agentId: string) => {
      calls.push({ kind: 'inventory', agentId })
      return inventory
    },
    mcpAuthStart: async (agentId: string, name: string) => {
      calls.push({ kind: 'authStart', agentId, name })
      startAttempt++
      if (startGate && options.gateStart?.(startAttempt)) await startGate
      if (options.startError) options.startError(startAttempt)
      const answer = startResponses[Math.min(startIndex, startResponses.length - 1)]
      startIndex++
      return answer
    },
    mcpAuthStatus: async (agentId: string, flowId: string) => {
      calls.push({ kind: 'authStatus', agentId, flowId })
      if (options.statusError) options.statusError()
      const status = statuses[Math.min(statusIndex, statuses.length - 1)]
      statusIndex++
      return status
    },
    mcpAuthComplete: async (
      agentId: string,
      flowId: string,
      body: { code: string; state?: string },
    ) => {
      calls.push({ kind: 'authComplete', agentId, flowId, body })
      if (options.completeError) options.completeError()
      return COMPLETE_OK
    },
    mcpAuthCancel: async (agentId: string, flowId: string) => {
      calls.push({ kind: 'authCancel', agentId, flowId })
    },
    mcpLogout: async (agentId: string, name: string) => {
      calls.push({ kind: 'logout', agentId, name })
      if (options.logoutError) options.logoutError()
      if (logoutGate) await logoutGate
    },
  } as unknown as ApiClient

  const authValue: AuthContextValue = {
    api: authApi,
    apiUrl: AUTH_API_URL,
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => {
      throw new Error('unused')
    },
    ensureSession: async () => null,
    isLoading: false,
  }

  const settingsApi = {
    models: async () => ({ models: [], model_groups: [] }),
    skills: async () => ({ skills: [] }),
    tools: async () => ({ tools: [] }),
  } as unknown as ApiClient

  const utils = render(
    <AuthContext.Provider value={authValue}>
      <QueryClientProvider client={queryClient}>
        <SettingsProvider settingsState={state} api={settingsApi}>
          <MemoryRouter initialEntries={['/agents/coder/mcp']}>
            <AgentMCPSection agentId="coder" />
          </MemoryRouter>
        </SettingsProvider>
      </QueryClientProvider>
    </AuthContext.Provider>,
  )

  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)

  const need = (id: string) => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    return el
  }

  const clickTestId = (id: string) => {
    fireEvent.click(need(id))
  }

  const of = <K extends FakeCall['kind']>(kind: K) =>
    calls.filter((call): call is Extract<FakeCall, { kind: K }> => call.kind === kind)

  return {
    ...utils,
    calls,
    writes,
    byTestId,
    need,
    clickTestId,
    starts: () => of('authStart'),
    polls: () => of('authStatus'),
    completes: () => of('authComplete'),
    cancels: () => of('authCancel'),
    logouts: () => of('logout'),
    inventories: () => of('inventory'),
    releaseLogout: () => releaseLogout(),
    releaseStart: () => releaseStart(),
  }
}

type Utils = ReturnType<typeof setup>

async function ready(u: Utils) {
  await waitFor(() => {
    expect(u.byTestId('mcp-rows')).toBeTruthy()
  })
  return u
}

async function openAuth(u: Utils, name: string) {
  u.clickTestId(`mcp-auth-start-${name}`)
  await waitFor(() => expect(u.byTestId('mcp-auth-modal')).toBeTruthy())
  return u
}

function withClipboard(writeText: (text: string) => Promise<void> | void) {
  const spy = mock(async (text: string) => {
    await writeText(text)
  })
  Object.defineProperty(navigator, 'clipboard', { value: { writeText: spy }, configurable: true })
  return spy
}

describe('AgentMCPSection — auth row controls', () => {
  test('an authenticated server shows the status chip and Sign out (no sign-in)', async () => {
    const u = await ready(setup())
    expect(u.need('mcp-auth-chip-oauth-done').textContent).toBe(tr('mcp.auth.statusAuthenticated'))
    const logout = u.need('mcp-auth-logout-oauth-done')
    expect(logout.getAttribute('aria-label')).toBe(
      tr('mcp.auth.logoutAria', { name: 'oauth-done' }),
    )
    expect(logout.textContent).toBe(tr('mcp.auth.logout'))
    expect(u.byTestId('mcp-auth-start-oauth-done')).toBeNull()
  })

  test('an authenticated server declaring NO requirement still shows the chip and Sign out', async () => {
    const u = await ready(setup())
    const name = 'token-stored'
    expect(u.need(`mcp-auth-chip-${name}`).textContent).toBe(tr('mcp.auth.statusAuthenticated'))
    const logout = u.need(`mcp-auth-logout-${name}`)
    expect(logout.getAttribute('aria-label')).toBe(tr('mcp.auth.logoutAria', { name }))
    expect(logout.textContent).toBe(tr('mcp.auth.logout'))
    expect(u.byTestId(`mcp-auth-start-${name}`)).toBeNull()
  })

  test('a required server offers Authenticate — with or without a declared method', async () => {
    const u = await ready(setup())
    for (const name of ['oauth-needed', 'saw-401']) {
      expect(u.need(`mcp-auth-chip-${name}`).textContent).toBe(tr('mcp.auth.statusRequired'))
      const start = u.need(`mcp-auth-start-${name}`)
      expect(start.getAttribute('aria-label')).toBe(tr('mcp.auth.buttonAria', { name }))
      expect(start.textContent).toBe(tr('mcp.auth.button'))
      expect(u.byTestId(`mcp-auth-logout-${name}`)).toBeNull()
    }
  })

  test('no auth report (stdio) and nothing to require ⇒ no chip, no button at all', async () => {
    const u = await ready(setup())
    for (const name of ['stdio', 'no-creds']) {
      expect(u.byTestId(`mcp-auth-chip-${name}`)).toBeNull()
      expect(u.byTestId(`mcp-auth-start-${name}`)).toBeNull()
      expect(u.byTestId(`mcp-auth-logout-${name}`)).toBeNull()
    }
    expect(u.calls.map((call) => call.kind)).toEqual(['inventory'])
  })

  test('both controls are disabled while an auth action for the row is in flight', async () => {
    const u = await ready(setup({ gateLogout: true }))
    u.clickTestId('mcp-auth-logout-oauth-done')
    await waitFor(() =>
      expect(u.need('mcp-auth-logout-oauth-done').hasAttribute('disabled')).toBe(true),
    )
    expect(u.logouts()).toHaveLength(1)
    expect(u.need('mcp-auth-start-oauth-needed').hasAttribute('disabled')).toBe(false)
    await act(async () => {
      u.releaseLogout()
    })

    await openAuth(u, 'oauth-needed')
    expect(u.need('mcp-auth-start-oauth-needed').hasAttribute('disabled')).toBe(true)
  })
})

describe('AgentMCPSection — starting a flow', () => {
  test('Authenticate starts exactly ONE flow and shows its authorization URL', async () => {
    const u = await ready(setup())
    expect(u.byTestId('mcp-auth-modal')).toBeNull()
    await openAuth(u, 'oauth-needed')

    await waitFor(() => expect(u.starts()).toHaveLength(1))
    const [call] = u.starts()
    expect(call).toEqual({ kind: 'authStart', agentId: 'coder', name: 'oauth-needed' })
    expect(`${AUTH_API_URL}${endpoints.agents.mcp.auth.start(call.agentId, call.name)}`).toBe(
      `${AUTH_API_URL}/api/v1/mcp/servers/oauth-needed/auth?agent_id=coder`,
    )

    const field = u.need('mcp-auth-url')
    expect(field.textContent).toBe(START.auth_url)
    expect(field.getAttribute('role')).toBe('textbox')
    expect(field.getAttribute('aria-readonly')).toBe('true')
    expect(field.getAttribute('aria-label')).toBe(tr('mcp.auth.urlLabel'))
    const link = u.need('mcp-auth-open') as HTMLAnchorElement
    expect(link.getAttribute('href')).toBe(START.auth_url)
    expect(link.getAttribute('target')).toBe('_blank')
    expect(link.getAttribute('rel')).toBe('noopener noreferrer')
    expect(link.textContent).toBe(tr('mcp.auth.openInBrowser'))

    expect(u.container.textContent).toContain(tr('mcp.auth.modalTitle', { name: 'oauth-needed' }))
    expect(u.container.textContent).not.toContain('LEAKED_ACCESS_TOKEN')
    expect(u.container.textContent).not.toContain('LEAKED_CLIENT_SECRET')
  })

  test('the start happens ONCE, even across re-renders and the whole poll', async () => {
    const u = await ready(setup({ statuses: [PENDING, DONE] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.starts()).toHaveLength(1))

    await waitFor(() => expect(u.polls().length).toBeGreaterThanOrEqual(1))
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('done'), {
      timeout: 4000,
    })
    expect(u.starts()).toHaveLength(1)
    u.clickTestId('mcp-auth-close')
    expect(u.byTestId('mcp-auth-modal')).toBeNull()
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.starts()).toHaveLength(2))
  })

  test('the poll drives the status: waiting → done, and the inventory is refetched', async () => {
    const u = await ready(setup({ statuses: [PENDING, DONE] }))
    const inventoryReadsBefore = u.inventories().length
    await openAuth(u, 'oauth-needed')

    expect(u.need('mcp-auth-status').textContent).toContain(tr('mcp.auth.waiting'))
    await waitFor(() => expect(u.need('mcp-auth-status').textContent).toBe(tr('mcp.auth.done')), {
      timeout: 4000,
    })
    expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('done')
    await waitFor(() => expect(u.inventories().length).toBeGreaterThan(inventoryReadsBefore), {
      timeout: 4000,
    })
  })

  test('a 501 mcp_auth_not_supported renders notSupported and NO paste box', async () => {
    const u = await ready(
      setup({
        startError: () => {
          throw new ApiError('this server does not support oauth', 501, 'mcp_auth_not_supported')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    const notice = u.need('mcp-auth-not-supported')
    expect(notice.textContent).toBe(tr('mcp.auth.notSupported'))
    expect(u.byTestId('mcp-auth-paste')).toBeNull()
    expect(u.byTestId('mcp-auth-submit')).toBeNull()
    expect(u.byTestId('mcp-auth-retry')).toBeNull()
    expect(u.need('mcp-auth-close')).toBeTruthy()
  })

  test('a 409 mcp_auth_not_required renders notRequired and NO paste box', async () => {
    const u = await ready(
      setup({
        startError: () => {
          throw new ApiError('the server needs no credentials', 409, 'mcp_auth_not_required')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-not-required')).toBeTruthy(), {
      timeout: 4000,
    })
    expect(u.need('mcp-auth-not-required').textContent).toBe(tr('mcp.auth.notRequired'))
    expect(u.byTestId('mcp-auth-paste')).toBeNull()
    expect(u.byTestId('mcp-auth-submit')).toBeNull()
  })

  test('a 503 mcp_auth_disabled is TERMINAL: no Retry that could never succeed', async () => {
    const u = await ready(
      setup({
        startError: () => {
          throw new ApiError('authorization storage is disabled', 503, 'mcp_auth_disabled')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')

    await waitFor(() =>
      expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('store-disabled'),
    )
    expect(u.need('mcp-auth-store-disabled').textContent).toBe(tr('mcp.auth.storeDisabled'))
    expect(u.byTestId('mcp-auth-retry')).toBeNull()
    expect(u.byTestId('mcp-auth-paste')).toBeNull()
    expect(u.byTestId('mcp-auth-submit')).toBeNull()
    expect(u.byTestId('mcp-auth-intro')).toBeNull()
    expect(u.need('mcp-auth-close')).toBeTruthy()
  })

  test('a plain 503 (mcp_unavailable) is still RETRYABLE — only the code is terminal', async () => {
    const u = await ready(
      setup({
        startError: (attempt) => {
          if (attempt === 1) throw new ApiError('gateway down', 503, 'mcp_unavailable')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('start-failed')
    u.clickTestId('mcp-auth-retry')
    await waitFor(() => expect(u.starts()).toHaveLength(2))
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('pending'))
  })

  test('a generic start failure shows the message + code and a Retry that starts a new flow', async () => {
    const u = await ready(
      setup({
        startError: (attempt) => {
          if (attempt === 1) throw new ApiError('gateway down', 503, 'mcp_unavailable')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    const failure = u.need('mcp-auth-error')
    expect(failure.getAttribute('role')).toBe('alert')
    expect(failure.textContent).toContain('gateway down')
    expect(failure.textContent).toContain('mcp_unavailable')
    expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('start-failed')

    u.clickTestId('mcp-auth-retry')
    await waitFor(() => expect(u.starts()).toHaveLength(2))
    await waitFor(() => expect(u.byTestId('mcp-auth-url')).toBeTruthy())
    expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('pending')
  })

  test('a swept flow (404) renders expired with a fresh start, not a dead paste box', async () => {
    const u = await ready(
      setup({
        statusError: () => {
          throw new ApiError('unknown flow', 404, 'mcp_auth_flow_not_found')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-expired')).toBeTruthy())
    expect(u.need('mcp-auth-expired').textContent).toBe(tr('mcp.auth.expired'))
    expect(u.byTestId('mcp-auth-paste')).toBeNull()
    expect(u.need('mcp-auth-retry')).toBeTruthy()
  })

  test('a poll failure that is not a 404 renders the failure with its code', async () => {
    const u = await ready(
      setup({
        statusError: () => {
          throw new ApiError('gateway is unreachable', 503, 'mcp_unavailable')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-error')).toBeTruthy())
    const failure = u.need('mcp-auth-error')
    expect(failure.textContent).toContain('gateway is unreachable')
    expect(failure.textContent).toContain('mcp_unavailable')
    expect(u.byTestId('mcp-auth-paste')).toBeTruthy()
  })

  test('a cancelled flow is translated — the frozen `canceled` sentinel is not prose', async () => {
    const u = await ready(setup({ statuses: [FLOW_CANCELED] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('error'))

    const failure = u.need('mcp-auth-error')
    expect(failure.textContent).toBe(tr('mcp.auth.canceled'))
    expect(failure.textContent).not.toBe(tr('mcp.auth.failed', { error: 'canceled' }))
  })

  test('a timeout keeps the server wording — the contract does not freeze that string', async () => {
    const u = await ready(setup({ statuses: [FLOW_TIMED_OUT] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('error'))

    expect(u.need('mcp-auth-error').textContent).toBe(
      tr('mcp.auth.failed', { error: 'authorization timed out after 5m0s' }),
    )
  })

  test('a retry drops the previous authorization URL while the new start is in flight', async () => {
    const u = await ready(
      setup({
        statusError: () => {
          throw new ApiError('unknown flow', 404, 'mcp_auth_flow_not_found')
        },
        startResponses: [START, START_RETRY],
        gateStart: (attempt) => attempt === 2,
      }),
    )
    await openAuth(u, 'oauth-needed')

    await waitFor(() => expect(u.byTestId('mcp-auth-expired')).toBeTruthy())
    expect(u.need('mcp-auth-url').textContent).toBe(START.auth_url)

    u.clickTestId('mcp-auth-retry')
    await waitFor(() =>
      expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('starting'),
    )
    expect(u.byTestId('mcp-auth-url')).toBeNull()
    expect(u.byTestId('mcp-auth-open')).toBeNull()

    await act(async () => {
      u.releaseStart()
    })
    await waitFor(() => expect(u.need('mcp-auth-url').textContent).toBe(START_RETRY.auth_url))
    await waitFor(() => expect(u.polls().map((poll) => poll.flowId)).toContain('flow-2'))
    expect(u.starts()).toHaveLength(2)
    expect(u.byTestId('mcp-auth-url')?.textContent).not.toBe(START.auth_url)
  })
})

describe('AgentMCPSection — manual completion (paste)', () => {
  test('a full redirect URL submits its code AND state, then reports done', async () => {
    const u = await ready(setup({ statuses: [PENDING, PENDING, DONE] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())
    expect(u.need('mcp-auth-manual-note').textContent).toBe(tr('mcp.auth.manualNote'))

    fireEvent.change(u.need('mcp-auth-paste'), {
      target: { value: 'http://127.0.0.1:41234/callback?code=THE-CODE&state=st-1&iss=as' },
    })
    expect(u.need('mcp-auth-submit').hasAttribute('disabled')).toBe(false)
    u.clickTestId('mcp-auth-submit')

    await waitFor(() => expect(u.completes()).toHaveLength(1))
    expect(u.completes()[0]).toEqual({
      kind: 'authComplete',
      agentId: 'coder',
      flowId: 'flow-1',
      body: { code: 'THE-CODE', state: 'st-1' },
    })
    expect(endpoints.agents.mcp.auth.complete('coder', 'flow-1')).toBe(
      '/api/v1/mcp/auth/flow-1/complete?agent_id=coder',
    )
    await waitFor(() => expect(u.need('mcp-auth-status').textContent).toBe(tr('mcp.auth.done')), {
      timeout: 4000,
    })
  })

  test('a bare code submits alone — no empty state parameter', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())

    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: '  BARE-CODE\n' } })
    u.clickTestId('mcp-auth-submit')
    await waitFor(() => expect(u.completes()).toHaveLength(1))
    expect(u.completes()[0]).toEqual({
      kind: 'authComplete',
      agentId: 'coder',
      flowId: 'flow-1',
      body: { code: 'BARE-CODE' },
    })
  })

  test('an empty or garbage box cannot submit; a refusal is surfaced, not submitted', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())
    const submit = () => u.need('mcp-auth-submit')

    expect(submit().hasAttribute('disabled')).toBe(true)
    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: '   ' } })
    expect(submit().hasAttribute('disabled')).toBe(true)
    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: 'not a code !!' } })
    expect(submit().hasAttribute('disabled')).toBe(true)

    fireEvent.change(u.need('mcp-auth-paste'), {
      target: { value: 'http://127.0.0.1:41234/callback?error=access_denied&state=st-1' },
    })
    expect(submit().hasAttribute('disabled')).toBe(true)
    const refusal = u.need('mcp-auth-paste-error')
    expect(refusal.getAttribute('role')).toBe('alert')
    expect(refusal.textContent).toBe(tr('mcp.auth.failed', { error: 'access_denied' }))
    fireEvent.click(submit())
    expect(u.completes()).toHaveLength(0)
  })

  test('a hostile error value never reaches the paragraph — the fixed sentence does', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())

    fireEvent.change(u.need('mcp-auth-paste'), {
      target: { value: 'http://127.0.0.1:41234/callback?error=%1B%5B31mhax&state=st-1' },
    })
    expect(u.need('mcp-auth-submit').hasAttribute('disabled')).toBe(true)
    const refusal = u.need('mcp-auth-paste-error')
    expect(refusal.textContent).toBe(tr('mcp.auth.refused'))
    expect(refusal.textContent).not.toContain('hax')
    expect(refusal.textContent).not.toContain('\u001b')
    // biome-ignore lint/suspicious/noControlCharactersInRegex: proving no control byte renders is the point of this assertion.
    expect(refusal.textContent).not.toMatch(/[\u0000-\u001f\u007f]/)
    expect(u.completes()).toHaveLength(0)
  })

  test('a bare query submits its parameters, never the literal text', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())

    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: 'code=THE-CODE&state=st-1' } })
    expect(u.need('mcp-auth-submit').hasAttribute('disabled')).toBe(false)
    u.clickTestId('mcp-auth-submit')
    await waitFor(() => expect(u.completes()).toHaveLength(1))
    expect(u.completes()[0]).toEqual({
      kind: 'authComplete',
      agentId: 'coder',
      flowId: 'flow-1',
      body: { code: 'THE-CODE', state: 'st-1' },
    })
  })

  test('a two-token paste (<code> <state>) submits both', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())

    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: '  TOKEN-1\n STATE-2 ' } })
    u.clickTestId('mcp-auth-submit')
    await waitFor(() => expect(u.completes()).toHaveLength(1))
    expect(u.completes()[0]).toEqual({
      kind: 'authComplete',
      agentId: 'coder',
      flowId: 'flow-1',
      body: { code: 'TOKEN-1', state: 'STATE-2' },
    })
  })

  test('a query naming no response parameter cannot submit (`foo=bar` is not a code)', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())

    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: 'foo=bar' } })
    expect(u.need('mcp-auth-submit').hasAttribute('disabled')).toBe(true)
    fireEvent.click(u.need('mcp-auth-submit'))
    expect(u.completes()).toHaveLength(0)
  })

  test('a failed completion surfaces inline with its machine code', async () => {
    const u = await ready(
      setup({
        completeError: () => {
          throw new ApiError('invalid grant', 502, 'mcp_auth_failed')
        },
      }),
    )
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.byTestId('mcp-auth-paste')).toBeTruthy())
    fireEvent.change(u.need('mcp-auth-paste'), { target: { value: 'THE-CODE' } })
    u.clickTestId('mcp-auth-submit')

    await waitFor(() => expect(u.byTestId('mcp-auth-complete-error')).toBeTruthy())
    const failure = u.need('mcp-auth-complete-error')
    expect(failure.getAttribute('role')).toBe('alert')
    expect(failure.textContent).toContain('invalid grant')
    expect(failure.textContent).toContain('mcp_auth_failed')
  })
})

describe('AgentMCPSection — clipboard', () => {
  test('Copy hands the authorization URL to the clipboard and confirms', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    const writeText = withClipboard(() => undefined)

    u.clickTestId('mcp-auth-copy')
    await waitFor(() => expect(u.need('mcp-auth-copy-status')).toBeTruthy())
    expect(writeText).toHaveBeenCalledWith(START.auth_url)
    expect(u.need('mcp-auth-copy-status').textContent).toBe(tr('mcp.auth.copied'))
  })

  test('a clipboard failure says so instead of silently pretending (the field stays selectable)', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    u.clickTestId('mcp-auth-copy')
    await waitFor(() => expect(u.need('mcp-auth-copy-status')).toBeTruthy())
    expect(u.need('mcp-auth-copy-status').textContent).toBe(tr('mcp.auth.copyFailed'))
    expect(u.need('mcp-auth-url').textContent).toBe(START.auth_url)
  })

  test('a throwing clipboard is a failure too', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    withClipboard(() => {
      throw new Error('denied by the browser')
    })
    u.clickTestId('mcp-auth-copy')
    await waitFor(() => expect(u.need('mcp-auth-copy-status')).toBeTruthy())
    expect(u.need('mcp-auth-copy-status').textContent).toBe(tr('mcp.auth.copyFailed'))
  })
})

describe('AgentMCPSection — closing a flow', () => {
  test('Escape while PENDING cancels the flow server-side and closes', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.polls().length).toBeGreaterThan(0))
    expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('pending')

    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(u.byTestId('mcp-auth-modal')).toBeNull())
    expect(u.cancels()).toEqual([{ kind: 'authCancel', agentId: 'coder', flowId: 'flow-1' }])
    expect(endpoints.agents.mcp.auth.cancel('coder', 'flow-1')).toBe(
      '/api/v1/mcp/auth/flow-1?agent_id=coder',
    )
  })

  test('the footer Cancel does the same while pending', async () => {
    const u = await ready(setup())
    await openAuth(u, 'oauth-needed')
    u.clickTestId('mcp-auth-cancel')
    await waitFor(() => expect(u.byTestId('mcp-auth-modal')).toBeNull())
    expect(u.cancels()).toHaveLength(1)
  })

  test('a DONE flow is never cancelled on close — the credential is already stored', async () => {
    const u = await ready(setup({ statuses: [DONE] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('done'))
    expect(u.byTestId('mcp-auth-cancel')).toBeNull()
    expect(u.need('mcp-auth-close')).toBeTruthy()

    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(u.byTestId('mcp-auth-modal')).toBeNull())
    expect(u.cancels()).toHaveLength(0)
  })

  test('an ERROR flow IS cancelled on close — it may still hold the loopback listener', async () => {
    const u = await ready(setup({ statuses: [FLOW_ERROR] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('error'))

    expect(u.need('mcp-auth-error').textContent).toContain('access_denied')
    expect(u.byTestId('mcp-auth-paste')).toBeTruthy()

    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(u.byTestId('mcp-auth-modal')).toBeNull())
    expect(u.cancels()).toEqual([{ kind: 'authCancel', agentId: 'coder', flowId: 'flow-1' }])
  })

  test('the footer Close is enough to cancel an ERROR flow too', async () => {
    const u = await ready(setup({ statuses: [FLOW_ERROR] }))
    await openAuth(u, 'oauth-needed')
    await waitFor(() => expect(u.need('mcp-auth-modal').getAttribute('data-phase')).toBe('error'))

    u.clickTestId('mcp-auth-close')
    await waitFor(() => expect(u.byTestId('mcp-auth-modal')).toBeNull())
    expect(u.cancels()).toEqual([{ kind: 'authCancel', agentId: 'coder', flowId: 'flow-1' }])
  })
})

describe('AgentMCPSection — sign out', () => {
  test('Sign out DELETEs the server credential and refetches the inventory', async () => {
    const u = await ready(setup())
    const readsBefore = u.inventories().length
    u.clickTestId('mcp-auth-logout-oauth-done')

    await waitFor(() => expect(u.logouts()).toHaveLength(1))
    expect(u.logouts()[0]).toEqual({ kind: 'logout', agentId: 'coder', name: 'oauth-done' })
    const [call] = u.logouts()
    expect(`${AUTH_API_URL}${endpoints.agents.mcp.auth.logout(call.agentId, call.name)}`).toBe(
      `${AUTH_API_URL}/api/v1/mcp/servers/oauth-done/auth?agent_id=coder`,
    )
    await waitFor(() => expect(u.inventories().length).toBeGreaterThan(readsBefore))
    expect(u.starts()).toHaveLength(0)
  })

  test('a logout failure surfaces on its row with the machine code, not as a toast', async () => {
    const u = await ready(
      setup({
        logoutError: () => {
          throw new ApiError('could not clear the credential', 500, 'mcp_auth_failed')
        },
      }),
    )
    expect(u.byTestId('mcp-row-error-oauth-needed')).toBeNull()

    u.clickTestId('mcp-auth-logout-oauth-done')
    await waitFor(() => expect(u.byTestId('mcp-row-error-oauth-done')).toBeTruthy())
    const error = u.need('mcp-row-error-oauth-done')
    expect(error.getAttribute('role')).toBe('alert')
    expect(error.textContent).toContain('could not clear the credential')
    expect(u.need('mcp-row-error-oauth-done-code').textContent).toBe('mcp_auth_failed')
    expect(u.byTestId('mcp-row-error-oauth-needed')).toBeNull()
  })
})
