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
  MCPLayerName,
  MCPQueryLayer,
  MCPRawFileResponse,
  MCPServerSummary,
  MCPToggleResponse,
} from '../../../lib/mcpTypes'
import type { EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { ApiError } from '../../../services/http/errors'
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { AgentMCPSection } from './AgentMCPSection'

afterEach(() => {
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

const RAW_GLOBAL: MCPRawFileResponse = {
  layer: 'global',
  path: '/home/u/.lele/mcp.json',
  exists: true,
  content: '{\n  "mcpServers": {}\n}',
}

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

type FakeCall =
  | { kind: 'inventory'; agentId: string }
  | { kind: 'raw'; agentId: string; layer: MCPLayerName }
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
) =>
  | MCPInventoryResponse
  | MCPToggleResponse
  | MCPRawFileResponse
  | Promise<MCPInventoryResponse | MCPToggleResponse | MCPRawFileResponse>

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
    options.handler ??
    ((call) => (call.kind === 'toggle' ? TOGGLE_OK : call.kind === 'raw' ? RAW_GLOBAL : inventory))

  const calls: FakeCall[] = []
  const authApi = {
    getToken: () => 'token-test',
    mcpInventory: async (agentId: string) => {
      const call: FakeCall = { kind: 'inventory', agentId }
      calls.push(call)
      return handler(call) as MCPInventoryResponse
    },
    mcpRaw: async (agentId: string, layer: MCPLayerName) => {
      const call: FakeCall = { kind: 'raw', agentId, layer }
      calls.push(call)
      return handler(call) as MCPRawFileResponse
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
    expect(u.byTestId('mcp-state-github')?.textContent).toBe('●')
    expect(u.byTestId('mcp-state-postgres')?.textContent).toBe('○')
    expect(u.byTestId('mcp-state-broken')?.textContent).toBe('⚠')
    expect(u.byTestId('mcp-layer-tag-github')?.textContent).toBe(tr('mcp.layer.global'))
    expect(u.byTestId('mcp-layer-tag-postgres')?.textContent).toBe(tr('mcp.layer.agent'))
    expect(u.byTestId('mcp-command-github')?.textContent).toBe('npx ${TOOLS_DIR}/mcp-github')
    expect(u.byTestId('mcp-args-github')?.textContent).toBe(tr('mcp.row.args', { n: 2 }))
    expect(u.byTestId('mcp-args-postgres')).toBeNull()
    expect(u.byTestId('mcp-url-wiki')?.textContent).toBe('https://mcp.example.com/sse')
    const shadow = u.need('mcp-shadow-github-agent')
    expect(shadow.textContent).toContain(tr('mcp.layer.agent'))
    expect(shadow.textContent).toContain('shadowed by layer "global"')
  })

  test('env/header key NAMES render as chips; no VALUE text ever renders', async () => {
    const u = await ready(setup())
    expect(u.need('mcp-envkey-github-GITHUB_TOKEN').textContent).toBe('GITHUB_TOKEN')
    expect(u.need('mcp-headerkey-wiki-Authorization').textContent).toBe('Authorization')
    expect(u.container.textContent).not.toContain('ghp_LEAKED_SECRET_VALUE')
    expect(u.container.textContent).not.toContain('Bearer LEAKED_BEARER_TOKEN')
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
    u.clickTestId('mcp-toggle-postgres')
    await waitFor(() => expect(u.toggles()).toHaveLength(2))
    expect(u.toggles()[1].layer).toBe('agent')
    expect(u.toggles()[1].name).toBe('postgres')
    expect(u.toggles()[1].enabled).toBe(true)
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
    await waitFor(() => expect(u.byTestId('mcp-force-github')).toBeTruthy())
    expect(u.need('mcp-row-error-github').textContent).toContain('shadowed by layer "project"')
    u.clickTestId('mcp-force-github')
    await waitFor(() => expect(u.toggles()).toHaveLength(2))
    expect(u.toggles()[1].layer).toBe('global')
    expect(u.toggles()[1].name).toBe('github')
    expect(u.toggles()[1].enabled).toBe(false)
    expect(u.toggles()[1].force).toBe(true)
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
    expect(u.need('mcp-layer-path-global').textContent).toBe('/home/u/.lele/mcp.json')
    expect(u.need('mcp-layer-exists-global').textContent).toBe(tr('mcp.layers.exists'))
    expect(u.need('mcp-layer-exists-agent').textContent).toBe(tr('mcp.layers.missing'))
    expect(u.need('mcp-layer-aliased-agent').textContent).toContain(
      tr('mcp.layers.aliasedWith', { layers: tr('mcp.layer.project') }),
    )
  })
})

