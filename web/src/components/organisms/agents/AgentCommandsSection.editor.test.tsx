import '../../../test/setup'
import { describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type {
  AgentCommandInfo,
  AgentCommandsResponse,
  EditableAgentConfig,
} from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { makeState, tr } from '../../../test/agentsHarness'
import { AgentCommandsSection } from './AgentCommandsSection'

const AGENT: EditableAgentConfig = { id: 'coder', name: 'Coder' }
const WS_DIR = '/home/u/.lele/workspace-coder/commands'

function command(overrides: Partial<AgentCommandInfo>): AgentCommandInfo {
  return {
    name: 'deploy',
    description: 'Deploy the current branch',
    source: 'workspace',
    path: `${WS_DIR}/deploy.md`,
    agent: '',
    model: '',
    allow_shell: false,
    allow_absolute_files: null,
    deletable: true,
    shadowed_by: '',
    ...overrides,
  }
}

const RESPONSE: AgentCommandsResponse = {
  agent_id: 'coder',
  workspace: '/home/u/.lele/workspace-coder',
  commands_dir: WS_DIR,
  commands_dir_exists: true,
  shared_by: 1,
  harness: { allow_shell: false, allow_absolute_files: false },
  commands: [
    command({ name: 'deploy', allow_shell: true }),
    command({
      name: 'deploy',
      source: 'global',
      path: '/home/u/.lele/commands/deploy.md',
      deletable: false,
      shadowed_by: 'workspace',
    }),
    command({
      name: 'review',
      source: 'config',
      path: '',
      agent: 'fixer',
      model: 'claude-opus',
      deletable: false,
    }),
  ],
  builtin: [
    { name: 'new', description: 'New session', usage: '/new' },
    { name: 'model', description: 'Show or set the model', usage: '/model [name]' },
  ],
}

const DEPLOY_MARKDOWN = [
  '---',
  'description: "Deploy the current branch"',
  'model: "gpt-5"',
  'allow_shell: true',
  '---',
  'Ship $1 from @src/main.ts',
].join('\n')

type Call =
  | { kind: 'detail'; agentId: string; name: string }
  | { kind: 'create'; agentId: string; body: Record<string, unknown> }
  | { kind: 'update'; agentId: string; name: string; body: Record<string, unknown> }
  | { kind: 'remove'; agentId: string; name: string }

function makeApi(options: { failCreate?: boolean; detail?: string } = {}) {
  const calls: Call[] = []
  const api = {
    models: async () => ({ models: ['gpt-5', 'claude-opus'], model_groups: [] }),
    agentCommands: async () => RESPONSE,
    agentCommand: async (agentId: string, name: string) => {
      calls.push({ kind: 'detail', agentId, name })
      return {
        name,
        content: options.detail ?? DEPLOY_MARKDOWN,
        path: `${WS_DIR}/${name}.md`,
        source: 'workspace',
        deletable: true,
      }
    },
    agentCommandCreate: async (agentId: string, body: Record<string, unknown>) => {
      calls.push({ kind: 'create', agentId, body })
      if (options.failCreate) throw new Error('command name already exists')
      return { ok: true }
    },
    agentCommandUpdate: async (agentId: string, name: string, body: Record<string, unknown>) => {
      calls.push({ kind: 'update', agentId, name, body })
      return { ok: true }
    },
    agentCommandRemove: async (agentId: string, name: string) => {
      calls.push({ kind: 'remove', agentId, name })
      return { ok: true }
    },
  } as unknown as ApiClient
  return { api, calls }
}

function setup(options: { failCreate?: boolean; detail?: string } = {}) {
  const { api, calls } = makeApi(options)
  const state = makeState([AGENT])
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={state} api={api}>
        <MemoryRouter initialEntries={['/agents/coder/commands']}>
          <AgentCommandsSection agent={AGENT} agentId="coder" />
        </MemoryRouter>
      </SettingsProvider>
    </QueryClientProvider>,
  )
  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
  const requireTestId = (id: string): HTMLElement => {
    const element = byTestId(id)
    if (!element) throw new Error(`expected element with data-testid="${id}"`)
    return element as HTMLElement
  }
  const callsOf = (kind: Call['kind']) => calls.filter((call) => call.kind === kind)
  const firstCall = <K extends Call['kind']>(kind: K) => {
    const call = calls.find((entry) => entry.kind === kind)
    if (!call) throw new Error(`expected a "${kind}" call`)
    return call as Extract<Call, { kind: K }>
  }
  const setValue = (id: string, value: string) => {
    fireEvent.change(requireTestId(id), { target: { value } })
  }
  const field = <T extends HTMLElement>(id: string) => requireTestId(id) as T
  const group = (id: string): HTMLElement => {
    const element = utils.container.querySelector(`#${id}`)
    if (!element) throw new Error(`expected element with id="${id}"`)
    return element as HTMLElement
  }
  const clickSegment = (groupId: string, label: string) => {
    const button = group(groupId).querySelector(`button[title="${label}"]`)
    if (!button) throw new Error(`expected segment "${label}" in ${groupId}`)
    fireEvent.click(button)
  }
  const selectedSegment = (groupId: string) =>
    group(groupId).querySelector('[aria-checked="true"]')?.textContent?.trim()
  return {
    ...utils,
    byTestId,
    requireTestId,
    calls,
    callsOf,
    firstCall,
    setValue,
    field,
    clickSegment,
    selectedSegment,
  }
}

