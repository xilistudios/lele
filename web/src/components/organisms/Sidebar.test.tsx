import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

/**
 * Sidebar navigation contract.
 *
 * "Chats" is the only destination left in the sidebar. Everything else moved
 * out of it:
 *   - Agents and Providers are tabs of Settings (`/settings/agents`,
 *     `/settings/providers`).
 *   - Skills, scheduled jobs, background processes and secrets live in the
 *     right-hand Tools drawer of the chat (organisms/ToolsSidebar).
 *
 * These tests pin the absence of the old "System" group so it cannot grow back
 * silently, and keep the rail/expanded variants honest.
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

/** Spanish labels of the destinations that used to live under "System". */
const MOVED_LABELS = ['Agentes', 'Proveedores', 'Habilidades', 'Tareas programadas', 'Secretos']

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

describe('Sidebar — navigation', () => {
  test('"Chats" is the only nav destination', () => {
    const view = renderSidebar()

    const nav = view.getByTestId('sidebar-nav-section')
    expect(nav.textContent).toContain('Chats')
    expect(nav.querySelectorAll('button').length).toBe(1)
  })

  test('the old System group (and its toggle) is gone', () => {
    const view = renderSidebar()

    expect(view.queryByTestId('sidebar-system-toggle')).toBeNull()
    expect(view.queryByTestId('sidebar-system-nav')).toBeNull()
    for (const label of MOVED_LABELS) {
      expect(view.queryByLabelText(label)).toBeNull()
    }
  })

  test('clicking Chats navigates to the chat history', () => {
    const view = renderSidebar()

    fireEvent.click(view.getByLabelText('Chats'))
    expect(view.getByTestId('location').textContent).toBe('/chats')
  })

  test('Chats is marked active only on its own route', () => {
    const onChats = renderSidebar({ path: '/chats' })
    expect(onChats.getByLabelText('Chats').getAttribute('aria-current')).toBe('page')
    cleanup()

    const onChat = renderSidebar({ path: '/chat/native:client-1:1' })
    expect(onChat.getByLabelText('Chats').getAttribute('aria-current')).toBeNull()
  })
})

describe('Sidebar — new chat + search share one row', () => {
  test('both actions are laid out on a single horizontal line', () => {
    const view = renderSidebar()

    const newChat = view.getByLabelText('Nuevo chat')
    const search = view.getByLabelText('Buscar')

    // Same parent: they are siblings of one `flex` row (jsdom has no layout
    // engine, so the class contract is what pins "same line").
    const row = newChat.parentElement
    expect(row).not.toBeNull()
    expect(search.parentElement?.parentElement).toBe(row)
    const classes = (row?.getAttribute('class') ?? '').split(/\s+/)
    expect(classes).toContain('flex')
    expect(classes).toContain('items-center')
    // A `flex-col` here would stack them again, which is the bug this pins.
    expect(classes).not.toContain('flex-col')
  })

  test('the new-chat label yields width instead of pushing search out of the drawer', () => {
    const view = renderSidebar()
    const newChat = view.getByLabelText('Nuevo chat')
    const classes = (newChat.getAttribute('class') ?? '').split(/\s+/)

    // `flex-1 min-w-0` + `truncate` is what lets a long localized label shrink
    // inside the 280px drawer without overflowing the search button.
    expect(classes).toContain('flex-1')
    expect(classes).toContain('min-w-0')
    // `truncate` lives on the label itself, not on the row.
    const label = Array.from(newChat.querySelectorAll('span')).find((el) =>
      (el.textContent ?? '').includes('Nuevo chat'),
    )
    expect(label).toBeDefined()
    expect((label?.getAttribute('class') ?? '').split(/\s+/)).toContain('truncate')
  })
})

describe('Sidebar — collapsed rail', () => {
  test('renders Chat as an icon button behind a tooltip', () => {
    const view = renderSidebar({ collapsed: true })

    // Rail rows have title=<label>; the expanded rows do not.
    expect(view.container.querySelectorAll('button[title="Chats"]').length).toBe(1)
    expect(view.queryByTestId('sidebar-system-toggle')).toBeNull()
  })

  test('the routed pages are no longer reachable from the rail', () => {
    const view = renderSidebar({ collapsed: true })

    for (const label of MOVED_LABELS) {
      expect(view.container.querySelectorAll(`button[title="${label}"]`).length).toBe(0)
    }
  })

  test('the collapsed nav is not a scroll container, so the tooltip is not clipped', () => {
    const view = renderSidebar({ collapsed: true })
    const nav = view.getByTestId('sidebar-nav-section')
    // Any `overflow` utility here forces the computed `overflow-x` to `auto`
    // as well, clipping the absolutely positioned Chats tooltip at the 59px
    // rail edge (jsdom has no layout engine, so this pins the class contract).
    for (const c of (nav.getAttribute('class') ?? '').split(/\s+/)) {
      expect(c).not.toMatch(/overflow/)
    }
  })
})
