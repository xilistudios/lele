import { afterEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, fireEvent, render } from '@testing-library/react'
import type { Agent, SecretInput } from '../../lib/types'
import i18n from '../../test/i18n'
import { SecretFormModal } from './SecretsPage'

afterEach(cleanup)

const AGENTS: Agent[] = [
  { id: 'coder', name: 'Coder agent', workspace: '', model: 'gpt' },
  { id: 'lele', name: 'Lele', workspace: '', model: 'gpt' },
]

function field(id: string): HTMLInputElement {
  const el = document.getElementById(id)
  if (!el) throw new Error(`#${id} not found`)
  return el as HTMLInputElement
}

function setup(agents: Agent[] = AGENTS) {
  const onSubmit = mock(async (_input: SecretInput) => {})
  const onClose = mock(() => {})
  const view = render(
    <SecretFormModal busy={false} agents={agents} onClose={onClose} onSubmit={onSubmit} />,
  )
  const chips = Array.from(view.container.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'))
  return { ...view, chips, onSubmit, onClose }
}

function typeRequiredFields() {
  fireEvent.change(field('secret-name'), { target: { value: 'openai.api_key' } })
  fireEvent.change(field('secret-value'), { target: { value: 'sk-secret' } })
}

function clickCreate(container: HTMLElement) {
  const create = Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent?.trim() === i18n.t('common.create'),
  )
  if (!create) throw new Error('create button not found')
  fireEvent.click(create)
}

describe('SecretFormModal — scope is an agent selector, not free text', () => {
  test('renders one selectable chip per configured agent and no free-text input', () => {
    const { chips, container } = setup()
    expect(chips.map((chip) => chip.getAttribute('aria-label'))).toEqual([
      'coder (Coder agent)',
      'lele (Lele)',
    ])
    expect(chips.map((chip) => chip.getAttribute('aria-checked'))).toEqual(['false', 'false'])
    expect(container.querySelector('#secret-scope input')).toBeNull()
  })

  test('selected chips become the submitted scope (order follows the clicks)', async () => {
    const view = setup()
    typeRequiredFields()
    fireEvent.click(view.chips[1])
    fireEvent.click(view.chips[0])
    await act(async () => clickCreate(view.container))
    expect(view.onSubmit).toHaveBeenCalledTimes(1)
    expect(view.onSubmit.mock.calls[0][0].scope).toEqual(['lele', 'coder'])
  })

  test('clicking a selected chip removes it from the scope', async () => {
    const view = setup()
    typeRequiredFields()
    fireEvent.click(view.chips[0])
    expect(view.chips[0].getAttribute('aria-checked')).toBe('true')
    fireEvent.click(view.chips[0])
    expect(view.chips[0].getAttribute('aria-checked')).toBe('false')
    await act(async () => clickCreate(view.container))
    expect(view.onSubmit.mock.calls[0][0].scope).toEqual([])
  })

  test('submitting with nothing selected sends an empty scope (= all agents)', async () => {
    const view = setup()
    typeRequiredFields()
    await act(async () => clickCreate(view.container))
    expect(view.onSubmit.mock.calls[0][0].scope).toEqual([])
  })

  test('an empty roster shows the "no agents" empty state instead of chips', () => {
    const { chips, container } = setup([])
    expect(chips).toHaveLength(0)
    const block = container.querySelector('[data-testid="secret-scope-empty"]')
    expect(block?.textContent).toBe(i18n.t('secrets.noAgents'))
  })

  test('name/value are still required before submitting', async () => {
    const view = setup()
    await act(async () => clickCreate(view.container))
    expect(view.onSubmit).not.toHaveBeenCalled()
    expect(view.container.textContent).toContain(i18n.t('secrets.errorName'))
  })
})
