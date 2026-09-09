import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

// The active nav item is marked with these classes (expanded sidebar rows).
const ACTIVE_CLASS = 'bg-surface-selected'

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

function renderAt(path: string) {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue },
        createElement(
          MemoryRouter,
          { initialEntries: [path] },
          // collapsed=false → full sidebar with labelled rows (desktop).
          createElement(Sidebar, { collapsed: false, mobileOpen: false, onClose: () => undefined }),
        ),
      ),
    ),
  )
}

function navButton(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('nav button[aria-label]')).find(
    (b) => b.getAttribute('aria-label') === label,
  ) as HTMLButtonElement | undefined
}

afterEach(() => {
  cleanup()
})

describe('Sidebar — isActiveRoute prefix matching', () => {
  test('exact path activates its item', () => {
    const view = renderAt('/agents')
    const agents = navButton(view.container, 'Agentes')
    expect(agents).toBeDefined()
    expect(agents?.className).toContain(ACTIVE_CLASS)
  })

  test('nested agent route keeps Agents active (/agents/coder)', () => {
    const view = renderAt('/agents/coder')
    const agents = navButton(view.container, 'Agentes')
    expect(agents?.className).toContain(ACTIVE_CLASS)
  })

  test('deeply nested agent route keeps Agents active (/agents/coder/tools)', () => {
    const view = renderAt('/agents/coder/tools')
    const agents = navButton(view.container, 'Agentes')
    expect(agents?.className).toContain(ACTIVE_CLASS)
  })

  test('prefix match is segment-aligned: /chats is not active on /chat/:id', () => {
    const view = renderAt('/chat/native:client-1:1')
    const chats = navButton(view.container, 'Chats')
    expect(chats).toBeDefined()
    expect(chats?.className).not.toContain(ACTIVE_CLASS)
    // And no other nav item lights up either.
    const agents = navButton(view.container, 'Agentes')
    expect(agents?.className).not.toContain(ACTIVE_CLASS)
  })

  test('only one nav item active on a nested route', () => {
    const view = renderAt('/agents/coder')
    const labels = ['Chats', 'Agentes', 'Proveedores', 'Habilidades', 'Secretos']
    const active = labels.filter((l) =>
      navButton(view.container, l)?.className.includes(ACTIVE_CLASS),
    )
    expect(active).toEqual(['Agentes'])
  })
})
