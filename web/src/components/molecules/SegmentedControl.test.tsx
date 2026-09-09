import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import { type SegmentOption, SegmentedControl } from './SegmentedControl'

const OPTIONS: SegmentOption<string>[] = [
  { value: '', label: 'Heredar' },
  { value: 'off', label: 'Off' },
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
]

function setup(
  overrides: { value?: string; disabled?: boolean; size?: 'sm' | 'md'; fullWidth?: boolean } = {},
) {
  const onChange = mock((_value: string) => {})
  const utils = render(
    <SegmentedControl
      ariaLabel="Thinking level"
      id="thinking"
      options={OPTIONS}
      value="off"
      onChange={onChange}
      {...overrides}
    />,
  )
  return { ...utils, onChange }
}

/** Options as rendered, in DOM order. */
const radios = (container: HTMLElement) =>
  Array.from(container.querySelectorAll<HTMLElement>('[role="radio"]'))

/** Last value reported to onChange. */
const lastCall = (onChange: ReturnType<typeof mock>) =>
  (onChange.mock.calls.at(-1) as [string] | undefined)?.[0]

describe('SegmentedControl', () => {
  test('renders a radiogroup with one radio per option', () => {
    const { container } = setup()
    expect(container.querySelector('[role="radiogroup"]')).toBeTruthy()
    expect(radios(container)).toHaveLength(5)
    expect(container.textContent).toContain('Heredar')
  })

  test('aria-checked marks only the current value; "" is a valid value', () => {
    const { container } = setup({ value: '' })
    expect(radios(container).map((item) => item.getAttribute('aria-checked'))).toEqual([
      'true',
      'false',
      'false',
      'false',
      'false',
    ])
    expect(radios(container)[0].textContent).toBe('Heredar')
  })

  test('roving tabindex: only the selected option is tabbable', () => {
    const { container } = setup({ value: 'low' })
    expect(radios(container).map((item) => item.getAttribute('tabindex'))).toEqual([
      '-1',
      '-1',
      '0',
      '-1',
      '-1',
    ])
  })

  test('clicking a segment reports its value', () => {
    const { container, onChange } = setup()
    fireEvent.click(radios(container)[3])
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(lastCall(onChange)).toBe('medium')
  })

  test('ArrowRight moves to the next option and activates it', () => {
    const { container, onChange } = setup({ value: 'off' })
    const items = radios(container)
    expect(items[1].getAttribute('tabindex')).toBe('0')
    fireEvent.keyDown(items[1], { key: 'ArrowRight' })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(lastCall(onChange)).toBe('low')
  })

  test('ArrowLeft wraps backwards past the first option', () => {
    const { container, onChange } = setup({ value: '' })
    fireEvent.keyDown(radios(container)[0], { key: 'ArrowLeft' })
    expect(lastCall(onChange)).toBe('high')
  })

  test('ArrowRight wraps forwards past the last option', () => {
    const { container, onChange } = setup({ value: 'high' })
    fireEvent.keyDown(radios(container)[4], { key: 'ArrowRight' })
    expect(lastCall(onChange)).toBe('')
  })

  test('Home / End jump to the ends', () => {
    const { container, onChange } = setup({ value: 'low' })
    const items = radios(container)
    fireEvent.keyDown(items[2], { key: 'End' })
    expect(lastCall(onChange)).toBe('high')
    fireEvent.keyDown(items[2], { key: 'Home' })
    expect(lastCall(onChange)).toBe('')
  })

  test('Enter and Space activate the focused option', () => {
    const { container, onChange } = setup({ value: 'off' })
    const items = radios(container)
    fireEvent.keyDown(items[1], { key: 'Enter' })
    fireEvent.keyDown(items[1], { key: ' ' })
    expect(onChange).toHaveBeenCalledTimes(2)
    expect(lastCall(onChange)).toBe('off')
  })

  test('sizes: md h-9 text-sm (default) · sm h-7 text-xs', () => {
    const { container } = setup()
    expect(radios(container)[0].className).toContain('h-9')
    expect(radios(container)[0].className).toContain('text-sm')

    const small = setup({ size: 'sm' })
    expect(radios(small.container)[0].className).toContain('h-7')
    expect(radios(small.container)[0].className).toContain('text-xs')
  })

  test('active segment carries the raised-surface classes', () => {
    const { container } = setup({ value: 'low' })
    const items = radios(container)
    expect(items[2].className).toContain('bg-background-secondary')
    expect(items[2].className).toContain('shadow-card')
    expect(items[0].className).toContain('text-text-tertiary')
  })

  test('fullWidth stretches the group, default does not', () => {
    const group = (container: HTMLElement) =>
      container.querySelector('[role="radiogroup"]') as HTMLElement
    expect(group(setup().container).className).not.toContain('w-full')
    expect(group(setup({ fullWidth: true }).container).className).toContain('w-full')
  })

  test('disabled blocks clicks and keys', () => {
    const { container, onChange } = setup({ disabled: true })
    const items = radios(container)
    fireEvent.click(items[2])
    fireEvent.keyDown(items[1], { key: 'ArrowRight' })
    expect(onChange).not.toHaveBeenCalled()
  })
})
