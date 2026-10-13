import '../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import type { SkillInfo } from '../../lib/types'
import i18n from '../../test/i18n'
import { SkillsList } from './SkillsList'

/**
 * Capability-driven rendering of the shared skills grid.
 *
 * `SkillsList` is reused by all three `/skills` views (All, Global, By agent),
 * which differ only in WHICH controls a card offers. That difference is now
 * expressed by the optional `canRemove` / `canToggle` predicates instead of
 * the hard-coded `source !== 'builtin'` rule. These tests pin both halves:
 *
 *  1. omitted predicates reproduce today's behaviour byte-for-byte (the
 *     original consumer keeps its look), and
 *  2. explicit predicates win — a card set can be made read-only or scoped
 *     without touching the component's markup.
 *
 * `screen` is unusable in this environment (see src/test/setup.ts): every query
 * comes from the render result.
 */

const tr = (key: string, options?: Record<string, unknown>) => i18n.t(key, options) as string

function skill(overrides: Partial<SkillInfo> & { name: string }): SkillInfo {
  return {
    id: overrides.name,
    description: `${overrides.name} description`,
    installed: true,
    enabled: true,
    source: 'workspace',
    ...overrides,
  }
}

function setup(options: {
  skills: SkillInfo[]
  withToggle?: boolean
  canRemove?: (skill: SkillInfo) => boolean
  canToggle?: (skill: SkillInfo) => boolean
  emptyTitle?: string
  emptyDescription?: string
}) {
  const removed: string[] = []
  const toggled: Array<[string, boolean]> = []

  const utils = render(
    <SkillsList
      skills={options.skills}
      isLoading={false}
      isRemoving={null}
      onRemove={(name) => removed.push(name)}
      onToggle={options.withToggle ? (name, enabled) => toggled.push([name, enabled]) : undefined}
      canRemove={options.canRemove}
      canToggle={options.canToggle}
      emptyTitle={options.emptyTitle}
      emptyDescription={options.emptyDescription}
    />,
  )

  /**
   * The card root is the only element with a bare `group` class; the name
   * heading is the anchor a user reads. No test id exists in the component and
   * none may be invented (the card markup is pinned elsewhere).
   */
  const cardFor = (name: string): HTMLElement => {
    const headings = Array.from(utils.container.querySelectorAll('h4'))
    const heading = headings.find((el) => el.textContent?.trim() === name)
    if (!heading) throw new Error(`no card for ${name}`)
    const card = heading.closest('.group')
    if (!card) throw new Error(`card for ${name} has no .group root`)
    return card as HTMLElement
  }

  const cards = () => Array.from(utils.container.querySelectorAll('.group')) as HTMLElement[]

  /** The enable/disable button, identified by its glyph (its title is an
   * inline i18n default and therefore not a stable selector here). */
  const toggleFor = (name: string): HTMLButtonElement | null => {
    const found = Array.from(cardFor(name).querySelectorAll('button')).find((button) => {
      const glyph = button.textContent?.trim()
      return glyph === '●' || glyph === '○'
    })
    return (found as HTMLButtonElement | undefined) ?? null
  }

  const removeFor = (name: string): HTMLButtonElement | null =>
    cardFor(name).querySelector(
      `button[title="${tr('skills.removeSkill')}"]`,
    ) as HTMLButtonElement | null

  const buttonWithText = (name: string, text: string): HTMLButtonElement | null => {
    const found = Array.from(cardFor(name).querySelectorAll('button')).find(
      (button) => button.textContent?.trim() === text,
    )
    return (found as HTMLButtonElement | undefined) ?? null
  }

  return { ...utils, removed, toggled, cardFor, cards, toggleFor, removeFor, buttonWithText }
}

describe('SkillsList — capability defaults (today’s rule)', () => {
  const CARDS = [
    skill({ name: 'alpha', source: 'workspace' }),
    skill({ name: 'beta', source: 'global' }),
    skill({ name: 'gamma', source: 'builtin' }),
  ]

  test('no predicates: a workspace card offers BOTH controls', () => {
    const utils = setup({ skills: CARDS, withToggle: true })
    expect(utils.toggleFor('alpha')).toBeTruthy()
    expect(utils.removeFor('alpha')).toBeTruthy()
  })

  test('no predicates: a builtin card offers NEITHER control', () => {
    const utils = setup({ skills: CARDS, withToggle: true })
    expect(utils.toggleFor('gamma')).toBeNull()
    expect(utils.removeFor('gamma')).toBeNull()
  })

  test('no predicates: a global card keeps both controls', () => {
    const utils = setup({ skills: CARDS, withToggle: true })
    expect(utils.toggleFor('beta')).toBeTruthy()
    expect(utils.removeFor('beta')).toBeTruthy()
  })

  test('no predicates: the toggle still reports the inverted state', () => {
    const utils = setup({ skills: [skill({ name: 'alpha', enabled: true })], withToggle: true })
    fireEvent.click(utils.toggleFor('alpha') as HTMLButtonElement)
    expect(utils.toggled).toEqual([['alpha', false]])
  })
})

