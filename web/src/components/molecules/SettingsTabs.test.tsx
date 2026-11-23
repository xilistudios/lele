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

type ScrollCall = { element: Element; options: ScrollIntoViewOptions | undefined }

type ScrollIntoViewHost = {
  scrollIntoView?: typeof Element.prototype.scrollIntoView
}

const elementProto = window.Element.prototype as ScrollIntoViewHost

function stubScrollIntoView(): { calls: ScrollCall[]; restore: () => void } {
  const calls: ScrollCall[] = []
  const original = elementProto.scrollIntoView
  elementProto.scrollIntoView = function scrollIntoViewStub(
    this: Element,
    arg?: boolean | ScrollIntoViewOptions,
  ) {
    calls.push({ element: this, options: typeof arg === 'object' ? arg : undefined })
  } as typeof Element.prototype.scrollIntoView
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
  const button = container.querySelector(`[data-testid="settings-tab-${tab}"]`)
  if (!button) throw new Error(`no tab button for ${tab}`)
  return button
}

const scrolledTabs = (calls: ScrollCall[]) =>
  calls.map((call) => call.element.getAttribute('data-testid'))

beforeEach(cleanup)

describe('SettingsTabs — active tab visibility', () => {
  test(
    'scrolls the active tab into view on mount',
    () => {
      const spy = stubScrollIntoView()
      try {
        const view = render(tree('mcp'))

        expect(spy.calls.length).toBe(1)
        expect(spy.calls[0].element).toBe(tabButton(view.container, 'mcp'))
        expect(spy.calls[0].options).toEqual({ block: 'nearest', inline: 'nearest' })
      } finally {
        spy.restore()
      }
    },
    TEST_TIMEOUT_MS,
  )

  test(
    'scrolls the new active tab into view when the tab changes',
    () => {
      const spy = stubScrollIntoView()
      try {
        const view = render(tree('general'))
        expect(scrolledTabs(spy.calls)).toEqual(['settings-tab-general'])

        view.rerender(tree('logs'))

        expect(scrolledTabs(spy.calls)).toEqual(['settings-tab-general', 'settings-tab-logs'])
        expect(spy.calls[1].options).toEqual({ block: 'nearest', inline: 'nearest' })
      } finally {
        spy.restore()
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
      } finally {
        restore()
      }
    },
    TEST_TIMEOUT_MS,
  )
})
