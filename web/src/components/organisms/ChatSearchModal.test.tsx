import '../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { act, cleanup, fireEvent, render, within } from '@testing-library/react'
import { useState } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import type { ChatSession } from '../../lib/types'
import i18n from '../../test/i18n'
import { ChatSearchModal } from './ChatSearchModal'

const sessions = [
  { key: 'native:one', name: 'First chat', updated: '2026-01-02T10:00:00Z', mode: 'agent' },
  { key: 'native:two', name: 'Second chat', updated: '2026-01-01T10:00:00Z', mode: 'agent' },
] as unknown as ChatSession[]

type Handles = { selected: string[]; closed: number }

const triggerLabel = 'open search'

function renderHarness(handles: Handles = { selected: [], closed: 0 }, initiallyOpen = true) {
  const logicValue = {
    sessions,
    currentSessionKey: null,
    processingSessions: new Set<string>(),
    onSelectSession: (key: string) => handles.selected.push(key),
    onDeleteSession: async () => undefined,
  } as unknown as AppLogicContextValue

  function Harness() {
    const [open, setOpen] = useState(initiallyOpen)
    return (
      <AppLogicContext.Provider value={logicValue}>
        <MemoryRouter>
          <button type="button" onClick={() => setOpen(true)}>
            {triggerLabel}
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
    const { getByRole } = renderHarness()

    expect(document.activeElement).toBe(getByRole('textbox'))
    expect(document.body.style.overflow).toBe('hidden')
  })

  test('gives focus back to the control that opened it', () => {
    const view = renderHarness({ selected: [], closed: 0 }, false)
    const trigger = view.getByRole('button', { name: triggerLabel }) as HTMLButtonElement

    trigger.focus()
    fireEvent.click(trigger)
    expect(document.activeElement).toBe(view.getByRole('textbox'))

    fireEvent.keyDown(document, { key: 'Escape' })

    expect(document.activeElement).toBe(trigger)
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
    const { getByRole, handles } = renderHarness()

    fireEvent.click(getByRole('button', { name: i18n.t('chat.closePanel') }))

    expect(handles.closed).toBe(1)
  })

  test('selecting a chat opens it and closes the dialog', () => {
    const { getByRole, handles } = renderHarness()

    fireEvent.click(getByRole('button', { name: /Second chat/ }))

    expect(handles.selected).toEqual(['native:two'])
    expect(handles.closed).toBe(1)
  })
})

describe('ChatSearchModal — keyboard containment', () => {
  test('Tab from the last control wraps to the first', () => {
    const { getByRole } = renderHarness()

    const dialog = getByRole('dialog')
    const first = getByRole('textbox')
    const last = within(dialog).getByRole('button', { name: i18n.t('chat.manageChats') })

    last.focus()
    fireEvent.keyDown(document, { key: 'Tab' })

    expect(document.activeElement).toBe(first)
  })

  test('Shift+Tab from the first control wraps to the last', () => {
    const { getByRole } = renderHarness()

    const dialog = getByRole('dialog')
    const last = within(dialog).getByRole('button', { name: i18n.t('chat.manageChats') })

    getByRole('textbox').focus()
    fireEvent.keyDown(document, { key: 'Tab', shiftKey: true })

    expect(document.activeElement).toBe(last)
  })
})
