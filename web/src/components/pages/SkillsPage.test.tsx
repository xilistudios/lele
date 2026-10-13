/**
 * SkillsPage — scope tabs + destination-aware install (plan T5, §5.8).
 *
 * The page is the integrator: it owns the URL-derived view (`?view=` /
 * `?agent=`), the two data sources that overlap (`GET /api/v1/skills` and the
 * per-agent catalog) and the rule that every mutation refreshes the cache the
 * acting hook did NOT already refresh.
 *
 * The assertions are about the WIRE, not the DOM: a URL router records every
 * `{method, path, body}` and 404s anything unexpected, so a stray request (or
 * a missing one) fails loudly. The env boots i18n in Spanish, so labels are
 * resolved through the same keys the component uses (`tr()`).
 */

import '../../test/setup'
import { afterEach, describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { createElement } from 'react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import '../../test/i18n'
import { AppLogicContext, type AppLogicContextValue } from '../../contexts/AppLogicContext'
import { AuthContext, type AuthContextValue } from '../../contexts/AuthContext'
import { createApiClient } from '../../lib/api'
import type { Agent, AgentCatalogResponse, SkillInfo } from '../../lib/types'
import i18n from '../../test/i18n'
import { SkillsPage } from './SkillsPage'

const originalFetch = globalThis.fetch

afterEach(() => {
  cleanup()
  globalThis.fetch = originalFetch
})

const tr = (key: string, options?: Record<string, unknown>) => i18n.t(key, options) as string

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const AGENTS: Agent[] = [
  {
    id: 'coder',
    name: 'Coder',
    workspace: '/home/u/.lele/workspace-coder',
    model: 'gpt',
    default: true,
  },
  { id: 'planner', name: 'Planner', workspace: '/home/u/.lele/workspace-planner', model: 'gpt' },
]

/** Four skills, one per source (plus a second global), in a fixed order. */
const SKILLS: SkillInfo[] = [
  {
    id: 'w1',
    name: 'workspace-skill',
    description: 'ws',
    installed: true,
    enabled: true,
    source: 'workspace',
  },
  {
    id: 'g1',
    name: 'global-a',
    description: 'ga',
    installed: true,
    enabled: true,
    source: 'global',
  },
  {
    id: 'b1',
    name: 'builtin-a',
    description: 'ba',
    installed: true,
    enabled: false,
    source: 'builtin',
  },
  {
    id: 'g2',
    name: 'global-b',
    description: 'gb',
    installed: true,
    enabled: false,
    source: 'global',
  },
]

/** One `deletable: true` (chrome) and two `deletable: false` entries. */
const CATALOGS: Record<string, AgentCatalogResponse> = {
  coder: {
    agent_id: 'coder',
    workspace: '/home/u/.lele/workspace-coder',
    tools: [],
    skills: [
      {
        name: 'chrome',
        description: 'browser',
        source: 'workspace',
        enabled: true,
        deletable: true,
      },
      {
        name: 'weather',
        description: 'forecast',
        source: 'global',
        enabled: false,
        deletable: false,
      },
      { name: 'memory', description: 'notes', source: 'builtin', enabled: true, deletable: false },
    ],
  },
  planner: {
    agent_id: 'planner',
    workspace: '/home/u/.lele/workspace-planner',
    tools: [],
    skills: [
      { name: 'roadmap', description: 'plan', source: 'workspace', enabled: true, deletable: true },
      {
        name: 'weather',
        description: 'forecast',
        source: 'global',
        enabled: true,
        deletable: false,
      },
    ],
  },
}

const AVAILABLE = [
  {
    name: 'tmux',
    repository: 'sipeed/lele-skills/tmux',
    description: 'Remote-control tmux',
    author: 'sipeed',
    tags: ['cli'],
  },
]

const SCANNED = [
  { name: 'alpha', description: 'a', path: 'alpha', has_skill: true },
  { name: 'beta', description: 'b', path: 'beta', has_skill: true },
]

// ---------------------------------------------------------------------------
// Fetch router
// ---------------------------------------------------------------------------

type Body = { url?: string; skills?: string[]; repo?: string; enabled?: boolean; scope?: string }
type Req = { method: string; path: string; url: string; body: Body | undefined }

function installServer(opts: { catalogError?: boolean } = {}): Req[] {
  const requests: Req[] = []
  globalThis.fetch = mock(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input)
    const path = new URL(url).pathname
    const method = (init?.method ?? 'GET').toUpperCase()
    const body =
      typeof init?.body === 'string'
        ? (JSON.parse(init.body) as Body)
        : (undefined as Body | undefined)
    requests.push({ method, path, url, body })
    const json = (data: unknown, status = 200) =>
      new Response(JSON.stringify(data), {
        status,
        headers: { 'Content-Type': 'application/json' },
      })

    // Global skills API
    if (path === '/api/v1/skills' && method === 'GET') return json({ skills: SKILLS })
    if (path === '/api/v1/skills' && method === 'POST')
      return json({ skill_id: (body?.url ?? '').split('/').pop(), message: 'ok' })
    if (path === '/api/v1/skills/available' && method === 'GET') return json({ skills: AVAILABLE })
    if (path === '/api/v1/skills/install-batch' && method === 'POST')
      return json({
        installed: body?.skills ?? [],
        count: body?.skills?.length ?? 0,
        message: 'ok',
      })
    if (path === '/api/v1/skills/scan' && method === 'POST')
      return json({ repo: body?.repo ?? '', skills: SCANNED })
    if (path.startsWith('/api/v1/skills/') && path.endsWith('/toggle') && method === 'PUT')
      return json({ name: 'x', enabled: body?.enabled, message: 'ok' })
    if (path.startsWith('/api/v1/skills/') && method === 'DELETE') return json({ message: 'ok' })

    // Per-agent API
    const catalog = path.match(/^\/api\/v1\/agents\/([^/]+)\/catalog$/)
    if (catalog && method === 'GET') {
      if (opts.catalogError) return json({ error: 'boom' }, 500)
      const id = decodeURIComponent(catalog[1])
      return json(CATALOGS[id] ?? { agent_id: id, workspace: '', tools: [], skills: [] })
    }
    const agentBatch = path.match(/^\/api\/v1\/agents\/([^/]+)\/skills\/install-batch$/)
    if (agentBatch && method === 'POST')
      return json({
        installed: body?.skills ?? [],
        count: body?.skills?.length ?? 0,
        message: 'ok',
      })
    const agentInstall = path.match(/^\/api\/v1\/agents\/([^/]+)\/skills\/install$/)
    if (agentInstall && method === 'POST')
      return json({ skill_id: (body?.url ?? '').split('/').pop(), message: 'ok' })
    const agentToggle = path.match(/^\/api\/v1\/agents\/([^/]+)\/skills\/([^/]+)\/toggle$/)
    if (agentToggle && method === 'PUT') return json({ message: 'ok', enabled: body?.enabled })
    const agentRemove = path.match(/^\/api\/v1\/agents\/([^/]+)\/skills\/([^/]+)$/)
    if (agentRemove && method === 'DELETE') return json({ message: 'ok' })

    return json({ error: `unexpected fetch: ${method} ${path}` }, 404)
  }) as unknown as typeof fetch
  return requests
}

