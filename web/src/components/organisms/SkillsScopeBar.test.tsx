import '../../test/setup'
import { afterEach, describe, expect, mock, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { SKILLS_VIEWS, type SkillsView } from '../../lib/skillsScope'
import type { Agent } from '../../lib/types'
import i18n from '../../test/i18n'
import { SkillsScopeBar } from './SkillsScopeBar'

/**
 * SkillsScopeBar (plan T4 / §5.7). The bar is fully controlled, so the suite
 * asserts only the *wiring*: what it renders per view, and which callback each
 * interaction fires. SegmentedControl's own keyboard/roving-tabindex logic is
 * covered by SegmentedControl.test.tsx — here we only prove the delegation.
 *
 * House conventions: `screen` is unusable (its global binding is captured
 * before the JSDOM setup), so queries go through `render`'s bound helpers; the
 * i18n instance boots in Spanish (`test/i18n.ts`), so labels are resolved with
 * the `tr()` helper.
 */

/** The test env boots in Spanish; resolve labels through i18n (house pattern). */
function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options) as string
}

const AGENTS: Agent[] = [
  { id: 'coder', name: 'Coder', workspace: '/w/coder', model: 'gpt-4o', default: true },
  // Empty name: the option label must fall back to the id (`a.name || a.id`).
  { id: 'planner', name: '', workspace: '/w/planner', model: 'gpt-4o' },
]

type Overrides = {
  view?: SkillsView
  agents?: Agent[]
  selectedAgentId?: string | null
}

function setup(overrides: Overrides = {}) {
  const onViewChange = mock((_view: SkillsView) => {})
  const onAgentChange = mock((_agentId: string) => {})
  const utils = render(
    <SkillsScopeBar
      view={overrides.view ?? 'all'}
      onViewChange={onViewChange}
      agents={overrides.agents ?? AGENTS}
      selectedAgentId={overrides.selectedAgentId ?? null}
      onAgentChange={onAgentChange}
    />,
  )
  return { ...utils, onViewChange, onAgentChange }
}

/** The three segments, in DOM order. */
const radios = (container: HTMLElement) =>
  Array.from(container.querySelectorAll<HTMLElement>('[role="radio"]'))

const hint = (container: HTMLElement) =>
  container.querySelector('[data-testid="skills-scope-hint"]')?.textContent

const select = (container: HTMLElement) =>
  container.querySelector<HTMLSelectElement>('select#skills-agent')

afterEach(() => cleanup())

describe('SkillsScopeBar — tabs', () => {
  test('renders a radiogroup of three radios inside the labelled wrapper (C7)', () => {
    const { container } = setup()
    const wrapper = container.querySelector('[data-testid="skills-scope-tabs"]')
    expect(wrapper).toBeTruthy()
    const group = wrapper?.querySelector('[role="radiogroup"]')
    expect(group).toBeTruthy()
    expect(group?.getAttribute('id')).toBe('skills-view')
    expect(group?.getAttribute('aria-label')).toBe(tr('skills.scopeLabel'))
    expect(radios(container)).toHaveLength(SKILLS_VIEWS.length)
    expect(radios(container).map((r) => r.textContent)).toEqual(
      SKILLS_VIEWS.map((v) => tr(`skills.view.${v}`)),
    )
  })

  test('aria-checked marks only the active view, in tab order', () => {
    const { container } = setup({ view: 'global' })
    expect(radios(container).map((r) => r.getAttribute('aria-checked'))).toEqual([
      'false',
      'true',
      'false',
    ])
  })

  test('roving tabindex: only the active segment is tabbable', () => {
    const { container } = setup({ view: 'agent' })
    expect(radios(container).map((r) => r.getAttribute('tabindex'))).toEqual(['-1', '-1', '0'])
  })

  test('clicking a segment reports its SkillsView', () => {
    const { container, onViewChange } = setup({ view: 'all' })
    const globalIndex = SKILLS_VIEWS.indexOf('global')
    expect(radios(container)[globalIndex].textContent).toBe(tr('skills.view.global'))
    fireEvent.click(radios(container)[globalIndex])
    expect(onViewChange).toHaveBeenCalledTimes(1)
    expect(onViewChange.mock.calls[0][0]).toBe('global')
  })

  test('clicking the already-active segment is harmless', () => {
    const { container, onViewChange } = setup({ view: 'all' })
    expect(() => fireEvent.click(radios(container)[0])).not.toThrow()
    // SegmentedControl re-reports the value it was already on; the bar must
    // neither crash nor invent a different view. Either 0 or 1 call is fine.
    expect(onViewChange.mock.calls.length).toBeLessThanOrEqual(1)
    if (onViewChange.mock.calls.length === 1) {
      expect(onViewChange.mock.calls[0][0]).toBe('all')
    }
  })
})