describe('AgentMCPSection — invalid rows', () => {
  test('an invalid row offers no working toggle and points at the raw editor', async () => {
    const u = await ready(setup())
    expect(u.need('mcp-invalid-broken').textContent).toContain('missing "command" field')
    const toggleBtn = u.need('mcp-toggle-broken') as HTMLButtonElement
    expect(toggleBtn.disabled).toBe(true)
    expect(toggleBtn.title).toBe(tr('mcp.toggle.invalidHint'))
    expect(u.need('mcp-repair-hint-broken').textContent).toBe(tr('mcp.toggle.invalidHint'))
    fireEvent.click(toggleBtn)
    expect(u.toggles()).toHaveLength(0)
    const repair = u.need('mcp-repair-broken') as HTMLButtonElement
    expect(repair.disabled).toBe(true)
    expect(repair.title).toBe(tr('mcp.editor.noPath'))
  })

  test('an invalid row on a real layer is one click from its raw file', async () => {
    const u = await ready(
      setup({
        inventory: {
          ...INVENTORY,
          servers: [
            ...(INVENTORY.servers ?? []),
            {
              name: 'badtool',
              layer: 'global',
              effective: 'invalid',
              defines: true,
              server: { command: 'node ${BIN}/x.js', invalid: 'unknown field "comand"' },
            },
          ],
        },
      }),
    )
    const repair = u.need('mcp-repair-badtool') as HTMLButtonElement
    expect(repair.disabled).toBe(false)
    fireEvent.click(repair)
    await waitFor(() => expect(u.calls.filter((call) => call.kind === 'raw')).toHaveLength(1))
    expect(u.calls.filter((call) => call.kind === 'raw')[0]).toEqual({
      kind: 'raw',
      agentId: 'coder',
      layer: 'global',
    })
    expect(u.byTestId('mcp-editor')).toBeTruthy()
    expect(u.toggles()).toHaveLength(0)
  })

  test('a toggle answered changed:false shows the no-change banner; the verdict does not move', async () => {
    const NOCHANGE: MCPToggleResponse = {
      ...TOGGLE_OK,
      name: 'postgres',
      enabled: true,
      changed: false,
      effective: 'disabled',
      effective_layer: 'agent',
    }
    const u = await ready(
      setup({ handler: (call) => (call.kind === 'toggle' ? NOCHANGE : INVENTORY) }),
    )
    u.clickTestId('mcp-toggle-postgres')
    await waitFor(() => expect(u.byTestId('mcp-row-nochange-postgres')).toBeTruthy())
    const banner = u.need('mcp-row-nochange-postgres')
    expect(banner.textContent).toContain(tr('mcp.toggle.noChange'))
    expect(u.need('mcp-row-nochange-postgres-code').textContent).toBe('changed:false')
    expect(u.need('mcp-state-postgres')?.textContent).toBe('○')
    expect(u.byTestId('mcp-row-error-postgres')).toBeNull()
  })
})

