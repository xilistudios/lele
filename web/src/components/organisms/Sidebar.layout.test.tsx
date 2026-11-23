import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { Sidebar } from './Sidebar'

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
const footerOf = (container: HTMLElement) =>
  container.querySelector('[data-testid="sidebar-footer"]')
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

  test('has no internal scroller of its own', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
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

  test('flexes on mobile too, so the session list owns the leftover height', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    contains(historyOf(container), 'flex-1', 'min-h-0')
    expect(classesOf(historyOf(container))).not.toContain('max-md:flex-none')
  })

  test('mobile variant does not disturb the desktop flex math', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    const classes = classesOf(historyOf(container))
    expect(classes).toContain('flex-1')
    expect(classes.filter((c) => c === 'flex-none')).toEqual([])
  })

  test('scrolls its own list so long histories stay reachable', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    contains(historyListOf(container), 'overflow-y-auto')
  })
})

describe('Sidebar — the session list is the only mobile scroll surface', () => {
  test('never scrolls as a whole below md', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    contains(asideOf(container), 'max-md:overflow-hidden')
    expect(classesOf(asideOf(container))).not.toContain('max-md:overflow-y-auto')
  })

  test('keeps the nav entry and the footer pinned outside the scroll area', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    contains(navOf(container), 'shrink-0')
    for (const c of classesOf(navOf(container))) expect(c).not.toMatch(/^max-md:/)
    contains(footerOf(container), 'mt-auto')
    expect(navOf(container)?.className ?? '').not.toMatch(/max-md:h-/)
  })

  test('never scrolls on desktop, where inner sections own their overflow', () => {
    setViewport(1280)
    const { container } = renderSidebar(manySessions)
    for (const c of classesOf(asideOf(container))) {
      if (c.includes('overflow')) expect(c.startsWith('max-md:')).toBe(true)
    }
  })
})

describe('Sidebar — both sections render together (no conditional hiding)', () => {
  test('history rows and the nav entry are in the DOM at once', () => {
    setViewport(390)
    const { container } = renderSidebar(manySessions)
    expect(historyOf(container)).not.toBeNull()
    expect(navOf(container)).not.toBeNull()
    const navText = navOf(container)?.textContent ?? ''
    expect(navText).toContain('Chats')
    expect(historyListOf(container)?.children.length).toBeGreaterThan(0)
  })
})
