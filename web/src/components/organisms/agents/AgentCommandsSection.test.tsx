import '../../../test/setup'
import { afterEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { AgentCommandsResponse, EditableAgentConfig } from '../../../lib/types'
import { createApiClient } from '../../../services/http/client'
import { autoCleanup, makeState, tr } from '../../../test/agentsHarness'
import { AgentCommandsSection } from './AgentCommandsSection'

const CODER: EditableAgentConfig = { id: 'coder', name: 'Coder' }

const RESPONSE: AgentCommandsResponse = {
  agent_id: 'coder',
  workspace: '/home/u/.lele/workspace-coder',
  commands_dir: '/home/u/.lele/workspace-coder/commands',
  commands_dir_exists: false,
  shared_by: 1,
  harness: { allow_shell: false, allow_absolute_files: false },
  commands: [
    {
      name: 'deploy',
      description: 'Deploy the current branch',
      source: 'workspace',
      path: '/home/u/.lele/workspace-coder/commands/deploy.md',
      agent: '',
      model: '',
      allow_shell: true,
      allow_absolute_files: null,
      deletable: true,
      shadowed_by: '',
    },
  ],
  builtin: [{ name: 'new', description: 'New session', usage: '/new' }],
}

const originalFetch = globalThis.fetch

afterEach(() => {
  globalThis.fetch = originalFetch
})

autoCleanup()

function mockFetch(payload: unknown, status = 200) {
  globalThis.fetch = mock(async (input: RequestInfo | URL) => {
    if (String(input).includes('/api/v1/models')) {
      return new Response(JSON.stringify({ models: [], model_groups: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    }
    return new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as unknown as typeof fetch
}

function setup() {
  const api = createApiClient('http://127.0.0.1:18793')
  const state = makeState([CODER])
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={state} api={api}>
        <MemoryRouter initialEntries={['/agents/coder/commands']}>
          <AgentCommandsSection agent={CODER} agentId="coder" />
        </MemoryRouter>
      </SettingsProvider>
    </QueryClientProvider>,
  )
  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
  return { ...utils, byTestId }
}

describe('AgentCommandsSection (stub, PD)', () => {
  test('skeleton while the list loads, then one row per command with its source badge', async () => {
    mockFetch(RESPONSE)
    const u = setup()

    const skeleton = u.byTestId('commands-loading')
    expect(skeleton).toBeTruthy()
    expect(skeleton?.getAttribute('role')).toBe('status')
    expect(skeleton?.getAttribute('aria-busy')).toBe('true')

    await waitFor(() => expect(u.byTestId('command-row-deploy')).toBeTruthy())
    const row = u.byTestId('command-row-deploy')
    expect(row?.textContent).toContain('/deploy')
    expect(row?.textContent).toContain('Deploy the current branch')
    expect(row?.textContent).toContain('Workspace')
    expect(u.byTestId('commands-list')).toBeTruthy()
    expect(u.byTestId('commands-empty')).toBeNull()
  })

  test('empty list renders the "create the first one" state', async () => {
    mockFetch({ ...RESPONSE, commands: [] })
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-empty')).toBeTruthy())
    expect(u.byTestId('commands-empty')?.textContent).toContain(
      tr('settings.agentPage.commands.emptyTitle'),
    )
  })

  test('a rejected fetch shows the error banner and retry re-issues the request', async () => {
    mockFetch({ error: 'agent not found', code: 'agent_not_found' }, 404)
    const u = setup()

    await waitFor(() => expect(u.byTestId('commands-retry')).toBeTruthy(), { timeout: 4000 })
    expect(u.container.textContent).toContain(tr('settings.agentPage.commands.loadError'))
    expect(u.byTestId('commands-loading')).toBeNull()

    const callCount = () =>
      (globalThis.fetch as unknown as { mock: { calls: unknown[] } }).mock.calls.length
    const before = callCount()
    expect(before).toBeGreaterThanOrEqual(1)

    fireEvent.click(u.byTestId('commands-retry') as HTMLElement)
    await waitFor(() => expect(callCount()).toBeGreaterThan(before))
  })
})