describe('AgentMCPSection — no-change banner (negative)', () => {
  test('a toggle answered changed:true renders NO banner; verdict comes from the inventory refetch', async () => {
    let inventoryCalls = 0
    const AFTER_DISABLE: MCPInventoryResponse = {
      ...INVENTORY,
      servers: (INVENTORY.servers ?? []).map((row) =>
        row.name === 'github' ? { ...row, effective: 'disabled' as const } : row,
      ),
    }
    const u = await ready(
      setup({
        handler: (call) => {
          if (call.kind === 'toggle') return TOGGLE_OK
          inventoryCalls++
          return inventoryCalls > 1 ? AFTER_DISABLE : INVENTORY
        },
      }),
    )
    u.clickTestId('mcp-toggle-github')
    await waitFor(() => expect(u.toggles()).toHaveLength(1))
    await waitFor(() => expect(u.calls.filter((call) => call.kind === 'inventory')).toHaveLength(2))
    await waitFor(() => expect(u.need('mcp-state-github')?.textContent).toBe('○'))
    expect(u.byTestId('mcp-row-nochange-github')).toBeNull()
    expect(u.container.querySelector('[data-testid^="mcp-row-nochange-"]')).toBeNull()
    expect(u.byTestId('mcp-row-error-github')).toBeNull()
  })

  test('a stale changed:false pairing (other name) renders nothing: pending and error guards', async () => {
    let toggleCount = 0
    let rejectSecond: (error: unknown) => void = () => undefined
    const secondToggle = new Promise<MCPToggleResponse>((_resolve, reject) => {
      rejectSecond = reject
    })
    const u = await ready(
      setup({
        handler: (call) => {
          if (call.kind === 'toggle') {
            toggleCount++
            if (toggleCount === 1) return { ...TOGGLE_OK, name: 'wiki', changed: false }
            return secondToggle
          }
          return INVENTORY
        },
      }),
    )
    u.clickTestId('mcp-toggle-wiki')
    await waitFor(() => expect(u.byTestId('mcp-row-nochange-wiki')).toBeTruthy())
    u.clickTestId('mcp-toggle-postgres')
    await waitFor(() => expect(u.toggles()).toHaveLength(2))
    await waitFor(() =>
      expect((u.need('mcp-toggle-postgres') as HTMLButtonElement).disabled).toBe(true),
    )
    expect(u.byTestId('mcp-row-nochange-postgres')).toBeNull()
    expect(u.byTestId('mcp-row-nochange-wiki')).toBeNull()
    rejectSecond(new ApiError('boom: disk full', 400, 'invalid_layer'))
    await waitFor(() => expect(u.byTestId('mcp-row-error-postgres')).toBeTruthy())
    expect(u.byTestId('mcp-row-nochange-postgres')).toBeNull()
    expect(u.byTestId('mcp-row-nochange-wiki')).toBeNull()
    expect(u.container.querySelector('[data-testid^="mcp-row-nochange-"]')).toBeNull()
  })

  test('a subsequent successful toggle clears the banner (no sticky status)', async () => {
    let toggleCount = 0
    const u = await ready(
      setup({
        handler: (call) => {
          if (call.kind === 'toggle') {
            toggleCount++
            return { ...TOGGLE_OK, name: 'wiki', changed: toggleCount !== 1 }
          }
          return INVENTORY
        },
      }),
    )
    u.clickTestId('mcp-toggle-wiki')
    await waitFor(() => expect(u.byTestId('mcp-row-nochange-wiki')).toBeTruthy())
    u.clickTestId('mcp-toggle-wiki')
    await waitFor(() => expect(u.toggles()).toHaveLength(2))
    await waitFor(() => expect(u.byTestId('mcp-row-nochange-wiki')).toBeNull())
    expect(u.container.querySelector('[data-testid^="mcp-row-nochange-"]')).toBeNull()
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
    await waitFor(() => expect(u.byTestId('mcp-retry')).toBeTruthy(), { timeout: 4000 })
    expect(u.container.textContent).toContain(tr('mcp.loadError'))
    failing = false
    u.clickTestId('mcp-retry')
    await waitFor(() => expect(u.byTestId('mcp-rows')).toBeTruthy(), { timeout: 4000 })
  })
})
