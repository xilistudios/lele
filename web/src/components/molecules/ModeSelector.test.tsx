import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { ModeSelector } from './ModeSelector'

/** Minimal logic value for ModeSelector. Override per-test as needed. */
const baseLogic = {
  chatMode: 'agent',
  groupsEnabled: false,
  onSelectMode: () => undefined,
} as unknown as AppLogicContextValue

/**
 * Render ModeSelector inside a container carrying the `dark` class.
 * Tailwind darkMode:'class' checks for a `.dark` ancestor; without this
 * wrapper the `dark:` variant in tabActive strings is inert during tests.
 * NOTE: class-string assertions do not evaluate CSS specificity — they
 * verify that the correct utility tokens are present in the DOM.
 */
function renderModeSelector(overrides?: Partial<AppLogicContextValue>) {
  const value = { ...baseLogic, ...overrides } as AppLogicContextValue
  return render(
    createElement(
      'div',
      { className: 'dark' },
      createElement(
        AppLogicContext.Provider,
        { value },
        createElement(MemoryRouter, { initialEntries: ['/'] }, createElement(ModeSelector)),
      ),
    ),
  )
}

/** Split a className string into a Set of discrete tokens. */
function classTokens(el: Element): Set<string> {
  return new Set(el.className.split(/\s+/).filter(Boolean))
}

beforeEach(() => {
  localStorage.clear()
})

afterEach(() => {
  cleanup()
})

// ── Track container ────────────────────────────────────────────────
describe('ModeSelector — segmented track uses bg-background-tertiary', () => {
  test('track container has bg-background-tertiary, NOT bg-background-secondary (anti-regression)', () => {
    const { container } = renderModeSelector()
    // Navigate past the dark wrapper div to reach the track
    const wrapper = container.firstElementChild as HTMLElement
    expect(wrapper).not.toBeNull()
    const track = wrapper.firstElementChild as HTMLElement
    expect(track).not.toBeNull()
    if (!track) return
    const tokens = classTokens(track)
    expect(tokens.has('bg-background-tertiary')).toBe(true)
    // Token-exact absence: bg-background-secondary must not appear anywhere in the class list
    expect([...tokens].filter((t) => t === 'bg-background-secondary')).toHaveLength(0)
  })
})

// ── Active / inactive tab styling ──────────────────────────────────
describe('ModeSelector — active tab tint and inactive border', () => {
  test('active agent tab carries bg-mode-agent/10 tint', () => {
    const { container } = renderModeSelector({ chatMode: 'agent' } as Partial<AppLogicContextValue>)
    const buttons = container.querySelectorAll('button')
    const activeBtn = Array.from(buttons).find((b) => b.getAttribute('aria-pressed') === 'true')
    expect(activeBtn).not.toBeNull()
    expect(activeBtn?.className).toContain('bg-mode-agent/10')
    expect(activeBtn?.className).toContain('text-text-primary')
  })

  test('active chat tab carries bg-mode-chat/10 tint', () => {
    const { container } = renderModeSelector({ chatMode: 'chat' } as Partial<AppLogicContextValue>)
    const activeBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.getAttribute('aria-pressed') === 'true',
    )
    expect(activeBtn).not.toBeNull()
    expect(activeBtn?.className).toContain('bg-mode-chat/10')
    expect(activeBtn?.className).toContain('text-text-primary')
  })

  test('inactive tab carries border-transparent', () => {
    const { container } = renderModeSelector({ chatMode: 'agent' } as Partial<AppLogicContextValue>)
    const inactiveBtns = Array.from(container.querySelectorAll('button')).filter(
      (b) => b.getAttribute('aria-pressed') !== 'true',
    )
    expect(inactiveBtns.length).toBeGreaterThan(0)
    for (const btn of inactiveBtns) {
      expect(btn.className).toContain('border-transparent')
    }
  })
})

// ── groupsEnabled gate ─────────────────────────────────────────────
describe('ModeSelector — groupsEnabled controls group tab visibility', () => {
  test('group tab is NOT rendered when groupsEnabled is false', () => {
    const { container } = renderModeSelector({
      groupsEnabled: false,
    } as Partial<AppLogicContextValue>)
    expect(container.textContent).not.toContain('Grupo')
  })

  test('group tab IS rendered when groupsEnabled is true', () => {
    const { container } = renderModeSelector({
      groupsEnabled: true,
    } as Partial<AppLogicContextValue>)
    expect(container.textContent).toContain('Grupo')
  })

  test('group tab carries text-text-primary and dark:text-mode-group (§2.4 light-theme exception)', () => {
    const { container } = renderModeSelector({
      chatMode: 'group',
      groupsEnabled: true,
    } as Partial<AppLogicContextValue>)
    const groupBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.getAttribute('aria-pressed') === 'true',
    )
    expect(groupBtn).not.toBeNull()
    if (!groupBtn) return
    const tokens = classTokens(groupBtn)
    expect(tokens.has('bg-mode-group/10')).toBe(true)
    expect(tokens.has('text-text-primary')).toBe(true)
    // Group's active tab carries identity color in dark via §2.4 dark: variant
    expect(tokens.has('dark:text-mode-group')).toBe(true)
    // Must NOT have a bare text-mode-group (would fail light AA at 3.95)
    expect(tokens.has('text-mode-group')).toBe(false)
  })
})

// ── §2.4 identity-color asymmetry (MAJOR-1 lock-in) ───────────────
describe('ModeSelector — active tab identity color asymmetry per §2.4', () => {
  test('chat active tab carries text-text-primary AND dark:text-mode-chat', () => {
    const { container } = renderModeSelector({ chatMode: 'chat' } as Partial<AppLogicContextValue>)
    const chatBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.getAttribute('aria-pressed') === 'true',
    )
    expect(chatBtn).not.toBeNull()
    if (!chatBtn) return
    const tokens = classTokens(chatBtn)
    expect(tokens.has('text-text-primary')).toBe(true)
    // Chat identity passes AA in dark (6.16) but fails in light (4.20)
    expect(tokens.has('dark:text-mode-chat')).toBe(true)
  })

  test('agent active tab carries text-text-primary but NOT dark:text-mode-agent', () => {
    const { container } = renderModeSelector({ chatMode: 'agent' } as Partial<AppLogicContextValue>)
    const agentBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.getAttribute('aria-pressed') === 'true',
    )
    expect(agentBtn).not.toBeNull()
    if (!agentBtn) return
    const tokens = classTokens(agentBtn)
    expect(tokens.has('text-text-primary')).toBe(true)
    // Agent identity fails AA in both themes (4.39 dark / 4.33 light) → no dark: variant
    expect(tokens.has('dark:text-mode-agent')).toBe(false)
  })

  test('active tab class does NOT contain bg-background-tertiary (track must not leak into tab)', () => {
    const { container } = renderModeSelector({ chatMode: 'chat' } as Partial<AppLogicContextValue>)
    const activeBtn = Array.from(container.querySelectorAll('button')).find(
      (b) => b.getAttribute('aria-pressed') === 'true',
    )
    expect(activeBtn).not.toBeNull()
    if (!activeBtn) return
    const tokens = classTokens(activeBtn)
    expect(tokens.has('bg-background-tertiary')).toBe(false)
  })
})