// ---------------------------------------------------------------------------
// Render harness
// ---------------------------------------------------------------------------

function LocationProbe() {
  const location = useLocation()
  return createElement(
    'div',
    { 'data-testid': 'location-probe' },
    `${location.pathname}${location.search}`,
  )
}

function renderPage(
  opts: {
    agents?: Agent[]
    catalogError?: boolean
    initialEntries?: string[]
  } = {},
) {
  const requests = installServer({ catalogError: opts.catalogError })
  const apiClient = createApiClient('http://127.0.0.1:18793')
  apiClient.setToken('token', 'refresh')
  const authValue = {
    api: apiClient,
    apiUrl: 'http://127.0.0.1:18793',
    session: { token: 'token', client_id: 'client-1', device_name: 'Test' },
  } as unknown as AuthContextValue
  const logicValue = {
    agents: opts.agents ?? AGENTS,
    currentAgent: null,
    sessions: [],
    currentSessionKey: null,
    parentSessionKey: null,
    processingSessions: new Set<string>(),
    chatMode: 'agent',
    sidebarOpen: true,
    mobileSidebarOpen: false,
    onCloseMobileSidebar: () => undefined,
    onOpenMobileSidebar: () => undefined,
    onToggleSidebar: () => undefined,
    onSelectSession: () => undefined,
    onDeleteSession: () => undefined,
    onCreateSession: () => undefined,
    onLogout: async () => undefined,
  } as unknown as AppLogicContextValue
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  const utils = render(
    createElement(
      QueryClientProvider,
      { client: queryClient },
      createElement(
        AuthContext.Provider,
        { value: authValue },
        createElement(
          AppLogicContext.Provider,
          { value: logicValue },
          createElement(
            MemoryRouter,
            { initialEntries: opts.initialEntries ?? ['/skills'] },
            createElement(SkillsPage),
            createElement(LocationProbe),
          ),
        ),
      ),
    ),
  )

  return { ...utils, requests, queryClient }
}

