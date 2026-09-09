import '../../../test/setup'
import { beforeEach, describe, expect, test } from 'bun:test'
import { act, cleanup, render, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import '../../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../../contexts/AuthContext'
import { useSettings } from '../../../contexts/SettingsContext'
import type { ApiClient } from '../../../services/http/client'
import { AgentEntityLayout } from './AgentEntityLayout'

// Route probes standing in for the real pages (AgentsListPage /
// AgentConfigPage) so the layout contract is tested without depending on them.

function ListProbe() {
  return <div data-testid="probe-list" />
}

function DetailProbe() {
  // Read the draft through the SettingsProvider the layout mounted: proves
  // children see the layout's settings state, not their own instance.
  const settings = useSettings()
  return (
    <div data-testid="probe-detail">{JSON.stringify({ hasDraft: !!settings.draftConfig })}</div>
  )
}

let navigateRef: ((to: string) => void) | null = null
function NavigateCapture() {
  navigateRef = useNavigate()
  return null
}

// Count config fetches to prove useSettingsConfig() is instantiated once for
// the whole agents route subtree and is NOT remounted on child navigation.
function makeApi(counters: { config: number }): ApiClient {
  return {
    config: async () => {
      counters.config++
      return {
        config: { agents: { list: [{ id: 'coder', name: 'Coder' }] } },
        meta: {
          config_path: '/tmp/config.json',
          source: 'file',
          can_save: true,
          restart_required_sections: [],
          secrets_by_path: {},
        },
      }
    },
    models: async () => ({ models: [], model_groups: [] }),
    validateConfig: async () => ({ valid: true }),
    saveConfig: async () => ({ config: {}, meta: {} }),
  } as unknown as ApiClient
}

const logicValue = {
  sessions: [],
  currentSessionKey: null,
  parentSessionKey: null,
  processingSessions: new Set<string>(),
  chatMode: 'agent',
  groupsEnabled: false,
  sidebarOpen: true,
  mobileSidebarOpen: false,
  onCreateSession: () => undefined,
  onDeleteSession: () => undefined,
  onToggleSidebar: () => undefined,
  onSelectMode: () => undefined,
  onSelectSession: () => undefined,
  onLogout: async () => undefined,
  onCloseMobileSidebar: () => undefined,
  onOpenMobileSidebar: () => undefined,
} as unknown as AppLogicContextValue

function renderLayout(initialEntry: string, counters: { config: number }) {
  const authValue = {
    api: makeApi(counters),
    apiUrl: 'http://localhost',
    session: { token: 't', client_id: 'c', device_name: 'd', expires: '' },
  } as unknown as AuthContextValue

  const tree = (children: ReactNode) => (
    <AuthContext.Provider value={authValue}>
      <AppLogicContext.Provider value={logicValue}>
        <MemoryRouter initialEntries={[initialEntry]}>{children}</MemoryRouter>
      </AppLogicContext.Provider>
    </AuthContext.Provider>
  )

  return render(
    tree(
      <>
        <NavigateCapture />
        <Routes>
          <Route path="/agents" element={<AgentEntityLayout />}>
            <Route index element={<ListProbe />} />
            <Route path=":agentId" element={<DetailProbe />} />
            <Route path=":agentId/:tab" element={<DetailProbe />} />
          </Route>
        </Routes>
      </>,
    ),
  )
}

beforeEach(() => {
  cleanup()
  navigateRef = null
})

describe('AgentEntityLayout', () => {
  test('renders the index child through <Outlet/> at /agents with layout chrome', async () => {
    const view = renderLayout('/agents', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-list')).not.toBeNull())
    // Chrome: Sidebar <aside> + SettingsHeader title (es locale for
    // 'sidebar.agents') + SettingsFooter save button. The label appears in
    // both the h1 and the sidebar nav row, hence getAllByText.
    expect(view.container.querySelector('aside')).not.toBeNull()
    expect(view.getAllByText('Agentes').length).toBeGreaterThan(0)
    expect(view.getByText('Guardar')).not.toBeNull()
  })

  test('renders nested :agentId/:tab children through <Outlet/>', async () => {
    const view = renderLayout('/agents/coder/tools', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-detail')).not.toBeNull())
    expect(view.queryByTestId('probe-list')).toBeNull()
  })

  test('useSettingsConfig is instantiated once and survives list→detail navigation', async () => {
    const counters = { config: 0 }
    const view = renderLayout('/agents', counters)
    await waitFor(() => expect(view.getByTestId('probe-list')).not.toBeNull())
    await waitFor(() => expect(counters.config).toBe(1))

    // Client-side navigation to a sibling route must NOT remount the layout:
    // a remount would re-run useSettingsConfig's load and silently drop the
    // unsaved draft — the core reason the hook lives in the layout, not in
    // the pages.
    act(() => {
      if (!navigateRef) throw new Error('navigate not captured')
      navigateRef('/agents/coder/tools')
    })

    await waitFor(() => expect(view.getByTestId('probe-detail')).not.toBeNull())
    expect(counters.config).toBe(1)
    expect(view.getByTestId('probe-detail').textContent).toContain('"hasDraft":true')
  })

  test('defaults the header title to sidebar.agents', async () => {
    const view = renderLayout('/agents', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-list')).not.toBeNull())
    // es locale: 'sidebar.agents' → 'Agentes' (h1 of SettingsHeader).
    const h1 = view.container.querySelector('h1')
    expect(h1?.textContent).toBe('Agentes')
  })
})
