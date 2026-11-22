import '../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import type { SkillInfo } from '../../lib/types'
import i18n from '../../test/i18n'
import { SkillsList } from './SkillsList'

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

  const cardFor = (name: string): HTMLElement => {
    const headings = Array.from(utils.container.querySelectorAll('h4'))
    const heading = headings.find((el) => el.textContent?.trim() === name)
    if (!heading) throw new Error(`no card for ${name}`)
    const card = heading.closest('.group')
    if (!card) throw new Error(`card for ${name} has no .group root`)
    return card as HTMLElement
  }

  const cards = () => Array.from(utils.container.querySelectorAll('.group')) as HTMLElement[]

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
    expect(utils.cards()).toHaveLength(2)
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