// ---------------------------------------------------------------------------
// DOM queries
// ---------------------------------------------------------------------------

/** `screen` is unusable here: every query goes through the render result. */
function byTestId<T extends HTMLElement = HTMLElement>(container: HTMLElement, id: string): T {
  const el = container.querySelector(`[data-testid="${id}"]`)
  if (!el) throw new Error(`no element with data-testid="${id}"`)
  return el as T
}

/** Card title order, which is also the order `SkillsList` renders. */
function cardNames(container: HTMLElement): string[] {
  return Array.from(container.querySelectorAll('h4')).map((el) => el.textContent ?? '')
}

function buttonWithText(container: HTMLElement, text: string): HTMLButtonElement {
  const btn = Array.from(container.querySelectorAll('button')).find(
    (b) => b.textContent?.trim() === text,
  )
  if (!btn) throw new Error(`button "${text}" not found`)
  return btn as HTMLButtonElement
}

function cardFor(container: HTMLElement, name: string): HTMLElement {
  const h4 = Array.from(container.querySelectorAll('h4')).find((el) => el.textContent === name)
  if (!h4) throw new Error(`card "${name}" not found`)
  const card = h4.closest('div.group') as HTMLElement | null
  if (!card) throw new Error(`card container for "${name}" not found`)
  return card
}

function locationOf(container: HTMLElement): string {
  return byTestId(container, 'location-probe').textContent ?? ''
}

function postsTo(requests: Req[], path: string): Req[] {
  return requests.filter((r) => r.method === 'POST' && r.path === path)
}

function getsTo(requests: Req[], path: string): Req[] {
  return requests.filter((r) => r.method === 'GET' && r.path === path)
}

/** Open the install modal and wait for the browse list to resolve. */
async function openInstallModal(container: HTMLElement) {
  fireEvent.click(byTestId(container, 'skills-install-button'))
  await waitFor(() => {
    expect(buttonWithText(container, tr('skills.install'))).toBeTruthy()
  })
}

// ===========================================================================
// 1–3 · Tab switching and deep links
// ===========================================================================

describe('SkillsPage — views', () => {
  test('1. mounts on All with one GET /api/v1/skills, four cards and no URL params', async () => {
    const { container, requests } = renderPage()

    await waitFor(() => {
      expect(cardNames(container)).toEqual(['workspace-skill', 'global-a', 'builtin-a', 'global-b'])
    })
    expect(getsTo(requests, '/api/v1/skills')).toHaveLength(1)
    // No eager catalog fetch: the agent view is not mounted yet.
    expect(requests.some((r) => r.path.endsWith('/catalog'))).toBe(false)
    expect(locationOf(container)).toBe('/skills')
  })

  test('2. Global filters client-side to the two global cards, without any request', async () => {
    const { container, requests } = renderPage()
    await waitFor(() => expect(cardNames(container)).toHaveLength(4))
    const before = requests.length

    fireEvent.click(buttonWithText(container, tr('skills.view.global')))

    await waitFor(() => expect(cardNames(container)).toEqual(['global-a', 'global-b']))
    expect(requests.length).toBe(before)
    expect(locationOf(container)).toBe('/skills?view=global')
  })

  test('3. a ?view=global deep link restores the Global view on cold mount', async () => {
    const { container } = renderPage({ initialEntries: ['/skills?view=global'] })

    await waitFor(() => expect(cardNames(container)).toEqual(['global-a', 'global-b']))
    expect(locationOf(container)).toBe('/skills?view=global')
  })

  test('3b. a ?view=agent&agent=planner deep link restores that agent on cold mount', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=planner'],
    })

    await waitFor(() => expect(locationOf(container)).toBe('/skills?view=agent&agent=planner'))
    await waitFor(() => expect(cardNames(container)).toEqual(['roadmap', 'weather']))
    expect(getsTo(requests, '/api/v1/agents/planner/catalog')).toHaveLength(1)
  })
})

