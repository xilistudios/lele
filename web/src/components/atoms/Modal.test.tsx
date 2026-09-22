/**
 * Modal.test.tsx
 *
 * Regression test for the "focus lost letter by letter" bug in the Add Agent
 * wizard (and every modal whose parent holds form state).
 *
 * Root cause: the open/close effect depended on `onClose`. A parent that
 * re-renders on each keystroke (e.g. AddAgentModal with local form state)
 * passes a fresh `handleClose` closure every render, so the effect re-ran on
 * every keystroke: its cleanup restored focus to the previously focused
 * element and the re-run moved focus to the first focusable element.
 *
 * These tests pin the contract:
 *  1. Typing in a modal input keeps focus even when onClose identity changes.
 *  2. Escape calls the LATEST onClose closure.
 *  3. Focus is restored to the trigger only when the modal actually closes.
 */

import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { useState } from 'react'
import '../../test/i18n'
import { Modal } from './Modal'

afterEach(cleanup)

/**
 * Mirrors AddAgentModal: form state lives in the parent of <Modal>, and the
 * onClose handler is an inline closure recreated on every render.
 */
function FormModal({ onClose }: { onClose: () => void }) {
  const [value, setValue] = useState('')
  // New closure identity on every render — the exact trigger of the bug.
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

/** Same shape but with an Escape handler whose closure content can change. */
function EscapeHarness({ onClose }: { onClose: () => void }) {
  const [n, setN] = useState(0)
  return (
    <Modal
      isOpen
      onClose={() => {
        setN(n + 1) // forces a fresh closure each render
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

    // Each keystroke re-renders FormModal → new handleClose identity. Before
    // the fix this re-ran the Modal effect and stole focus on every character.
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
    // First render passes closure A...
    const view = render(<EscapeHarness onClose={() => calls.push('A')} />)
    // ...second render passes closure B.
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
