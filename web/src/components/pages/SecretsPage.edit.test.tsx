/**
 * SecretsPage — editing a stored secret.
 *
 * Before this, `/secrets` could create, reveal and delete a secret but not edit
 * one: fixing a typo in a description meant deleting and re-typing the secret.
 * Editing is now first class:
 *
 *  1. every card has an *Edit* button that opens the form prefilled with the
 *     secret's metadata (its value is never fetched to fill the form);
 *  2. the name is the keyring key, so it is shown but not editable;
 *  3. an empty value means "keep the stored secret", so the PUT carries the
 *     metadata only and the plaintext never round-trips through the browser;
 *  4. a non-empty value replaces it;
 *  5. the page sends the edit as `PUT /api/v1/secrets/{name}` — never the
 *     create endpoint, and never with a changed name.
 *
 * The test env boots i18n in Spanish, so labels are resolved through the same
 * keys the component uses (the house pattern).
 */

import '../../test/setup'
import { afterEach, describe, expect, mock, test } from 'bun:test'
import { act, cleanup, fireEvent, render } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import type { Agent, SecretInput, SecretMeta } from '../../lib/types'
import i18n from '../../test/i18n'
import { SecretFormModal, SecretsPage } from './SecretsPage'

afterEach(cleanup)

function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options) as string
}

const AGENTS: Agent[] = [
  { id: 'coder', name: 'Coder agent', workspace: '', model: 'gpt' },
  { id: 'lele', name: 'Lele', workspace: '', model: 'gpt' },
]

const SECRET: SecretMeta = {
  name: 'openai.api_key',
  description: 'OpenAI key',
  tags: ['provider'],
  scope: ['coder'],
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-02T00:00:00Z',
  created_by: 'tui',
}

/** Find a field by DOM id; throws instead of non-null asserting. */
function field(id: string): HTMLInputElement {
  const el = document.getElementById(id)
  if (!el) throw new Error(`#${id} not found`)
  return el as HTMLInputElement
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement {
  const btn = Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent?.trim() === label,
  )
  if (!btn) throw new Error(`button "${label}" not found`)
  return btn as HTMLButtonElement
}

/** Flush the mocked promises a component chain awaits (no timers). */
async function flush(times = 4): Promise<void> {
  for (let i = 0; i < times; i++) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }
}

// ---------------------------------------------------------------------------
// Modal contract
// ---------------------------------------------------------------------------

function renderModal(initial: SecretMeta | null = null) {
  const onSubmit = mock(async (_input: SecretInput) => {})
  const onClose = mock(() => {})
  const view = render(
    <SecretFormModal
      busy={false}
      agents={AGENTS}
      initial={initial}
      onClose={onClose}
      onSubmit={onSubmit}
    />,
  )
  const chips = Array.from(view.container.querySelectorAll<HTMLButtonElement>('[role="checkbox"]'))
  return { ...view, chips, onSubmit, onClose }
}

function clickLabel(container: HTMLElement, label: string): void {
  fireEvent.click(findButton(container, label))
}

describe('SecretFormModal — edit mode', () => {
  test('opens prefilled and marks the name as fixed', () => {
    const { container } = renderModal(SECRET)

    expect(container.textContent).toContain(tr('secrets.editSecret'))
    expect(field('secret-name').value).toBe('openai.api_key')
    expect(field('secret-name').disabled).toBe(true)
    expect(field('secret-desc').value).toBe('OpenAI key')
    expect(field('secret-tags').value).toBe('provider')
    // The scope comes from the chips, preselected from the stored scope.
    expect(
      Array.from(container.querySelectorAll('[role="checkbox"]')).map((c) =>
        c.getAttribute('aria-checked'),
      ),
    ).toEqual(['true', 'false'])
    // The value is deliberately not fetched: it starts empty and blank = keep.
    expect(field('secret-value').value).toBe('')
    expect(field('secret-value').placeholder).toBe(tr('secrets.valueKeepPlaceholder'))
    // Edit submits with Save, not Create.
    expect(findButton(container, tr('common.save'))).not.toBeNull()
  })

  test('submits an empty value when the plaintext is left alone', async () => {
    const view = renderModal(SECRET)
    fireEvent.change(field('secret-desc'), { target: { value: 'OpenAI key (prod)' } })
    await act(async () => clickLabel(view.container, tr('common.save')))

    expect(view.onSubmit).toHaveBeenCalledTimes(1)
    const input = view.onSubmit.mock.calls[0][0]
    expect(input.name).toBe('openai.api_key')
    expect(input.value).toBe('')
    expect(input.description).toBe('OpenAI key (prod)')
    expect(input.tags).toEqual(['provider'])
    expect(input.scope).toEqual(['coder'])
    expect(view.onClose).toHaveBeenCalledTimes(1)
  })

  test('whitespace-only input is treated as "keep the current value"', async () => {
    const view = renderModal(SECRET)
    fireEvent.change(field('secret-value'), { target: { value: '   ' } })
    await act(async () => clickLabel(view.container, tr('common.save')))

    expect(view.onSubmit.mock.calls[0][0].value).toBe('')
  })

  test('a new value is submitted verbatim when the editor types one', async () => {
    const view = renderModal(SECRET)
    fireEvent.change(field('secret-value'), { target: { value: ' sk-rotated ' } })
    await act(async () => clickLabel(view.container, tr('common.save')))

    expect(view.onSubmit.mock.calls[0][0].value).toBe(' sk-rotated ')
  })

  test('the scope can be edited from the chips', async () => {
    const view = renderModal(SECRET)
    fireEvent.click(view.chips[0]) // coder off
    fireEvent.click(view.chips[1]) // lele on
    await act(async () => clickLabel(view.container, tr('common.save')))

    expect(view.onSubmit.mock.calls[0][0].scope).toEqual(['lele'])
  })

  test('creating still requires a value', async () => {
    const view = renderModal(null)
    expect(view.container.textContent).toContain(tr('secrets.newSecret'))
    fireEvent.change(field('secret-name'), { target: { value: 'gh.new_token' } })
    await act(async () => clickLabel(view.container, tr('common.create')))

    expect(view.onSubmit).not.toHaveBeenCalled()
    expect(view.container.textContent).toContain(tr('secrets.errorValue'))
  })
})

