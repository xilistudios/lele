import { describe, expect, mock, test } from 'bun:test'
import { render } from '@testing-library/react'
import i18n from '../../test/i18n'
import { ToolCallDisplay } from './ToolCallDisplay'

const LABEL = i18n.t('toolCalls.goal')

const ACTION = '🔍 Reviewing goal result...'

const GENERIC_ICON_START = 'M14.7 6.3a1 1 0 0 0 0 1.4'

function renderGoal(overrides: Record<string, string | undefined> = {}) {
  return render(
    <ToolCallDisplay
      toolName="goal"
      toolArgs={ACTION}
      toolStatus="completed"
      expanded={false}
      onToggleExpand={mock(() => {})}
      {...overrides}
    />,
  )
}

describe('ToolCallDisplay for tool "goal"', () => {
  test('renders the goal label and the backend action as summary', () => {
    const { container } = renderGoal()

    expect(container.textContent).toContain(LABEL)
    expect(container.textContent).toContain(ACTION)
    expect(container.textContent).not.toContain(i18n.t('toolCalls.genericAction'))
  })

  test('uses the dedicated target icon instead of the generic wrench', () => {
    const { container } = renderGoal()

    const paths = Array.from(container.querySelectorAll('svg path')).map((p) => p.getAttribute('d'))
    expect(paths.length).toBeGreaterThan(0)
    expect(paths.some((d) => d?.startsWith(GENERIC_ICON_START))).toBe(false)
    expect(paths[0]).toContain('a10 10 0 1 0 10 10')
    expect(paths).toContain('M12 6v6l4 2')
  })

  test('tints the icon with the accent color token', () => {
    const { getByTestId } = renderGoal()

    expect(getByTestId('tool-icon').className).toContain('text-accent-primary')
  })

  test('resolves the goal label in every shipped locale', () => {
    expect(i18n.t('toolCalls.goal', { lng: 'en' })).toBe('Goal review')
    expect(i18n.t('toolCalls.goal', { lng: 'es' })).toBe('Revisión de objetivo')
    expect(i18n.t('toolCalls.goal', { lng: 'pt' })).toBe('Revisão de objetivo')
  })
})
