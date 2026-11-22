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

/**
 * Settings shell contract.
 *
 * `SettingsPage` is the layout for the whole `/settings` subtree: it owns the
 * single `useSettingsConfig` draft and renders children through `<Outlet/>`.
 * That includes the agent detail pages, because Agents and Providers are
 * Settings sections now. Two invariants are pinned here:
 *
 * 1. One config load for the whole subtree — navigating between a tab and an
 *    agent detail page must NOT remount the hook (a remount would silently
 *    drop the unsaved draft).
 * 2. The tab strip is visible on every settings tab and hidden on agent detail
 *    pages, which own their own tab strip.
 */

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
    // An icon per entry, and no two entries sharing one: the glyph has to
    // disambiguate the entry, not repeat the label.
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

    // The agent page owns its own tab strip; two side by side would leave the
    // content a narrow column.
    expect(tabStrip(view.container)).toBeNull()
    expect(view.container.querySelector('h1')?.textContent).toBe('Agentes')
  })

  test('an unknown tab redirects to general', async () => {
    const view = renderLayout('/settings/nope', { config: 0 })
    // The content normalises the URL with `replace`, so the address bar always
    // describes what is on screen.
    await waitFor(() => expect(view.getByTestId('location').textContent).toBe('/settings/general'))
  })
})

describe('SettingsPage — one draft for the whole subtree', () => {
  test('useSettingsConfig is loaded once and survives tab ↔ agent navigation', async () => {
    const counters = { config: 0 }
    const view = renderLayout('/settings/general', counters)
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())
    await waitFor(() => expect(counters.config).toBe(1))

    // Tab → agent detail (the agent list's "Configure" link).
    act(() => {
      if (!navigateRef) throw new Error('navigate not captured')
      navigateRef('/settings/agents/coder/tools')
    })
    await waitFor(() => expect(view.getByTestId('probe-detail')).not.toBeNull())
    expect(counters.config).toBe(1)
    expect(view.getByTestId('probe-detail').textContent).toContain('"hasDraft":true')

    // Agent detail → another tab.
    act(() => {
      if (!navigateRef) throw new Error('navigate not captured')
      navigateRef('/settings/providers')
    })
    await waitFor(() => expect(view.getByTestId('probe-tab')).not.toBeNull())
    expect(counters.config).toBe(1)
    expect(view.getByTestId('probe-tab').textContent).toContain('"hasDraft":true')
  })
})
