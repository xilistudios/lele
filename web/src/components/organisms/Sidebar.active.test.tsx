import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

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
    const view = renderAt('/chats')
    const chats = navButton(view.container, 'Chats')
    expect(chats).toBeDefined()
    expect(chats?.className).toContain(ACTIVE_CLASS)
  })

  test('nested chats route keeps Chats active (/chats/anything)', () => {
    const view = renderAt('/chats/anything')
    const chats = navButton(view.container, 'Chats')
    expect(chats?.className).toContain(ACTIVE_CLASS)
  })

  test('prefix match is segment-aligned: /chats is not active on /chat/:id', () => {
    const view = renderAt('/chat/native:client-1:1')
    const chats = navButton(view.container, 'Chats')
    expect(chats).toBeDefined()
    expect(chats?.className).not.toContain(ACTIVE_CLASS)
  })

  test('nothing in the sidebar lights up on the pages that moved out', () => {
    for (const path of [
      '/settings/agents',
      '/settings/agents/coder/tools',
      '/settings/providers',
      '/skills',
      '/cron',
      '/secrets',
      '/background-exec',
    ]) {
      const view = renderAt(path)
      const active = Array.from(view.container.querySelectorAll('nav button[aria-label]')).filter(
        (b) => b.className.includes(ACTIVE_CLASS),
      )
      expect({ path, active: active.length }).toEqual({ path, active: 0 })
      cleanup()
    }
  })
})
