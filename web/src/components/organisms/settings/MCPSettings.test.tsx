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
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { MCPSettings } from './MCPSettings'

afterEach(() => {
  cleanup()
})

const AUTH_API_URL = 'http://127.0.0.1:18793'

const INVENTORY: MCPInventoryResponse = {
  agent_id: 'coder',
  layers: [
    { layer: 'global', path: '/home/u/.lele/mcp.json', exists: true },
    { layer: 'agent', path: '/home/u/.lele/workspace-coder/mcp.json', exists: false },
  ],
  servers: [
    {
      name: 'github',
      layer: 'global',
      path: '/home/u/.lele/mcp.json',
      effective: 'enabled',
      defines: true,
      server: { command: 'npx ${TOOLS_DIR}/mcp-github', args: 2 } as MCPServerSummary,
    },
    {
      name: 'postgres',
      layer: 'agent',
      effective: 'disabled',
      defines: true,
      server: { command: 'docker', args: 0 },
    },
  ],
  warnings: [],
}

const RAW_GLOBAL: MCPRawFileResponse = {
  layer: 'global',
  path: '/home/u/.lele/mcp.json',
  exists: true,
  content: '{\n  "mcpServers": {}\n}',
}

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

type FakeCall =
  | { kind: 'inventory'; agentId: string }
  | { kind: 'raw'; agentId: string; layer: MCPLayerName }
  | { kind: 'toggle'; agentId: string; layer: MCPQueryLayer; name: string; enabled: boolean }

const CODER: EditableAgentConfig = { id: 'coder', name: 'Coder' }
const RESEARCHER: EditableAgentConfig = { id: 'researcher', name: 'Researcher', default: true }

function setup(
  agents: EditableAgentConfig[] = [CODER, RESEARCHER],
  options: { remoteList?: EditableAgentConfig[] } = {},
) {
  const writes: Write[] = []
  const calls: FakeCall[] = []

  const authApi = {
    getToken: () => 'token-test',
    mcpInventory: async (agentId: string) => {
      calls.push({ kind: 'inventory', agentId })
      return INVENTORY
    },
    mcpRaw: async (agentId: string, layer: MCPLayerName) => {
      calls.push({ kind: 'raw', agentId, layer })
      return RAW_GLOBAL
    },
    mcpToggle: async (agentId: string, layer: MCPQueryLayer, name: string, enabled: boolean) => {
      calls.push({ kind: 'toggle', agentId, layer, name, enabled })
      return TOGGLE_OK
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

  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  const build = (list: EditableAgentConfig[], remoteList?: EditableAgentConfig[]) => (
    <AuthContext.Provider value={authValue}>
      <QueryClientProvider client={queryClient}>
        <SettingsProvider settingsState={makeState(list, { writes, remoteList })} api={settingsApi}>
          <MemoryRouter initialEntries={['/settings/mcp']}>
            <MCPSettings />
          </MemoryRouter>
        </SettingsProvider>
      </QueryClientProvider>
    </AuthContext.Provider>
  )

  const utils = render(build(agents, options.remoteList))
  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
  const need = (id: string) => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    return el
  }
  const selectOf = () => utils.container.querySelector('#mcp-agent-select') as HTMLSelectElement
  const inventories = () =>
    calls.filter(
      (call): call is Extract<FakeCall, { kind: 'inventory' }> => call.kind === 'inventory',
    )
  const toggles = () =>
    calls.filter((call): call is Extract<FakeCall, { kind: 'toggle' }> => call.kind === 'toggle')

  return {
    ...utils,
    calls,
    writes,
    byTestId,
    need,
    selectOf,
    inventories,
    toggles,
    rerenderAgents: (list: EditableAgentConfig[], remoteList?: EditableAgentConfig[]) =>
      utils.rerender(build(list, remoteList)),
  }
}

async function ready(u: ReturnType<typeof setup>) {
  await waitFor(() => expect(u.byTestId('mcp-settings-page')).not.toBeNull())
  await waitFor(() => expect(u.byTestId('mcp-rows')).not.toBeNull())
  return u
}

describe('MCPSettings — agent selection', () => {
  test('defaults to the agent marked default and loads its inventory', async () => {
    const u = await ready(setup())
    expect(u.selectOf().value).toBe('researcher')
    expect(u.inventories()[0]).toEqual({ kind: 'inventory', agentId: 'researcher' })
  })

  test('falls back to the first agent when none is marked default', async () => {
    const u = await ready(setup([CODER, { id: 'researcher', name: 'Researcher' }]))
    expect(u.selectOf().value).toBe('coder')
    expect(u.inventories()[0]).toEqual({ kind: 'inventory', agentId: 'coder' })
  })

  test('switching agents reloads the inventory for the new id', async () => {
    const u = await ready(setup())
    fireEvent.change(u.selectOf(), { target: { value: 'coder' } })
    await waitFor(() => expect(u.byTestId('mcp-row-github')).not.toBeNull())
    expect(u.selectOf().value).toBe('coder')
    expect(u.inventories().some((call) => call.agentId === 'coder')).toBe(true)
  })

  test('empty agent list offers the implicit main agent', async () => {
    const u = await ready(setup([]))
    expect(u.selectOf().value).toBe('main')
    expect(u.selectOf().options).toHaveLength(1)
    expect(u.inventories()[0]).toEqual({ kind: 'inventory', agentId: 'main' })
    expect(u.container.textContent).toContain(tr('settings.mcpPage.implicitMain'))
  })

  test('a deleted selection falls back to the default agent', async () => {
    const u = await ready(setup())
    fireEvent.change(u.selectOf(), { target: { value: 'coder' } })
    expect(u.selectOf().value).toBe('coder')

    u.rerenderAgents([RESEARCHER])
    expect(u.selectOf().value).toBe('researcher')
  })
})

describe('MCPSettings — server management', () => {
  test('toggling a server never writes to the config draft', async () => {
    const u = await ready(setup())
    fireEvent.click(u.need('mcp-toggle-github'))
    await waitFor(() => expect(u.toggles()).toHaveLength(1))
    expect(u.toggles()[0]).toEqual({
      kind: 'toggle',
      agentId: 'researcher',
      layer: 'global',
      name: 'github',
      enabled: false,
    })
    expect(u.writes).toHaveLength(0)
  })

  test('a draft-only agent shows the unsaved banner', async () => {
    const u = await ready(
      setup([{ id: 'newbie', name: 'Newbie', default: true }], { remoteList: [] }),
    )
    expect(u.byTestId('mcp-unsaved-agent')).not.toBeNull()
  })

  test('renders the layers intro text', async () => {
    const u = await ready(setup())
    expect(u.container.textContent).toContain(tr('settings.mcpPage.intro'))
    expect(u.byTestId('mcp-unsaved-agent')).toBeNull()
  })
})
