import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import {
  SOURCE_COLORS,
  SOURCE_LABELS,
  sourceBadgeClasses,
  sourceBadgeLabel,
} from '../../../lib/skillSource'
import type { AgentCatalogResponse, EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { AgentSkillsSection } from './AgentSkillsSection'

/**
 * The shared agentsHarness pins a models-only fake api inside SettingsProvider,
 * but this section fetches its catalog through `api.getAgentCatalog`
 * (react-query), so the provider is assembled here with a catalog-capable fake.
 * makeState is reused untouched: same draft + recorded-writes contract.
 *
 * `screen` is unusable in this environment (see agentsHarness header): all
 * queries come from the render result.
 */

/** Workspace the fake catalog reports; the install dialog echoes it. */
const AGENT_WORKSPACE = '/home/u/.lele/workspace-coder'

// `deletable` mirrors what the backend computes: true ONLY for a skill living
// in this agent's own workspace dir (global/builtin are shared with others).
const CATALOG: AgentCatalogResponse = {
  agent_id: 'coder',
  workspace: AGENT_WORKSPACE,
  tools: [],
  skills: [
    {
      name: 'weather',
      description: 'Get weather and forecasts',
      source: 'global',
      enabled: true,
      deletable: false,
    },
    {
      name: 'chrome',
      description: 'Automate Chrome for debugging',
      source: 'workspace',
      enabled: true,
      deletable: true,
    },
    {
      name: 'memory',
      description: 'Organize memory files',
      source: 'builtin',
      enabled: false,
      deletable: false,
    },
  ],
}

const EMPTY_CATALOG: AgentCatalogResponse = {
  agent_id: 'coder',
  workspace: AGENT_WORKSPACE,
  tools: [],
  skills: [],
}

function makeApi(getCatalog: (id: string) => Promise<AgentCatalogResponse>): ApiClient {
  return {
    models: async () => ({ models: [], model_groups: [] }),
    getAgentCatalog: getCatalog,
  } as unknown as ApiClient
}

function setup(options: {
  agent: EditableAgentConfig
  index?: number
  agentId?: string
  catalog?: AgentCatalogResponse
  getCatalog?: (id: string) => Promise<AgentCatalogResponse>
  dirtyPaths?: string[]
}) {
  const writes: Write[] = []
  const agentId = options.agentId ?? 'coder'
  const api = makeApi(options.getCatalog ?? (async () => options.catalog ?? CATALOG))
  const state = makeState([options.agent], { writes, dirtyPaths: options.dirtyPaths })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={state} api={api}>
        <MemoryRouter initialEntries={['/agents/coder/skills']}>
          <AgentSkillsSection agent={options.agent} index={options.index ?? 0} agentId={agentId} />
        </MemoryRouter>
      </SettingsProvider>
    </QueryClientProvider>,
  )

  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)

  /** Click a test id, failing loudly (not silently on null) when missing. */
  const clickTestId = (id: string) => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    fireEvent.click(el)
  }

  return { ...utils, writes, byTestId, clickTestId }
}

/** Wait until the catalog resolves and the real grid is on screen. */
async function ready(utils: ReturnType<typeof setup>) {
  await waitFor(() => {
    expect(utils.byTestId('skills-grid')).toBeTruthy()
  })
  return utils
}

function cardFor(utils: ReturnType<typeof setup>, name: string): HTMLLabelElement {
  const cards = Array.from(utils.container.querySelectorAll('label')) as HTMLLabelElement[]
  const card = cards.find((element) => element.textContent?.includes(name))
  if (!card) throw new Error(`no ToggleCard for ${name}`)
  return card
}

function inputFor(utils: ReturnType<typeof setup>, name: string): HTMLInputElement {
  return cardFor(utils, name).querySelector('input[type="checkbox"]') as HTMLInputElement
}

