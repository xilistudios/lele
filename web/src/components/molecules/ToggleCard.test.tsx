import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import { Badge } from '../atoms/Badge'
import { ToggleCard } from './ToggleCard'

function setup(overrides: Partial<Parameters<typeof ToggleCard>[0]> = {}) {
  const onChange = mock((_checked: boolean) => {})
  const utils = render(
    <ToggleCard
      checked={false}
      id="tool-read-file"
      onChange={onChange}
      title="read_file"
      {...overrides}
    />,
  )
  const input = utils.container.querySelector('input[type="checkbox"]') as HTMLInputElement
  const card = utils.container.querySelector('label') as HTMLElement
  return { ...utils, onChange, input, card }
}

describe('ToggleCard', () => {
  test('root is a label wrapping a real sr-only checkbox', () => {
    const { card, input } = setup()
    expect(card).toBeTruthy()
    expect(input.className).toContain('sr-only')
    // The whole card is the click target: the label points at the input.
    expect(card.getAttribute('for')).toBe(input.id)
  })

  test('the checkbox is named by the title (accessible "read_file")', () => {
    const { input } = setup()
    expect(input.closest('label')?.textContent).toContain('read_file')
  })

  test('clicking the input reports a boolean, not an event', () => {
    const { input, onChange } = setup()
    fireEvent.click(input)
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(typeof onChange.mock.calls[0][0]).toBe('boolean')
    expect(onChange.mock.calls[0][0]).toBe(true)
  })

  test('an already-checked card reports false when toggled off', () => {
    const { input, onChange } = setup({ checked: true })
    fireEvent.click(input)
    expect(onChange.mock.calls[0][0]).toBe(false)
  })

  test('checked state drives the on/off classes', () => {
    const off = setup()
    expect(off.card.className).toContain('border-border')
    expect(off.card.className).toContain('bg-background-secondary')
    expect(off.card.className).toContain('hover:border-border-strong')
    expect(off.card.className).not.toContain('bg-accent-subtle')

    const on = setup({ checked: true })
    expect(on.card.className).toContain('border-interaction-primary/40')
    expect(on.card.className).toContain('bg-accent-subtle')
  })

  test('the custom check is filled and shows a glyph when on', () => {
    const off = setup()
    const offBox = off.card.querySelector('span[aria-hidden="true"]:last-child') as HTMLElement
    expect(offBox.className).toContain('border-border-strong')
    expect(offBox.querySelector('svg')).toBeNull()

    const on = setup({ checked: true })
    const onBox = on.card.querySelector('span[aria-hidden="true"]:last-child') as HTMLElement
    expect(onBox.className).toContain('bg-interaction-primary')
    expect(onBox.querySelector('svg')).toBeTruthy()
  })

  test('size sm = p-3 + 16px check (tools) · md = p-3.5 + 18px check (skills)', () => {
    const sm = setup({ size: 'sm' })
    expect(sm.card.className).toContain('p-3')
    const smBox = sm.card.querySelector('span[aria-hidden="true"]:last-child') as HTMLElement
    expect(smBox.className).toContain('h-4')

    const md = setup({ size: 'md' })
    expect(md.card.className).toContain('p-3.5')
    const mdBox = md.card.querySelector('span[aria-hidden="true"]:last-child') as HTMLElement
    expect(mdBox.className).toContain('h-[18px]')
  })

  test('title is monospace by default and can be switched off', () => {
    expect(setup().card.querySelector('span[title]')?.className).toContain('font-mono')
    const prose = setup({ titleMono: false })
    const title = prose.card.querySelector('span[title]') as HTMLElement
    expect(title.className).not.toContain('font-mono')
    expect(title.className).toContain('text-sm')
  })

  test('descriptionLines selects the clamp and the size', () => {
    const tool = setup({ description: 'desc', descriptionLines: 1 })
    const toolText = tool.card.querySelector('.line-clamp-1')
    expect(toolText?.textContent).toBe('desc')
    expect(toolText?.className).toContain('text-[11px]')

    const skill = setup({ description: 'desc', descriptionLines: 2 })
    expect(skill.card.querySelector('.line-clamp-2')?.textContent).toBe('desc')
  })

  test('badge renders in the title row', () => {
    const { card } = setup({ badge: <Badge variant="warning">Not installed</Badge> })
    expect(card.textContent).toContain('Not installed')
    expect(card.querySelector('.line-clamp-1')).toBeNull()
  })

  test('warning is announced through aria-describedby', () => {
    const { card, input } = setup({ warning: 'Disabled globally' })
    const describedBy = input.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    const warningEl = card.querySelector(`[id="${describedBy}"]`) as HTMLElement
    expect(warningEl.textContent).toBe('Disabled globally')
  })

  test('disabled greys the card and stops changes', () => {
    const { input, onChange, card } = setup({ disabled: true })
    expect(card.className).toContain('opacity-40')
    expect(input.disabled).toBe(true)
    fireEvent.click(input)
    expect(onChange).not.toHaveBeenCalled()
  })

  test('focus is announced on the card via focus-within', () => {
    const { card } = setup()
    expect(card.className).toContain('focus-within:outline-2')
    expect(card.className).toContain('focus-within:outline-interaction-primary')
  })
})