// ===========================================================================
// 4–6 · By-agent view, selector and normalisation
// ===========================================================================

describe('SkillsPage — by agent', () => {
  test('4. By agent fetches the default agent catalog and pins it in the URL', async () => {
    const { container, requests } = renderPage()
    await waitFor(() => expect(cardNames(container)).toHaveLength(4))

    fireEvent.click(buttonWithText(container, tr('skills.view.agent')))

    await waitFor(() => expect(locationOf(container)).toBe('/skills?view=agent&agent=coder'))
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))
    expect(getsTo(requests, '/api/v1/agents/coder/catalog')).toHaveLength(1)
  })

  test('5. changing the selector requests the other catalog and rewrites the URL', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=coder'],
    })
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))

    fireEvent.change(container.querySelector('select#skills-agent') as HTMLSelectElement, {
      target: { value: 'planner' },
    })

    await waitFor(() => expect(locationOf(container)).toBe('/skills?view=agent&agent=planner'))
    await waitFor(() => expect(cardNames(container)).toEqual(['roadmap', 'weather']))
    expect(getsTo(requests, '/api/v1/agents/planner/catalog')).toHaveLength(1)
  })

  test('6. an unknown ?agent= falls back to the default and is REPLACED in the URL', async () => {
    const { container } = renderPage({
      initialEntries: ['/skills?view=agent&agent=ghost'],
    })

    await waitFor(() => expect(locationOf(container)).toBe('/skills?view=agent&agent=coder'))
    expect(locationOf(container)).not.toContain('ghost')
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))
  })
})

// ===========================================================================
// 7–10 · Destination-aware install (the heart of the feature)
// ===========================================================================

