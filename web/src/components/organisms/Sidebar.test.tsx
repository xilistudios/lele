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
    expect(toggle.getAttribute('aria-controls')).toBe('sidebar-system-nav')
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
    // The `aria-controls` target is unmounted while collapsed, so the
    // attribute must not dangle at a missing id.
    expect(toggle.getAttribute('aria-controls')).toBeNull()
    expect(view.queryByTestId('sidebar-system-nav')).toBeNull()
    expect(view.queryByLabelText('Habilidades')).toBeNull()

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(toggle.getAttribute('aria-controls')).toBe('sidebar-system-nav')
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
    expect(toggle.getAttribute('aria-controls')).toBeNull()
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

  test('the header toggle is a visual no-op while a child route forces the group open', () => {
    // A stored-collapsed group is still forced open by the active child: a
    // click must neither collapse it nor persist a close the user never saw.
    localStorage.setItem('lele.systemNavOpen', 'false')
    const view = renderSidebar({ path: '/skills' })

    const toggle = view.getByTestId('sidebar-system-toggle')
    expect(toggle.getAttribute('aria-expanded')).toBe('true')

    fireEvent.click(toggle)

    expect(toggle.getAttribute('aria-expanded')).toBe('true')
    expect(view.getByTestId('sidebar-system-nav')).not.toBeNull()
    expect(localStorage.getItem('lele.systemNavOpen')).toBe('false')
  })
})

/**
 * The collapsed-rail System popover lives behind one icon button.
 *
 * `Popover` always mounts its children (hiding them with `opacity-0
 * pointer-events-none`), so merely finding the six labels does NOT prove the
 * popover is open. These tests pin the real contract by asserting the
 * visibility classes on the panel, both transitions (open then close) and the
 * close-after-select behaviour.
 */
function systemPopover(view: ReturnType<typeof renderSidebar>) {
  const trigger = view.getByLabelText(SYSTEM_HEADER)
  // The Popover root is the trigger's parent; the panel is its last child
  // (after the trigger and, when present, the hover tooltip).
  const root = trigger.parentElement as HTMLElement
  const panel = root.lastElementChild as HTMLElement
  return { trigger, root, panel }
}

describe('Sidebar — System popover (collapsed rail)', () => {
  test('stays hidden until the trigger is clicked, then toggles closed again', () => {
    const view = renderSidebar({ collapsed: true })

    // The collapsible header belongs to the expanded sidebar only.
    expect(view.queryByTestId('sidebar-system-toggle')).toBeNull()

    const { trigger, root, panel } = systemPopover(view)

    // Closed: the panel is mounted but hidden, and the root carries `group`
    // (hover tooltip).
    expect(root.className).toContain('group')
    expect(panel.className).toContain('opacity-0')
    expect(panel.className).toContain('pointer-events-none')

    fireEvent.click(trigger)

    // Open: the panel is visible and interactive.
    expect(root.className).not.toContain('group')
    expect(panel.className).toContain('opacity-100')
    expect(panel.className).toContain('scale-100')
    expect(panel.className).not.toContain('pointer-events-none')

    for (const label of SYSTEM_LABELS) {
      expect(view.getByLabelText(label)).toBeDefined()
    }

    // A second click on the trigger closes it again.
    fireEvent.click(trigger)
    expect(root.className).toContain('group')
    expect(panel.className).toContain('opacity-0')
    expect(panel.className).toContain('pointer-events-none')
  })

  test('selecting an entry navigates and closes the popover', () => {
    const view = renderSidebar({ collapsed: true })
    const { trigger, root, panel } = systemPopover(view)

    fireEvent.click(trigger)
    expect(panel.className).toContain('opacity-100')

    fireEvent.click(view.getByLabelText('Proveedores'))

    expect(view.getByTestId('location').textContent).toBe('/providers')
    // The popover must close after navigating, not stay open over the page.
    expect(root.className).toContain('group')
    expect(panel.className).toContain('opacity-0')
    expect(panel.className).toContain('pointer-events-none')
  })

  test('the collapsed nav is not a scroll container, so the popover is not clipped', () => {
    const view = renderSidebar({ collapsed: true })
    const nav = view.getByTestId('sidebar-nav-section')
    // Any `overflow` utility here forces the computed `overflow-x` to `auto`
    // as well, clipping the absolutely positioned System popover and the Chats
    // tooltip at the 59px rail edge (jsdom has no layout engine, so this pins
    // the class contract that prevents it).
    for (const c of (nav.getAttribute('class') ?? '').split(/\s+/)) {
      expect(c).not.toMatch(/overflow/)
    }
  })

  test('closes on Escape', () => {
    const view = renderSidebar({ collapsed: true })
    const { trigger, panel } = systemPopover(view)

    fireEvent.click(trigger)
    expect(panel.className).toContain('opacity-100')

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(panel.className).toContain('opacity-0')
    expect(panel.className).toContain('pointer-events-none')
  })
})
