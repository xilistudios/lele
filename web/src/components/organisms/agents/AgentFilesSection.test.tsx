import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { AgentFileInfo, EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { type Write, autoCleanup, makeState, tr } from '../../../test/agentsHarness'
import { AgentFilesSection, agentFilesQueryKey } from './AgentFilesSection'

/**
 * `tab=files` is read-only (spec §4.8): it lists the workspace files as chips
 * that deep-link into `AgentFilesPage`, plus one CTA to the editor. These tests
 * are the contract that keeps it from turning into a second editor: no writes,
 * correct links, human-readable sizes, and the three fetch states.
 */

const AGENT: EditableAgentConfig = {
  id: 'coder',
  name: 'Coder',
  workspace: '~/.lele/workspace-coder',
}

const FILES: AgentFileInfo[] = [
  { name: 'AGENTS.md', size: 4200, editable: true },
  { name: 'MEMORY.md', size: 12 * 1024, editable: true },
  { name: 'soul.bin', size: 900, editable: false },
]

function makeApi(files: AgentFileInfo[], shouldFail = false): ApiClient {
  return {
    models: async () => ({ models: [], model_groups: [] }),
    agentFiles: async () => {
      if (shouldFail) throw new Error('files endpoint down')
      return { files }
    },
  } as unknown as ApiClient
}

function setup(
  options: { agent?: EditableAgentConfig; files?: AgentFileInfo[]; shouldFail?: boolean } = {},
) {
  const writes: Write[] = []
  const agent = options.agent ?? AGENT
  const api = makeApi(options.files ?? FILES, options.shouldFail)
  const state = makeState([agent], { writes })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={state} api={api}>
        <MemoryRouter initialEntries={['/agents/coder/files']}>
          <AgentFilesSection agent={agent} agentId={agent.id} />
        </MemoryRouter>
      </SettingsProvider>
    </QueryClientProvider>,
  )

  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
  /** Element with a test id, failing loudly instead of silently on null. */
  const need = (id: string) => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    return el
  }
  return { ...utils, writes, byTestId, need }
}

autoCleanup()

describe('AgentFilesSection', () => {
  test('query key is namespaced per agent (invalidation contract)', () => {
    expect(agentFilesQueryKey('coder')).toEqual(['agentFiles', 'coder'])
  })

  test('renders one chip per file with the size formatted, linked to the editor', async () => {
    const u = setup()
    await waitFor(() => {
      expect(u.byTestId('files-list')).toBeTruthy()
    })

    expect(u.need('files-chip-AGENTS.md').textContent).toContain('AGENTS.md')
    expect(u.need('files-chip-AGENTS.md').textContent).toContain('4.1 KB')
    expect(u.need('files-chip-AGENTS.md').getAttribute('href')).toBe(
      '/settings/agent/coder/AGENTS.md',
    )

    expect(u.need('files-chip-MEMORY.md').textContent).toContain('12.0 KB')
    // Non-editable files are listed too — the editor decides what to show them.
    expect(u.need('files-chip-soul.bin').textContent).toContain('900 B')
    expect(u.need('files-chip-soul.bin').getAttribute('href')).toBe(
      '/settings/agent/coder/soul.bin',
    )
  })

  test('shows the workspace path and a CTA to the editor root', async () => {
    const u = setup()
    await waitFor(() => {
      expect(u.byTestId('files-workspace')).toBeTruthy()
    })
    expect(u.need('files-workspace').textContent).toBe('~/.lele/workspace-coder')
    expect(u.need('files-open').closest('a')?.getAttribute('href')).toBe('/settings/agent/coder')
  })

  test('an agent without a workspace says it inherits instead of rendering a blank line', async () => {
    const u = setup({ agent: { id: 'coder' } })
    await waitFor(() => {
      expect(u.byTestId('files-workspace')).toBeTruthy()
    })
    expect(u.need('files-workspace').textContent).toBe(tr('settings.agentPage.workspaceInherited'))
  })

  test('empty list → empty state with the hint, no chips', async () => {
    const u = setup({ files: [] })
    await waitFor(() => {
      expect(u.byTestId('files-empty')).toBeTruthy()
    })
    expect(u.need('files-empty').textContent).toContain(tr('settings.agentPage.filesEmpty'))
    expect(u.need('files-empty').textContent).toContain(tr('settings.agentPage.filesEmptyHint'))
    expect(u.byTestId('files-list')).toBeNull()
  })

  test('loading renders a status skeleton, not an empty state', () => {
    const u = setup()
    // Synchronously after mount the query is still pending.
    expect(u.need('files-loading').getAttribute('role')).toBe('status')
    expect(u.need('files-loading').getAttribute('aria-busy')).toBe('true')
    expect(u.byTestId('files-empty')).toBeNull()
    expect(u.byTestId('files-list')).toBeNull()
  })

  test('fetch failure → error banner whose retry refetches', async () => {
    const u = setup({ shouldFail: true })
    // The section asks react-query for one retry (backoff ≈ 1s), so the banner
    // only lands after that attempt fails — same budget as the skills test.
    await waitFor(
      () => {
        expect(u.byTestId('files-retry')).toBeTruthy()
      },
      { timeout: 4000 },
    )
    expect(u.container.textContent).toContain(tr('settings.agentPage.filesLoadError'))
    expect(u.byTestId('files-list')).toBeNull()

    // The retry button must exist and be clickable; the query is what it drives.
    fireEvent.click(u.need('files-retry'))
    await waitFor(() => {
      expect(u.container.textContent).toContain(tr('settings.agentPage.filesLoadError'))
    })
  })

  test('never writes config (§5.3: the files tab has no dirty dot)', async () => {
    const u = setup()
    await waitFor(() => {
      expect(u.byTestId('files-list')).toBeTruthy()
    })
    fireEvent.click(u.need('files-chip-AGENTS.md'))
    fireEvent.click(u.need('files-open'))
    expect(u.writes).toEqual([])
  })
})
