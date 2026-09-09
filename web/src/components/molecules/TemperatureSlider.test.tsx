import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import i18n from '../../test/i18n'
import { TemperatureSlider } from './TemperatureSlider'

// The shared test i18n boots in Spanish; read the label through the same key
// the component uses so the assertion holds for any active locale.
const NOT_SET = i18n.t('settings.agentPage.tempNotSet', { defaultValue: 'Model default' })
const RESET = i18n.t('settings.agentPage.tempReset', { defaultValue: 'Reset to model default' })

function setup(
  overrides: {
    value?: number
    disabled?: boolean
    min?: number
    max?: number
    modelDefault?: number
  } = {},
) {
  const onChange = mock((_value: number | undefined) => {})
  const utils = renderSlider({ ...overrides, onChange })
  const input = utils.container.querySelector('input[type="range"]') as HTMLInputElement
  return { ...utils, input, onChange }
}

/** Render the slider bare (no mock wiring). */
function renderSlider(
  props: Partial<Parameters<typeof TemperatureSlider>[0]> & {
    onChange: (value: number | undefined) => void
  },
) {
  return render(
    <TemperatureSlider
      ariaLabel="Temperature"
      id="temp"
      value={undefined}
      {...props}
      onChange={props.onChange}
    />,
  )
}

/** Click the reset button (only rendered when a value is set). */
function pressReset(container: HTMLElement) {
  const button = container.querySelector(`button[aria-label="${RESET}"]`) as HTMLButtonElement
  fireEvent.click(button)
}

describe('TemperatureSlider', () => {
  test('native range input with the spec range and step', () => {
    const { input } = setup({ value: 1 })
    expect(input.min).toBe('0')
    expect(input.max).toBe('2')
    expect(input.step).toBe('0.05')
    expect(input.value).toBe('1')
  })

  test('exposes the slider ARIA contract', () => {
    const { input } = setup({ value: 0.85 })
    expect(input.getAttribute('aria-valuemin')).toBe('0')
    expect(input.getAttribute('aria-valuemax')).toBe('2')
    expect(input.getAttribute('aria-valuenow')).toBe('0.85')
    expect(input.getAttribute('aria-valuetext')).toBe('0.85')
    expect(input.getAttribute('aria-label')).toBe('Temperature')
  })

  test('dragging writes a number', () => {
    const { input, onChange } = setup({ value: 0.7 })
    fireEvent.change(input, { target: { value: '1.35' } })
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toBe(1.35)
  })

  test('undefined value = inherit mode: thumb parked at the model default (0.7)', () => {
    const { container, input } = setup({ value: undefined })
    expect(input.value).toBe('0.7')
    expect(input.getAttribute('aria-valuenow')).toBe('0.7')
    expect(input.getAttribute('aria-valuetext')).toContain(NOT_SET)
    expect(input.dataset.inherited).toBe('true')
    expect(input.className).toContain('lele-range--inherit')
    // "Default" badge next to the readout, and the readout itself shows 0.7.
    expect(container.textContent).toContain(NOT_SET)
    expect(container.querySelector('.font-mono')?.textContent).toBe('0.7')
  })

  test('modelDefault moves the inherited parking point and the tick', () => {
    const { container, input } = setup({ modelDefault: 1.2, value: undefined })
    expect(input.value).toBe('1.2')
    expect(input.getAttribute('aria-valuetext')).toContain(NOT_SET)
    expect(container.textContent).toContain('1.2')
  })

  test('reset writes undefined (inherit), not 0.7', () => {
    const { container, onChange } = setup({ value: 1.4 })
    pressReset(container)
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(onChange.mock.calls[0][0]).toBeUndefined()
  })

  test('reset button only exists while a value is set', () => {
    const set = setup({ value: 0.7 })
    expect(set.container.querySelector('button')).toBeTruthy()
    const inherited = setup({ value: undefined })
    expect(inherited.container.querySelector('button')).toBeNull()
  })

  test('first drag from inherit mode produces a number (and thus dirty)', () => {
    const { input, onChange } = setup({ value: undefined })
    fireEvent.change(input, { target: { value: '0.2' } })
    expect(onChange.mock.calls[0][0]).toBe(0.2)
  })

  test('ticks: 0 · 0.7 (marked as default) · 2', () => {
    const { getByTestId } = setup({ value: undefined })
    const ticksRow = getByTestId('temp-ticks') as HTMLElement
    expect(Array.from(ticksRow.querySelectorAll('span')).map((span) => span.textContent)).toEqual([
      '0',
      '0.7',
      '2',
    ])
    expect(ticksRow.querySelector('span[title]')?.getAttribute('title')).toContain(NOT_SET)
  })

  test('custom min/max are honored', () => {
    const { input } = setup({ max: 1, min: 0, value: 0.5 })
    expect(input.min).toBe('0')
    expect(input.max).toBe('1')
  })

  test('disabled stops interaction and marks the control', () => {
    const { container, input, onChange } = setup({ disabled: true, value: 1 })
    expect(input.disabled).toBe(true)
    expect(input.getAttribute('aria-disabled')).toBe('true')
    pressReset(container)
    expect(onChange).not.toHaveBeenCalled()
  })

  test('out-of-range values are clamped for display instead of breaking the track', () => {
    const { input } = setup({ value: 9 })
    expect(input.value).toBe('2')
    const low = setup({ value: -3 })
    expect(low.input.value).toBe('0')
  })
})
