/**
 * CronPage.JobFormModal.test.tsx
 *
 * Regression test for the focus-steal bug in the cron job form — the same bug
 * class as the Modal atom fix (see ../atoms/Modal.test.tsx).
 *
 * Root cause: the dialog's keydown/focus effect depended on `onClose`. The
 * call site renders `<JobFormModal onClose={() => setFormOpen(false)} />`, an
 * inline closure with a fresh identity on every CronPage re-render (react-query
 * refetch of the jobs list, expandedId/busyId/formBusy state changes, ...).
 * Each parent re-render re-ran the effect, and its initial `.focus()` moved
 * focus from the field being typed in back to the first focusable element.
 *
 * These tests pin the contract:
 *  1. Initial focus lands on the first field when the modal opens (mount).
 *  2. A parent re-render with a NEW onClose identity does NOT steal focus.
 *  3. Escape calls the LATEST onClose closure after such a re-render.
 *  4. Unmounting removes the Escape listener.
 *
 * Deps are mocked minimally: a real AuthContext.Provider supplies
 * `{ api: null }`, which makes useAvailableModels a no-op (it early-returns
 * for a null api — no fetch, no state updates). This avoids mock.module,
 * which is process-wide in Bun and cannot be reverted from within a file.
 */

import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import '../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { JobFormModal } from './CronPage'

afterEach(cleanup)

const authValue = { api: null } as unknown as AuthContextValue

/**
 * Mirrors the CronPage call site: JobFormModal is mounted while the form is
 * open and receives an inline onClose closure that gets a fresh identity on
 * every parent render — the exact trigger of the bug.
 */
function CronFormHarness({ onClose }: { onClose: () => void }) {
  return (
    <AuthContext.Provider value={authValue}>
      <JobFormModal
        initial={null}
        agents={[]}
        busy={false}
        onClose={() => onClose()}
        onSubmit={async () => {}}
      />
    </AuthContext.Provider>
  )
}

/** Find a form field by its DOM id; throws instead of non-null asserting. */
function field(id: string): HTMLElement {
  const el = document.getElementById(id)
  if (!el) throw new Error(`#${id} not found`)
  return el
}

describe('JobFormModal focus regression — parent re-renders must not steal focus', () => {
  test('initial focus lands on the first field when the modal opens', () => {
    render(<CronFormHarness onClose={() => {}} />)
    expect(document.activeElement).toBe(field('cron-name'))
  })

  test('re-render with a new onClose identity does not move focus away from the typed field', () => {
    const { rerender } = render(<CronFormHarness onClose={() => {}} />)

    // Focus a field that is NOT the first focusable element.
    const message = field('cron-message')
    message.focus()
    expect(document.activeElement).toBe(message)

    // New parent render → new inline onClose identity (refetch/state change).
    // Before the fix this re-ran the effect and moved focus to #cron-name.
    rerender(<CronFormHarness onClose={() => {}} />)

    expect(document.activeElement).toBe(message)
    expect(document.activeElement).not.toBe(field('cron-name'))
  })

  test('Escape calls the latest onClose closure after a re-render', () => {
    const calls: string[] = []
    // First render passes closure A...
    const view = render(<CronFormHarness onClose={() => calls.push('A')} />)
    // ...second render passes a fresh closure B (new parent state / refetch).
    view.rerender(<CronFormHarness onClose={() => calls.push('B')} />)

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(calls).toEqual(['B'])
  })

  test('unmount removes the Escape listener', () => {
    const calls: string[] = []
    const { unmount } = render(<CronFormHarness onClose={() => calls.push('A')} />)
    unmount()

    fireEvent.keyDown(document, { key: 'Escape' })
    expect(calls).toEqual([])
  })
})