describe('SkillsList — canRemove', () => {
  const CARDS = [
    skill({ name: 'alpha', source: 'workspace' }),
    skill({ name: 'beta', source: 'global' }),
    skill({ name: 'gamma', source: 'builtin' }),
  ]

  test('() => false hides Remove on every card — builtin included', () => {
    const utils = setup({ skills: CARDS, canRemove: () => false, withToggle: true })
    expect(utils.removeFor('alpha')).toBeNull()
    expect(utils.removeFor('beta')).toBeNull()
    expect(utils.removeFor('gamma')).toBeNull()
    // Only Remove is gated: the toggles are unaffected.
    expect(utils.toggleFor('alpha')).toBeTruthy()
  })

  test('(s) => s.source === "global" shows Remove ONLY on global cards', () => {
    const utils = setup({
      skills: CARDS,
      canRemove: (entry) => entry.source === 'global',
    })
    expect(utils.removeFor('beta')).toBeTruthy()
    expect(utils.removeFor('alpha')).toBeNull()
    expect(utils.removeFor('gamma')).toBeNull()
  })
})

describe('SkillsList — canToggle', () => {
  const CARDS = [
    skill({ name: 'alpha', source: 'workspace' }),
    skill({ name: 'beta', source: 'global' }),
  ]

  test('onToggle omitted hides every toggle even with a permissive canToggle', () => {
    const utils = setup({ skills: CARDS, canToggle: () => true, withToggle: false })
    expect(utils.toggleFor('alpha')).toBeNull()
    expect(utils.toggleFor('beta')).toBeNull()
    // Remove is independent and still offered.
    expect(utils.removeFor('alpha')).toBeTruthy()
  })

  test('canToggle: () => false hides the toggle while onToggle is wired', () => {
    const utils = setup({ skills: CARDS, canToggle: () => false, withToggle: true })
    expect(utils.toggleFor('alpha')).toBeNull()
    expect(utils.toggleFor('beta')).toBeNull()
  })
})

describe('SkillsList — empty state copy', () => {
  test('omitting the overrides keeps skills.noSkills / skills.noSkillsDesc', () => {
    const utils = setup({ skills: [] })
    expect(utils.container.querySelector('h3')?.textContent).toContain(tr('skills.noSkills'))
    expect(utils.container.querySelector('p')?.textContent).toContain(tr('skills.noSkillsDesc'))
  })

  test('emptyTitle / emptyDescription replace the default copy', () => {
    const utils = setup({
      skills: [],
      emptyTitle: 'Aún no hay habilidades globales',
      emptyDescription: 'Elige esta pestaña antes de instalar.',
    })
    expect(utils.container.querySelector('h3')?.textContent).toBe('Aún no hay habilidades globales')
    expect(utils.container.querySelector('p')?.textContent).toBe(
      'Elige esta pestaña antes de instalar.',
    )
  })

  test('an override only replaces the string it targets', () => {
    const utils = setup({ skills: [], emptyTitle: 'Solo título' })
    expect(utils.container.querySelector('h3')?.textContent).toBe('Solo título')
    expect(utils.container.querySelector('p')?.textContent).toContain(tr('skills.noSkillsDesc'))
  })
})

describe('SkillsList — duplicate names across sources (F3)', () => {
  test('same name, different source: both cards render (composite key)', () => {
    const utils = setup({
      skills: [
        skill({ name: 'memory', source: 'workspace' }),
        skill({ name: 'memory', source: 'builtin' }),
      ],
      withToggle: true,
    })
    // `key={skill.name}` alone would collapse these two into one node.
    expect(utils.cards()).toHaveLength(2)
    // The workspace twin keeps its Remove control.
    expect(utils.removeFor('memory')).toBeTruthy()
    expect(utils.cards()[0].textContent).toContain('Workspace')
  })

  test('the confirmation is per source: removing one card leaves the other idle', () => {
    const utils = setup({
      skills: [
        skill({ name: 'memory', source: 'workspace' }),
        skill({ name: 'memory', source: 'global' }),
      ],
    })
    const first = utils.cards()[0]
    fireEvent.click(first.querySelector(`button[title="${tr('skills.removeSkill')}"]`) as Element)

    // Exactly one card switched to the confirm state.
    const confirming = utils
      .cards()
      .filter((card) => card.textContent?.includes(tr('skills.removeConfirmYes')))
    expect(confirming).toHaveLength(1)
    expect(confirming[0]).toBe(first)
  })
})

describe('SkillsList — inline remove confirmation', () => {
  test('clicking Remove asks first, then Yes calls onRemove(name) exactly once', () => {
    const utils = setup({ skills: [skill({ name: 'alpha' })] })

    fireEvent.click(utils.removeFor('alpha') as HTMLButtonElement)
    expect(utils.removed).toEqual([])

    fireEvent.click(
      utils.buttonWithText('alpha', tr('skills.removeConfirmYes')) as HTMLButtonElement,
    )
    expect(utils.removed).toEqual(['alpha'])
    // The confirmation closes on success.
    expect(utils.buttonWithText('alpha', tr('skills.removeConfirmYes'))).toBeNull()
  })

  test('No cancels the confirmation and removes nothing', () => {
    const utils = setup({ skills: [skill({ name: 'alpha' })] })

    fireEvent.click(utils.removeFor('alpha') as HTMLButtonElement)
    fireEvent.click(
      utils.buttonWithText('alpha', tr('skills.removeConfirmNo')) as HTMLButtonElement,
    )

    expect(utils.removed).toEqual([])
    expect(utils.removeFor('alpha')).toBeTruthy()
  })
})
