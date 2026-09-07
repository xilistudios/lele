import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

const RAIL_WIDTH = 'w-[60px]'
const FULL_WIDTH = 'w-[280px]'

const authValue = { session: { device_name: 'Phone' } } as unknown as AuthContextValue

// The Sidebar only destructures a handful of cold-state fields; everything else
// is unused by it (QuickChatPanel stays closed). ModeSelector needs chatMode,
// onSelectMode and groupsEnabled.
const logicValue = {
  sessions: [],
  currentSessionKey: 'native:client-1:1',
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

const originalInnerWidth = window.innerWidth

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
}

function renderSidebar(collapsed: boolean) {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue },
        createElement(
          MemoryRouter,
          { initialEntries: ['/'] },
          // mobileOpen=true: this is the state right after the user taps the
          // hamburger, i.e. the drawer is on screen.
          createElement(Sidebar, { collapsed, mobileOpen: true, onClose: () => undefined }),
        ),
      ),
    ),
  )
}

function asideClass(container: HTMLElement): string {
  const aside = container.querySelector('aside')
  if (!aside) throw new Error('Sidebar <aside> not rendered')
  return aside.className
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  Object.defineProperty(window, 'innerWidth', { value: originalInnerWidth, configurable: true })
})

describe('Sidebar — mobile drawer ignores the desktop collapsed preference', () => {
  test('mobile + collapsed preference still renders the full 280px drawer', () => {
    setViewport(390)

    const view = renderSidebar(true)
    const cls = asideClass(view.container)

    expect(cls).toContain(FULL_WIDTH)
    expect(cls).not.toContain(RAIL_WIDTH)
  })

  test('mobile drawer renders nav entries as labelled rows, not tooltip icons', () => {
    setViewport(390)

    const view = renderSidebar(true)

    // The rail renders each nav entry as an IconButton carrying title=<label>
    // and reveals the text only through a hover tooltip (unreachable by
    // touch). The expanded drawer renders plain labelled rows instead.
    expect(view.container.querySelectorAll('button[title="Agentes"]').length).toBe(0)
    expect(view.container.querySelectorAll('button[title="Secretos"]').length).toBe(0)
  })

  test('mobile drawer shows the mode selector that the rail hides', () => {
    setViewport(390)

    const view = renderSidebar(true)

    // ModeSelector is rendered only in the expanded sidebar. 'Agente' is its
    // tab label and appears nowhere in the rail (its tooltips are 'Agentes',
    // 'Chats', ...).
    expect(view.getByText('Agente')).not.toBeNull()
  })
})

describe('Sidebar — desktop rail keeps working', () => {
  test('desktop + collapsed preference renders the 60px icon rail', () => {
    setViewport(1280)

    const view = renderSidebar(true)
    const cls = asideClass(view.container)

    expect(cls).toContain(RAIL_WIDTH)
    expect(cls).not.toContain(FULL_WIDTH)
    // The rail contract: tooltip-backed icon buttons and no mode selector.
    expect(view.container.querySelectorAll('button[title="Agentes"]').length).toBe(1)
    expect(view.queryByText('Agente')).toBeNull()
  })

  test('desktop + expanded preference renders the full sidebar', () => {
    setViewport(1280)

    const view = renderSidebar(false)
    const cls = asideClass(view.container)

    expect(cls).toContain(FULL_WIDTH)
    expect(view.getByText('Agente')).not.toBeNull()
    expect(view.container.querySelectorAll('button[title="Agentes"]').length).toBe(0)
  })
})
