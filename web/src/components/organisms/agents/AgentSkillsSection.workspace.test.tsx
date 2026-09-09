import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { AgentCatalogResponse, EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { AgentSkillsSection } from './AgentSkillsSection'

/**
 * Workspace administration of the per-agent Skills tab.
 *
 * The sibling file (AgentSkillsSection.test.tsx) covers the ALLOWLIST — what
 * the agent may use, written to the config draft and saved with the form. This
 * one covers the other half: what is INSTALLED and ENABLED in the agent's own
 * workspace, which goes straight to REST and must refresh the grid afterwards.
 *
 * `screen` is unusable in this environment (see agentsHarness header): all
 * queries come from the render result.
 */

/** Workspace the fake catalog reports; the install dialog echoes it. */
const AGENT_WORKSPACE = '/home/u/.lele/workspace-coder'

const AGENT: EditableAgentConfig = { id: 'coder', name: 'Coder' }

/** One skill per source, so the delete rules can be told apart by name. */
const CATALOG: AgentCatalogResponse = {
  agent_id: 'coder',
  workspace: AGENT_WORKSPACE,
  tools: [],
  skills: [
    {
      name: 'chrome',
      description: 'Automate Chrome for debugging',
      source: 'workspace',
      enabled: true,
      deletable: true,
    },
    {
      name: 'weather',
      description: 'Get weather and forecasts',
      source: 'global',
      enabled: true,
      deletable: false,
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

/** Recorded calls, so a test can assert WHICH agent and WHICH state was sent. */
type Call =
  | { kind: 'toggle'; agentId: string; name: string; enabled: boolean }
  | { kind: 'remove'; agentId: string; name: string }
  | { kind: 'install'; agentId: string; url: string; scope?: string }
  | { kind: 'installBatch'; agentId: string; repo: string; skills: string[]; scope?: string }
  | { kind: 'scan'; repo: string }

function makeApi(calls: Call[], options: { failToggle?: boolean } = {}): ApiClient {
  return {
    models: async () => ({ models: [], model_groups: [] }),
    getAgentCatalog: async () => CATALOG,
    availableSkills: async () => {
      return {
        skills: [
          {
            name: 'tmux',
            repository: 'sipeed/lele-skills/tmux',
            description: 'Remote-control tmux',
            author: 'sipeed',
            tags: ['cli'],
          },
        ],
      }
    },
    scanSkills: async (repo: string) => {
      calls.push({ kind: 'scan', repo })
      return { repo, skills: [] }
    },
    agentToggleSkill: async (agentId: string, name: string, enabled: boolean) => {
      calls.push({ kind: 'toggle', agentId, name, enabled })
      if (options.failToggle) throw new Error('workspace is read only')
      return { message: 'ok' }
    },
    agentRemoveSkill: async (agentId: string, name: string) => {
      calls.push({ kind: 'remove', agentId, name })
      return { message: 'ok' }
    },
    agentInstallSkill: async (agentId: string, url: string, scope?: string) => {
      calls.push({ kind: 'install', agentId, url, scope })
      return { skill_id: url.split('/').pop() ?? url, message: 'ok' }
    },
    agentInstallSkillsBatch: (agentId: string, repo: string, skills: string[], scope?: string) => {
      calls.push({ kind: 'installBatch', agentId, repo, skills, scope })
      return Promise.resolve({ installed: skills, count: skills.length, message: 'ok' })
    },
  } as unknown as ApiClient
}

function setup(options: { failToggle?: boolean } = {}) {
  const writes: Write[] = []
  const calls: Call[] = []
  const api = makeApi(calls, options)
  const state = makeState([AGENT], { writes })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={state} api={api}>
        <MemoryRouter initialEntries={['/agents/coder/skills']}>
          <AgentSkillsSection agent={AGENT} index={0} agentId="coder" />
        </MemoryRouter>
      </SettingsProvider>
    </QueryClientProvider>,
  )

  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
  /** Click a test id, failing loudly (not silently on null) when missing. */
  const requireTestId = (id: string): HTMLElement => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    return el as HTMLElement
  }
  const callsOf = (kind: Call['kind']) => calls.filter((call) => call.kind === kind)
  const buttonsWithText = (text: string) =>
    Array.from(utils.container.querySelectorAll('button')).filter(
      (button) => button.textContent?.trim() === text,
    )
  /** The card's own checkbox (the allowlist control), found the house way. */
  const inputFor = (name: string) => {
    const cards = Array.from(utils.container.querySelectorAll('label')) as HTMLLabelElement[]
    const card = cards.find((element) => element.textContent?.includes(name))
    if (!card) throw new Error(`no ToggleCard for ${name}`)
    return card.querySelector('input[type="checkbox"]') as HTMLInputElement
  }

  return { ...utils, writes, calls, callsOf, byTestId, requireTestId, buttonsWithText, inputFor }
}

/** Wait until the catalog resolves and the real grid is on screen. */
async function ready(utils: ReturnType<typeof setup>) {
  await waitFor(() => {
    expect(utils.byTestId('skills-grid')).toBeTruthy()
  })
  return utils
}

/** Open the install dialog and switch it to the "Install from URL" tab. */
async function openUrlTab(utils: ReturnType<typeof setup>) {
  fireEvent.click(utils.requireTestId('skills-add'))
  await waitFor(() => {
    expect(utils.byTestId('install-scope')).toBeTruthy()
  })
  const urlTab = utils.buttonsWithText(tr('skills.installFromUrl'))[0]
  expect(urlTab).toBeTruthy()
  fireEvent.click(urlTab as HTMLElement)
  await waitFor(() => {
    expect(utils.container.querySelector('#github-repo-input')).toBeTruthy()
  })
}

describe('AgentSkillsSection — workspace enable/disable', () => {
  test('disabling a workspace skill sends enabled=false for THIS agent', async () => {
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skill-toggle-chrome'))

    await waitFor(() => {
      expect(utils.callsOf('toggle')).toEqual([
        { kind: 'toggle', agentId: 'coder', name: 'chrome', enabled: false },
      ])
    })
  })

  test('a GLOBAL skill can still be disabled for this agent alone', async () => {
    // The per-agent workspace.json is exactly where "not for me" lives; only
    // REMOVING is restricted to skills this agent owns.
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skill-toggle-weather'))

    await waitFor(() => {
      expect(utils.callsOf('toggle')).toEqual([
        { kind: 'toggle', agentId: 'coder', name: 'weather', enabled: false },
      ])
    })
  })

  test('a disabled skill offers an enable action (not a dead icon)', async () => {
    // `memory` is installed but disabled in the catalog.
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skill-toggle-memory'))

    await waitFor(() => {
      expect(utils.callsOf('toggle')).toEqual([
        { kind: 'toggle', agentId: 'coder', name: 'memory', enabled: true },
      ])
    })
  })

  test('the toggle never writes the config draft (it is not an allowlist edit)', async () => {
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skill-toggle-chrome'))
    await waitFor(() => expect(utils.callsOf('toggle')).toHaveLength(1))

    expect(utils.writes).toHaveLength(0)
  })

  test('the card actions cancel the label default activation', async () => {
    // The card root is a <label> bound to the checkbox, so ANY click inside it
    // would toggle the allowlist unless the action wrapper cancels the label's
    // default activation. jsdom never performs label activation itself, so
    // asserting `input.checked` here would pass vacuously: what is checked is
    // the mechanism — the click leaving the card already cancelled, which is
    // what a real browser acts on. (React delegates its own handlers above the
    // label, so the listener has to sit at the document level.)
    const utils = await ready(setup())

    for (const id of ['skill-toggle-chrome', 'skill-remove-chrome']) {
      const action = utils.requireTestId(id)
      expect(action.closest('label')).toBeTruthy()
      // `MouseEvent` is not on globalThis here (setup.ts only installs
      // window/document), so it comes through window.
      const event = new window.MouseEvent('click', { bubbles: true, cancelable: true })
      action.dispatchEvent(event)
      expect(event.defaultPrevented).toBe(true)
    }

    // And the allowlist must stay untouched by those clicks.
    expect(utils.writes).toHaveLength(0)
  })

  test('a failed toggle surfaces the message and still refreshes', async () => {
    const utils = await ready(setup({ failToggle: true }))

    fireEvent.click(utils.requireTestId('skill-toggle-chrome'))

    await waitFor(() => {
      expect(utils.byTestId('skills-action-error')?.textContent).toContain('workspace is read only')
    })
    // The grid is still there: the failure must not blank the panel.
    expect(utils.byTestId('skills-grid')).toBeTruthy()
  })
})

describe('AgentSkillsSection — workspace remove', () => {
  test('remove is offered only for skills the server marked deletable', async () => {
    const utils = await ready(setup())

    expect(utils.byTestId('skill-remove-chrome')).toBeTruthy()
    expect(utils.byTestId('skill-remove-weather')).toBeNull()
    expect(utils.byTestId('skill-remove-memory')).toBeNull()
  })

  test('removal asks for confirmation before deleting', async () => {
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skill-remove-chrome'))
    expect(utils.callsOf('remove')).toHaveLength(0)

    utils.requireTestId('skill-remove-confirm-chrome')
    fireEvent.click(utils.requireTestId('skill-remove-yes-chrome'))

    await waitFor(() => {
      expect(utils.callsOf('remove')).toEqual([
        { kind: 'remove', agentId: 'coder', name: 'chrome' },
      ])
    })
  })

  test('cancelling the confirmation deletes nothing', async () => {
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skill-remove-chrome'))
    const cancel = utils.buttonsWithText(tr('common.cancel'))[0]
    expect(cancel).toBeTruthy()
    fireEvent.click(cancel as HTMLElement)

    expect(utils.callsOf('remove')).toHaveLength(0)
    expect(utils.byTestId('skill-remove-confirm-chrome')).toBeNull()
  })
})

describe('AgentSkillsSection — install into the agent', () => {
  test('the toolbar exposes an Add skill control', async () => {
    const utils = await ready(setup())
    expect(utils.byTestId('skills-add')).toBeTruthy()
  })

  test('the URL tab defaults to the agent workspace scope', async () => {
    const utils = await ready(setup())
    await openUrlTab(utils)

    const input = utils.container.querySelector('#github-repo-input') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'sipeed/lele-skills/tmux' } })

    const installButton = utils.buttonsWithText(tr('skills.install'))[0]
    expect(installButton).toBeTruthy()
    fireEvent.click(installButton as HTMLElement)

    await waitFor(() => {
      expect(utils.callsOf('install')).toEqual([
        { kind: 'install', agentId: 'coder', url: 'sipeed/lele-skills/tmux', scope: 'workspace' },
      ])
    })
  })

  test('the scope control prints the concrete workspace directory', async () => {
    const utils = await ready(setup())
    await openUrlTab(utils)

    expect(utils.byTestId('install-scope-workspace')?.textContent).toContain(AGENT_WORKSPACE)
    expect(utils.byTestId('install-scope-global')?.textContent).toContain('~/.lele/skills')
  })

  test('choosing Global sends scope=global', async () => {
    const utils = await ready(setup())
    await openUrlTab(utils)

    fireEvent.click(utils.requireTestId('install-scope-global'))
    const input = utils.container.querySelector('#github-repo-input') as HTMLInputElement
    fireEvent.change(input, { target: { value: 'owner/repo/skill' } })

    fireEvent.click(utils.buttonsWithText(tr('skills.install'))[0] as HTMLElement)

    await waitFor(() => {
      expect(utils.callsOf('install')).toEqual([
        { kind: 'install', agentId: 'coder', url: 'owner/repo/skill', scope: 'global' },
      ])
    })
  })

  test('the Browse tab installs through the same scoped call', async () => {
    const utils = await ready(setup())

    fireEvent.click(utils.requireTestId('skills-add'))
    await waitFor(() => {
      expect(utils.byTestId('install-scope')).toBeTruthy()
    })
    // Browse is the default tab, and its list loads asynchronously: wait for
    // the row's Install button before clicking it.
    let browseButton: HTMLButtonElement | undefined
    await waitFor(() => {
      browseButton = utils.buttonsWithText(tr('skills.install'))[0]
      expect(browseButton).toBeTruthy()
    })
    // The per-skill Install button must also carry the chosen scope, otherwise
    // half the dialog ignores the picker.
    fireEvent.click(browseButton as HTMLElement)

    await waitFor(() => {
      expect(utils.callsOf('install')).toEqual([
        { kind: 'install', agentId: 'coder', url: 'sipeed/lele-skills/tmux', scope: 'workspace' },
      ])
    })
  })

  test('the Skills page dialog stays scope-less (no destination control)', async () => {
    // Regression guard for the shared component: InstallSkillModal must not
    // grow a scope picker for its original consumer.
    const { InstallSkillModal } = await import('../InstallSkillModal')
    const utils = render(
      <InstallSkillModal
        isOpen
        onClose={() => {}}
        availableSkills={[]}
        isAvailableLoading={false}
        isInstalling={false}
        isScanning={false}
        scanResults={null}
        onInstall={() => {}}
        onScan={async () => null}
        onInstallBatch={() => {}}
        onClearScan={() => {}}
      />,
    )
    expect(utils.container.querySelector('[data-testid="install-scope"]')).toBeNull()
  })
})