// ---------------------------------------------------------------------------
// Page wiring: Edit button → PUT
// ---------------------------------------------------------------------------

type UpdateCall = { name: string; input: Omit<SecretInput, 'name'> }

function makeApi() {
  const calls = {
    update: [] as UpdateCall[],
    create: [] as SecretInput[],
    get: [] as string[],
  }
  const api = {
    secrets: {
      list: async () => ({
        secrets: [SECRET],
        status: { enabled: true, backend: 'file', count: 1 },
      }),
      audit: async () => ({ audit: [] }),
      get: async (name: string) => {
        calls.get.push(name)
        return { secret: SECRET, value: 'sk-stored' }
      },
      create: async (input: SecretInput) => {
        calls.create.push(input)
        return { secret: SECRET }
      },
      update: async (name: string, input: Omit<SecretInput, 'name'>) => {
        calls.update.push({ name, input })
        return { secret: SECRET }
      },
      remove: async () => ({ deleted: SECRET.name }),
    },
  }
  return { api: api as unknown as AuthContextValue['api'], calls }
}

function logicValue(): AppLogicContextValue {
  return {
    agents: AGENTS,
    sessions: [],
    currentSessionKey: null,
    parentSessionKey: null,
    processingSessions: new Set<string>(),
    sidebarOpen: false,
    mobileSidebarOpen: false,
    onCloseMobileSidebar: () => undefined,
    onOpenMobileSidebar: () => undefined,
    onToggleSidebar: () => undefined,
    onSelectSession: () => undefined,
    onDeleteSession: () => undefined,
    onCreateSession: () => undefined,
    onSelectMode: () => undefined,
    onLogout: async () => undefined,
  } as unknown as AppLogicContextValue
}

async function renderPage() {
  const { api, calls } = makeApi()
  const view = render(
    createElement(
      AuthContext.Provider,
      { value: { api, session: { device_name: 'Test' } } as unknown as AuthContextValue },
      createElement(
        AppLogicContext.Provider,
        { value: logicValue() },
        createElement(MemoryRouter, { initialEntries: ['/secrets'] }, createElement(SecretsPage)),
      ),
    ),
  )
  // Wait for the mounted hook to land the secret card.
  await flush()
  return { ...view, calls }
}

describe('SecretsPage — editing a card', () => {
  test('the Edit button opens the form prefilled from the card', async () => {
    const view = await renderPage()
    expect(view.container.textContent).toContain('openai.api_key')

    await act(async () => clickLabel(view.container, tr('common.edit')))
    await flush()

    expect(view.container.textContent).toContain(tr('secrets.editSecret'))
    expect(field('secret-name').value).toBe('openai.api_key')
    expect(field('secret-name').disabled).toBe(true)
    expect(field('secret-desc').value).toBe('OpenAI key')
  })

  test('saving sends PUT to the secret path (never create, never a renamed key)', async () => {
    const view = await renderPage()
    await act(async () => clickLabel(view.container, tr('common.edit')))
    await flush()

    fireEvent.change(field('secret-desc'), { target: { value: 'OpenAI key (prod)' } })
    await act(async () => clickLabel(view.container, tr('common.save')))
    await flush()

    expect(view.calls.create).toHaveLength(0)
    expect(view.calls.update).toHaveLength(1)
    expect(view.calls.update[0].name).toBe('openai.api_key')
    expect(view.calls.update[0].input).toEqual({
      value: '',
      description: 'OpenAI key (prod)',
      tags: ['provider'],
      scope: ['coder'],
    })
  })

  test('an edit never fetches the stored plaintext to fill the form', async () => {
    const view = await renderPage()
    await act(async () => clickLabel(view.container, tr('common.edit')))
    await flush()

    // Revealing is what calls GET /secrets/{name}; opening the editor must not.
    expect(view.calls.get).toHaveLength(0)
    expect(field('secret-value').value).toBe('')
  })
})
