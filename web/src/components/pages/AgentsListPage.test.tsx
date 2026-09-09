import '../../test/setup'
import { afterEach, beforeEach, describe, expect, test } from 'bun:test'
import { cleanup, fireEvent, render, within } from '@testing-library/react'
import type { ComponentProps } from 'react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { SettingsProvider } from '../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../hooks/useSettingsConfig'
import type { EditableConfig } from '../../lib/types'
import type { ApiClient } from '../../services/http/client'
import i18n from '../../test/i18n'
import { AgentCard } from '../organisms/agents/AgentCard'
import { AgentsListPage } from './AgentsListPage'

/**
 * AgentsListPage + AgentCard (spec §3). Rendered through a SettingsProvider
 * fed with a hand-made `SettingsConfigState` (same pattern as
 * AgentEntityLayout.test.tsx) so the page sees a fixed draft and we can
 * assert exactly which `updateField` calls removals produce.
 *
 * House convention: `screen` is unusable here (its global-document binding
 * is captured before the JSDOM setup runs), so every query goes through the
 * bound helpers returned by `render` / `within`.
 */

/** The test env boots in Spanish; resolve labels through i18n (house pattern). */
function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options) as string
}

const LIST = [
  {
    id: 'coder',
    name: 'Coder',
    description: 'writes code',
    model: { primary: 'gpt-4o' },
    skills: ['github', 'weather'],
    tools: ['exec'],
  },
  { id: 'researcher', model: { primary: 'claude-3-5-sonnet' } },
  { id: 'main', default: true },
]

function makeState(overrides: Partial<SettingsConfigState> = {}): SettingsConfigState {
  const draft = {
    agents: { defaults: { model: 'gemini-2.5-pro' }, list: LIST },
  } as unknown as EditableConfig
  return {
    remoteConfig: draft,
    draftConfig: draft,
    metadata: null,
    dirtyPaths: new Set<string>(),
    validationErrors: [],
    saveState: 'idle',
    saveError: null,
    updateField: () => undefined,
    updateSecretField: () => undefined,
    replaceDraft: () => undefined,
    reset: () => undefined,
    validate: async () => true,
    save: async () => true,
    isDirty: false,
    isLoading: false,
    hasErrors: false,
    ...overrides,
  }
}

const api = { models: async () => ({ models: [], model_groups: [] }) } as unknown as ApiClient

function renderPage(state: SettingsConfigState) {
  return render(
    <SettingsProvider settingsState={state} api={api}>
      <MemoryRouter initialEntries={['/agents']}>
        <AgentsListPage />
      </MemoryRouter>
    </SettingsProvider>,
  )
}

/** Prints the current location so navigation targets can be asserted. */
function LocationProbe() {
  const location = useLocation()
  return <div data-testid="location-probe">{location.pathname}</div>
}

/** The ✕ lives inside a focus-restoration wrapper span; click the real button. */
function clickRemove(getAllByTestId: (id: string) => HTMLElement[], i: number) {
  const btn = getAllByTestId('agent-remove-btn')[i].querySelector('button') as HTMLButtonElement
  fireEvent.click(btn)
}

beforeEach(() => {
  cleanup()
})
afterEach(() => {
  cleanup()
})

describe('AgentsListPage — grid and toolbar', () => {
  test('renders one card per agent in list order', () => {
    const { getAllByTestId } = renderPage(makeState())
    const cards = getAllByTestId('agent-card')
    expect(cards).toHaveLength(3)
    expect(cards[0].textContent).toContain('coder')
    expect(cards[1].textContent).toContain('researcher')
    expect(cards[2].textContent).toContain('main')
  })

  test('count shows the total when there is no active filter', () => {
    const { getByTestId } = renderPage(makeState())
    expect(getByTestId('agents-count').textContent).toBe(
      tr('settings.agentPage.count', { count: 3 }),
    )
  })

  test('skeleton grid while loading; no cards', () => {
    const { getAllByTestId, queryByTestId } = renderPage(makeState({ isLoading: true }))
    expect(getAllByTestId('agent-card-skeleton').length).toBeGreaterThan(0)
    expect(queryByTestId('agent-card')).toBeNull()
  })

  test('empty state with CTA when there are no agents', () => {
    const empty = { agents: { defaults: {}, list: [] } } as unknown as EditableConfig
    const { getByTestId, queryByTestId } = renderPage(
      makeState({ draftConfig: empty, remoteConfig: empty }),
    )
    expect(getByTestId('agents-empty')).not.toBeNull()
    expect(queryByTestId('agent-card')).toBeNull()
  })
})

