import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import i18n from '../../test/i18n'
import { AgentChipMultiSelect } from './AgentChipMultiSelect'

// The shared test i18n boots in Spanish; read labels through the same keys the
// component uses so the assertions hold for any active locale.
const NO_OTHER_AGENTS = i18n.t('settings.agentPage.noOtherAgents', {
  defaultValue: 'No other agents to delegate to yet.',
})

const AGENTS = [
  { id: 'coder', name: 'Coder Agent' },
  { id: 'lele' },
  { id: 'research', name: 'Research' },
]

type Overrides = Partial<{
  value: string[]
  disabled: boolean
  agents: Array<{ id: string; name?: string }>
  empty: string
}>

function setup(overrides: Overrides = {}) {
  const onChange = mock((_value: string[]) => {})
  const props = {
    id: 'allowed',
    agents: AGENTS,
    value: ['coder'],
    ...overrides,
    onChange,
  }
  const utils = render(<AgentChipMultiSelect {...props} />)
  const chips = Array.from(utils.container.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'))
  return { ...utils, chips, onChange }
}

describe('AgentChipMultiSelect', () => {
  test('renders one chip per candidate, in the order received (positional dirty paths)', () => {
    const { chips } = setup()
    // The visible text is the avatar initial + the id; compare ids via aria-label.
    expect(chips.map((chip) => chip.getAttribute('aria-label'))).toEqual([
      'coder (Coder Agent)',
      'lele',
      'research (Research)',
    ])
    expect(chips.map((chip) => chip.id)).toEqual([
      'allowed-coder',
      'allowed-lele',
      'allowed-research',
    ])
  })

  test('each chip shows the avatar and the id (never the name as label)', () => {
    const { chips } = setup()
    const first = chips[0]
    expect(first.querySelector('[aria-hidden="true"]')).toBeTruthy() // AgentAvatar
    expect(first.textContent).toContain('coder')
    // name is available as tooltip/accessible name, not as visible duplicate text
    expect(first.getAttribute('title')).toBe('Coder Agent')
    expect(first.getAttribute('aria-label')).toBe('coder (Coder Agent)')
  })

  test('aria-checked reflects membership in value', () => {
    const { chips } = setup({ value: ['lele', 'research'] })
    expect(chips.map((chip) => chip.getAttribute('aria-checked'))).toEqual([
      'false',
      'true',
      'true',
    ])
  })

  test('clicking an unselected chip appends it and keeps the existing ones', () => {
    const { chips, onChange } = setup({ value: ['coder'] })
    fireEvent.click(chips[1])
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toEqual(['coder', 'lele'])
  })

  test('clicking a selected chip removes it, preserving the order of the rest', () => {
    const { chips, onChange } = setup({ value: ['coder', 'lele', 'research'] })
    fireEvent.click(chips[1])
    expect(onChange.mock.calls[0][0]).toEqual(['coder', 'research'])
  })

  test('on/off classes follow the spec', () => {
    const { chips } = setup({ value: ['coder'] })
    expect(chips[0].className).toContain('border-interaction-primary/40')
    expect(chips[0].className).toContain('bg-accent-subtle')
    expect(chips[0].className).toContain('text-text-primary')
    expect(chips[1].className).toContain('border-border')
    expect(chips[1].className).toContain('bg-background-secondary')
    expect(chips[1].className).toContain('text-text-tertiary')
    expect(chips[1].className).toContain('hover:border-border-strong')
  })

  test('chip shape: rounded-full pills inside a wrapping flex row with 6px gaps', () => {
    const { chips, container } = setup()
    expect(chips[0].className).toContain('rounded-full')
    expect(chips[0].className).toContain('text-xs')
    const group = container.querySelector('[role="group"]') as HTMLElement
    expect(group.className).toContain('flex')
    expect(group.className).toContain('flex-wrap')
    expect(group.className).toContain('gap-1.5')
  })

  test('Space/Enter work natively on buttons (keyboard toggle)', () => {
    const { chips, onChange } = setup({ value: [] })
    fireEvent.click(chips[2]) // what Space/Enter dispatch on a <button>
    expect(onChange.mock.calls[0][0]).toEqual(['research'])
  })

  test('disabled stops toggling and dims the chips', () => {
    const { chips, onChange } = setup({ disabled: true })
    fireEvent.click(chips[0])
    expect(onChange).not.toHaveBeenCalled()
    expect(chips[0].className).toContain('opacity-40')
    expect(chips[0].disabled).toBe(true)
  })

  test('empty candidates render the dashed block instead of chips', () => {
    const { chips, container } = setup({ agents: [] })
    expect(chips).toHaveLength(0)
    const block = container.querySelector('[data-testid="allowed-empty"]') as HTMLElement
    expect(block.className).toContain('border-dashed')
    expect(block.textContent).toContain(NO_OTHER_AGENTS)
  })

  test('custom empty content wins', () => {
    const { container } = setup({ agents: [], empty: 'nada aquí' })
    expect(container.querySelector('[data-testid="allowed-empty"]')?.textContent).toBe('nada aquí')
  })
})
