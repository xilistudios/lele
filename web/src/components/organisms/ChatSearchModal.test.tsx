import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render } from '@testing-library/react'
import { useState } from 'react'
import { MemoryRouter } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import type { ChatSession } from '../../lib/types'
import { ChatSearchModal } from './ChatSearchModal'

const sessions = [
  { key: 'native:one', name: 'First chat', updated: '2026-01-02T10:00:00Z', mode: 'agent' },
  { key: 'native:two', name: 'Second chat', updated: '2026-01-01T10:00:00Z', mode: 'agent' },
] as unknown as ChatSession[]

type Handles = { selected: string[]; closed: number }

function renderHarness(handles: Handles = { selected: [], closed: 0 }) {
  const logicValue = {
    sessions,
    currentSessionKey: null,
    processingSessions: new Set<string>(),
    onSelectSession: (key: string) => handles.selected.push(key),
    onDeleteSession: async () => undefined,
  } as unknown as AppLogicContextValue

  function Harness() {
    const [open, setOpen] = useState(true)
    return (
      <AppLogicContext.Provider value={logicValue}>
        <MemoryRouter>
          <button type="button" onClick={() => setOpen(true)}>
            reopen
          </button>
          <ChatSearchModal
            isOpen={open}
            onClose={() => {
              handles.closed += 1
              setOpen(false)
            }}
          />
        </MemoryRouter>
      </AppLogicContext.Provider>
    )
  }

  return { ...render(<Harness />), handles }
}

const wait = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

afterEach(() => {
  cleanup()
  document.body.style.overflow = ''
})

describe('ChatSearchModal — dialog shell', () => {
  test('renders a modal dialog with the entrance animation', () => {
    const { getByRole } = renderHarness()

    const dialog = getByRole('dialog')
    expect(dialog.getAttribute('aria-modal')).toBe('true')
    expect(dialog.className).toContain('modal-content-in')
  })

  test('focuses the search field and locks page scroll while open', () => {
    const { getByPlaceholderText } = renderHarness()

    expect(document.activeElement).toBe(getByPlaceholderText(/buscar/i))
    expect(document.body.style.overflow).toBe('hidden')
  })
})

describe('ChatSearchModal — closing', () => {
  test('runs the exit animation before unmounting and restores page scroll', async () => {
    const { getByRole, queryByRole } = renderHarness()

    fireEvent.keyDown(document, { key: 'Escape' })

    const dialog = getByRole('dialog')
    expect(dialog.className).toContain('modal-content-out')
    expect(document.body.style.overflow).toBe('')

    await act(async () => {
      await wait(300)
    })
    expect(queryByRole('dialog')).toBeNull()
  })

  test('the close button closes the dialog', () => {
    const { getByLabelText, handles } = renderHarness()

    fireEvent.click(getByLabelText(/cerrar panel/i))

    expect(handles.closed).toBe(1)
  })

  test('selecting a chat opens it and closes the dialog', () => {
    const { getByText, handles } = renderHarness()

    fireEvent.click(getByText('Second chat'))

    expect(handles.selected).toEqual(['native:two'])
    expect(handles.closed).toBe(1)
  })
})

describe('ChatSearchModal — keyboard containment', () => {
  test('Tab from the last control wraps to the first', () => {
    const { getByRole, getByText } = renderHarness()

    const dialog = getByRole('dialog')
    const first = getByRole('textbox')
    const controls = dialog.querySelectorAll<HTMLElement>('button, input')
    const last = controls[controls.length - 1]
    expect(last).toBe(getByText(/administrar chats/i))

    last.focus()
    fireEvent.keyDown(document, { key: 'Tab' })

    expect(document.activeElement).toBe(first)
  })

  test('Shift+Tab from the first control wraps to the last', () => {
    const { getByRole, getByText } = renderHarness()

    const dialog = getByRole('dialog')
    const controls = dialog.querySelectorAll<HTMLElement>('button, input')
    const last = controls[controls.length - 1]

    getByRole('textbox').focus()
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })

    expect(document.activeElement).toBe(last)
    expect(last).toBe(getByText(/administrar chats/i))
  })
})
