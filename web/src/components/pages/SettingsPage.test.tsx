import '../../test/setup'
import { beforeEach, describe, expect, test } from 'bun:test'
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { MemoryRouter, Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { useSettings } from '../../contexts/SettingsContext'
import type { ApiClient } from '../../services/http/client'
import { SettingsPage } from './SettingsPage'

function TabProbe() {
  const settings = useSettings()
  return <div data-testid="probe-tab">{JSON.stringify({ hasDraft: !!settings.draftConfig })}</div>
}

function DetailProbe() {
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

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

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

  return render(
    <AuthContext.Provider value={authValue}>
      <AppLogicContext.Provider value={logicValue}>
        <MemoryRouter initialEntries={[initialEntry]}>
          <NavigateCapture />
          <LocationProbe />
          <Routes>
            <Route path="/settings" element={<SettingsPage />}>
              <Route index element={<Navigate to="general" replace />} />
              <Route path=":tab" element={<TabProbe />} />
              <Route path="agents/:agentId/:tab?" element={<DetailProbe />} />
            </Route>
          </Routes>
        </MemoryRouter>
      </AppLogicContext.Provider>
    </AuthContext.Provider>,
  )
}

beforeEach(() => {
  cleanup()
  navigateRef = null
})

const tabStrip = (container: HTMLElement) =>
  container.querySelector('[data-testid="settings-tabs"]')

describe('SettingsPage — layout & tab strip', () => {
  test('renders the tab strip and the tab content for a settings tab', async () => {
    const view = renderLayout('/settings/providers', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    expect(tabStrip(view.container)).not.toBeNull()
    expect(
      view.container
        .querySelector('[data-testid="settings-tab-providers"]')
        ?.getAttribute('aria-current'),
    ).toBe('page')
  })

  test('every tab entry carries its own icon', async () => {
    const view = renderLayout('/settings/general', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    const buttons = Array.from(tabStrip(view.container)?.querySelectorAll('button') ?? [])
    expect(buttons.length).toBeGreaterThan(0)

    const glyphs = buttons.map((button) => button.querySelector('svg')?.innerHTML ?? null)
    expect(glyphs).not.toContain(null)
    expect(new Set(glyphs).size).toBe(buttons.length)
  })

  test('the agents list is a settings tab: strip visible, Agents highlighted', async () => {
    const view = renderLayout('/settings/agents', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    expect(tabStrip(view.container)).not.toBeNull()
    expect(
      view.container
        .querySelector('[data-testid="settings-tab-agents"]')
        ?.getAttribute('aria-current'),
    ).toBe('page')
  })

  test('an agent detail page hides the settings strip and titles the header "Agentes"', async () => {
    const view = renderLayout('/settings/agents/coder/tools', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-detail')).not.toBeNull())

    expect(tabStrip(view.container)).toBeNull()
    expect(view.container.querySelector('h1')?.textContent).toBe('Agentes')
  })

  test('an unknown tab redirects to general', async () => {
    const view = renderLayout('/settings/nope', { config: 0 })
    await waitFor(() => expect(view.getByTestId('location').textContent).toBe('/settings/general'))
  })
})

describe('SettingsPage — one draft for the whole subtree', () => {
  test('useSettingsConfig is loaded once and survives tab ↔ agent navigation', async () => {
    const counters = { config: 0 }
    const view = renderLayout('/settings/general', counters)
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())
    await waitFor(() => expect(counters.config).toBe(1))

    act(() => {
      if (!navigateRef) throw new Error('navigate not captured')
      navigateRef('/settings/agents/coder/tools')
    })
    await waitFor(() => expect(view.getByTestId('probe-detail')).not.toBeNull())
    expect(counters.config).toBe(1)
    expect(view.getByTestId('probe-detail').textContent).toContain('"hasDraft":true')

    act(() => {
      if (!navigateRef) throw new Error('navigate not captured')
      navigateRef('/settings/providers')
    })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())
    expect(counters.config).toBe(1)
    expect(view.getByTestId('probe-tab').textContent).toContain('"hasDraft":true')
  })
})

describe('SettingsPage — new tabs', () => {
  const tabIds = (container: HTMLElement) =>
    Array.from(tabStrip(container)?.querySelectorAll('button') ?? []).map((button) =>
      button.getAttribute('data-testid'),
    )

  test('subagents is a settings tab: strip visible, highlighted, no redirect', async () => {
    const view = renderLayout('/settings/subagents', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    expect(view.getByTestId('location').textContent).toBe('/settings/subagents')
    expect(tabStrip(view.container)).not.toBeNull()
    expect(
      view.container
        .querySelector('[data-testid="settings-tab-subagents"]')
        ?.getAttribute('aria-current'),
    ).toBe('page')
  })

  test('mcp is a settings tab: strip visible, highlighted, no redirect', async () => {
    const view = renderLayout('/settings/mcp', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    expect(view.getByTestId('location').textContent).toBe('/settings/mcp')
    expect(tabStrip(view.container)).not.toBeNull()
    expect(
      view.container
        .querySelector('[data-testid="settings-tab-mcp"]')
        ?.getAttribute('aria-current'),
    ).toBe('page')
  })

  test('the strip places subagents after agents and mcp after tools', async () => {
    const view = renderLayout('/settings/general', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    const ids = tabIds(view.container)
    expect(ids[ids.indexOf('settings-tab-agents') + 1]).toBe('settings-tab-subagents')
    expect(ids[ids.indexOf('settings-tab-tools') + 1]).toBe('settings-tab-mcp')
  })
})
describe('SettingsPage — scroll area containment', () => {
  test('the scroll area is a containment block for absolutely positioned descendants', async () => {
    const view = renderLayout('/settings/mcp', { config: 0 })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())

    const scrollArea = view.getByTestId('settings-scroll-area')
    expect(scrollArea.className).toContain('overflow-y-auto')
    expect(scrollArea.className).toContain('relative')
    expect(scrollArea.contains(view.getByTestId('probe-tab'))).toBe(true)
  })
})
