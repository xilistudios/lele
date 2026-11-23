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

const sessions = Array.from({ length: 30 }, (_, i) => ({
  key: `native:client-${i}`,
  name: `Session ${i}`,
  kind: 'chat',
  mode: 'agent',
  updated: new Date(Date.now() - i * 60_000).toISOString(),
}))

const logicValue = (list: unknown[] = []) =>
  ({
    sessions: list,
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
  }) as unknown as AppLogicContextValue

const originalInnerWidth = window.innerWidth

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
}

function renderSidebar(collapsed: boolean, list: unknown[] = []) {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue(list) },
        createElement(
          MemoryRouter,
          { initialEntries: ['/'] },
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

    expect(view.container.querySelectorAll('button[title="Chats"]').length).toBe(0)
    expect(view.container.querySelectorAll('button[title="Secretos"]').length).toBe(0)
  })

  test('mobile drawer shows the mode selector that the rail hides', () => {
    setViewport(390)

    const view = renderSidebar(true)

    expect(view.getByText('Agente')).not.toBeNull()
  })
})

describe('Sidebar — the mobile drawer covers the viewport', () => {
  test('the drawer is full width and full height on mobile, desktop width intact', () => {
    setViewport(390)

    const view = renderSidebar(true)
    const classes = asideClass(view.container).split(/\s+/)

    expect(classes).toContain('max-md:w-full')
    expect(classes).toContain('max-md:h-dvh')
    expect(classes).toContain(FULL_WIDTH)
    expect(classes).not.toContain(RAIL_WIDTH)
  })

  test('the desktop rail keeps its capped width on desktop', () => {
    setViewport(1280)

    const classes = asideClass(renderSidebar(true).container).split(/\s+/)

    expect(classes).toContain(RAIL_WIDTH)
    expect(classes).not.toContain('w-full')
  })
})

describe('Sidebar — desktop rail keeps working', () => {
  test('desktop + collapsed preference renders the 60px icon rail', () => {
    setViewport(1280)

    const view = renderSidebar(true)
    const cls = asideClass(view.container)

    expect(cls).toContain(RAIL_WIDTH)
    expect(cls).not.toContain(FULL_WIDTH)
    expect(view.container.querySelectorAll('button[title="Chats"]').length).toBe(1)
    expect(view.container.querySelectorAll('button[title="Sistema"]').length).toBe(0)
    expect(view.container.querySelectorAll('button[title="Agentes"]').length).toBe(0)
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
describe('Sidebar — only the session list scrolls on mobile', () => {
  test('the drawer clips instead of scrolling as a whole', () => {
    setViewport(390)

    const classes = asideClass(renderSidebar(false).container).split(/\s+/)

    expect(classes).toContain('max-md:overflow-hidden')
    expect(classes).not.toContain('max-md:overflow-y-auto')
  })

  test('the history section flexes on mobile so the nav and footer stay put', () => {
    setViewport(390)

    const { container } = renderSidebar(false)
    const history = container.querySelector('[data-testid="sidebar-history-section"]')
    if (!history) throw new Error('history section not rendered')
    const classes = history.className.split(/\s+/)

    expect(classes).toContain('flex-1')
    expect(classes).toContain('min-h-0')
    expect(classes).not.toContain('max-md:flex-none')
  })

  test('the session list is the scroll surface that is clipped by the drawer', () => {
    setViewport(390)

    const { container } = renderSidebar(false, sessions)
    const list = container.querySelector('[data-testid="sidebar-history-section"] nav')
    if (!list) throw new Error('session list not rendered')
    const classes = list.className.split(/\s+/)

    expect(classes).toContain('overflow-y-auto')
    expect(classes).toContain('min-h-0')
    expect(list.querySelector('[data-testid="sidebar-history-sentinel"]')).not.toBeNull()
  })

  test('the nav entry and the footer carry no clipped height on mobile', () => {
    setViewport(390)

    const { container } = renderSidebar(false)
    const nav = container.querySelector('[data-testid="sidebar-nav-section"]')
    const footer = container.querySelector('[data-testid="sidebar-footer"]')
    if (!nav || !footer) throw new Error('nav section or footer not rendered')

    for (const token of nav.className.split(/\s+/)) expect(token).not.toMatch(/^max-md:/)
    expect(nav.className.split(/\s+/)).toContain('shrink-0')
    for (const token of footer.className.split(/\s+/)) expect(token).not.toMatch(/^max-md:/)
    expect(footer.className.split(/\s+/)).toContain('mt-auto')
  })
})
