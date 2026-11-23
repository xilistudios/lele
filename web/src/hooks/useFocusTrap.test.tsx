import '../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { useRef, useState } from 'react'
import { useFocusTrap } from './useFocusTrap'

function Harness({
  startActive = true,
  hidden = false,
}: { startActive?: boolean; hidden?: boolean }) {
  const [active, setActive] = useState(startActive)
  const containerRef = useRef<HTMLDivElement>(null)
  useFocusTrap(containerRef, active)

  return (
    <div>
      <button type="button" onClick={() => setActive((v) => !v)}>
        toggle
      </button>
      <div ref={containerRef}>
        <input aria-label="inside first" />
        <button type="button" aria-label="inside last">
          last
        </button>
        {hidden && <input type="hidden" name="token" />}
        {hidden && <button type="button" aria-label="inside disabled" disabled />}
      </div>
    </div>
  )
}

afterEach(cleanup)

describe('useFocusTrap', () => {
  test('Tab on the last element jumps to the first', () => {
    const { getByLabelText } = render(<Harness />)
    const first = getByLabelText('inside first')
    const last = getByLabelText('inside last')
    ;(last as HTMLElement).focus()
    fireEvent.keyDown(document, { key: 'Tab' })

    expect(document.activeElement).toBe(first)
  })

  test('Shift+Tab on the first element jumps to the last', () => {
    const { getByLabelText } = render(<Harness />)
    const first = getByLabelText('inside first')
    const last = getByLabelText('inside last')
    ;(first as HTMLElement).focus()
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })

    expect(document.activeElement).toBe(last)
  })

  test('Tab is left alone once the trap is inactive', () => {
    const { getByLabelText, getByRole } = render(<Harness />)
    const last = getByLabelText('inside last')

    fireEvent.click(getByRole('button', { name: 'toggle' }))
    ;(last as HTMLElement).focus()
    fireEvent.keyDown(document, { key: 'Tab' })

    expect(document.activeElement).toBe(last)
  })

  test('hidden and disabled controls never take the wrap endpoints', () => {
    const { getByLabelText } = render(<Harness hidden />)
    const first = getByLabelText('inside first')
    const last = getByLabelText('inside last')
    ;(last as HTMLElement).focus()
    fireEvent.keyDown(document, { key: 'Tab' })

    expect(document.activeElement).toBe(first)
    ;(first as HTMLElement).focus()
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })

    expect(document.activeElement).toBe(last)
  })
})