describe('AgentsListPage — search', () => {
  test('filters by id and description (case-insensitive)', () => {
    const { getByTestId, getAllByTestId } = renderPage(makeState())
    const search = getByTestId('agents-search') as HTMLInputElement

    fireEvent.change(search, { target: { value: 'CODER' } })
    expect(getAllByTestId('agent-card')).toHaveLength(1)

    fireEvent.change(search, { target: { value: 'writes code' } })
    const cards = getAllByTestId('agent-card')
    expect(cards).toHaveLength(1)
    expect(cards[0].textContent).toContain('coder')
  })

  test('count switches to shown-of-total while filtering', () => {
    const { getByTestId } = renderPage(makeState())
    fireEvent.change(getByTestId('agents-search'), { target: { value: 'researcher' } })
    expect(getByTestId('agents-count').textContent).toBe(
      tr('settings.agentPage.countFiltered', { shown: 1, total: 3 }),
    )
  })

  test('no-matches message keeps the query visible', () => {
    const { getByTestId, queryByTestId } = renderPage(makeState())
    fireEvent.change(getByTestId('agents-search'), { target: { value: 'zzz' } })
    expect(getByTestId('agents-no-matches').textContent).toContain('zzz')
    expect(queryByTestId('agent-card')).toBeNull()
  })

  test('clear button empties the search and restores the grid', () => {
    const { getByTestId, getAllByTestId, getByLabelText } = renderPage(makeState())
    fireEvent.change(getByTestId('agents-search'), { target: { value: 'main' } })
    expect(getAllByTestId('agent-card')).toHaveLength(1)
    fireEvent.click(getByLabelText(tr('settings.agentPage.clearSearch')))
    expect((getByTestId('agents-search') as HTMLInputElement).value).toBe('')
    expect(getAllByTestId('agent-card')).toHaveLength(3)
  })
})

describe('AgentCard — meta and badges', () => {
  test('meta line: model · skills · tools · subagents', () => {
    const { getAllByTestId } = renderPage(makeState())
    const coder = (getAllByTestId('agent-card-meta')[0].textContent ?? '') as string
    expect(coder).toContain('gpt-4o')
    expect(coder).toContain(tr('settings.agentPage.metaSkills', { count: 2 }))
    expect(coder).toContain(tr('settings.agentPage.metaTools', { count: 1 }))
    expect(coder).toContain(tr('settings.agentPage.metaSubagentsOff'))
  })

  test('unset skills/tools read "all"; model falls back to ≈defaults with inherit badge', () => {
    const { getAllByTestId } = renderPage(makeState())
    const mainMeta = getAllByTestId('agent-card-meta')[2].textContent ?? ''
    expect(mainMeta).toContain(tr('settings.agentPage.metaAllSkills'))
    expect(mainMeta).toContain(tr('settings.agentPage.metaAllTools'))
    expect(mainMeta).toContain('≈ gemini-2.5-pro')
    expect(getAllByTestId('agent-card')[2].textContent).toContain(
      tr('settings.agentPage.inheritsModel', { model: 'gemini-2.5-pro' }),
    )
  })

  test('default badge only on the default agent', () => {
    const { getAllByTestId } = renderPage(makeState())
    const cards = getAllByTestId('agent-card')
    const badge = tr('settings.defaultBadge')
    expect(cards[2].textContent).toContain(badge)
    expect(cards[0].textContent).not.toContain(badge)
  })

  test('modified badge uses the ORIGINAL index while search filters', () => {
    // Dirty path belongs to agents.list.1 (researcher).
    const state = makeState({ dirtyPaths: new Set(['agents.list.1.model.primary']) })
    const { getByTestId, getAllByTestId } = renderPage(state)
    fireEvent.change(getByTestId('agents-search'), { target: { value: 'researcher' } })
    expect(getAllByTestId('agent-card')[0].textContent).toContain(tr('settings.modifiedBadge'))
  })
})

