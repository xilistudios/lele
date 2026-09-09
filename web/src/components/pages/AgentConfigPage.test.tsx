import '../../test/setup'
import { describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import '../../test/i18n'
import { SettingsProvider } from '../../contexts/SettingsContext'
import type { AgentCatalogResponse, EditableAgentConfig } from '../../lib/types'
import type { ApiClient } from '../../services/http/client'
import { type Write, autoCleanup, makeState, settle, tr } from '../../test/agentsHarness'
import { AgentConfigPage } from './AgentConfigPage'

/**
 * `/agents/:agentId/:tab?` (spec §4.1, §5.1, §5.2, §5.3). The page is pure
 * composition, so the tests only cover what it owns: URL → tab normalisation,
 * the "unknown agent" block (which must NOT appear while the config is still
 * loading), which section a tab renders, the tabpanel wiring, and the
 * modified badge.
 */

const CODER: EditableAgentConfig = {
  id: 'coder',
  name: 'Coder',
  workspace: '~/.lele/workspace-coder',
}

const CATALOG: AgentCatalogResponse = {
  agent_id: 'coder',
  tools: [{ name: 'exec', description: 'Run a shell command' }],
  skills: [
    {
      name: 'memory',
      description: 'Memory files',
      source: 'builtin',
      enabled: true,
      deletable: false,
    },
  ],
  workspace: '/home/u/.lele/workspace-coder',
}

const api = {
  models: async () => ({ models: ['gpt-4o'], model_groups: [] }),
  getAgentCatalog: async () => CATALOG,
  agentFiles: async () => ({ files: [] }),
} as unknown as ApiClient

function setup(
  entry: string,
  options: {
    agents?: EditableAgentConfig[]
    isLoading?: boolean
    dirtyPaths?: string[]
  } = {},
) {
  const writes: Write[] = []
  const agents = options.agents ?? [CODER]
  const state = makeState(agents, {
    writes,
    isLoading: options.isLoading,
    dirtyPaths: options.dirtyPaths,
    defaultsModel: 'gpt-4o',
  })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  /** Every location the router visits — how "did it redirect?" is observed. */
  const visited: string[] = []
  function Probe() {
    visited.push(useLocation().pathname)
    return null
  }

  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={state} api={api}>
        <MemoryRouter initialEntries={[entry]}>
          <Probe />
          <Routes>
            <Route path="/agents" element={<div data-testid="agents-list" />} />
            <Route path="/agents/:agentId" element={<AgentConfigPage />} />
            <Route path="/agents/:agentId/:tab" element={<AgentConfigPage />} />
          </Routes>
        </MemoryRouter>
      </SettingsProvider>
    </QueryClientProvider>,
  )

  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
  return { ...utils, writes, visited, byTestId }
}

autoCleanup()

describe('AgentConfigPage — tab normalisation (§5.1)', () => {
  test('a bare /agents/:agentId is replaced by …/general', async () => {
    const u = setup('/agents/coder')
    await settle(120)
    expect(u.visited[u.visited.length - 1]).toBe('/agents/coder/general')
  })

  test('an unknown tab is replaced by …/general instead of rendering a broken page', async () => {
    const u = setup('/agents/coder/not-a-tab')
    await settle(120)
    expect(u.visited[u.visited.length - 1]).toBe('/agents/coder/general')
    expect(u.byTestId('agent-config-page')).toBeTruthy()
  })

  test('a valid tab is left alone (no redirect loop)', async () => {
    const u = setup('/agents/coder/tools')
    await settle(120)
    expect(u.visited).toEqual(['/agents/coder/tools'])
  })

  test('clicking a tab navigates to its route', async () => {
    const u = setup('/agents/coder/general')
    await settle(120)
    fireEvent.click(u.getByRole('tab', { name: tr('settings.agentPage.tab.model') }))
    expect(u.visited[u.visited.length - 1]).toBe('/agents/coder/model')
  })
})

