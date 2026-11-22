import { afterEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render } from '@testing-library/react'
import '../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { JobFormModal } from './CronPage'

afterEach(cleanup)

const authValue = { api: null } as unknown as AuthContextValue

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

    const message = field('cron-message')
    message.focus()
    expect(document.activeElement).toBe(message)

    rerender(<CronFormHarness onClose={() => {}} />)

    expect(document.activeElement).toBe(message)
    expect(document.activeElement).not.toBe(field('cron-name'))
  })

  test('Escape calls the latest onClose closure after a re-render', () => {
    const calls: string[] = []
    const view = render(<CronFormHarness onClose={() => calls.push('A')} />)
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