describe('lib/skillSource (single source of truth)', () => {
  test('keeps the palette SkillsList shipped (byte-identical)', () => {
    expect(SOURCE_COLORS.workspace).toBe('bg-state-info-light text-state-info border-state-info/30')
    expect(SOURCE_COLORS.global).toBe(
      'bg-state-success-light text-state-success border-state-success/30',
    )
    expect(SOURCE_COLORS.builtin).toBe('bg-surface-muted text-text-tertiary border-border/50')
    expect(SOURCE_LABELS).toEqual({ workspace: 'Workspace', global: 'Global', builtin: 'Built-in' })
  })

  test('unknown sources fall back to the neutral style and show verbatim', () => {
    expect(sourceBadgeClasses('weird' as never)).toBe(SOURCE_COLORS.builtin)
    expect(sourceBadgeClasses(undefined)).toBe(SOURCE_COLORS.builtin)
    expect(sourceBadgeLabel('weird' as never)).toBe('weird')
    expect(sourceBadgeLabel('global')).toBe('Global')
  })
})

describe('AgentSkillsSection — banner', () => {
  test('undefined skills → info "all" banner with §4.5.1 classes', async () => {
    const utils = await ready(setup({ agent: { id: 'coder' } }))
    const banner = utils.byTestId('skills-banner') as HTMLElement
    expect(banner.textContent).toContain(tr('settings.agentPage.skillsAllBanner'))
    expect(banner.className).toContain('border-state-info/30')
    expect(banner.className).toContain('bg-state-info-light')
  })

  test('empty array also shows the info banner ([] ≡ undefined)', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: [] } }))
    const banner = utils.byTestId('skills-banner') as HTMLElement
    expect(banner.textContent).toContain(tr('settings.agentPage.skillsAllBanner'))
  })

  test('N≥1 → allowlist banner with the count, neutral classes', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: ['weather', 'chrome'] } }))
    const banner = utils.byTestId('skills-banner') as HTMLElement
    expect(banner.textContent).toContain(tr('settings.agentPage.skillsCustomBanner', { count: 2 }))
    expect(banner.className).toContain('border-border')
    expect(banner.className).toContain('bg-background-tertiary')
    expect(banner.className).not.toContain('bg-state-info-light')
  })
})

describe('AgentSkillsSection — grid + writing', () => {
  test('one ToggleCard per catalog skill, alphabetical, with source badge', async () => {
    const utils = await ready(setup({ agent: { id: 'coder' } }))
    const cards = Array.from(utils.container.querySelectorAll('[data-testid="skills-grid"] label'))
    expect(cards).toHaveLength(3)
    expect(cards[0].textContent).toContain('chrome')
    expect(cards[0].textContent).toContain('Workspace')
    expect(cards[1].textContent).toContain('memory')
    expect(cards[1].textContent).toContain('Built-in')
    expect(cards[2].textContent).toContain('weather')
    expect(cards[2].textContent).toContain('Global')
    // md card (§7.3: p-3.5)
    expect(cards[0].className).toContain('p-3.5')
  })

  test('allowlisted skills render checked (on classes)', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: ['weather'] } }))
    expect(inputFor(utils, 'weather').checked).toBe(true)
    expect(cardFor(utils, 'weather').className).toContain('bg-accent-subtle')
    expect(inputFor(utils, 'chrome').checked).toBe(false)
  })

  test('toggling on writes the full sorted array at agents.list.{index}.skills', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: ['chrome'] }, index: 2 }))
    fireEvent.click(inputFor(utils, 'weather'))
    const write = utils.writes.find(([path]) => path === 'agents.list.2.skills')
    expect(write).toBeDefined()
    expect(write?.[1]).toEqual(['chrome', 'weather'])
  })

  test('toggling off writes the remaining array', async () => {
    const utils = await ready(
      setup({ agent: { id: 'coder', skills: ['chrome', 'weather'] }, index: 1 }),
    )
    fireEvent.click(inputFor(utils, 'weather'))
    const write = utils.writes.find(([path]) => path === 'agents.list.1.skills')
    expect(write?.[1]).toEqual(['chrome'])
  })

  test('installed-but-globally-disabled: dimmed + warning, still selectable', async () => {
    const utils = await ready(setup({ agent: { id: 'coder' } }))
    const card = cardFor(utils, 'memory')
    expect(card.textContent).toContain(tr('settings.agentPage.skillDisabledGlobally'))
    // opacity-60 wrapper (the card itself stays interactive, only dimmed)
    expect((card.parentElement as HTMLElement).className).toContain('opacity-60')
    const input = card.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(input.disabled).toBe(false)
    fireEvent.click(input)
    const write = utils.writes.find(([path]) => path === 'agents.list.0.skills')
    expect(write?.[1]).toEqual(['memory'])
  })
})

