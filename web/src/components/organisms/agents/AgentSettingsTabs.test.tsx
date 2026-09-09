import '../../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import '../../../test/i18n'
import { AGENT_TABS, type AgentTab, isSectionDirty } from '../../../lib/agentDirty'
import type { ConfigError } from '../../../lib/types'
import {
  AGENT_TAB_ORDER,
  AGENT_TAB_PANEL_ID,
  AgentSettingsTabs,
  sectionHasError,
} from './AgentSettingsTabs'

type Props = {
  agentIndex?: number
  activeTab?: AgentTab
  dirtyPaths?: Set<string>
  validationErrors?: ConfigError[]
}

function setup(overrides: Props = {}) {
  const calls: AgentTab[] = []
  const utils = render(
    <AgentSettingsTabs
      agentIndex={overrides.agentIndex ?? 2}
      activeTab={overrides.activeTab ?? 'model'}
      dirtyPaths={overrides.dirtyPaths ?? new Set()}
      validationErrors={overrides.validationErrors ?? []}
      onTabChange={(tab) => calls.push(tab)}
    />,
  )
  return { ...utils, tabs: utils.getAllByRole('tab'), calls }
}

/** Which tabs show a dot of the given kind? Returns their ids. */
function dotted(tabs: HTMLElement[], kind: 'dirty' | 'error'): string[] {
  return tabs
    .filter((tab) => tab.querySelector(`[data-testid="tab-dot-${kind}"]`))
    .map((tab) => tab.id)
}

const originalInnerWidth = window.innerWidth

function setViewport(width: number) {
  Object.defineProperty(window, 'innerWidth', { value: width, configurable: true })
}

function renderAt(width: number) {
  setViewport(width)
  return render(
    <AgentSettingsTabs
      agentIndex={0}
      activeTab="general"
      dirtyPaths={new Set()}
      validationErrors={[]}
      onTabChange={() => undefined}
    />,
  )
}

describe('sectionHasError', () => {
  const error = (path: string): ConfigError => ({ path, message: 'bad', code: 'invalid' })

  test('no errors lights nothing', () => {
    expect(sectionHasError([], 2, 'model')).toBe(false)
  })

  test('a nested error lights its own section (§5.4)', () => {
    const errors = [error('agents.list.2.model.primary')]
    expect(sectionHasError(errors, 2, 'model')).toBe(true)
    expect(sectionHasError(errors, 2, 'general')).toBe(false)
  })

  test('an error of another agent lights nothing', () => {
    const errors = [error('agents.list.3.model.primary')]
    expect(sectionHasError(errors, 2, 'model')).toBe(false)
  })

  test('array-segment children match (…tools[0])', () => {
    expect(sectionHasError([error('agents.list.2.tools[0]')], 2, 'tools')).toBe(true)
  })

  test('errors above the section light nothing (duplicate id → footer reports it)', () => {
    const errors = [error('agents.list.2')]
    expect(AGENT_TABS.every((tab) => !sectionHasError(errors, 2, tab))).toBe(true)
  })

  test('files never lights: that tab writes no config', () => {
    expect(sectionHasError([error('agents.list.2.name')], 2, 'files')).toBe(false)
  })
})