async function openEditor(u: ReturnType<typeof setup>, name: string) {
  await waitFor(() => expect(u.byTestId(`command-row-${name}`)).toBeTruthy())
  fireEvent.click(u.requireTestId(`command-edit-${name}`))
}

function fillNewCommand(u: ReturnType<typeof setup>, name = 'hotfix') {
  u.setValue('command-editor-name', name)
  u.setValue('command-editor-description', 'Run the hotfix flow')
  u.setValue('command-editor-template', 'Fix $1 now')
}
describe('AgentCommandsSection — the table (T-F4)', () => {
  test('a shadowed row is dimmed, titled with its winner, and has no actions', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())

    const rows = u.container.querySelectorAll('[data-testid="command-row-deploy"]')
    expect(rows.length).toBe(2)
    const loser = Array.from(rows).find((row) => row.textContent?.includes('Global')) as Element
    expect(loser.getAttribute('class')).toContain('opacity-60')
    expect(loser.getAttribute('title')).toBe(
      tr('settings.agentPage.commands.shadowedBy', { source: 'workspace' }),
    )
    expect(loser.querySelector('[data-testid="command-edit-deploy"]')).toBeNull()
    expect(loser.querySelector('[data-testid="command-remove-deploy"]')).toBeNull()
    expect(loser.textContent).toContain(tr('settings.agentPage.commands.shadowedNoActions'))

    const winner = Array.from(rows).find((row) => row !== loser) as Element
    expect(winner.getAttribute('class') ?? '').not.toContain('opacity-60')
    expect(winner.querySelector('[data-testid="command-remove-deploy"]')).toBeTruthy()
  })

  test('a non-deletable row disables delete but never the editor', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('command-row-review')).toBeTruthy())

    const remove = u.requireTestId('command-remove-review') as HTMLButtonElement
    expect(remove.disabled).toBe(true)
    expect(remove.getAttribute('title')).toBe(tr('settings.agentPage.commands.notDeletable'))
    expect((u.requireTestId('command-edit-review') as HTMLButtonElement).disabled).toBe(false)
    expect((u.requireTestId('command-remove-deploy') as HTMLButtonElement).disabled).toBe(false)
  })

  test('the shared-workspace banner and the missing-folder chip follow the payload', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    expect(u.byTestId('commands-shared-banner')).toBeNull()
    expect(u.byTestId('commands-dir-missing')).toBeNull()
    expect(u.requireTestId('commands-dir').textContent).toBe(WS_DIR)
  })

  test('global harness permissions are echoed, not editable', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-harness')).toBeTruthy())
    const block = u.requireTestId('commands-harness')
    expect(block.textContent).toContain('harness.allow_shell')
    expect(block.textContent).toContain('harness.allow_absolute_files')
    expect(block.textContent).toContain(tr('common.disabled'))
    expect(block.querySelector('button, input, select')).toBeNull()
  })
})