describe('AgentSkillsSection — orphans (§4.5.4)', () => {
  test('allowlist names missing from the catalog render above the grid, never hidden', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: ['weather', 'ghost-skill'] } }))
    const block = utils.byTestId('skills-orphans') as HTMLElement
    expect(block).toBeTruthy()
    expect(block.textContent).toContain('ghost-skill')
    expect(block.textContent).toContain(tr('settings.agentPage.skillNotInstalled'))
    expect(block.className).toContain('mb-3')
    expect(block.className).toContain('space-y-2')
    const orphanCard = block.firstElementChild as HTMLElement
    expect(orphanCard.className).toContain('border-state-warning/40')
    expect(orphanCard.className).toContain('bg-state-warning-light/30')
    // The grid still shows the installed ones, and the orphan block sits ABOVE it.
    expect(utils.container.querySelectorAll('[data-testid="skills-grid"] label')).toHaveLength(3)
    const grid = utils.byTestId('skills-grid') as HTMLElement
    const siblings = Array.from(block.parentElement?.children ?? [])
    expect(siblings.indexOf(block)).toBeLessThan(siblings.indexOf(grid))
  })

  test('✕ removes only that name and writes the full remaining array', async () => {
    const utils = await ready(
      setup({ agent: { id: 'coder', skills: ['ghost-skill', 'weather', 'zeta-old'] } }),
    )
    const removeBtn = utils
      .byTestId('skills-orphan-ghost-skill')
      ?.querySelector('button') as HTMLButtonElement
    expect(removeBtn).toBeTruthy()
    fireEvent.click(removeBtn)
    const write = utils.writes.find(([path]) => path === 'agents.list.0.skills')
    expect(write?.[1]).toEqual(['weather', 'zeta-old'])
  })

  test('orphans alone keep the panel usable (no false "nothing installed" empty state)', async () => {
    const utils = setup({
      agent: { id: 'coder', skills: ['ghost-skill'] },
      catalog: EMPTY_CATALOG,
    })
    await waitFor(() => {
      expect(utils.byTestId('skills-orphans')).toBeTruthy()
    })
    expect(utils.container.querySelector('a[href="/skills"]')).toBeNull()
  })
})

describe('AgentSkillsSection — toolbar', () => {
  test('search filters by name and by description (case-insensitive)', async () => {
    const utils = await ready(setup({ agent: { id: 'coder' } }))
    const search = utils.byTestId('skills-search') as HTMLInputElement
    fireEvent.change(search, { target: { value: 'CHROME' } })
    let cards = utils.container.querySelectorAll('[data-testid="skills-grid"] label')
    expect(cards).toHaveLength(1)
    expect(cards[0].textContent).toContain('chrome')
    // "forecasts" only exists in weather's description
    fireEvent.change(search, { target: { value: 'forecasts' } })
    cards = utils.container.querySelectorAll('[data-testid="skills-grid"] label')
    expect(cards).toHaveLength(1)
    expect(cards[0].textContent).toContain('weather')
  })

  test('search without results shows the no-matches hint', async () => {
    const utils = await ready(setup({ agent: { id: 'coder' } }))
    const search = utils.byTestId('skills-search') as HTMLInputElement
    fireEvent.change(search, { target: { value: 'zzz' } })
    expect(utils.byTestId('skills-no-matches')).toBeTruthy()
    expect(utils.byTestId('skills-grid')).toBeNull()
  })

  test('counter reads selected-of-total over the FULL catalog while filtering', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: ['weather', 'chrome'] } }))
    const counter = utils.byTestId('skills-count') as HTMLElement
    expect(counter.textContent).toContain(
      tr('settings.agentPage.skillsCount', { selected: 2, total: 3 }),
    )
    const search = utils.byTestId('skills-search') as HTMLInputElement
    fireEvent.change(search, { target: { value: 'chrome' } })
    expect(counter.textContent).toContain(
      tr('settings.agentPage.skillsCount', { selected: 2, total: 3 }),
    )
  })

  test('batch Ninguna writes [] and batch Todas writes the complete installed list', async () => {
    const utils = await ready(setup({ agent: { id: 'coder', skills: ['weather'] } }))
    utils.clickTestId('skills-batch-none')
    expect(utils.writes.at(-1)).toEqual(['agents.list.0.skills', []])
    // "Todas" ignores the active search filter: the full catalog, sorted.
    const search = utils.byTestId('skills-search') as HTMLInputElement
    fireEvent.change(search, { target: { value: 'wea' } })
    utils.clickTestId('skills-batch-all')
    expect(utils.writes.at(-1)?.[0]).toBe('agents.list.0.skills')
    expect(utils.writes.at(-1)?.[1]).toEqual(['chrome', 'memory', 'weather'])
  })
})

