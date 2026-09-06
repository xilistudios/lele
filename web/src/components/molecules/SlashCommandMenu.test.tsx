import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import type { SlashCommandInfo } from '../../lib/types'
import '../../test/i18n'
import { SlashCommandMenu } from './SlashCommandMenu'

const clear: SlashCommandInfo = { name: '/clear', description: 'Clear history.', usage: '/clear' }
const compact: SlashCommandInfo = {
  name: '/compact',
  description: 'Compact history.',
  usage: '/compact',
}

describe('SlashCommandMenu', () => {
  test('renders a listbox with one option per command', () => {
    const { getByTestId, getAllByRole } = render(
      <SlashCommandMenu items={[clear, compact]} activeIndex={0} onSelect={mock(() => {})} />,
    )

    expect(getByTestId('slash-command-menu')).toBeTruthy()
    const options = getAllByRole('option')
    expect(options).toHaveLength(2)
    expect(options[0].textContent).toContain('/clear')
    expect(options[0].textContent).toContain('Clear history.')
  })

  test('marks only the active row as selected', () => {
    const { getAllByRole } = render(
      <SlashCommandMenu items={[clear, compact]} activeIndex={1} onSelect={mock(() => {})} />,
    )

    const options = getAllByRole('option')
    expect(options[0].getAttribute('aria-selected')).toBe('false')
    expect(options[1].getAttribute('aria-selected')).toBe('true')
  })

  test('mousedown selects the command without a click handler', () => {
    const seen: SlashCommandInfo[] = []
    const onSelect = mock((command: SlashCommandInfo) => {
      seen.push(command)
    })
    const { getAllByRole } = render(
      <SlashCommandMenu items={[clear, compact]} activeIndex={0} onSelect={onSelect} />,
    )

    fireEvent.mouseDown(getAllByRole('option')[1])
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(seen[0]).toEqual(compact)
  })

  test('hover reports the row index', () => {
    const onHover = mock(() => {})
    const { getAllByRole } = render(
      <SlashCommandMenu
        items={[clear, compact]}
        activeIndex={0}
        onSelect={mock(() => {})}
        onHover={onHover}
      />,
    )

    fireEvent.mouseEnter(getAllByRole('option')[1])
    expect(onHover).toHaveBeenCalledWith(1)
  })

  test('renders empty when there are no items', () => {
    const { getByTestId, queryAllByRole } = render(
      <SlashCommandMenu items={[]} activeIndex={0} onSelect={mock(() => {})} />,
    )

    expect(getByTestId('slash-command-menu')).toBeTruthy()
    expect(queryAllByRole('option')).toHaveLength(0)
  })

  // ── Custom (harness) commands ───────────────────────────────────────────

  const review: SlashCommandInfo = {
    name: '/review',
    description: 'Review the current diff',
    usage: '/review [args]',
    source: 'workspace',
  }

  test('renders custom commands with their description and source badge', () => {
    const { getAllByRole } = render(
      <SlashCommandMenu items={[clear, review]} activeIndex={1} onSelect={mock(() => {})} />,
    )

    const options = getAllByRole('option')
    expect(options[1].textContent).toContain('/review')
    expect(options[1].textContent).toContain('Review the current diff')
    expect(options[1].textContent).toContain('workspace')
  })

  test('built-in rows carry no source badge', () => {
    const { getAllByRole, queryByTestId } = render(
      <SlashCommandMenu items={[clear]} activeIndex={0} onSelect={mock(() => {})} />,
    )

    expect(getAllByRole('option')).toHaveLength(1)
    expect(queryByTestId('slash-command-source-/clear')).toBeNull()
  })

  test('selecting a custom command hands the whole entry to the composer', () => {
    const onSelect = mock(() => {})
    const { getAllByRole } = render(
      <SlashCommandMenu items={[review]} activeIndex={0} onSelect={onSelect} />,
    )

    fireEvent.mouseDown(getAllByRole('option')[0])
    expect(onSelect).toHaveBeenCalledWith(review)
  })
})