describe('AgentCommandsSection — built-in block (T-F7)', () => {
  test('lists every built-in with its usage plus the globality note', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-builtin')).toBeTruthy())
    const block = u.requireTestId('commands-builtin')
    expect(block.getAttribute('open')).toBeNull()
    expect(block.textContent).toContain('/model [name]')
    expect(block.textContent).toContain('Show or set the model')
    expect(block.textContent).toContain(tr('settings.agentPage.commands.builtinNote'))
    expect(block.textContent).toContain(
      tr('settings.agentPage.commands.builtinTitle', { count: RESPONSE.builtin.length }),
    )
  })
})

describe('AgentCommandEditorDialog — create (T-F5)', () => {
  test('an invalid name blocks the save and never reaches the API', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))

    u.setValue('command-editor-description', 'Run the hotfix flow')
    u.setValue('command-editor-template', 'Fix $1 now')
    u.setValue('command-editor-name', 'Bad Name')

    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)
    expect(u.container.textContent).toContain(tr('settings.agentPage.commands.editor.nameInvalid'))
    expect(u.callsOf('create').length).toBe(0)

    u.setValue('command-editor-name', '../../escape')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)
    expect(u.callsOf('create').length).toBe(0)

    u.setValue('command-editor-name', 'hotfix')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(false)
    expect(u.container.textContent).not.toContain(
      tr('settings.agentPage.commands.editor.nameInvalid'),
    )
  })

  test('an empty description or template also blocks the save', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))

    u.setValue('command-editor-name', 'hotfix')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)

    u.setValue('command-editor-description', 'Run the hotfix flow')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)

    u.setValue('command-editor-template', '   ')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)
    expect(u.callsOf('create').length).toBe(0)
  })

  test('a valid create posts name + scope + the serialized markdown', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))
    fillNewCommand(u)

    const save = u.requireTestId('command-editor-save')
    expect(save.hasAttribute('disabled')).toBe(false)
    fireEvent.click(save)

    await waitFor(() => expect(u.callsOf('create').length).toBe(1))
    const call = u.firstCall('create')
    expect(call.agentId).toBe('coder')
    expect(call.body.name).toBe('hotfix')
    expect(call.body.scope).toBe('workspace')
    expect(call.body.content).toBe('---\ndescription: "Run the hotfix flow"\n---\nFix $1 now')
    await waitFor(() => expect(u.byTestId('command-editor-save')).toBeNull())
  })

  test('the optional fields travel in fixed key order', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))
    fillNewCommand(u, 'deep')
    u.setValue('command-editor-model', 'gpt-5')
    u.clickSegment('command-allow-shell', tr('common.yes'))
    u.clickSegment('command-allow-absolute', tr('common.no'))

    fireEvent.click(u.requireTestId('command-editor-save'))
    await waitFor(() => expect(u.callsOf('create').length).toBe(1))
    const content = u.firstCall('create').body.content as string
    expect(content).toBe(
      [
        '---',
        'description: "Run the hotfix flow"',
        'model: "gpt-5"',
        'allow_shell: true',
        'allow_absolute_files: false',
        '---',
        'Fix $1 now',
      ].join('\n'),
    )
  })

  test('create states the workspace destination and always posts that scope', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))
    fillNewCommand(u)

    const destination = u.requireTestId('command-editor-destination')
    expect(destination.textContent).toContain(WS_DIR)
    expect(destination.textContent).toContain(
      tr('settings.agentPage.commands.editor.destinationHint'),
    )
    expect(u.byTestId('command-editor-scope')).toBeNull()

    fireEvent.click(u.requireTestId('command-editor-save'))
    await waitFor(() => expect(u.callsOf('create').length).toBe(1))
    expect(u.firstCall('create').body.scope).toBe('workspace')
  })

  test('the two extra name rules the server enforces block the save inline', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))
    u.setValue('command-editor-description', 'Run the hotfix flow')
    u.setValue('command-editor-template', 'Fix $1 now')

    u.setValue('command-editor-name', 'review.md')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)
    expect(u.container.textContent).toContain(
      tr('settings.agentPage.commands.editor.nameReservedExt'),
    )

    u.setValue('command-editor-name', 'a'.repeat(65))
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)
    expect(u.container.textContent).toContain(
      tr('settings.agentPage.commands.editor.nameTooLong', { max: 64 }),
    )

    u.setValue('command-editor-name', 'a'.repeat(64))
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(false)
    expect(u.callsOf('create').length).toBe(0)
  })

  test('a rejected create shows the server message and keeps the draft', async () => {
    const u = setup({ failCreate: true })
    await waitFor(() => expect(u.byTestId('commands-list')).toBeTruthy())
    fireEvent.click(u.requireTestId('commands-new'))
    fillNewCommand(u)
    fireEvent.click(u.requireTestId('command-editor-save'))

    await waitFor(() => expect(u.byTestId('command-editor-error')).toBeTruthy())
    expect(u.byTestId('command-editor-save')).toBeTruthy()
    expect(u.requireTestId('command-editor-error').textContent).toContain(
      'command name already exists',
    )
    expect(u.field<HTMLTextAreaElement>('command-editor-template').value).toBe('Fix $1 now')
  })
})