describe('AgentSkillsSection — loading / empty / error', () => {
  test('loading: 4 ToggleCard skeletons marked aria-busy (no spinner)', async () => {
    // A never-resolving promise keeps the query in isLoading.
    let release: (value: AgentCatalogResponse) => void = () => undefined
    const utils = setup({
      agent: { id: 'coder' },
      getCatalog: () =>
        new Promise<AgentCatalogResponse>((resolve) => {
          release = resolve
        }),
    })
    // Same a11y pattern as the agents list: aria-busy + sr-only text (the
    // house does not use live-region roles on skeleton blocks).
    const status = utils.container.querySelector('[aria-busy="true"]') as HTMLElement
    expect(status).toBeTruthy()
    expect(status.textContent).toContain(tr('settings.loading'))
    // 4 skeleton cards × 4 pulsing blocks each (title bar, 2 description
    // lines mirroring the line-clamp-2 text, 18px check) — §5.1 keeps the
    // final geometry so swapping in real ToggleCards causes no reflow.
    expect(status.querySelectorAll('.animate-pulse')).toHaveLength(16)
    expect(utils.container.querySelectorAll('input[type="checkbox"]')).toHaveLength(0)
    release(CATALOG) // avoid a dangling promise
    await waitFor(() => {
      expect(utils.byTestId('skills-grid')).toBeTruthy()
    })
  })

  test('empty catalog → dashed block + link to /skills', async () => {
    const utils = setup({ agent: { id: 'coder' }, catalog: EMPTY_CATALOG })
    await waitFor(() => {
      expect(utils.container.querySelector('a[href="/skills"]')).toBeTruthy()
    })
    const link = utils.container.querySelector('a[href="/skills"]') as HTMLAnchorElement
    expect(link.textContent).toContain(tr('settings.agentPage.goToSkills'))
    expect(utils.container.textContent).toContain(tr('settings.agentPage.skillsNoneInstalled'))
  })

  test('catalog fetch error → retryable banner; retry refetches', async () => {
    let failing = true
    let attempts = 0
    const utils = setup({
      agent: { id: 'coder' },
      getCatalog: async () => {
        attempts++
        if (failing) throw new Error('boom')
        return CATALOG
      },
    })
    // The query retries once internally (backoff), so allow for that cycle.
    await waitFor(() => expect(utils.byTestId('skills-retry')).toBeTruthy(), { timeout: 4000 })
    failing = false
    const attemptsBeforeRetry = attempts
    utils.clickTestId('skills-retry')
    await waitFor(() => expect(utils.byTestId('skills-grid')).toBeTruthy())
    expect(attempts).toBeGreaterThan(attemptsBeforeRetry)
  })
})

describe('AgentSkillsSection — dirty hint (§5.3)', () => {
  test('a dirty skills path shows the in-panel "unsaved changes" hint', async () => {
    const utils = await ready(
      setup({
        agent: { id: 'coder', skills: ['weather'] },
        index: 3,
        dirtyPaths: ['agents.list.3.skills'],
      }),
    )
    expect(utils.byTestId('skills-dirty-hint')?.textContent).toContain(
      tr('settings.agentPage.sectionHasChanges'),
    )
  })

  test('a dirty path of another section shows no hint', async () => {
    const utils = await ready(
      setup({
        agent: { id: 'coder', skills: ['weather'] },
        index: 3,
        dirtyPaths: ['agents.list.3.model.primary'],
      }),
    )
    expect(utils.byTestId('skills-dirty-hint')).toBeNull()
  })
})
