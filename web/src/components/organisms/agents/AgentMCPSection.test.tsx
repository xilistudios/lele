import '../../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../../contexts/AuthContext'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type {
  MCPInventoryResponse,
  MCPQueryLayer,
  MCPServerSummary,
  MCPToggleResponse,
} from '../../../lib/mcpTypes'
import type { EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { ApiError } from '../../../services/http/errors'
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { AgentMCPSection } from './AgentMCPSection'

/**
 * Harness style of AgentSkillsSection.test.tsx (SettingsProvider + makeState
 * + testid helpers), with one addition the Skills panel does not need: the
 * MCP hooks talk to the house ApiClient on AuthContext (`api.mcpInventory`,
 * `api.mcpToggle`), so the provider carries a recording fake of exactly those
 * two methods. The recording is how the assertions check the toggle contract
 * (ROW layer, never `auto`; `force: true` on the 409 retry) and that no call
 * beyond the inventory read ever leaves the page — in particular nothing that
 * could fetch a secret VALUE (the API only ships key NAMES).
 *
 * `screen` is unusable in this environment (agentsHarness header): all
 * queries come from the render result.
 */

afterEach(() => {
  // @testing-library/react does not auto-cleanup under bun.
  cleanup()
})

const AUTH_API_URL = 'http://127.0.0.1:18793'

const TOGGLE_OK: MCPToggleResponse = {
  name: 'github',
  enabled: false,
  changed: true,
  removed: false,
  created: false,
  layer: 'global',
  path: '/home/u/.lele/mcp.json',
  effective: 'disabled',
  effective_layer: 'global',
}

/**
 * Fixture with everything the panel must render — and two value-shaped fields
 * (`env`, `headers`) that the real API NEVER sends, smuggled in on purpose:
 * if any code path ever rendered a value, the leak assertions would catch it.
 */
const INVENTORY: MCPInventoryResponse = {
  agent_id: 'coder',
  layers: [
    { layer: 'global', path: '/home/u/.lele/mcp.json', exists: true },
    {
      layer: 'agent',
      path: '/home/u/.lele/workspace-coder/mcp.json',
      exists: false,
      aliased_with: ['project'],
    },
    // No `path` ⇔ disabled layer: the panel must say so explicitly.
    { layer: 'project', exists: false },
  ],
  servers: [
    {
      name: 'github',
      layer: 'global',
      path: '/home/u/.lele/mcp.json',
      effective: 'enabled',
      defines: true,
      server: {
        command: 'npx ${TOOLS_DIR}/mcp-github',
        args: 2,
        env_keys: ['GITHUB_TOKEN'],
        env: { GITHUB_TOKEN: 'ghp_LEAKED_SECRET_VALUE' },
      } as MCPServerSummary,
      shadowed: [{ layer: 'agent', reason: 'shadowed by layer "global"' }],
    },
    {
      name: 'postgres',
      layer: 'agent',
      effective: 'disabled',
      defines: true,
      server: { command: 'docker', args: 0 },
    },
    {
      name: 'broken',
      layer: 'project',
      effective: 'invalid',
      defines: true,
      server: { url: 'http://localhost:9999/sse', invalid: 'missing "command" field' },
    },
    {
      name: 'wiki',
      layer: 'global',
      effective: 'enabled',
      defines: true,
      server: {
        url: 'https://mcp.example.com/sse',
        header_keys: ['Authorization'],
        headers: { Authorization: 'Bearer LEAKED_BEARER_TOKEN' },
      } as MCPServerSummary,
    },
  ],
  warnings: ['project layer disabled: no workspace root'],
}

/** One recorded call against the MCP ApiClient seam. */
type FakeCall =
  | { kind: 'inventory'; agentId: string }
  | {
      kind: 'toggle'
      agentId: string
      layer: MCPQueryLayer
      name: string
      enabled: boolean
      force?: boolean
    }

type Handler = (
  call: FakeCall,
) => MCPInventoryResponse | MCPToggleResponse | Promise<MCPInventoryResponse | MCPToggleResponse>

function setup(
  options: {
    inventory?: MCPInventoryResponse
    handler?: Handler
  } = {},
) {
  const agent: EditableAgentConfig = { id: 'coder' }
  const writes: Write[] = []
  const state = makeState([agent], { writes })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const inventory = options.inventory ?? INVENTORY
  const handler: Handler =
    options.handler ?? ((call) => (call.kind === 'toggle' ? TOGGLE_OK : inventory))

  /** Recording ApiClient: only the two methods the MCP hooks call. */
  const calls: FakeCall[] = []
  const authApi = {
    getToken: () => 'token-test',
    mcpInventory: async (agentId: string) => {
      const call: FakeCall = { kind: 'inventory', agentId }
      calls.push(call)
      return handler(call) as MCPInventoryResponse
    },
    mcpToggle: async (
      agentId: string,
      layer: MCPQueryLayer,
      name: string,
      enabled: boolean,
      force?: boolean,
    ) => {
      const call: FakeCall = { kind: 'toggle', agentId, layer, name, enabled, force }
      calls.push(call)
      return handler(call) as MCPToggleResponse
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

  /** Query a testid, failing loudly (not silently on null) when missing. */
  const need = (id: string) => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    return el
  }

  const clickTestId = (id: string) => {
    fireEvent.click(need(id))
  }

  const toggles = () =>
    calls.filter((call): call is Extract<FakeCall, { kind: 'toggle' }> => call.kind === 'toggle')

  return { ...utils, calls, toggles, writes, byTestId, need, clickTestId }
}

/** Wait until the inventory resolves and the row list is on screen. */
async function ready(utils: ReturnType<typeof setup>) {
  await waitFor(() => {
    expect(utils.byTestId('mcp-rows')).toBeTruthy()
  })
  return utils
}

describe('AgentMCPSection — rows', () => {
  test('render state glyph, winning layer tag, RAW command, args and shadow chip', async () => {
    const u = await ready(setup())
    expect(u.byTestId('mcp-row-github')).toBeTruthy()
    // state: enabled ● / disabled ○ / invalid ⚠ + sr-only label (§10.5)
    expect(u.byTestId('mcp-state-github')?.textContent).toBe('●')
    expect(u.byTestId('mcp-state-postgres')?.textContent).toBe('○')
    expect(u.byTestId('mcp-state-broken')?.textContent).toBe('⚠')
    // winning layer tag
    expect(u.byTestId('mcp-layer-tag-github')?.textContent).toBe(tr('mcp.layer.global'))
    expect(u.byTestId('mcp-layer-tag-postgres')?.textContent).toBe(tr('mcp.layer.agent'))
    // command rendered RAW: ${VAR} shows literally, never interpolated
    expect(u.byTestId('mcp-command-github')?.textContent).toBe('npx ${TOOLS_DIR}/mcp-github')
    expect(u.byTestId('mcp-args-github')?.textContent).toBe(tr('mcp.row.args', { n: 2 }))
    expect(u.byTestId('mcp-args-postgres')).toBeNull() // args: 0 → no chip
    // url row + shadow chip with the server's reason
    expect(u.byTestId('mcp-url-wiki')?.textContent).toBe('https://mcp.example.com/sse')
    const shadow = u.need('mcp-shadow-github-agent')
    expect(shadow.textContent).toContain(tr('mcp.layer.agent'))
    expect(shadow.textContent).toContain('shadowed by layer "global"')
  })

  test('env/header key NAMES render as chips; no VALUE text ever renders', async () => {
    const u = await ready(setup())
    // names are displayed…
    expect(u.need('mcp-envkey-github-GITHUB_TOKEN').textContent).toBe('GITHUB_TOKEN')
    expect(u.need('mcp-headerkey-wiki-Authorization').textContent).toBe('Authorization')
    // …values are not, even though the fixture smuggles them through `env`/`headers`
    expect(u.container.textContent).not.toContain('ghp_LEAKED_SECRET_VALUE')
    expect(u.container.textContent).not.toContain('Bearer LEAKED_BEARER_TOKEN')
    // and no call ever leaves to fetch one: only the inventory read happened
    expect(u.calls.map((call) => call.kind)).toEqual(['inventory'])
  })

  test('source filter strip filters rows client-side by row.layer', async () => {
    const u = await ready(setup())
    expect(u.byTestId('mcp-row-github')).toBeTruthy()
    expect(u.byTestId('mcp-filter-all')?.getAttribute('aria-pressed')).toBe('true')
    u.clickTestId('mcp-filter-agent')
    expect(u.byTestId('mcp-filter-agent')?.getAttribute('aria-pressed')).toBe('true')
    expect(u.byTestId('mcp-row-postgres')).toBeTruthy()
    expect(u.byTestId('mcp-row-github')).toBeNull()
    expect(u.byTestId('mcp-row-wiki')).toBeNull()
    // layer rows are NOT filtered — they are context, not results
    expect(u.byTestId('mcp-layer-global')).toBeTruthy()
    u.clickTestId('mcp-filter-project')
    expect(u.byTestId('mcp-row-broken')).toBeTruthy()
    expect(u.byTestId('mcp-row-postgres')).toBeNull()
    u.clickTestId('mcp-filter-all')
    expect(u.byTestId('mcp-row-github')).toBeTruthy()
    expect(u.byTestId('mcp-row-wiki')).toBeTruthy()
  })
})

describe('AgentMCPSection — toggle', () => {
  test('calls mcpToggle with the ROW layer (never auto) and the right enabled', async () => {
    const u = await ready(setup())
    // enabled winner → disable
    u.clickTestId('mcp-toggle-github')
    await waitFor(() => expect(u.toggles()).toHaveLength(1))
    expect(u.toggles()[0]).toEqual({
      kind: 'toggle',
      agentId: 'coder',
      layer: 'global',
      name: 'github',
      enabled: false,
      force: undefined,
    })
    expect(u.toggles().every((call) => call.layer !== 'auto')).toBe(true)
    // disabled row → enable, through ITS layer
    u.clickTestId('mcp-toggle-postgres')
    await waitFor(() => expect(u.toggles()).toHaveLength(2))
    expect(u.toggles()[1].layer).toBe('agent')
    expect(u.toggles()[1].name).toBe('postgres')
    expect(u.toggles()[1].enabled).toBe(true)
    // the MCP tab is file-backed: no draft path was ever written
    expect(u.writes).toHaveLength(0)
  })

  test('the toggle is disabled while its write is pending', async () => {
    let releaseToggle: (response: MCPToggleResponse) => void = () => undefined
    const pendingToggle = new Promise<MCPToggleResponse>((resolve) => {
      releaseToggle = resolve
    })
    const u = await ready(
      setup({
        handler: (call) => (call.kind === 'toggle' ? pendingToggle : INVENTORY),
      }),
    )
    u.clickTestId('mcp-toggle-github')
    await waitFor(() => {
      expect((u.need('mcp-toggle-github') as HTMLButtonElement).disabled).toBe(true)
    })
    releaseToggle(TOGGLE_OK)
    await waitFor(() => {
      expect((u.need('mcp-toggle-github') as HTMLButtonElement).disabled).toBe(false)
    })
  })

  test('a 409 mcp_entry_shadowed reveals the error + force button; force retries force=true', async () => {
    let toggleCount = 0
    const u = await ready(
      setup({
        handler: (call) => {
          if (call.kind === 'toggle') {
            toggleCount++
            if (toggleCount === 1) {
              throw new ApiError(
                'shadowed by layer "project"; retry with force=true',
                409,
                'mcp_entry_shadowed',
              )
            }
            return TOGGLE_OK
          }
          return INVENTORY
        },
      }),
    )
    u.clickTestId('mcp-toggle-github')
    // the error surfaces ON the affected row, with the force affordance
    await waitFor(() => expect(u.byTestId('mcp-force-github')).toBeTruthy())
    expect(u.need('mcp-row-error-github').textContent).toContain('shadowed by layer "project"')
    u.clickTestId('mcp-force-github')
    await waitFor(() => expect(u.toggles()).toHaveLength(2))
    // same row/layer/enabled, now forced
    expect(u.toggles()[1].layer).toBe('global')
    expect(u.toggles()[1].name).toBe('github')
    expect(u.toggles()[1].enabled).toBe(false)
    expect(u.toggles()[1].force).toBe(true)
    // once the forced write succeeds the conflict banner is gone
    await waitFor(() => expect(u.byTestId('mcp-force-github')).toBeNull())
  })

  test('a generic failure shows the message but no force button', async () => {
    const u = await ready(
      setup({
        handler: (call) => {
          if (call.kind === 'toggle') {
            throw new ApiError('boom: disk full', 400, 'invalid_layer')
          }
          return INVENTORY
        },
      }),
    )
    u.clickTestId('mcp-toggle-postgres')
    await waitFor(() => expect(u.byTestId('mcp-row-error-postgres')).toBeTruthy())
    expect(u.need('mcp-row-error-postgres').textContent).toContain('boom: disk full')
    expect(u.byTestId('mcp-force-postgres')).toBeNull()
  })
})

describe('AgentMCPSection — layers', () => {
  test('a layer without path renders the disabled/no-root hint', async () => {
    const u = await ready(setup())
    expect(u.byTestId('mcp-layer-project')).toBeTruthy()
    expect(u.byTestId('mcp-layer-path-project')).toBeNull()
    expect(u.need('mcp-layer-disabled-project').textContent).toBe(tr('mcp.layers.disabled'))
    // layers WITH a path show it, plus existence and aliases
    expect(u.need('mcp-layer-path-global').textContent).toBe('/home/u/.lele/mcp.json')
    expect(u.need('mcp-layer-exists-global').textContent).toBe(tr('mcp.layers.exists'))
    expect(u.need('mcp-layer-exists-agent').textContent).toBe(tr('mcp.layers.missing'))
    expect(u.need('mcp-layer-aliased-agent').textContent).toContain(
      tr('mcp.layers.aliasedWith', { layers: tr('mcp.layer.project') }),
    )
  })
})

describe('AgentMCPSection — invalid rows', () => {
  test('an invalid row shows its message and is still toggleable', async () => {
    const u = await ready(setup())
    expect(u.need('mcp-invalid-broken').textContent).toContain('missing "command" field')
    const button = u.need('mcp-toggle-broken') as HTMLButtonElement
    expect(button.disabled).toBe(false)
    fireEvent.click(button)
    await waitFor(() => expect(u.toggles()).toHaveLength(1))
    expect(u.toggles()[0].layer).toBe('project')
    expect(u.toggles()[0].name).toBe('broken')
    // invalid ≠ enabled: the toggle switches it on
    expect(u.toggles()[0].enabled).toBe(true)
  })
})

describe('AgentMCPSection — warnings', () => {
  test('inventory warnings render as a dismissible list', async () => {
    const u = await ready(setup())
    expect(u.need('mcp-warnings').textContent).toContain(
      'project layer disabled: no workspace root',
    )
    u.clickTestId('mcp-warning-dismiss-0')
    expect(u.byTestId('mcp-warnings')).toBeNull()
  })
})

describe('AgentMCPSection — loading / empty / error', () => {
  test('loading: aria-busy block with sr-only text (no spinner)', async () => {
    let release: (inventory: MCPInventoryResponse) => void = () => undefined
    const gate = new Promise<MCPInventoryResponse>((resolve) => {
      release = resolve
    })
    const u = setup({ handler: () => gate })
    const status = u.container.querySelector('[aria-busy="true"]') as HTMLElement
    expect(status).toBeTruthy()
    expect(status.textContent).toContain(tr('mcp.loading'))
    expect(u.byTestId('mcp-rows')).toBeNull()
    release(INVENTORY)
    await waitFor(() => expect(u.byTestId('mcp-rows')).toBeTruthy())
  })

  test('empty inventory → dashed empty state, no rows', async () => {
    const u = setup({ inventory: { agent_id: 'coder' } })
    await waitFor(() => expect(u.byTestId('mcp-empty')).toBeTruthy())
    expect(u.container.textContent).toContain(tr('mcp.empty.title'))
    expect(u.byTestId('mcp-rows')).toBeNull()
    expect(u.container.querySelector('[data-testid="mcp-filter"]')).toBeNull()
  })

  test('load error → retryable banner; retry recovers', async () => {
    let failing = true
    const u = setup({
      handler: (call) => {
        if (call.kind === 'inventory' && failing) throw new Error('boom')
        return call.kind === 'toggle' ? TOGGLE_OK : INVENTORY
      },
    })
    // the inventory query retries once internally, so allow for that cycle
    await waitFor(() => expect(u.byTestId('mcp-retry')).toBeTruthy(), { timeout: 4000 })
    expect(u.container.textContent).toContain(tr('mcp.loadError'))
    failing = false
    u.clickTestId('mcp-retry')
    await waitFor(() => expect(u.byTestId('mcp-rows')).toBeTruthy(), { timeout: 4000 })
  })
})
