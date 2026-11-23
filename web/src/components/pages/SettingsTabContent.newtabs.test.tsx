import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import '../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { SettingsProvider } from '../../contexts/SettingsContext'
import type { MCPInventoryResponse, MCPServerSummary } from '../../lib/mcpTypes'
import type { EditableAgentConfig } from '../../lib/types'
import type { ApiClient } from '../../services/http/client'
import { makeState } from '../../test/agentsHarness'
import { SettingsTabContent } from './SettingsTabContent'

afterEach(() => {
  cleanup()
})

const INVENTORY: MCPInventoryResponse = {
  agent_id: 'coder',
  layers: [{ layer: 'global', path: '/home/u/.lele/mcp.json', exists: true }],
  servers: [
    {
      name: 'github',
      layer: 'global',
      path: '/home/u/.lele/mcp.json',
      effective: 'enabled',
      defines: true,
      server: { command: 'npx mcp-github' } as MCPServerSummary,
    },
  ],
  warnings: [],
}

const AGENTS: EditableAgentConfig[] = [{ id: 'coder', name: 'Coder', default: true }]

const DEFAULTS = { subagent_timeout_minutes: 30, subagent_retention_minutes: 5 }

const authApi = {
  getToken: () => 'token-test',
  mcpInventory: async () => INVENTORY,
  mcpRaw: async () => ({ layer: 'global', path: '/home/u/.lele/mcp.json', exists: false }),
} as unknown as ApiClient

const authValue = {
  api: authApi,
  apiUrl: 'http://127.0.0.1:18793',
  session: null,
  setApiUrl: () => {},
  persistSession: () => {},
  handleAuth: async () => null,
  ensureSession: async () => null,
  isLoading: false,
} as unknown as AuthContextValue

const settingsApi = {
  models: async () => ({ models: [], model_groups: [] }),
  skills: async () => ({ skills: [] }),
  tools: async () => ({ tools: [] }),
  agentFiles: async () => ({ files: [] }),
} as unknown as ApiClient

function renderTab(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const state = makeState(AGENTS, { defaults: DEFAULTS })
  return render(
    <AuthContext.Provider value={authValue}>
      <QueryClientProvider client={queryClient}>
        <SettingsProvider settingsState={state} api={settingsApi}>
          <MemoryRouter initialEntries={[path]}>
            <Routes>
              <Route path="/settings/:tab" element={<SettingsTabContent />} />
            </Routes>
          </MemoryRouter>
        </SettingsProvider>
      </QueryClientProvider>
    </AuthContext.Provider>,
  )
}

describe('SettingsTabContent — new tabs', () => {
  test('the subagents tab renders SubagentsSettings', async () => {
    const view = renderTab('/settings/subagents')
    await waitFor(() => expect(view.getByTestId('subagents-settings-page')).not.toBeNull())
    expect(view.getByTestId('subagents-open-agent')).not.toBeNull()
  })

  test('the mcp tab renders MCPSettings', async () => {
    const view = renderTab('/settings/mcp')
    await waitFor(() => expect(view.getByTestId('mcp-settings-page')).not.toBeNull())
    expect(view.container.querySelector('#mcp-agent-select')).not.toBeNull()
  })
})
