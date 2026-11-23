import '../../test/setup'
import { beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import type { SettingsTab } from '../../lib/settingsTabs'
import { SettingsTabs } from './SettingsTabs'

const TEST_TIMEOUT_MS = 20_000

const logicValue = { groupsEnabled: false } as unknown as AppLogicContextValue

const NAV_TESTID = 'settings-tabs'
const TAB_TESTID_PREFIX = 'settings-tab-'

type Span = { left: number; right: number }

type StripLayout = {
  view: Span
  tabs: Partial<Record<SettingsTab, Span>>
}

const NO_SPAN: Span = { left: 0, right: 0 }

function toRect({ left, right }: Span): DOMRect {
  return {
    left,
    right,
    width: right - left,
    top: 0,
    bottom: 0,
    height: 0,
    x: left,
    y: 0,
    toJSON: () => ({ left, right }),
  } as DOMRect
}

function scrolledBy(element: Element): number {
  const nav = element.closest(`[data-testid="${NAV_TESTID}"]`) as HTMLElement | null
  return nav?.scrollLeft ?? 0
}

function layoutRect(layout: StripLayout, element: Element): DOMRect {
  const testId = element.getAttribute('data-testid')
  if (testId === NAV_TESTID) return toRect(layout.view)
  if (!testId?.startsWith(TAB_TESTID_PREFIX)) return toRect(NO_SPAN)
  const span = layout.tabs[testId.slice(TAB_TESTID_PREFIX.length) as SettingsTab]
  if (!span) return toRect(NO_SPAN)
  const shift = scrolledBy(element)
  return toRect({ left: span.left - shift, right: span.right - shift })
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