describe('SkillsPage — install destination', () => {
  test('7. Global install states the destination and sends scope:"global"', async () => {
    const { container, requests } = renderPage({ initialEntries: ['/skills?view=global'] })
    await waitFor(() => expect(cardNames(container)).toEqual(['global-a', 'global-b']))

    await openInstallModal(container)
    expect(byTestId(container, 'install-destination').textContent).toContain(
      tr('skills.installIntoTarget', { target: tr('skills.scopeGlobal') }),
    )

    fireEvent.click(buttonWithText(container, tr('skills.install')))
    await waitFor(() => expect(postsTo(requests, '/api/v1/skills')).toHaveLength(1))
    expect(postsTo(requests, '/api/v1/skills')[0].body).toEqual({
      url: 'sipeed/lele-skills/tmux',
      scope: 'global',
    })
  })

  test('8. All install keeps the legacy body: {url} and no scope key', async () => {
    const { container, requests } = renderPage()
    await waitFor(() => expect(cardNames(container)).toHaveLength(4))

    await openInstallModal(container)
    expect(container.querySelector('[data-testid="install-destination"]')?.textContent).toContain(
      tr('skills.installIntoTarget', { target: tr('skills.targetDefaultWorkspace') }),
    )

    fireEvent.click(buttonWithText(container, tr('skills.install')))
    await waitFor(() => expect(postsTo(requests, '/api/v1/skills')).toHaveLength(1))
    const body = postsTo(requests, '/api/v1/skills')[0].body ?? {}
    expect(body).toEqual({ url: 'sipeed/lele-skills/tmux' })
    expect(Object.keys(body)).toEqual(['url'])
  })

  test('9. By agent installs into the agent workspace and the picker switches scope only', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=coder'],
    })
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))

    await openInstallModal(container)
    // The By-agent view keeps the INTERACTIVE picker, defaulting to workspace.
    expect(byTestId(container, 'install-scope')).toBeTruthy()
    expect(container.querySelector('[data-testid="install-destination"]')).toBeNull()

    fireEvent.click(buttonWithText(container, tr('skills.install')))
    await waitFor(() =>
      expect(postsTo(requests, '/api/v1/agents/coder/skills/install')).toHaveLength(1),
    )
    expect(postsTo(requests, '/api/v1/agents/coder/skills/install')[0].body).toEqual({
      url: 'sipeed/lele-skills/tmux',
      scope: 'workspace',
    })

    fireEvent.click(byTestId(container, 'install-scope-global'))
    await waitFor(() =>
      expect(buttonWithText(container, tr('skills.install')).disabled).toBe(false),
    )
    fireEvent.click(buttonWithText(container, tr('skills.install')))
    await waitFor(() =>
      expect(postsTo(requests, '/api/v1/agents/coder/skills/install')).toHaveLength(2),
    )
    expect(postsTo(requests, '/api/v1/agents/coder/skills/install')[1].body).toEqual({
      url: 'sipeed/lele-skills/tmux',
      scope: 'global',
    })
  })

  test('10. Scan + batch on Global carries the repo, the skills and scope:"global"', async () => {
    const { container, requests } = renderPage({ initialEntries: ['/skills?view=global'] })
    await waitFor(() => expect(cardNames(container)).toEqual(['global-a', 'global-b']))

    fireEvent.click(byTestId(container, 'skills-install-button'))
    await waitFor(() => expect(buttonWithText(container, tr('skills.install'))).toBeTruthy())

    fireEvent.click(buttonWithText(container, tr('skills.installFromUrl')))
    const repoInput = document.getElementById('github-repo-input') as HTMLInputElement
    fireEvent.change(repoInput, { target: { value: 'owner/repo' } })

    fireEvent.click(buttonWithText(container, tr('skills.scan', { defaultValue: 'Scan' })))
    await waitFor(() => expect(postsTo(requests, '/api/v1/skills/scan')).toHaveLength(1))

    const installSelected = tr('skills.installSelected', {
      count: 2,
      defaultValue: 'Install {{count}} skills',
    })
    await waitFor(() => expect(buttonWithText(container, installSelected)).toBeTruthy())
    fireEvent.click(buttonWithText(container, installSelected))

    await waitFor(() => expect(postsTo(requests, '/api/v1/skills/install-batch')).toHaveLength(1))
    expect(postsTo(requests, '/api/v1/skills/install-batch')[0].body).toEqual({
      repo: 'owner/repo',
      skills: ['alpha', 'beta'],
      scope: 'global',
    })
  })
})

// ===========================================================================
// 11–13 · Per-view remove and toggle capabilities
// ===========================================================================

describe('SkillsPage — capabilities', () => {
  test('11. By agent: only a deletable card offers remove, and it confirms inline', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=coder'],
    })
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))

    const removeTitle = tr('skills.removeSkill')
    expect(
      cardFor(container, 'chrome').querySelector(`button[title="${removeTitle}"]`),
    ).toBeTruthy()
    expect(cardFor(container, 'weather').querySelector(`button[title="${removeTitle}"]`)).toBeNull()
    expect(cardFor(container, 'memory').querySelector(`button[title="${removeTitle}"]`)).toBeNull()

    fireEvent.click(
      cardFor(container, 'chrome').querySelector(`button[title="${removeTitle}"]`) as HTMLElement,
    )
    fireEvent.click(buttonWithText(container, tr('skills.removeConfirmYes')))

    await waitFor(() =>
      expect(
        requests.filter(
          (r) => r.method === 'DELETE' && r.path === '/api/v1/agents/coder/skills/chrome',
        ),
      ).toHaveLength(1),
    )
  })

  test('12. By agent toggle disables through the per-agent endpoint', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=coder'],
    })
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))

    fireEvent.click(
      cardFor(container, 'chrome').querySelector(
        `button[title="${tr('skills.disable', { defaultValue: 'Disable' })}"]`,
      ) as HTMLElement,
    )

    await waitFor(() =>
      expect(
        requests.filter(
          (r) => r.method === 'PUT' && r.path === '/api/v1/agents/coder/skills/chrome/toggle',
        ),
      ).toHaveLength(1),
    )
    const toggle = requests.find(
      (r) => r.method === 'PUT' && r.path === '/api/v1/agents/coder/skills/chrome/toggle',
    )
    expect(toggle?.body).toEqual({ enabled: false })
  })

  test('13. Global hides the toggle and removes through the global endpoint', async () => {
    const { container, requests } = renderPage({ initialEntries: ['/skills?view=global'] })
    await waitFor(() => expect(cardNames(container)).toEqual(['global-a', 'global-b']))

    // D6: no per-agent toggle in the Global view.
    expect(
      container.querySelector(
        `button[title="${tr('skills.disable', { defaultValue: 'Disable' })}"]`,
      ),
    ).toBeNull()
    expect(
      container.querySelector(`button[title="${tr('skills.enable', { defaultValue: 'Enable' })}"]`),
    ).toBeNull()

    const removeTitle = tr('skills.removeSkill')
    fireEvent.click(
      cardFor(container, 'global-a').querySelector(`button[title="${removeTitle}"]`) as HTMLElement,
    )
    fireEvent.click(buttonWithText(container, tr('skills.removeConfirmYes')))

    await waitFor(() =>
      expect(
        requests.filter((r) => r.method === 'DELETE' && r.path === '/api/v1/skills/global-a'),
      ).toHaveLength(1),
    )
  })
})