describe('AgentCommandEditorDialog — edit (T-F6)', () => {
  test('loads the file and parses every frontmatter field back into the form', async () => {
    const u = setup()
    await openEditor(u, 'deploy')

    await waitFor(() => expect(u.callsOf('detail').length).toBe(1))
    expect(u.callsOf('detail')[0]).toEqual({ kind: 'detail', agentId: 'coder', name: 'deploy' })

    await waitFor(() => expect(u.field<HTMLInputElement>('command-editor-model').value).toBe(''))
    await waitFor(() =>
      expect(u.field<HTMLInputElement>('command-editor-model').value).toBe('gpt-5'),
    )
    expect(u.field<HTMLInputElement>('command-editor-description').value).toBe(
      'Deploy the current branch',
    )
    expect(u.field<HTMLTextAreaElement>('command-editor-template').value).toBe(
      'Ship $1 from @src/main.ts',
    )
    expect(u.selectedSegment('command-allow-shell')).toBe(tr('common.yes'))
    expect(u.selectedSegment('command-allow-absolute')).toBe(
      tr('settings.agentPage.commands.editor.inherit'),
    )
    expect(u.byTestId('command-editor-name')).toBeNull()
  })

  test('editing the template re-serializes the untouched keys and PUTs them', async () => {
    const u = setup()
    await openEditor(u, 'deploy')
    await waitFor(() =>
      expect(u.field<HTMLTextAreaElement>('command-editor-template').value).toBe(
        'Ship $1 from @src/main.ts',
      ),
    )

    u.setValue('command-editor-template', 'Ship $1 to staging')
    fireEvent.click(u.requireTestId('command-editor-save'))

    await waitFor(() => expect(u.callsOf('update').length).toBe(1))
    const call = u.firstCall('update')
    expect(call.name).toBe('deploy')
    expect(call.body.content).toBe(
      [
        '---',
        'description: "Deploy the current branch"',
        'model: "gpt-5"',
        'allow_shell: true',
        '---',
        'Ship $1 to staging',
      ].join('\n'),
    )
    await waitFor(() => expect(u.byTestId('command-editor-save')).toBeNull())
  })

  test('switching absolute files to "yes" writes the key explicitly', async () => {
    const u = setup()
    await openEditor(u, 'deploy')
    await waitFor(() =>
      expect(u.field<HTMLInputElement>('command-editor-model').value).toBe('gpt-5'),
    )

    u.clickSegment('command-allow-absolute', tr('common.yes'))
    fireEvent.click(u.requireTestId('command-editor-save'))

    await waitFor(() => expect(u.callsOf('update').length).toBe(1))
    const content = u.firstCall('update').body.content
    expect(content).toContain('allow_absolute_files: true')
  })

  test('a file without frontmatter opens an empty form over the whole text', async () => {
    const u = setup({ detail: 'no fences here\njust a template' })
    await openEditor(u, 'deploy')
    await waitFor(() =>
      expect(u.field<HTMLTextAreaElement>('command-editor-template').value).toBe(
        'no fences here\njust a template',
      ),
    )
    expect(u.field<HTMLInputElement>('command-editor-description').value).toBe('')
    expect(u.requireTestId('command-editor-save').hasAttribute('disabled')).toBe(true)
  })
})

