import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../../hooks/useSettingsConfig'
import type { AgentCatalogResponse, EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { type Write, autoCleanup, makeState, tr } from '../../../test/agentsHarness'
import { AgentToolsSection } from './AgentToolsSection'

/**
 * `tab=tools` (spec §4.6) — the section with the most behaviour behind it:
 * mode derivation, batch writes, search filtering and the conditional spawn
 * warning. Rendered through the shared SettingsProvider harness (fixed draft,
 * recorded `updateField` writes) plus a fresh QueryClient per test, so each
 * catalog fetch is isolated and never cached across mounts.
 *
 * House convention: `screen` is unusable here (its global-document binding is
 * captured before the JSDOM setup runs), so every query goes through the
 * helpers returned by `render`. Every expected string is resolved through
 * `tr()` — the same i18n instance the component reads — so the assertions do
 * not depend on which locale the detector picked.
 */

/** Catalog as the agent's live registry would return it (sorted by name). */
const CATALOG: AgentCatalogResponse = {
  agent_id: 'coder',
  tools: [
    { name: 'exec', description: 'Run a shell command' },
    { name: 'i2c', description: 'Talk to I2C peripherals' },
    { name: 'read_file', description: 'Read a file from disk' },
    { name: 'spawn', description: 'Spawn a subagent' },
    { name: 'web_search', description: 'Search the web' },
  ],
  skills: [],
}

const ALL_NAMES = CATALOG.tools.map((tool) => tool.name)

function makeApi(catalog: Partial<AgentCatalogResponse> | 'fail' = CATALOG): ApiClient {
  const models = async () => ({ models: [], model_groups: [] })
  if (catalog === 'fail') {
    return {
      models,
      getAgentCatalog: async () => {
        throw new Error('catalog unavailable')
      },
    } as unknown as ApiClient
  }
  return {
    models,
    getAgentCatalog: async (): Promise<AgentCatalogResponse> => ({
      agent_id: 'coder',
      tools: [],
      skills: [],
      ...catalog,
    }),
  } as unknown as ApiClient
}

type SetupOptions = {
  /** Value of `agents.list[index].tools`; undefined = "all" mode. */
  tools?: string[]
  subagents?: EditableAgentConfig['subagents']
  catalog?: Partial<AgentCatalogResponse> | 'fail'
  /** Catalog promise that never settles: exercises the loading state. */
  pending?: boolean
  dirtyPaths?: string[]
}

function setup(options: SetupOptions = {}) {
  const agent: EditableAgentConfig = {
    id: 'coder',
    name: 'Coder',
    tools: options.tools,
    subagents: options.subagents,
  }
  const agents = [{ id: 'main', default: true }, agent]
  const writes: Write[] = []
  const state: SettingsConfigState = makeState(agents, {
    writes,
    dirtyPaths: options.dirtyPaths,
  })

  const api = options.pending
    ? ({
        models: async () => ({ models: [], model_groups: [] }),
        getAgentCatalog: () => new Promise<AgentCatalogResponse>(() => undefined),
      } as unknown as ApiClient)
    : makeApi(options.catalog)

  // A client per test: the catalog fetch is never shared with another mount.
  // `retry:false` keeps the failure-path test fast. (Do NOT set gcTime:0 — it
  // evicts the resolved data while the observer is still mounted and the
  // query loops in pending forever.)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })

  const utils = render(
    <SettingsProvider settingsState={state} api={api}>
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={['/agents/coder/tools']}>
          <AgentToolsSection agent={agent} index={1} agentId="coder" />
        </MemoryRouter>
      </QueryClientProvider>
    </SettingsProvider>,
  )

  const lastWrite = (path: string) => [...writes].reverse().find((write) => write[0] === path)

  /** The checkbox of the ToggleCard whose label mentions `name`. */
  const checkboxFor = (name: string): HTMLInputElement => {
    const boxes = utils.container.querySelectorAll('input[type="checkbox"]')
    for (const box of Array.from(boxes)) {
      if (box.closest('label')?.textContent?.includes(name)) return box as HTMLInputElement
    }
    throw new Error(`no tool card for ${name}`)
  }

  /** CategoryGroup header button for a translated group title. */
  const groupHeader = (title: string): HTMLButtonElement => {
    const header = utils
      .queryAllByRole('button')
      .find((button) => button.textContent?.includes(title))
    if (!header) throw new Error(`no group header for ${title}`)
    return header as HTMLButtonElement
  }

  /** The collapsible panel a group header controls. */
  const panelFor = (title: string): HTMLElement => {
    const id = groupHeader(title).getAttribute('aria-controls') as string
    return utils.container.querySelector(`[id="${id}"]`) as HTMLElement
  }

  const categoryTitle = (key: string) => tr(`settings.agentPage.category.${key}`)

  return { ...utils, writes, lastWrite, checkboxFor, groupHeader, panelFor, categoryTitle }
}

autoCleanup()

/**
 * Wait until the catalog resolved and the skeleton is gone.
 * Plain polling instead of testing-library's waitFor: under bun + jsdom the
 * library's MutationObserver-based wait never observes the React 19 render
 * flush, so it hangs until its own timeout even though the DOM is ready.
 */
async function ready(u: ReturnType<typeof setup>) {
  for (let i = 0; i < 80 && u.queryByTestId('tools-skeleton'); i++) {
    await new Promise((resolve) => setTimeout(resolve, 25))
  }
  expect(u.queryByTestId('tools-skeleton')).toBeNull()
  return u
}

describe('AgentToolsSection — mode derived from the value (§4.6.1)', () => {
  test('absent tools value = all mode: banner shown, toolbar hidden', async () => {
    const u = await ready(setup())
    const all = u.getByRole('radio', { name: tr('settings.agentPage.toolsModeAll') })
    const custom = u.getByRole('radio', { name: tr('settings.agentPage.toolsModeCustom') })
    expect(all.getAttribute('aria-checked')).toBe('true')
    expect(custom.getAttribute('aria-checked')).toBe('false')
    // The banner interpolates the live catalog size.
    expect(u.getByTestId('tools-all-banner').textContent).toContain(
      tr('settings.agentPage.toolsAllBanner', { total: ALL_NAMES.length }),
    )
    expect(u.queryByTestId('tools-toolbar')).toBeNull()
  })

  test('array tools value = custom mode: toolbar shown, all-mode banner gone', async () => {
    const u = await ready(setup({ tools: ['exec', 'read_file'] }))
    const custom = u.getByRole('radio', { name: tr('settings.agentPage.toolsModeCustom') })
    expect(custom.getAttribute('aria-checked')).toBe('true')
    expect(u.queryByTestId('tools-all-banner')).toBeNull()
    expect(u.getByTestId('tools-toolbar')).toBeTruthy()
    expect(u.getByTestId('tools-count').textContent).toContain(
      tr('settings.agentPage.toolsCount', { active: 2, total: 5 }),
    )
  })

  // "Zero tools" is not expressible on the backend (an empty allowlist keeps
  // every tool and `omitempty` would drop the key), so a legacy `tools: []`
  // must render as unrestricted rather than lie about access.
  test('an empty array renders as ALL mode (backend semantics: empty = no restriction)', async () => {
    const u = await ready(setup({ tools: [] }))
    const all = u.getByRole('radio', { name: tr('settings.agentPage.toolsModeAll') })
    const custom = u.getByRole('radio', { name: tr('settings.agentPage.toolsModeCustom') })
    expect(all.getAttribute('aria-checked')).toBe('true')
    expect(custom.getAttribute('aria-checked')).toBe('false')
    expect(u.getByTestId('tools-all-banner')).toBeTruthy()
    expect(u.queryByTestId('tools-toolbar')).toBeNull()
  })

  test('segmented control to custom writes the whole catalog; back to all writes undefined', async () => {
    const u = await ready(setup())
    fireEvent.click(u.getByRole('radio', { name: tr('settings.agentPage.toolsModeCustom') }))
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual([...ALL_NAMES].sort())

    // The harness draft is a fixed snapshot: `updateField` records the write
    // but does not feed it back, so mode is re-derived from a fresh mount that
    // already carries the array (what the real provider does after a write).
    cleanup()
    const custom = await ready(setup({ tools: [...ALL_NAMES] }))
    fireEvent.click(custom.getByRole('radio', { name: tr('settings.agentPage.toolsModeAll') }))
    const toAll = custom.lastWrite('agents.list.1.tools')
    expect(toAll).toBeTruthy()
    expect(toAll?.[1]).toBeUndefined()
  })

  test('unchecking a card from all mode enters custom with catalog minus that tool', async () => {
    const u = await ready(setup())
    // All mode previews every card as checked.
    expect(u.checkboxFor('i2c').checked).toBe(true)
    fireEvent.click(u.checkboxFor('i2c'))
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual(
      ALL_NAMES.filter((name) => name !== 'i2c').sort(),
    )
  })
})

describe('AgentToolsSection — toggling and batch (§4.6.2, §4.6.3)', () => {
  test('checking a missing tool adds it and keeps the array name-sorted', async () => {
    const u = await ready(setup({ tools: ['exec', 'read_file'] }))
    expect(u.checkboxFor('web_search').checked).toBe(false)
    fireEvent.click(u.checkboxFor('web_search'))
    expect(u.lastWrite('agents.list.1.tools')).toEqual([
      'agents.list.1.tools',
      ['exec', 'read_file', 'web_search'],
    ])
  })

  test('deselecting the LAST selected tool returns the agent to all mode', async () => {
    const u = await ready(setup({ tools: ['exec'] }))
    fireEvent.click(u.checkboxFor('exec'))
    const write = u.lastWrite('agents.list.1.tools')
    expect(write).toBeTruthy()
    expect(write?.[1]).toBeUndefined()
  })

  test('unchecking a selected tool removes it, sorted again', async () => {
    const u = await ready(setup({ tools: [...ALL_NAMES] }))
    fireEvent.click(u.checkboxFor('spawn'))
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual(
      ALL_NAMES.filter((name) => name !== 'spawn').sort(),
    )
  })

  test('every write is sorted and deduplicated (stable diffs, §4.6.7)', async () => {
    const u = await ready(setup({ tools: ['web_search', 'exec', 'exec'] }))
    fireEvent.click(u.checkboxFor('read_file'))
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual(['exec', 'read_file', 'web_search'])
  })

  test('batch Essentials writes only the essentials the live catalog actually has', async () => {
    const u = await ready(setup({ tools: ['exec'] }))
    fireEvent.click(u.getByTestId('tools-batch-essential'))
    // The catalog has exec/read_file/web_search from ESSENTIAL_TOOLS but not
    // write_file, edit_file, list_dir, web_fetch, send_file, sleep, spawn —
    // naming tools the agent does not have would be dead config.
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual(['exec', 'read_file', 'web_search'])
  })

  // Clearing the selection writes `undefined`, never `[]`: the backend maps an
  // empty allowlist to "all tools", so persisting `[]` would store a value the
  // UI could not honestly re-render.
  test('batch None writes undefined (back to unrestricted) and batch All writes the full catalog', async () => {
    const u = await ready(setup({ tools: ['exec'] }))
    fireEvent.click(u.getByTestId('tools-batch-none'))
    const none = u.lastWrite('agents.list.1.tools')
    expect(none).toBeTruthy()
    expect(none?.[1]).toBeUndefined()
    fireEvent.click(u.getByTestId('tools-batch-all'))
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual([...ALL_NAMES].sort())
  })
})

describe('AgentToolsSection — categories and search (§4.6.4, §4.6.5)', () => {
  test('cards are grouped per category with active/total counters in the header', async () => {
    const u = await ready(setup({ tools: ['exec', 'read_file'] }))
    const files = u.categoryTitle('files')
    const hardware = u.categoryTitle('hardware')
    // Files: read_file is the only catalog tool of the group → 1 of 1.
    expect(u.groupHeader(files).textContent).toContain(files)
    expect(u.panelFor(files).textContent).toContain('read_file')
    expect(u.groupHeader(hardware).textContent).toMatch(/0.*1/)
    // ToggleCard sm renders the catalog description.
    expect(u.panelFor(hardware).textContent).toContain('Talk to I2C peripherals')
  })

  test('allowlisted tools outside the catalog fall into Other and stay removable', async () => {
    const u = await ready(setup({ tools: ['exec', 'legacy_thing', 'quantum_tool'] }))
    const other = u.categoryTitle('other')
    const panel = u.panelFor(other)
    expect(panel.textContent).toContain('quantum_tool')
    expect(panel.textContent).toContain('legacy_thing')
    expect(u.checkboxFor('quantum_tool').checked).toBe(true)
    fireEvent.click(u.checkboxFor('quantum_tool'))
    expect(u.lastWrite('agents.list.1.tools')?.[1]).toEqual(['exec', 'legacy_thing'])
  })

  test('search hides groups without hits and force-expands those with them', async () => {
    const u = await ready(setup({ tools: [...ALL_NAMES] }))
    // Collapse Files first: a search must override the user's collapse.
    const files = u.categoryTitle('files')
    fireEvent.click(u.groupHeader(files))
    expect(u.groupHeader(files).getAttribute('aria-expanded')).toBe('false')

    fireEvent.change(u.getByTestId('tools-search'), { target: { value: 'read' } })
    expect(u.queryByText(files)).toBeTruthy()
    expect(u.groupHeader(files).getAttribute('aria-expanded')).toBe('true')
    expect(u.panelFor(files).textContent).toContain('read_file')
    // Hardware (i2c) has no match at all: the whole group disappears.
    expect(u.queryByText(u.categoryTitle('hardware'))).toBeNull()
    expect(u.queryByTestId('tools-no-matches')).toBeNull()
  })

  test('search also matches descriptions; zero hits shows the no-match hint', async () => {
    const u = await ready(setup({ tools: [...ALL_NAMES] }))
    fireEvent.change(u.getByTestId('tools-search'), { target: { value: 'peripherals' } })
    expect(u.panelFor(u.categoryTitle('hardware')).textContent).toContain('i2c')

    fireEvent.change(u.getByTestId('tools-search'), { target: { value: 'zzz-nothing' } })
    expect(u.getByTestId('tools-no-matches').textContent).toContain('zzz-nothing')
  })

  test('search input and batch toolbar exist only in custom mode', async () => {
    const u = await ready(setup())
    expect(u.queryByTestId('tools-search')).toBeNull()
    fireEvent.click(u.getByRole('radio', { name: tr('settings.agentPage.toolsModeCustom') }))
    // The write does not re-render the fixture draft, so re-mount in custom.
    cleanup()
    const custom = await ready(setup({ tools: [...ALL_NAMES] }))
    expect(custom.getByTestId('tools-search')).toBeTruthy()
    expect(custom.getByTestId('tools-batch-none')).toBeTruthy()
  })
})

describe('AgentToolsSection — spawn warning (§4.6.6)', () => {
  const subagents = { allow_agents: ['researcher'] }

  test('shows when subagents are enabled + custom mode + spawn is off the list', async () => {
    const u = await ready(setup({ tools: ['exec'], subagents }))
    const warning = u.getByTestId('tools-spawn-warning')
    expect(warning.textContent).toContain('researcher')
    expect(u.getByTestId('tools-spawn-warning-link').getAttribute('href')).toBe(
      '/agents/coder/subagents',
    )
  })

  test('hidden in all mode (spawn is implicitly available)', async () => {
    const u = await ready(setup({ subagents }))
    expect(u.queryByTestId('tools-spawn-warning')).toBeNull()
  })

  test('hidden when spawn is in the allowlist', async () => {
    const u = await ready(setup({ tools: ['exec', 'spawn'], subagents }))
    expect(u.queryByTestId('tools-spawn-warning')).toBeNull()
  })

  test('hidden when subagents are not enabled', async () => {
    const u = await ready(setup({ tools: ['exec'] }))
    expect(u.queryByTestId('tools-spawn-warning')).toBeNull()
  })

  test('hidden when allow_agents is present but empty', async () => {
    const u = await ready(setup({ tools: ['exec'], subagents: { allow_agents: [] } }))
    expect(u.queryByTestId('tools-spawn-warning')).toBeNull()
  })
})

describe('AgentToolsSection — loading, empty and error states (§4.6.7)', () => {
  test('pending catalog renders two skeleton category groups and no banner', () => {
    const u = setup({ pending: true })
    const skeleton = u.getByTestId('tools-skeleton')
    // 2 groups × (1 header + 3 cards).
    expect(skeleton.querySelectorAll('[class*="animate-pulse"]').length).toBe(8)
    expect(u.queryByTestId('tools-all-banner')).toBeNull()
    expect(u.queryByTestId('tools-empty')).toBeNull()
  })

  test('empty catalog renders the dashed empty block', async () => {
    const u = await ready(setup({ catalog: { tools: [] } }))
    const empty = u.getByTestId('tools-empty')
    expect(empty.className).toContain('border-dashed')
    expect(empty.textContent).toContain(tr('settings.agentPage.toolsEmpty'))
    expect(u.queryByTestId('tools-toolbar')).toBeNull()
  })

  test('failed catalog fetch shows the error with a retry affordance', async () => {
    const u = await ready(setup({ catalog: 'fail' }))
    const error = u.getByTestId('tools-load-error')
    expect(error.textContent).toContain(tr('settings.agentPage.toolsLoadError'))
    expect(error.textContent).toContain(tr('settings.agentPage.retry'))
    expect(u.queryByTestId('tools-empty')).toBeNull()
  })

  test('dirty tools path surfaces the unsaved-changes marker', async () => {
    const clean = await ready(setup())
    expect(clean.queryByTestId('tools-dirty')).toBeNull()
    cleanup()
    const dirty = await ready(setup({ dirtyPaths: ['agents.list.1.tools'] }))
    expect(dirty.getByTestId('tools-dirty').textContent).toContain(
      tr('settings.agentPage.sectionHasChanges'),
    )
  })
})