describe('AgentConfigPage — unknown agent (§5.2)', () => {
  test('while the config loads it shows a skeleton, never a 404', () => {
    const u = setup('/agents/coder/general', { isLoading: true })
    expect(u.byTestId('agent-config-loading')).toBeTruthy()
    expect(u.byTestId('agent-config-loading')?.getAttribute('role')).toBe('status')
    expect(u.byTestId('agent-config-loading')?.getAttribute('aria-busy')).toBe('true')
    expect(u.byTestId('agent-config-not-found')).toBeNull()
  })

  test('after loading, an id missing from agents.list renders the not-found block', async () => {
    const u = setup('/agents/ghost/general', { isLoading: false })
    await settle(120)
    const block = u.byTestId('agent-config-not-found')
    expect(block).toBeTruthy()
    expect(block?.textContent).toContain('ghost')
    expect(block?.textContent).toContain(tr('settings.agentPage.notFoundDesc'))
    // No automatic redirect: the user must be able to read the URL that failed.
    expect(u.visited[u.visited.length - 1]).toBe('/agents/ghost/general')
  })

  test('the not-found block offers a way back to the list', async () => {
    const u = setup('/agents/ghost/general')
    await settle(120)
    fireEvent.click(u.getByRole('button', { name: tr('settings.agentPage.backToList') }))
    expect(u.visited[u.visited.length - 1]).toBe('/agents')
  })
})

describe('AgentConfigPage — section dispatch (§4.1)', () => {
  test('general renders the general section', async () => {
    const u = setup('/agents/coder/general')
    await settle(120)
    expect(u.byTestId('agent-id-field')).toBeTruthy()
  })

  test('model renders the model section', async () => {
    const u = setup('/agents/coder/model')
    await settle(120)
    expect(u.container.textContent).toContain(tr('settings.sections.model'))
    expect(u.byTestId('agent-id-field')).toBeNull()
  })

  test('skills renders the skills grid', async () => {
    const u = setup('/agents/coder/skills')
    await settle(200)
    expect(u.byTestId('skills-grid')).toBeTruthy()
  })

  test('tools renders the tools section', async () => {
    const u = setup('/agents/coder/tools')
    await settle(200)
    expect(u.byTestId('tools-all-banner')).toBeTruthy()
  })

  test('subagents renders the delegation toggle', async () => {
    const u = setup('/agents/coder/subagents')
    await settle(120)
    expect(u.container.textContent).toContain(tr('settings.agentPage.subagentsEnable'))
  })

  test('files renders the workspace summary', async () => {
    const u = setup('/agents/coder/files')
    await settle(200)
    expect(u.byTestId('files-empty')).toBeTruthy()
  })
})

describe('AgentConfigPage — wiring', () => {
  test('the content panel is a tabpanel labelled by the selected tab (§6)', async () => {
    const u = setup('/agents/coder/model')
    await settle(120)
    const panel = u.container.querySelector('[role="tabpanel"]')
    expect(panel).toBeTruthy()
    expect(panel?.getAttribute('aria-labelledby')).toBe('agent-tab-model')
    expect(panel?.getAttribute('id')).toBe('agent-tab-panel')
    expect(
      u
        .getByRole('tab', { name: tr('settings.agentPage.tab.model') })
        .getAttribute('aria-selected'),
    ).toBe('true')
  })

  test('the header shows the agent and marks it modified from any dirty path (§5.3)', async () => {
    const clean = setup('/agents/coder/general')
    await settle(120)
    expect(clean.byTestId('badge-modified')).toBeNull()

    const dirty = setup('/agents/coder/general', { dirtyPaths: ['agents.list.0.name'] })
    await settle(120)
    expect(dirty.byTestId('badge-modified')).toBeTruthy()
  })

  test('the header is rendered above the section, not inside its width cap', async () => {
    const u = setup('/agents/coder/skills')
    await settle(200)
    const panel = u.container.querySelector('[role="tabpanel"]')
    expect(panel?.querySelector('header')).toBeTruthy()
  })
})
