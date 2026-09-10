import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

/**
 * Layout contract of the sidebar (see Sidebar.tsx).
 *
 * The drawer is `fixed inset-y-0`: its height is the viewport height and every
 * section below the header competes for that fixed budget.
 *
 * Desktop (≥ md) — unchanged behaviour:
 *   nav menu `shrink-0` (natural height, never squeezed) + history section
 *   `flex-1 min-h-0` (absorbs the leftover and scrolls its own list).
 *
 * Mobile (< md) — the bug this fixes:
 *   the `shrink-0` menu consumed the whole budget, so the history section
 *   collapsed to zero (its `shrink-0` header/"show more" then painted over the
 *   first menu rows) and on short viewports the last menu entries (Secrets,
 *   device footer) fell off the bottom edge with nothing able to scroll to
 *   them. Fix: the drawer itself becomes the single scroll surface
 *   (`max-md:overflow-y-auto` on <aside>) and the history section keeps a
 *   minimum height (`max-md:min-h-[...]`) instead of collapsing.
 *
 * jsdom has no layout engine, so these tests assert the class contract: they
 * fail if the responsive prefixes are dropped, swapped or removed.
 */

const authValue = { session: { device_name: 'Phone' } } as unknown as AuthContextValue

const session = (i: number) => ({
  key: `native:client-${i}`,
  name: `Session ${i}`,
  kind: 'chat',
  updated_at: new Date(Date.now() - i * 60_000).toISOString(),
  message_count: i,
})

const manySessions = Array.from({ length: 30 }, (_, i) => session(i))

function logicValue(sessions: unknown[]): AppLogicContextValue {
  return {
    sessions,
    currentSessionKey: 'native:client-0',
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
}

const originalInnerWidth = window.innerWidth

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
}

function renderSidebar(sessions: unknown[]) {
  return render(
    createElement(
      AuthContext.Provider,
      { value: authValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue(sessions) },
        createElement(
          MemoryRouter,
          { initialEntries: ['/'] },
          createElement(Sidebar, { collapsed: false, mobileOpen: true, onClose: () => undefined }),
        ),
      ),
    ),
  )
}

function classesOf(el: Element | null): string[] {
  if (!el) throw new Error('element not rendered')
  return (el.getAttribute('class') ?? '').split(/\s+/)
}

function contains(el: Element | null, ...tokens: string[]) {
  const classes = classesOf(el)
  for (const token of tokens) expect(classes).toContain(token)
}

const asideOf = (container: HTMLElement) => container.querySelector('aside')
const historyOf = (container: HTMLElement) =>
  container.querySelector('[data-testid="sidebar-history-section"]')
const navOf = (container: HTMLElement) =>
  container.querySelector('[data-testid="sidebar-nav-section"]')
/** The inner list scroller of the history section (only rendered when sessions exist). */
const historyListOf = (container: HTMLElement) => historyOf(container)?.querySelector('nav') ?? null

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
  Object.defineProperty(window, 'innerWidth', { value: originalInnerWidth, configurable: true })
})

describe('Sidebar — nav menu keeps its natural height', () => {
  test('is shrink-0 so the history section can never squeeze it', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    contains(navOf(container), 'shrink-0')
  })

  test('has no internal scroller: the drawer is the single scroll surface on mobile', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    // An `overflow-y-auto` here would capture the touch gesture in a short
    // inner scroller and the drawer could no longer be scrolled past the menu.
    for (const c of classesOf(navOf(container))) {
      expect(c).not.toMatch(/overflow/)
      expect(c).not.toBe('min-h-0')
    }
  })
})

describe('Sidebar — chat history section', () => {
  test('absorbs the leftover desktop height and can shrink below content', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    contains(historyOf(container), 'flex-1', 'min-h-0')
  })

  test('keeps its natural height on mobile instead of collapsing under the menu', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    contains(historyOf(container), 'max-md:flex-none')
  })

  test('mobile variant does not disturb the desktop flex math', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    const classes = classesOf(historyOf(container))
    // `flex-none` must only exist behind the max-md: prefix — an unprefixed
    // one would stop the section from growing on desktop.
    expect(classes).toContain('flex-1')
    expect(classes.filter((c) => c === 'flex-none')).toEqual([])
  })

  test('scrolls its own list so long histories stay reachable', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    contains(historyListOf(container), 'overflow-y-auto')
  })
})

describe('Sidebar — drawer is the mobile scroll surface', () => {
  test('scrolls as a whole below md', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    contains(asideOf(container), 'max-md:overflow-y-auto')
  })

  test('never scrolls on desktop, where inner sections own their overflow', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    // Only max-md:-prefixed overflow is allowed on the drawer: an unprefixed
    // or md: overflow would double-scroll against the inner list on desktop.
    for (const c of classesOf(asideOf(container))) {
      if (c.includes('overflow')) expect(c.startsWith('max-md:')).toBe(true)
    }
  })
})

describe('Sidebar — both sections render together (no conditional hiding)', () => {
  test('history rows and the last nav entries are in the DOM at once', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    expect(historyOf(container)).not.toBeNull()
    expect(navOf(container)).not.toBeNull()
    // The entries that used to fall off the bottom edge of the drawer.
    const navText = navOf(container)?.textContent ?? ''
    expect(navText).toContain('Secretos')
    expect(historyListOf(container)?.children.length).toBeGreaterThan(0)
  })
})
