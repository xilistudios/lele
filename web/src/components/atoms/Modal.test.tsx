import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { useState } from 'react'
import '../../test/i18n'
import { Modal } from './Modal'

afterEach(cleanup)

function FormModal({ onClose }: { onClose: () => void }) {
  const [value, setValue] = useState('')
  const handleClose = () => {
    setValue('')
    onClose()
  }
  return (
    <Modal isOpen onClose={handleClose} title="Add agent">
      <label htmlFor="modal-test-field">Agent ID</label>
      <input id="modal-test-field" value={value} onChange={(e) => setValue(e.target.value)} />
    </Modal>
  )
}

function EscapeHarness({ onClose }: { onClose: () => void }) {
  const [n, setN] = useState(0)
  return (
    <Modal
      isOpen
      onClose={() => {
        setN(n + 1)
        onClose()
      }}
      title="Escape"
    >
      <input aria-label="field" readOnly value={String(n)} />
    </Modal>
  )
}

describe('Modal focus regression — parent re-renders must not steal focus', () => {
  test('typing letter by letter keeps focus when onClose identity changes each render', () => {
    const { getByLabelText } = render(<FormModal onClose={() => {}} />)
    const input = getByLabelText('Agent ID') as HTMLInputElement

    input.focus()
    expect(document.activeElement).toBe(input)

    let expected = ''
    for (const ch of 'my-agent') {
      expected += ch
      fireEvent.change(input, { target: { value: expected } })
      expect(document.activeElement).toBe(input)
      expect(input.value).toBe(expected)
    }

    expect(input.value).toBe('my-agent')
  })

  test('re-render with a new onClose identity does not move focus to the first focusable', () => {
    const { rerender, getByLabelText } = render(<FormModal onClose={() => {}} />)
    const input = getByLabelText('Agent ID') as HTMLInputElement

    input.focus()
    rerender(<FormModal onClose={() => {}} />)

    expect(document.activeElement).toBe(input)
  })

  test('Escape calls the latest onClose closure', () => {
    const calls: string[] = []
    const view = render(<EscapeHarness onClose={() => calls.push('A')} />)
    view.rerender(<EscapeHarness onClose={() => calls.push('B')} />)

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(calls).toEqual(['B'])
  })

  test('focus returns to the trigger when the modal closes', () => {
    function Toggle() {
      const [open, setOpen] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            Open
          </button>
          {open && (
            <Modal isOpen onClose={() => setOpen(false)} title="Toggle">
              <input aria-label="field" />
            </Modal>
          )}
        </>
      )
    }

    const { getByText, getByLabelText } = render(<Toggle />)
    const trigger = getByText('Open') as HTMLButtonElement

    trigger.focus()
    fireEvent.click(trigger)
    const input = getByLabelText('field') as HTMLInputElement
    input.focus()

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(document.activeElement).toBe(trigger)
  })
})