describe('SkillsScopeBar — agent selector', () => {
  test('the select renders ONLY in the agent view', () => {
    expect(select(setup({ view: 'all' }).container)).toBeNull()
    cleanup()
    expect(select(setup({ view: 'global' }).container)).toBeNull()
    cleanup()
    expect(select(setup({ view: 'agent' }).container)).toBeTruthy()
  })

  test('lists every agent, labelling by name and falling back to the id', () => {
    const { container } = setup({ view: 'agent', selectedAgentId: 'coder' })
    const selectEl = select(container)
    expect(selectEl).toBeTruthy()
    expect(
      Array.from(selectEl?.options ?? []).map((o) => ({ value: o.value, label: o.textContent })),
    ).toEqual([
      { value: 'coder', label: 'Coder' },
      { value: 'planner', label: 'planner' },
    ])
    expect(selectEl?.value).toBe('coder')
  })

  test('changing the select reports the chosen agent id', () => {
    const { container, onAgentChange } = setup({ view: 'agent', selectedAgentId: 'coder' })
    const selectEl = select(container)
    if (!selectEl) throw new Error('select not rendered')
    fireEvent.change(selectEl, { target: { value: 'planner' } })
    expect(onAgentChange).toHaveBeenCalledTimes(1)
    expect(onAgentChange.mock.calls[0][0]).toBe('planner')
  })

  test("the visible <label> is the select's accessible name (C8)", () => {
    const { container } = setup({ view: 'agent', selectedAgentId: 'coder' })
    const label = container.querySelector('label[for="skills-agent"]')
    expect(label).toBeTruthy()
    expect(label?.textContent).toBe(tr('skills.selectAgent'))
  })

  test('a null selected id with agents present still renders a labelled select', () => {
    const { container } = setup({ view: 'agent', selectedAgentId: null })
    const selectEl = select(container)
    expect(selectEl).toBeTruthy()
    expect(selectEl?.options).toHaveLength(AGENTS.length)
    expect(container.querySelector('label[for="skills-agent"]')?.textContent).toBe(
      tr('skills.selectAgent'),
    )
    // The bar passes `selectedAgentId ?? ''`, but no <option> carries `''`, so
    // the browser (jsdom included) displays the first agent rather than a blank
    // select — inherent DOM behaviour, not component logic. In practice the
    // page only reaches this state with an empty agent list (D11), where the
    // whole block is skipped (covered above).
  })

  test('agents: [] in the agent view renders no select and does not throw (D11)', () => {
    const { container } = setup({ view: 'agent', agents: [], selectedAgentId: null })
    expect(select(container)).toBeNull()
    expect(container.querySelector('label[for="skills-agent"]')).toBeNull()
    expect(hint(container)).toBeTruthy()
  })
})

describe('SkillsScopeBar — hint line', () => {
  test('all view', () => {
    const { container } = setup({ view: 'all' })
    expect(hint(container)).toBe(tr('skills.viewAllHint'))
  })

  test('global view', () => {
    const { container } = setup({ view: 'global' })
    expect(hint(container)).toBe(tr('skills.viewGlobalHint'))
  })

  test('agent view interpolates the selected agent display name', () => {
    const { container } = setup({ view: 'agent', selectedAgentId: 'coder' })
    const text = hint(container)
    expect(text).toBe(tr('skills.viewAgentHint', { agent: 'Coder' }))
    expect(text).toContain('Coder')
    expect(text).not.toContain('{{agent}}')
  })

  test('agent view falls back to the id when the name is empty', () => {
    const { container } = setup({ view: 'agent', selectedAgentId: 'planner' })
    expect(hint(container)).toBe(tr('skills.viewAgentHint', { agent: 'planner' }))
  })

  test('agent view with no resolved agent never leaks the placeholder', () => {
    const { container } = setup({ view: 'agent', selectedAgentId: null })
    expect(hint(container)).toBe(tr('skills.viewAgentHint', { agent: tr('skills.selectAgent') }))
    expect(hint(container)).not.toContain('{{agent}}')
  })

  test('a stale id not present in the list also falls back, without the placeholder', () => {
    const { container } = setup({ view: 'agent', selectedAgentId: 'ghost' })
    expect(hint(container)).toBe(tr('skills.viewAgentHint', { agent: tr('skills.selectAgent') }))
    expect(hint(container)).not.toContain('{{agent}}')
  })
})