// ===========================================================================
// 14–17 · Dual-cache refresh, errors, empty states, count
// ===========================================================================

describe('SkillsPage — data coherence and edge cases', () => {
  test('14. a By-agent install refreshes BOTH caches (D7)', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=coder'],
    })
    await waitFor(() => expect(cardNames(container)).toEqual(['chrome', 'weather', 'memory']))
    expect(getsTo(requests, '/api/v1/skills')).toHaveLength(1)
    expect(getsTo(requests, '/api/v1/agents/coder/catalog')).toHaveLength(1)

    await openInstallModal(container)
    fireEvent.click(buttonWithText(container, tr('skills.install')))

    await waitFor(() =>
      expect(postsTo(requests, '/api/v1/agents/coder/skills/install')).toHaveLength(1),
    )
    // The agent hook invalidates the catalog; the page refetches the global list.
    await waitFor(() => expect(getsTo(requests, '/api/v1/agents/coder/catalog')).toHaveLength(2))
    await waitFor(() => expect(getsTo(requests, '/api/v1/skills')).toHaveLength(2))
  })

  test('15. a catalog error shows the banner + Retry, and All still renders', async () => {
    const { container, requests } = renderPage({
      initialEntries: ['/skills?view=agent&agent=coder'],
      catalogError: true,
    })

    await waitFor(() => expect(container.textContent).toContain(tr('skills.catalogLoadError')), {
      timeout: 15000,
    })
    const before = getsTo(requests, '/api/v1/agents/coder/catalog').length
    fireEvent.click(buttonWithText(container, tr('common.retry', { defaultValue: 'Retry' })))
    await waitFor(
      () => expect(getsTo(requests, '/api/v1/agents/coder/catalog').length).toBeGreaterThan(before),
      { timeout: 5000 },
    )

    fireEvent.click(buttonWithText(container, tr('skills.view.all')))
    await waitFor(() => expect(cardNames(container)).toHaveLength(4))
  })

  test('16. no agents → the empty state explains it and install is disabled', async () => {
    const { container } = renderPage({ agents: [], initialEntries: ['/skills?view=agent'] })

    await waitFor(() => expect(container.textContent).toContain(tr('skills.noAgents')))
    expect(container.textContent).toContain(tr('skills.noAgentsDesc'))
    expect(byTestId<HTMLButtonElement>(container, 'skills-install-button').disabled).toBe(true)
    expect(container.querySelector('select#skills-agent')).toBeNull()
    expect(container.querySelector('a[href="/agents"]')).toBeTruthy()
  })

  test('17. the ARIA-live count reports the VISIBLE length per view', async () => {
    const { container } = renderPage()
    const count = () => byTestId(container, 'skills-count')

    await waitFor(() => expect(count().textContent).toBe(tr('skills.installedCount', { count: 4 })))
    expect(count().getAttribute('aria-live')).toBe('polite')

    fireEvent.click(buttonWithText(container, tr('skills.view.global')))
    await waitFor(() => expect(count().textContent).toBe(tr('skills.installedCount', { count: 2 })))

    fireEvent.click(buttonWithText(container, tr('skills.view.agent')))
    await waitFor(() => expect(count().textContent).toBe(tr('skills.installedCount', { count: 3 })))
  })
})
