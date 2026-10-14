import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render, within } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

/**
 * Sidebar "System" subnav group.
 *
 * Only "Chats" is a top-level nav entry; the other six destinations (Agents,
 * Providers, Skills, Background Execs, Cron, Secrets) live under a collapsible
 * "System" group (expanded sidebar) or behind a System icon-button popover
 * (collapsed rail). Routes are unchanged. The test i18n default locale is
 * Spanish, so the assertions use the Spanish labels.
 */

const authValue = { session: { device_name: 'Desktop' } } as unknown as AuthContextValue

const logicValue = {
  sessions: [],
  currentSessionKey: null,
  parentSessionKey: null,
  processingSessions: new Set<string>(),
  chatMode: 'agent',
  groupsEnabled: false,
  onCreateSession: () => undefined,
  onDeleteSession: () => undefined,
  onToggleSidebar: () => undefined,
  onSelectMode: () => undefined,
  onSelectSession: () => undefined,
  onLogout: async () => undefined,
} as unknown as AppLogicContextValue

// es locale labels for the six grouped destinations + the group header.
const SYSTEM_LABELS = [
  'Agentes',
  'Proveedores',
  'Habilidades',
  'Procesos en Segundo Plano',
  'Tareas programadas',
  'Secretos',
]
const SYSTEM_HEADER = 'Sistema'

function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location">{location.pathname}</div>
}

type RenderOptions = { collapsed?: boolean; path?: string }

function renderSidebar({ collapsed = false, path = '/' }: RenderOptions = {}) {
  return render(
    <AuthContext.Provider value={authValue}>
      <AppLogicContext.Provider value={logicValue}>
        <MemoryRouter initialEntries={[path]}>
          <Sidebar collapsed={collapsed} mobileOpen={false} onClose={() => undefined} />
          <LocationProbe />
        </MemoryRouter>
      </AppLogicContext.Provider>
    </AuthContext.Provider>,
  )
}

const originalInnerWidth = window.innerWidth

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
}

beforeEach(() => {
  localStorage.clear()
  setViewport(1280)
})

afterEach(() => {
  cleanup()
  localStorage.clear()
  Object.defineProperty(window, 'innerWidth', { value: originalInnerWidth, configurable: true })
})

describe('Sidebar — System subnav (expanded)', () => {
  test('keeps Chats top-level and groups the other six under System (open by default)', () => {
    const view = renderSidebar()

    // Chats is the only top-level entry.
    const chats = view.getByLabelText('Chats')
    expect(chats).toBeDefined()

    // The group header is present and expanded by default.
    const toggle = view.getByTestId('sidebar-system-toggle')
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(toggle.textContent).toContain(SYSTEM_HEADER)

    // All six destinations render inside the group, and Chats does NOT.
    const group = view.getByTestId('sidebar-system-nav')
    for (const label of SYSTEM_LABELS) {
      expect(within(group).getByLabelText(label)).toBeDefined()
    }
    expect(within(group).queryByLabelText('Chats')).toBeNull()
  })

  test('clicking the System header collapses then re-opens the group', () => {
    const view = renderSidebar()

    const toggle = view.getByTestId('sidebar-system-toggle')
    expect(view.queryByTestId('sidebar-system-nav')).not.toBeNull()

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(view.queryByTestId('sidebar-system-nav')).toBeNull()
    expect(view.queryByLabelText('Habilidades')).toBeNull()

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(view.getByTestId('sidebar-system-nav')).not.toBeNull()
  })

  test('clicking a child entry navigates to its route', () => {
    const view = renderSidebar()

    fireEvent.click(view.getByLabelText('Habilidades'))
    expect(view.getByTestId('location').textContent).toBe('/skills')

    fireEvent.click(view.getByLabelText('Secretos'))
    expect(view.getByTestId('location').textContent).toBe('/secrets')
  })
})

describe('Sidebar — System subnav persistence', () => {
  test('defaults to open when no preference is stored', () => {
    const view = renderSidebar()
    expect(view.getByTestId('sidebar-system-toggle').getAttribute('aria-expanded')).toBe('true')
  })

  test('mounts collapsed when the stored preference is false', () => {
    localStorage.setItem('lele.systemNavOpen', 'false')
    const view = renderSidebar()

    const toggle = view.getByTestId('sidebar-system-toggle')
    expect(toggle.getAttribute('aria-expanded')).toBe('false')
    expect(view.queryByTestId('sidebar-system-nav')).toBeNull()
  })

  test('toggling writes the preference to lele.systemNavOpen', () => {
    const view = renderSidebar()
    const toggle = view.getByTestId('sidebar-system-toggle')

    fireEvent.click(toggle)
    expect(localStorage.getItem('lele.systemNavOpen')).toBe('false')

    fireEvent.click(toggle)
    expect(localStorage.getItem('lele.systemNavOpen')).toBe('true')
  })
})

describe('Sidebar — active route auto-opens the System group', () => {
  test('a stored-closed group still renders its active child, without overwriting the preference', () => {
    localStorage.setItem('lele.systemNavOpen', 'false')
    const view = renderSidebar({ path: '/skills' })

    // Rendered expanded even though the stored preference says closed.
    const group = view.getByTestId('sidebar-system-nav')
    const skills = within(group).getByLabelText('Habilidades')
    expect(skills.getAttribute('aria-current')).toBe('page')

    // The preference was not clobbered by the auto-expansion.
    expect(localStorage.getItem('lele.systemNavOpen')).toBe('false')
    expect(view.getByTestId('sidebar-system-toggle').getAttribute('aria-expanded')).toBe('true')
  })

  test('non-system routes leave children without aria-current', () => {
    const view = renderSidebar({ path: '/chats' })
    expect(view.getByLabelText('Agentes').getAttribute('aria-current')).toBeNull()
  })
})

describe('Sidebar — System popover (collapsed rail)', () => {
  test('exposes a System icon button and opens a popover listing the six entries', () => {
    const view = renderSidebar({ collapsed: true })

    // The collapsible header belongs to the expanded sidebar only.
    expect(view.queryByTestId('sidebar-system-toggle')).toBeNull()

    const systemButton = view.getByLabelText(SYSTEM_HEADER)
    // Popover root carries the `group` class while closed (hover tooltip).
    const popoverRoot = systemButton.parentElement as HTMLElement
    expect(popoverRoot.className).toContain('group')

    fireEvent.click(systemButton)
    expect(popoverRoot.className).not.toContain('group')

    for (const label of SYSTEM_LABELS) {
      expect(view.getByLabelText(label)).toBeDefined()
    }
  })

  test('clicking a popover entry navigates', () => {
    const view = renderSidebar({ collapsed: true })

    fireEvent.click(view.getByLabelText(SYSTEM_HEADER))
    fireEvent.click(view.getByLabelText('Proveedores'))

    expect(view.getByTestId('location').textContent).toBe('/providers')
  })
})
