import '../../test/setup'
import { beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import type { SettingsTab } from '../../lib/settingsTabs'
import {
  INADEQUATE_ACCENT_CLASS,
  SELECTED_BACKGROUND_CLASS,
  expectSelectedAccentRecipe,
} from '../../test/accentRecipe'
import { SettingsTabs } from './SettingsTabs'

const TEST_TIMEOUT_MS = 20_000

const logicValue = { groupsEnabled: false } as unknown as AppLogicContextValue

const NAV_TESTID = 'settings-tabs'
const TAB_TESTID_PREFIX = 'settings-tab-'

type Span = { left: number; right: number; top?: number; bottom?: number }

type StripLayout = {
  view: Span
  tabs: Partial<Record<SettingsTab, Span>>
}

const NO_SPAN: Span = { left: 0, right: 0, top: 0, bottom: 0 }

function toRect({ left, right, top = 0, bottom = 0 }: Span): DOMRect {
  return {
    left,
    right,
    width: right - left,
    top,
    bottom,
    height: bottom - top,
    x: left,
    y: top,
    toJSON: () => ({ left, right, top, bottom }),
  } as DOMRect
}

function scrolledBy(element: Element): { x: number; y: number } {
  const nav = element.closest(`[data-testid="${NAV_TESTID}"]`) as HTMLElement | null
  return { x: nav?.scrollLeft ?? 0, y: nav?.scrollTop ?? 0 }
}

function layoutRect(layout: StripLayout, element: Element): DOMRect {
  const testId = element.getAttribute('data-testid')
  if (testId === NAV_TESTID) return toRect(layout.view)
  if (!testId?.startsWith(TAB_TESTID_PREFIX)) return toRect(NO_SPAN)
  const span = layout.tabs[testId.slice(TAB_TESTID_PREFIX.length) as SettingsTab]
  if (!span) return toRect(NO_SPAN)
  const shift = scrolledBy(element)
  return toRect({
    left: span.left - shift.x,
    right: span.right - shift.x,
    top: (span.top ?? 0) - shift.y,
    bottom: (span.bottom ?? 0) - shift.y,
  })
}

type ElementProto = {
  getBoundingClientRect: Element['getBoundingClientRect']
  scrollIntoView?: Element['scrollIntoView']
}

const elementProto = window.Element.prototype as unknown as ElementProto

function stubLayout(layout: StripLayout): () => void {
  const original = elementProto.getBoundingClientRect
  elementProto.getBoundingClientRect = function stubbedRect(this: Element) {
    return layoutRect(layout, this)
  }
  return () => {
    elementProto.getBoundingClientRect = original
  }
}

function stubScrollIntoView(): { calls: Element[]; restore: () => void } {
  const calls: Element[] = []
  const original = elementProto.scrollIntoView
  elementProto.scrollIntoView = function scrollIntoViewStub(this: Element) {
    calls.push(this)
  }
  return {
    calls,
    restore: () => {
      elementProto.scrollIntoView = original
    },
  }
}

function withoutScrollIntoView(): () => void {
  const original = elementProto.scrollIntoView
  elementProto.scrollIntoView = undefined
  return () => {
    elementProto.scrollIntoView = original
  }
}

function tree(activeTab: SettingsTab) {
  return (
    <AppLogicContext.Provider value={logicValue}>
      <MemoryRouter initialEntries={[`/settings/${activeTab}`]}>
        <SettingsTabs activeTab={activeTab} onTabChange={() => undefined} />
      </MemoryRouter>
    </AppLogicContext.Provider>
  )
}

const tabButton = (container: HTMLElement, tab: SettingsTab) => {
  const button = container.querySelector(`[data-testid="${TAB_TESTID_PREFIX}${tab}"]`)
  if (!button) throw new Error(`no tab button for ${tab}`)
  return button
}

const tabScroller = (container: HTMLElement) => {
  const nav = container.querySelector(`[data-testid="${NAV_TESTID}"]`)
  if (!nav) throw new Error('no tab strip')
  return nav as HTMLElement
}

beforeEach(cleanup)

describe('SettingsTabs — active tab visibility', () => {
  test(
    'scrolls the active tab into view on mount',
    () => {
      const restoreLayout = stubLayout({
        view: { left: 0, right: 300 },
        tabs: { mcp: { left: 500, right: 580 } },
      })
      const spy = stubScrollIntoView()
      try {
        const view = render(tree('mcp'))

        expect(tabButton(view.container, 'mcp').getAttribute('aria-current')).toBe('page')
        expect(tabScroller(view.container).scrollLeft).toBe(280)
        expect(spy.calls.length).toBe(0)
      } finally {
        spy.restore()
        restoreLayout()
      }
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'scrolls the new active tab into view when the tab changes',
    () => {
      const restoreLayout = stubLayout({
        view: { left: 0, right: 300 },
        tabs: { general: { left: 0, right: 80 }, logs: { left: 700, right: 790 } },
      })
      try {
        const view = render(tree('general'))
        expect(tabScroller(view.container).scrollLeft).toBe(0)

        view.rerender(tree('logs'))
        expect(tabScroller(view.container).scrollLeft).toBe(490)

        view.rerender(tree('general'))
        expect(tabScroller(view.container).scrollLeft).toBe(0)
      } finally {
        restoreLayout()
      }
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'leaves the desktop rail still when the active tab already fits',
    () => {
      const restoreLayout = stubLayout({
        view: { left: 0, right: 200 },
        tabs: { logs: { left: 8, right: 150 } },
      })
      const spy = stubScrollIntoView()
      try {
        const view = render(tree('logs'))

        expect(tabScroller(view.container).scrollLeft).toBe(0)
        expect(spy.calls.length).toBe(0)
      } finally {
        spy.restore()
        restoreLayout()
      }
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'renders without scrolling when the environment has no scrollIntoView',
    () => {
      const restore = withoutScrollIntoView()
      try {
        const view = render(tree('subagents'))
        expect(tabButton(view.container, 'subagents').getAttribute('aria-current')).toBe('page')
        expect(tabScroller(view.container).scrollLeft).toBe(0)
      } finally {
        restore()
      }
    },
    TEST_TIMEOUT_MS,
  )
})

describe('SettingsTabs — rail scroll contract', () => {
  test(
    'desktop rail scrolls vertically and can shrink inside the settings row',
    () => {
      const view = render(tree('general'))
      const tokens = (tabScroller(view.container).getAttribute('class') ?? '').split(/\s+/)

      expect(tokens).toContain('md:overflow-y-auto')
      expect(tokens).toContain('md:min-h-0')
      expect(tokens).toContain('md:overflow-x-visible')
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'reveals the active tab on the vertical axis in the desktop rail',
    () => {
      const restoreLayout = stubLayout({
        view: { left: 0, right: 200, top: 100, bottom: 500 },
        tabs: {
          general: { left: 8, right: 150, top: 100, bottom: 140 },
          logs: { left: 8, right: 150, top: 600, bottom: 640 },
        },
      })
      try {
        const view = render(tree('general'))
        expect(tabScroller(view.container).scrollTop).toBe(0)

        view.rerender(tree('logs'))
        expect(tabScroller(view.container).scrollTop).toBe(140)
        expect(tabScroller(view.container).scrollLeft).toBe(0)

        view.rerender(tree('general'))
        expect(tabScroller(view.container).scrollTop).toBe(0)
      } finally {
        restoreLayout()
      }
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'keeps the horizontal reveal for the mobile tab row',
    () => {
      const restoreLayout = stubLayout({
        view: { left: 0, right: 360, top: 64, bottom: 104 },
        tabs: {
          general: { left: 8, right: 88, top: 72, bottom: 96 },
          logs: { left: 700, right: 790, top: 72, bottom: 96 },
        },
      })
      try {
        const view = render(tree('general'))
        expect(tabScroller(view.container).scrollLeft).toBe(0)

        view.rerender(tree('logs'))
        expect(tabScroller(view.container).scrollLeft).toBe(430)
        expect(tabScroller(view.container).scrollTop).toBe(0)
      } finally {
        restoreLayout()
      }
    },
    TEST_TIMEOUT_MS,
  )
})

describe('SettingsTabs — active pill recipe', () => {
  test(
    'active tab paints accent-text on surface-selected (AA in both themes)',
    () => {
      const view = render(tree('mcp'))
      expectSelectedAccentRecipe(tabButton(view.container, 'mcp'))
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'inactive tabs never take the selected background',
    () => {
      const view = render(tree('mcp'))
      const inactive = view.container.querySelectorAll(
        `[data-testid^="${TAB_TESTID_PREFIX}"]:not([aria-current="page"])`,
      )
      expect(inactive.length).toBeGreaterThan(0)
      for (const tab of inactive) {
        const tokens = (tab.getAttribute('class') ?? '').split(/\s+/)
        expect(tokens).not.toContain(SELECTED_BACKGROUND_CLASS)
        expect(tokens).not.toContain(INADEQUATE_ACCENT_CLASS)
      }
    },
    TEST_TIMEOUT_MS,
  )
})
