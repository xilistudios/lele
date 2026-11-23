import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { fireEvent, render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import {
  autoCleanup,
  makeState,
  renderWithSettings,
  settle,
  tr,
  withModels,
} from '../../../test/agentsHarness'
import { SubagentsSettings } from './SubagentsSettings'

const DEFAULTS = {
  subagent_timeout_minutes: 30,
  subagent_max_concurrent: 0,
  subagent_max_retries: 2,
  subagent_max_iterations: 0,
  subagent_retention_minutes: 5,
}

const CODER: EditableAgentConfig = { id: 'coder', name: 'Coder' }
const RESEARCHER: EditableAgentConfig = { id: 'researcher', name: 'Researcher', default: true }
const AGENTS = [CODER, RESEARCHER]

const pageApi = {
  models: async () => ({ models: [], model_groups: [] }),
  skills: async () => ({ skills: [] }),
  tools: async () => ({ tools: [] }),
  agentFiles: async () => ({ files: [] }),
} as unknown as ApiClient

function pageTree(agents: EditableAgentConfig[], options: Parameters<typeof makeState>[1] = {}) {
  const state = makeState(agents, options)
  return (
    <SettingsProvider settingsState={state} api={pageApi}>
      <MemoryRouter initialEntries={['/settings/subagents']}>
        <SubagentsSettings />
      </MemoryRouter>
    </SettingsProvider>
  )
}

function setup(agents: EditableAgentConfig[] = AGENTS, defaults = DEFAULTS) {
  return renderWithSettings(<SubagentsSettings />, agents, { defaults })
}

const selectOf = (container: HTMLElement) =>
  container.querySelector('#subagents-agent-select') as HTMLSelectElement | null

autoCleanup()

describe('SubagentsSettings — global defaults', () => {
  test('renders the five global defaults with the values from the config', () => {
    const u = setup()
    expect(u.getByLabelText(tr('settings.fields.subagentTimeout'))).toHaveProperty('value', '30')
    expect(u.getByLabelText(tr('settings.fields.subagentMaxConcurrent'))).toHaveProperty(
      'value',
      '0',
    )
    expect(u.getByLabelText(tr('settings.fields.subagentMaxRetries'))).toHaveProperty('value', '2')
    expect(u.getByLabelText(tr('settings.fields.subagentMaxIterations'))).toHaveProperty(
      'value',
      '0',
    )
    expect(u.getByLabelText(tr('settings.fields.subagentRetention'))).toHaveProperty('value', '5')
  })

  test('the 0-semantics help texts render as translated text, not raw keys', () => {
    const u = setup()
    for (const key of [
      'settings.descriptions.subagentDefaults',
      'settings.descriptions.subagentTimeout',
      'settings.descriptions.subagentMaxConcurrent',
      'settings.descriptions.subagentMaxRetries',
      'settings.descriptions.subagentMaxIterations',
      'settings.descriptions.subagentRetention',
    ]) {
      expect(u.container.textContent).toContain(tr(key))
      expect(u.container.textContent).not.toContain(key)
    }
  })

  test('editing a default writes its dotted path', () => {
    const u = setup()
    fireEvent.change(u.getByLabelText(tr('settings.fields.subagentTimeout')), {
      target: { value: '45' },
    })
    expect(u.lastWrite('agents.defaults.subagent_timeout_minutes')).toEqual([
      'agents.defaults.subagent_timeout_minutes',
      45,
    ])
  })

  test('zero is written as 0, not undefined', () => {
    const u = setup()
    fireEvent.change(u.getByLabelText(tr('settings.fields.subagentMaxRetries')), {
      target: { value: '0' },
    })
    expect(u.lastWrite('agents.defaults.subagent_max_retries')).toEqual([
      'agents.defaults.subagent_max_retries',
      0,
    ])
  })

  test('dirty badge and validation error render for a default field', () => {
    const u = renderWithSettings(<SubagentsSettings />, AGENTS, {
      defaults: DEFAULTS,
      dirtyPaths: ['agents.defaults.subagent_retention_minutes'],
      validationErrors: [
        { path: 'agents.defaults.subagent_retention_minutes', message: 'boom', code: 'invalid' },
      ],
    })
    expect(u.container.textContent).toContain(tr('settings.modified'))
    expect(u.container.textContent).toContain('boom')
  })
})

describe('SubagentsSettings — agent selector', () => {
  test('selector defaults to the agent marked default', () => {
    const u = setup([CODER, { ...RESEARCHER, subagents: { allow_agents: [] } }])
    expect(selectOf(u.container)?.value).toBe('researcher')
    expect(u.getByRole('group', { name: tr('settings.agentPage.subagentsAllowed') })).not.toBeNull()
  })

  test('the default selection writes to its own agent index', () => {
    const u = setup([{ id: '   ' }, CODER, RESEARCHER])
    expect(selectOf(u.container)?.value).toBe('researcher')
    fireEvent.click(u.getByLabelText(tr('settings.agentPage.subagentsEnable')))
    expect(u.lastWrite('agents.list.2.subagents')).toEqual([
      'agents.list.2.subagents',
      { allow_agents: [] },
    ])
    expect(u.lastWrite('agents.list.1.subagents')).toBeUndefined()
    expect(u.lastWrite('agents.list.0.subagents')).toBeUndefined()
  })

  test('switching the selector edits the chosen agent index', () => {
    const u = setup()
    fireEvent.change(u.getByLabelText(tr('settings.subagentsPage.agentLabel')), {
      target: { value: 'coder' },
    })
    expect(selectOf(u.container)?.value).toBe('coder')
    fireEvent.click(u.getByLabelText(tr('settings.agentPage.subagentsEnable')))
    expect(u.lastWrite('agents.list.0.subagents')).toEqual([
      'agents.list.0.subagents',
      { allow_agents: [] },
    ])
  })

  test('switching agents closes the open subagent model picker', async () => {
    withModels(['gpt-4o'])
    const u = setup([
      { ...CODER, subagents: { allow_agents: [] } },
      { ...RESEARCHER, subagents: { allow_agents: [] } },
    ])
    await settle()

    const searchInput = () =>
      u.queryByLabelText(`${tr('settings.agentPage.subagentsModel')} search`)
    fireEvent.click(u.getByLabelText(tr('settings.agentPage.subagentsModel')))
    await settle(250)
    expect(searchInput()).not.toBeNull()
    fireEvent.change(searchInput() as HTMLElement, { target: { value: 'gpt' } })

    fireEvent.change(u.getByLabelText(tr('settings.subagentsPage.agentLabel')), {
      target: { value: 'coder' },
    })
    expect(selectOf(u.container)?.value).toBe('coder')
    expect(searchInput()).toBeNull()
  })

  test('a blank default entry does not win the selection', () => {
    const u = setup([{ id: '   ', default: true }, CODER])
    expect(selectOf(u.container)?.value).toBe('coder')
  })

  test('a padded id is trimmed and still writes its own index', () => {
    const u = setup([{ id: ' coder ', name: 'Coder', default: true }])
    expect(selectOf(u.container)?.value).toBe('coder')
    fireEvent.click(u.getByLabelText(tr('settings.agentPage.subagentsEnable')))
    expect(u.lastWrite('agents.list.0.subagents')).toEqual([
      'agents.list.0.subagents',
      { allow_agents: [] },
    ])
  })

  test('a deleted selection falls back to the default agent', () => {
    const view = render(pageTree(AGENTS, { defaults: DEFAULTS }))
    fireEvent.change(view.getByLabelText(tr('settings.subagentsPage.agentLabel')), {
      target: { value: 'coder' },
    })
    expect(selectOf(view.container)?.value).toBe('coder')

    view.rerender(pageTree([RESEARCHER], { defaults: DEFAULTS }))
    expect(selectOf(view.container)?.value).toBe('researcher')
  })

  test('empty agent list keeps defaults editable and shows the no-agents hint', () => {
    const u = setup([])
    const hint = u.getByTestId('subagents-no-agents')
    expect(hint.textContent).toContain(tr('settings.subagentsPage.noAgents'))
    expect(u.getByLabelText(tr('settings.fields.subagentTimeout'))).not.toBeNull()
    expect(selectOf(u.container)).toBeNull()
    const link = u.getByRole('link', { name: tr('settings.subagentsPage.noAgentsAction') })
    expect(link.getAttribute('href')).toBe('/settings/agents')
    expect(link.textContent).not.toBe(tr('settings.tabs.agents'))
  })

  test('open-agent link encodes the agent id', () => {
    const u = setup([{ id: 'a b', default: true }])
    expect(u.getByTestId('subagents-open-agent').getAttribute('href')).toBe(
      '/settings/agents/a%20b/subagents',
    )
  })
})