describe('AgentCommandEditorDialog — view (config / directory)', () => {
  test('a config command opens read-only and fires NO detail request', async () => {
    const u = setup()
    await openEditor(u, 'review')

    expect(u.callsOf('detail').length).toBe(0)
    expect(u.byTestId('command-editor-readonly')).toBeTruthy()
    expect(u.byTestId('command-editor-save')).toBeNull()
    expect(u.field<HTMLInputElement>('command-editor-description').value).toBe(
      'Deploy the current branch',
    )
    expect(u.field<HTMLInputElement>('command-editor-agent').value).toBe('fixer')
    expect(u.field<HTMLInputElement>('command-editor-model').value).toBe('claude-opus')
    expect((u.field<HTMLInputElement>('command-editor-model') as HTMLInputElement).disabled).toBe(
      true,
    )
    expect(u.field<HTMLTextAreaElement>('command-editor-template').readOnly).toBe(true)
    expect(u.byTestId('command-editor-destination')).toBeNull()
  })
})

describe('AgentCommandsSection — delete', () => {
  test('delete is confirmed inline and refreshes the list', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('command-row-deploy')).toBeTruthy())

    fireEvent.click(u.requireTestId('command-remove-deploy'))
    expect(u.byTestId('command-remove-confirm-deploy')).toBeTruthy()
    expect(u.callsOf('remove').length).toBe(0)

    fireEvent.click(u.requireTestId('command-remove-yes-deploy'))
    await waitFor(() => expect(u.callsOf('remove').length).toBe(1))
    expect(u.callsOf('remove')[0]).toEqual({ kind: 'remove', agentId: 'coder', name: 'deploy' })
    await waitFor(() => expect(u.byTestId('commands-notice')).toBeTruthy())
    expect(u.requireTestId('commands-notice').textContent).toContain(
      tr('settings.agentPage.commands.removed', { name: 'deploy' }),
    )
  })

  test('canceling the confirmation sends nothing', async () => {
    const u = setup()
    await waitFor(() => expect(u.byTestId('command-row-deploy')).toBeTruthy())
    fireEvent.click(u.requireTestId('command-remove-deploy'))
    const cancel = Array.from(u.container.querySelectorAll('button')).find(
      (button) => button.textContent?.trim() === tr('common.cancel'),
    )
    expect(cancel).toBeTruthy()
    fireEvent.click(cancel as HTMLElement)
    expect(u.byTestId('command-remove-confirm-deploy')).toBeNull()
    expect(u.callsOf('remove').length).toBe(0)
  })
})
describe('AgentCommandsSection — cache invalidation', () => {
  test('a mutation re-issues the list GET (the file on disk is the truth)', async () => {
    const listCalls: number[] = []
    const { api, calls } = makeApi()
    const counting = {
      ...api,
      agentCommands: async () => {
        listCalls.push(Date.now())
        return RESPONSE
      },
    } as unknown as ApiClient

    const state = makeState([AGENT])
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const utils = render(
      <QueryClientProvider client={queryClient}>
        <SettingsProvider settingsState={state} api={counting}>
          <MemoryRouter initialEntries={['/agents/coder/commands']}>
            <AgentCommandsSection agent={AGENT} agentId="coder" />
          </MemoryRouter>
        </SettingsProvider>
      </QueryClientProvider>,
    )
    const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)
    await waitFor(() => expect(byTestId('commands-list')).toBeTruthy())
    expect(listCalls.length).toBe(1)

    fireEvent.click(byTestId('command-remove-deploy') as HTMLElement)
    fireEvent.click(byTestId('command-remove-yes-deploy') as HTMLElement)
    await waitFor(() => expect(calls.filter((c) => c.kind === 'remove').length).toBe(1))
    await waitFor(() => expect(listCalls.length).toBe(2))
  })
})