describe('AgentCard — inline remove', () => {
  test('✕ arms the confirmation; Remove rewrites agents.list without the agent', () => {
    const calls: Array<[string, unknown]> = []
    const state = makeState({
      updateField: (path: string, value: unknown) => {
        calls.push([path, value])
      },
    })
    const { getAllByTestId, getByTestId } = renderPage(state)
    clickRemove(getAllByTestId, 0)
    expect(getByTestId('agent-confirm-remove')).not.toBeNull()

    fireEvent.click(getByTestId('agent-confirm-remove'))
    expect(calls).toHaveLength(1)
    const [path, value] = calls[0]
    expect(path).toBe('agents.list')
    expect((value as Array<{ id: string }>).length).toBe(2)
    expect((value as Array<{ id: string }>).some((a) => a.id === 'coder')).toBe(false)
  })

  test('Cancel closes the confirmation without touching the draft', () => {
    let touched = 0
    const state = makeState({ updateField: () => void touched++ })
    const { getAllByTestId, getByTestId, queryByTestId } = renderPage(state)
    clickRemove(getAllByTestId, 1)
    fireEvent.click(getByTestId('agent-cancel-remove'))
    expect(queryByTestId('agent-confirm-remove')).toBeNull()
    expect(touched).toBe(0)
  })

  test('confirmation names the agent (display name when present)', () => {
    const { getAllByTestId, getByTestId } = renderPage(makeState())
    clickRemove(getAllByTestId, 0)
    // coder has name "Coder" → confirm text uses it, not the id.
    expect(getByTestId('agent-confirm-remove').parentElement?.textContent).toContain(
      tr('settings.agentPage.removeConfirm', { name: 'Coder' }),
    )
    cleanup()
    const second = renderPage(makeState())
    clickRemove(second.getAllByTestId, 1)
    expect(getByTestId('agent-confirm-remove').parentElement?.textContent).toContain(
      tr('settings.agentPage.removeConfirm', { name: 'researcher' }),
    )
  })

  test('Escape closes the confirmation', () => {
    const { getAllByTestId, getByTestId, queryByTestId } = renderPage(makeState())
    clickRemove(getAllByTestId, 0)
    expect(getByTestId('agent-confirm-remove')).not.toBeNull()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(queryByTestId('agent-confirm-remove')).toBeNull()
  })

  test('click outside the card closes the confirmation', () => {
    const { getAllByTestId, getByTestId, queryByTestId } = renderPage(makeState())
    clickRemove(getAllByTestId, 0)
    expect(getByTestId('agent-confirm-remove')).not.toBeNull()
    fireEvent.pointerDown(getByTestId('agents-count'))
    expect(queryByTestId('agent-confirm-remove')).toBeNull()
  })
})

describe('AgentCard — actions & auto-cancel', () => {
  /** Standalone card (no page) to control props like the timeout. */
  function renderCard(overrides: Partial<ComponentProps<typeof AgentCard>> = {}) {
    return render(<MemoryRouter>{renderCardNode(overrides)}</MemoryRouter>)
  }

  function renderCardNode(overrides: Partial<ComponentProps<typeof AgentCard>> = {}) {
    return (
      <AgentCard
        agent={LIST[0]}
        isModified={false}
        defaultsModel="gemini-2.5-pro"
        onRemove={() => undefined}
        {...overrides}
      />
    )
  }

  function renderCardInRoutes() {
    return render(
      <MemoryRouter initialEntries={['/agents']}>
        <Routes>
          <Route path="/agents" element={renderCardNode()} />
          <Route path="/agents/:agentId" element={<LocationProbe />} />
          <Route path="/settings/agent/:agentId" element={<LocationProbe />} />
        </Routes>
      </MemoryRouter>,
    )
  }

  test('id link and Configure go to the agent config page', () => {
    const { getByText } = renderCardInRoutes()
    const link = getByText('coder').closest('a')
    expect(link?.getAttribute('href')).toBe('/agents/coder')
    fireEvent.click(getByText(tr('settings.agentPage.configure')))
    // /agents/coder matched the detail route (the list route unmounts).
    expect(getByText('/agents/coder')).not.toBeNull()
  })

  test('Files button goes to the agent files page', () => {
    const { getByLabelText, getByText } = renderCardInRoutes()
    fireEvent.click(getByLabelText(tr('settings.agentPage.filesAria')))
    expect(getByText('/settings/agent/coder')).not.toBeNull()
  })

  test('confirmation auto-cancels after its timeout window', async () => {
    const { getByTestId, getAllByTestId, queryByTestId } = renderCard({ confirmTimeoutMs: 40 })
    clickRemove(getAllByTestId, 0)
    expect(getByTestId('agent-confirm-remove')).not.toBeNull()
    await new Promise((resolve) => setTimeout(resolve, 120))
    expect(queryByTestId('agent-confirm-remove')).toBeNull()
  })

  test('actions live above the stretched link hit-area (z-order contract)', () => {
    const { getByText } = renderCard()
    const card = getByText('coder').closest('[data-testid="agent-card"]')
    expect(card).not.toBeNull()
    // The id anchor carries the stretched ::after hit area…
    const link = getByText('coder')
    expect(link.className).toContain('after:absolute')
    expect(link.className).toContain('after:inset-0')
    // …and the actions row sits on top of it.
    const actions = within(card as HTMLElement).getByText(
      tr('settings.agentPage.configure'),
    ).parentElement
    expect(actions?.className).toContain('z-10')
  })
})

describe('AgentsListPage — add agent wizard', () => {
  test('"Add agent" opens the wizard modal', async () => {
    const { getByText, findByText } = renderPage(makeState())
    fireEvent.click(getByText(tr('settings.addAgent')))
    expect(await findByText(tr('settings.addAgentModal.title'))).not.toBeNull()
  })
})