describe('AgentSettingsTabs', () => {
  beforeEach(() => setViewport(1280))
  afterEach(() => {
    Object.defineProperty(window, 'innerWidth', { value: originalInnerWidth, configurable: true })
    cleanup()
  })

  test('renders the six tabs in spec order (§1.3)', () => {
    const { tabs } = setup()
    expect(AGENT_TAB_ORDER).toEqual([...AGENT_TABS])
    expect(tabs.map((tab) => tab.id)).toEqual(AGENT_TABS.map((tab) => `agent-tab-${tab}`))
  })

  test('every tab carries an icon (§4.1 diferencia 1)', () => {
    const { tabs } = setup()
    for (const tab of tabs) {
      expect(tab.querySelector('svg')).toBeTruthy()
    }
  })

  test('active tab is aria-selected and owns the roving tabIndex (§6)', () => {
    const { tabs } = setup({ activeTab: 'skills' })
    const selected = tabs.filter((tab) => tab.getAttribute('aria-selected') === 'true')
    expect(selected).toHaveLength(1)
    expect(selected[0].id).toBe('agent-tab-skills')
    expect(selected[0].getAttribute('tabindex')).toBe('0')
    for (const tab of tabs.filter((t) => t !== selected[0])) {
      expect(tab.getAttribute('aria-selected')).toBe('false')
      expect(tab.getAttribute('tabindex')).toBe('-1')
    }
  })

  test('each tab controls the shared panel (§6)', () => {
    const { tabs } = setup()
    for (const tab of tabs) {
      expect(tab.getAttribute('aria-controls')).toBe(AGENT_TAB_PANEL_ID)
    }
  })

  test("clicking a tab reports its id (navigation is the parent's job)", () => {
    const { tabs, calls } = setup()
    fireEvent.click(tabs[3])
    fireEvent.click(tabs[5])
    expect(calls).toEqual(['tools', 'files'])
  })

  describe('keyboard (§6)', () => {
    test('ArrowDown and ArrowRight advance and activate', () => {
      const { tabs, calls } = setup({ activeTab: 'general' })
      fireEvent.keyDown(tabs[0], { key: 'ArrowDown' })
      fireEvent.keyDown(tabs[0], { key: 'ArrowRight' })
      expect(calls).toEqual(['model', 'model'])
    })

    test('ArrowUp and ArrowLeft go back', () => {
      const { tabs, calls } = setup({ activeTab: 'model' })
      fireEvent.keyDown(tabs[1], { key: 'ArrowUp' })
      fireEvent.keyDown(tabs[1], { key: 'ArrowLeft' })
      expect(calls).toEqual(['general', 'general'])
    })

    test('wraps around at both ends', () => {
      const { tabs, calls } = setup({ activeTab: 'files' })
      fireEvent.keyDown(tabs[5], { key: 'ArrowDown' })
      expect(calls).toEqual(['general'])
      fireEvent.keyDown(tabs[0], { key: 'ArrowUp' })
      expect(calls).toEqual(['general', 'files'])
    })

    test('Home/End jump to the ends', () => {
      const { tabs, calls } = setup({ activeTab: 'skills' })
      fireEvent.keyDown(tabs[2], { key: 'End' })
      fireEvent.keyDown(tabs[2], { key: 'Home' })
      expect(calls).toEqual(['files', 'general'])
    })

    test('unrelated keys do nothing', () => {
      const { tabs, calls } = setup()
      fireEvent.keyDown(tabs[1], { key: 'a' })
      fireEvent.keyDown(tabs[1], { key: 'Enter' })
      fireEvent.keyDown(tabs[1], { key: ' ' })
      expect(calls).toEqual([])
    })

    test('the activated tab receives focus', () => {
      const { tabs } = setup({ activeTab: 'general' })
      fireEvent.keyDown(tabs[0], { key: 'ArrowDown' })
      expect(document.activeElement).toBe(tabs[1])
    })
  })

  describe('dirty dot (§5.3)', () => {
    test('absent when the section is clean', () => {
      const { tabs } = setup({ dirtyPaths: new Set(['agents.list.2.name']) })
      expect(dotted(tabs, 'dirty')).toEqual(['agent-tab-general'])
    })

    test('blue dot only on the section holding the change', () => {
      const { tabs } = setup({ dirtyPaths: new Set(['agents.list.2.model.primary']) })
      expect(dotted(tabs, 'dirty')).toEqual(['agent-tab-model'])
      expect(tabs[1].querySelector('.bg-state-info')).toBeTruthy()
    })

    test('is positional: another agent dirty lights nothing here', () => {
      const { tabs } = setup({ dirtyPaths: new Set(['agents.list.3.model.primary']) })
      expect(dotted(tabs, 'dirty')).toEqual([])
    })

    test('reads the index it is given, not a fixed one', () => {
      const agentIndex = 5
      const dirty = new Set(['agents.list.5.tools', 'agents.list.2.model'])
      const { tabs } = setup({ agentIndex, dirtyPaths: dirty })
      expect(dotted(tabs, 'dirty')).toEqual(['agent-tab-tools'])
    })

    test('nested path lights its section (…tools.allowed[0] → tools)', () => {
      const { tabs } = setup({ dirtyPaths: new Set(['agents.list.2.tools.allowed[0]']) })
      expect(dotted(tabs, 'dirty')).toEqual(['agent-tab-tools'])
    })

    test('files never gets a dot', () => {
      const { tabs } = setup({ dirtyPaths: new Set(['agents.list.2.files']) })
      expect(dotted(tabs, 'dirty')).toEqual([])
    })

    test('dot is not colour-only: aria-hidden circle + sr-only text (§10.5)', () => {
      const { tabs } = setup({ dirtyPaths: new Set(['agents.list.2.name']) })
      const dot = tabs[0].querySelector('[data-testid="tab-dot-dirty"]')
      expect(dot?.getAttribute('aria-hidden')).toBe('true')
      expect(tabs[0].querySelector('.sr-only')?.textContent).toBe(
        'Cambios sin guardar en esta sección',
      )
    })
  })

  describe('error dot (§5.4)', () => {
    test('error wins over dirty (priority error > dirty > nothing)', () => {
      const { tabs } = setup({
        dirtyPaths: new Set(['agents.list.2.model.primary']),
        validationErrors: [{ path: 'agents.list.2.model.primary', message: 'bad', code: 'x' }],
      })
      expect(dotted(tabs, 'error')).toEqual(['agent-tab-model'])
      expect(dotted(tabs, 'dirty')).toEqual([])
      expect(tabs[1].querySelector('.bg-state-error')).toBeTruthy()
    })

    test('an error lights its tab even when nothing is dirty', () => {
      const { tabs } = setup({
        validationErrors: [{ path: 'agents.list.2.name', message: 'required', code: 'x' }],
      })
      expect(dotted(tabs, 'error')).toEqual(['agent-tab-general'])
    })
  })

  describe('aria-orientation follows the layout (§6, §8)', () => {
    test('vertical on the rail (>=1024px)', () => {
      const { getByRole } = renderAt(1280)
      expect(getByRole('tablist').getAttribute('aria-orientation')).toBe('vertical')
    })

    test('horizontal in the bar (<1024px)', () => {
      const { getByRole } = renderAt(800)
      expect(getByRole('tablist').getAttribute('aria-orientation')).toBe('horizontal')
    })
  })

  test('dots agree with isSectionDirty for every tab of a multi-section draft', () => {
    const dirty = new Set([
      'agents.list.2.name',
      'agents.list.2.model',
      'agents.list.2.skills[0]',
      'agents.list.1.tools',
    ])
    const { tabs } = setup({ dirtyPaths: dirty })
    const expected = AGENT_TAB_ORDER.filter((tab) => isSectionDirty(dirty, 2, tab))
    expect(dotted(tabs, 'dirty')).toEqual(expected.map((tab) => `agent-tab-${tab}`))
    expect(expected).toEqual(['general', 'model', 'skills'])
  })
})
