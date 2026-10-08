import '../../../test/setup'
import { afterEach, describe, expect, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import '../../../test/i18n'
import { AuthContext, type AuthContextValue } from '../../../contexts/AuthContext'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type {
  MCPInventoryResponse,
  MCPLayerName,
  MCPRawFileResponse,
  MCPValidateResponse,
} from '../../../lib/mcpTypes'
import type { EditableAgentConfig } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import { ApiError } from '../../../services/http/errors'
import { type Write, makeState, tr } from '../../../test/agentsHarness'
import { AgentMCPSection } from './AgentMCPSection'

/**
 * The raw mcp.json editor of the MCP tab (the Commands tab counterpart is
 * `AgentCommandsSection.editor.test.tsx`): same harness (AuthContext recording
 * fake + SettingsProvider + makeState), driven against the raw/validate/save
 * seams the editor adds on top of the inventory and toggle ones.
 *
 * The fake keeps a FILES store standing in for disk: mcpRaw reads it, PUT
 * writes it (so the post-save invalidation refetch observes the new bytes,
 * exactly like the real route pair). What a write must send, what a 422 must
 * NOT do (touch the draft) and which warnings may block it (none) are the
 * contracts pinned here.
 *
 * `screen` is unusable in this environment (agentsHarness header): all
 * queries come from the render result.
 */

afterEach(() => {
  // @testing-library/react does not auto-cleanup under bun.
  cleanup()
})

const INVENTORY: MCPInventoryResponse = {
  agent_id: 'coder',
  layers: [
    { layer: 'global', path: '/home/u/.lele/mcp.json', exists: true },
    {
      layer: 'agent',
      path: '/home/u/.lele/workspace-coder/mcp.json',
      exists: false,
      aliased_with: ['project'],
    },
    // No `path` ⇔ disabled layer: its Edit button must be disabled.
    { layer: 'project', exists: false },
  ],
  servers: [
    {
      name: 'github',
      layer: 'global',
      path: '/home/u/.lele/mcp.json',
      effective: 'enabled',
      defines: true,
      server: { command: 'npx ${TOOLS_DIR}/mcp-github' },
    },
  ],
}

/** The literal bytes the fake serves for the global layer (NEVER expanded). */
const RAW_GLOBAL = `{
  "mcpServers": {
    "github": {
      "command": "npx \${TOOLS_DIR}/mcp-github",
      "env": { "GITHUB_TOKEN": "\${GITHUB_TOKEN}" }
    }
  }
}`

const RAW: Record<MCPLayerName, MCPRawFileResponse> = {
  global: {
    layer: 'global',
    path: '/home/u/.lele/mcp.json',
    exists: true,
    content: RAW_GLOBAL,
    aliased_with: [],
  },
  agent: {
    layer: 'agent',
    path: '/home/u/.lele/workspace-coder/mcp.json',
    exists: false,
    content: '',
    aliased_with: ['project'],
  },
  project: { layer: 'project', path: '/home/u/project/mcp.json', exists: false, content: '' },
}

/** One recorded call against the MCP ApiClient seam. */
type Call =
  | { kind: 'inventory' }
  | { kind: 'raw'; agentId: string; layer: MCPLayerName }
  | { kind: 'putRaw'; agentId: string; layer: MCPLayerName; body: { content: string } }
  | { kind: 'validate'; agentId: string; body: { layer: MCPLayerName; content: string } }

type Options = {
  /** Bytes a save returns (server side, may differ from what was typed). */
  putRaw?: (layer: MCPLayerName, body: { content: string }) => string
  /** `warnings[]` the save response carries (200, per-entry problems). */
  putWarnings?: string[]
  /** Thrown by every save attempt (ApiError → the editor's error banner). */
  putError?: () => never
  /** Response of each validate call, in order (last one repeats). */
  validate?: MCPValidateResponse[]
  /** Inventory fields to override (e.g. `servers: []` + `warnings` for the empty-panel tests). */
  inventory?: Partial<MCPInventoryResponse>
}

function setup(options: Options = {}) {
  const agent: EditableAgentConfig = { id: 'coder' }
  const writes: Write[] = []
  const state = makeState([agent], { writes })
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })

  // The fake "disk": PUT mutates it, GET reads it — so the post-save
  // invalidation refetch of BOTH the raw query and the inventory observes
  // what was written, like the real backend.
  const files: Partial<Record<MCPLayerName, MCPRawFileResponse>> = {
    global: { ...RAW.global },
    agent: { ...RAW.agent },
  }

  const calls: Call[] = []
  let validateCount = 0
  let putCount = 0

  // The inventory the fake serves; `options.inventory` overrides fields of
  // it (servers/warnings/layers) without duplicating the fixture.
  const baseInventory: MCPInventoryResponse = { ...INVENTORY, ...(options.inventory ?? {}) }

  const inventory = (): MCPInventoryResponse => ({
    ...baseInventory,
    layers: (baseInventory.layers ?? []).map((layer) => {
      const file = files[layer.layer as MCPLayerName]
      return file ? { ...layer, exists: file.exists } : layer
    }),
  })

  const authApi = {
    getToken: () => 'token-test',
    mcpInventory: async () => {
      calls.push({ kind: 'inventory' })
      return inventory()
    },
    mcpRaw: async (agentId: string, layer: MCPLayerName) => {
      calls.push({ kind: 'raw', agentId, layer })
      const file = files[layer]
      if (!file) throw new ApiError('unknown layer', 400, 'invalid_layer')
      return { ...file }
    },
    mcpPutRaw: async (agentId: string, layer: MCPLayerName, body: { content: string }) => {
      calls.push({ kind: 'putRaw', agentId, layer, body })
      putCount++
      if (options.putError) options.putError()
      const saved = options.putRaw ? options.putRaw(layer, body) : body.content
      files[layer] = { ...RAW[layer], exists: true, content: saved }
      return { ...files[layer], warnings: options.putWarnings }
    },
    mcpValidate: async (agentId: string, body: { layer: MCPLayerName; content: string }) => {
      calls.push({ kind: 'validate', agentId, body })
      const responses = options.validate ?? [{ valid: true }]
      const response = responses[Math.min(validateCount, responses.length - 1)]
      validateCount++
      return { ...response }
    },
  } as unknown as ApiClient

  const authValue: AuthContextValue = {
    api: authApi,
    apiUrl: 'http://127.0.0.1:18793',
    session: null,
    setApiUrl: () => {},
    persistSession: () => {},
    handleAuth: async () => {
      throw new Error('unused')
    },
    ensureSession: async () => null,
    isLoading: false,
  }

  const settingsApi = {
    models: async () => ({ models: [], model_groups: [] }),
    skills: async () => ({ skills: [] }),
    tools: async () => ({ tools: [] }),
  } as unknown as ApiClient

  const utils = render(
    <AuthContext.Provider value={authValue}>
      <QueryClientProvider client={queryClient}>
        <SettingsProvider settingsState={state} api={settingsApi}>
          <MemoryRouter initialEntries={['/agents/coder/mcp']}>
            <AgentMCPSection agentId="coder" />
          </MemoryRouter>
        </SettingsProvider>
      </QueryClientProvider>
    </AuthContext.Provider>,
  )

  const byTestId = (id: string) => utils.container.querySelector(`[data-testid="${id}"]`)

  /** Query a testid, failing loudly (not silently on null) when missing. */
  const need = (id: string) => {
    const el = byTestId(id)
    if (!el) throw new Error(`expected element with data-testid="${id}"`)
    return el
  }

  const clickTestId = (id: string) => {
    fireEvent.click(need(id))
  }

  const callsOf = <K extends Call['kind']>(kind: K) =>
    calls.filter((call): call is Extract<Call, { kind: K }> => call.kind === kind)
  const puts = () =>
    calls.filter((call): call is Extract<Call, { kind: 'putRaw' }> => call.kind === 'putRaw')
  const validates = () =>
    calls.filter((call): call is Extract<Call, { kind: 'validate' }> => call.kind === 'validate')

  const setValue = (id: string, value: string) => {
    fireEvent.change(need(id), { target: { value } })
  }

  /** Wait until the editor is mounted AND its textarea shows the file bytes. */
  const openEditor = async (layer: MCPLayerName) => {
    clickTestId(`mcp-edit-${layer}`)
    await waitFor(() => expect(byTestId('mcp-editor-content')).toBeTruthy())
  }

  return {
    ...utils,
    byTestId,
    need,
    clickTestId,
    calls,
    callsOf,
    puts,
    validates,
    setValue,
    openEditor,
    putCount: () => putCount,
    writes,
  }
}

/** Wait until the inventory resolves and the panel is on screen. */
async function ready(utils: ReturnType<typeof setup>) {
  await waitFor(() => {
    expect(utils.byTestId('mcp-layers')).toBeTruthy()
  })
  return utils
}

describe('AgentMCPSection — raw editor: open & pre-fill', () => {
  test('opens on the layer row, fetches the raw route and prefills literal bytes', async () => {
    const u = await ready(setup())
    // Nothing raw is fetched while the editor is closed.
    expect(u.callsOf('raw')).toHaveLength(0)
    // The disabled layer (no path) never offers the editor.
    expect((u.need('mcp-edit-project') as HTMLButtonElement).disabled).toBe(true)

    u.clickTestId('mcp-edit-global')
    await waitFor(() => expect(u.callsOf('raw')).toHaveLength(1))
    expect(u.callsOf('raw')[0]).toEqual({ kind: 'raw', agentId: 'coder', layer: 'global' })

    await waitFor(() =>
      expect((u.need('mcp-editor-content') as HTMLTextAreaElement).value).toBe(RAW_GLOBAL),
    )
    const textarea = u.need('mcp-editor-content') as HTMLTextAreaElement
    expect(textarea.value).toBe(RAW_GLOBAL)
    // `${VAR}` stays literal — the editor shows file bytes, never expanded.
    expect(textarea.value).toContain('${TOOLS_DIR}')
    expect(textarea.value).toContain('${GITHUB_TOKEN}')
    expect(u.container.textContent).not.toContain('expanded')
    // visible path line
    expect(u.need('mcp-editor-path').textContent).toBe('/home/u/.lele/mcp.json')
    // no aliasing warning for this layer, and no create-hint (file exists)
    expect(u.byTestId('mcp-editor-aliased')).toBeNull()
    expect(u.byTestId('mcp-editor-create-hint')).toBeNull()
  })
})

describe('AgentMCPSection — raw editor: missing file', () => {
  test('shows the create-hint and saves the payload byte-identically, invalidating the inventory', async () => {
    const u = await ready(
      setup({
        // the fake server "normalizes" the write with a trailing newline
        putRaw: (_layer, body) => `${body.content}\n`,
      }),
    )
    await u.openEditor('agent')

    // exists:false → the hint that saving creates the file.
    expect(u.need('mcp-editor-create-hint').textContent).toBe(tr('mcp.editor.createHint'))
    expect(u.need('mcp-editor-path').textContent).toBe('/home/u/.lele/workspace-coder/mcp.json')

    const payload = '{"mcpServers":{"docs":{"command":"uvx mcp-docs"}}}'
    u.setValue('mcp-editor-content', payload)
    u.clickTestId('mcp-editor-save')

    // The PUT body is the typed bytes, exactly — no serialization of our own.
    await waitFor(() => expect(u.puts()).toHaveLength(1))
    expect(u.puts()[0].layer).toBe('agent')
    expect(u.puts()[0].body.content).toBe(payload)
    expect(u.writes).toHaveLength(0) // file-backed tab: no config draft write

    // The save response `content` is what is on disk: the textarea shows it
    // (the fake "normalizes" with a trailing newline, like a formatter would).
    await waitFor(() =>
      expect((u.need('mcp-editor-content') as HTMLTextAreaElement).value).toBe(`${payload}\n`),
    )
    expect(u.need('mcp-editor-saved')).toBeTruthy()

    // The hooks invalidated the inventory AND this layer's raw query: both
    // re-fetched, the layer row now reports the file as existing
    // (exists:false → exists:true) and the raw query serves the saved bytes.
    await waitFor(() => expect(u.callsOf('inventory').length).toBeGreaterThanOrEqual(2))
    await waitFor(() => expect(u.callsOf('raw').length).toBeGreaterThanOrEqual(2))
    await waitFor(() =>
      expect(u.need('mcp-layer-exists-agent').textContent).toBe(tr('mcp.layers.exists')),
    )
    await waitFor(() => expect(u.byTestId('mcp-editor-create-hint')).toBeNull())

    // …and the editor is clean again: closing must NOT warn.
    u.clickTestId('mcp-editor-close')
    expect(u.byTestId('mcp-editor-unsaved')).toBeNull()
    expect(u.byTestId('mcp-editor')).toBeNull()
  })
})

describe('AgentMCPSection — raw editor: validate', () => {
  test('shows a fatal error and warnings distinctly; Save stays usable with warnings only', async () => {
    const u = await ready(
      setup({
        validate: [
          {
            valid: false,
            error: 'entry "broken": missing "command" field',
            warnings: ['entry "wiki": unknown field "typo"'],
          },
          { valid: true, warnings: ['entry "wiki": unknown field "typo"'] },
        ],
        putWarnings: ['entry "wiki": unknown field "typo"'],
      }),
    )
    await u.openEditor('global')

    u.clickTestId('mcp-editor-validate')
    await waitFor(() => expect(u.byTestId('mcp-editor-validate-fatal')).toBeTruthy())
    // fatal and warnings are DISTINCT blocks, both from the same response
    expect(u.need('mcp-editor-validate-fatal').textContent).toContain(
      'entry "broken": missing "command" field',
    )
    expect(u.need('mcp-editor-validate-warnings').textContent).toContain(
      'entry "wiki": unknown field "typo"',
    )
    expect(u.byTestId('mcp-editor-validate-ok')).toBeNull()
    // validate is reachable even for a broken document — and validates the DRAFT
    expect(u.validates()[0].body).toEqual({
      layer: 'global',
      content: RAW_GLOBAL,
    })
    // warnings are NOT fatal: Save is enabled right now
    expect((u.need('mcp-editor-save') as HTMLButtonElement).disabled).toBe(false)

    // warnings only → no fatal block, Save still enabled and it WORKS
    u.clickTestId('mcp-editor-validate')
    await waitFor(() => expect(u.byTestId('mcp-editor-validate-ok')).toBeTruthy())
    expect(u.byTestId('mcp-editor-validate-fatal')).toBeNull()
    expect(u.need('mcp-editor-validate-warnings-0').textContent).toContain(
      'entry "wiki": unknown field "typo"',
    )
    expect((u.need('mcp-editor-save') as HTMLButtonElement).disabled).toBe(false)

    u.clickTestId('mcp-editor-save')
    await waitFor(() => expect(u.puts()).toHaveLength(1))
    expect(u.puts()[0].body.content).toBe(RAW_GLOBAL)
    // post-save warnings are shown AFTER the successful write
    await waitFor(() => expect(u.need('mcp-editor-save-warnings-0')).toBeTruthy())
    expect(u.need('mcp-editor-saved')).toBeTruthy()
  })
})

describe('AgentMCPSection — raw editor: save errors', () => {
  test('a 422 config_invalid renders the message and does NOT clear the dirty state', async () => {
    const u = await ready(
      setup({
        putError: () => {
          throw new ApiError('servers: broken: missing "command" field', 422, 'config_invalid')
        },
      }),
    )
    await u.openEditor('global')

    const draft = `${RAW_GLOBAL}\n// my edit`
    u.setValue('mcp-editor-content', draft)
    u.clickTestId('mcp-editor-save')

    await waitFor(() => expect(u.byTestId('mcp-editor-save-error')).toBeTruthy())
    expect(u.need('mcp-editor-save-error').textContent).toContain(
      'servers: broken: missing "command" field',
    )
    expect(u.byTestId('mcp-editor-save-error-code')?.textContent).toBe('config_invalid')
    expect(u.byTestId('mcp-editor-saved')).toBeNull()

    // The file was NOT written and the draft is still dirty: the bytes are in
    // the textarea and closing still demands an explicit discard.
    expect((u.need('mcp-editor-content') as HTMLTextAreaElement).value).toBe(draft)
    u.clickTestId('mcp-editor-close')
    expect(u.need('mcp-editor-unsaved').textContent).toContain(tr('mcp.editor.unsavedClose'))
    expect(u.byTestId('mcp-editor')).toBeTruthy() // still open
    expect(u.puts()).toHaveLength(1)
  })

  test('403 mcp_path_not_allowed and 400 mcp_layer_unavailable/body_invalid render their own messages', async () => {
    let attempt = 0
    const u = await ready(
      setup({
        putError: () => {
          attempt++
          if (attempt === 1) {
            throw new ApiError('path not allowed: /etc/mcp.json', 403, 'mcp_path_not_allowed')
          }
          if (attempt === 2) {
            throw new ApiError(
              'project layer unavailable: no workspace root',
              400,
              'mcp_layer_unavailable',
            )
          }
          throw new ApiError('body: expected object', 400, 'body_invalid')
        },
      }),
    )
    await u.openEditor('global')

    // 403: the server's message + its code, never a generic toast
    u.clickTestId('mcp-editor-save')
    await waitFor(() => expect(u.byTestId('mcp-editor-save-error')).toBeTruthy())
    expect(u.need('mcp-editor-save-error').textContent).toContain('path not allowed: /etc/mcp.json')
    expect(u.byTestId('mcp-editor-save-error-code')?.textContent).toBe('mcp_path_not_allowed')

    // 400 mcp_layer_unavailable replaces it with ITS message
    u.clickTestId('mcp-editor-save')
    await waitFor(() =>
      expect(u.need('mcp-editor-save-error').textContent).toContain(
        'project layer unavailable: no workspace root',
      ),
    )
    expect(u.byTestId('mcp-editor-save-error-code')?.textContent).toBe('mcp_layer_unavailable')

    // 400 body_invalid likewise
    u.clickTestId('mcp-editor-save')
    await waitFor(() =>
      expect(u.need('mcp-editor-save-error').textContent).toContain('body: expected object'),
    )
    expect(u.byTestId('mcp-editor-save-error-code')?.textContent).toBe('body_invalid')
    expect(u.puts()).toHaveLength(3)
    // none of the three wrote anything: the fake only mutates `files` on success
    expect(u.callsOf('inventory').length).toBe(1)
  })
})

describe('AgentMCPSection — raw editor: aliasing', () => {
  test('the aliasing warning appears when aliased_with is non-empty', async () => {
    const u = await ready(setup())
    await u.openEditor('global')
    // global has no aliases → no warning
    expect(u.byTestId('mcp-editor-aliased')).toBeNull()

    // agent aliases `project` (same file on disk) → prominent warning naming it
    u.clickTestId('mcp-edit-agent')
    await waitFor(() => expect(u.byTestId('mcp-editor-aliased')).toBeTruthy())
    expect(u.need('mcp-editor-aliased').textContent).toBe(
      tr('mcp.editor.aliased', { layers: tr('mcp.layer.project') }),
    )
    // switching to a clean draft did not demand a discard
    expect(u.byTestId('mcp-editor-unsaved')).toBeNull()
    await waitFor(() =>
      expect((u.need('mcp-editor-content') as HTMLTextAreaElement).value).toBe(''),
    )
  })
})

describe('AgentMCPSection — raw editor: dirty tracking', () => {
  test('closing with unsaved changes surfaces the inline warning and the draft is not sent', async () => {
    const u = await ready(setup())
    await u.openEditor('global')

    const draft = `${RAW_GLOBAL}\n// work in progress`
    u.setValue('mcp-editor-content', draft)

    u.clickTestId('mcp-editor-close')
    // inline warning (never a modal) — and nothing was written or thrown away
    await waitFor(() => expect(u.byTestId('mcp-editor-unsaved')).toBeTruthy())
    expect(u.need('mcp-editor-unsaved').textContent).toContain(tr('mcp.editor.unsavedTitle'))
    expect(u.byTestId('mcp-editor')).toBeTruthy()
    expect(u.puts()).toHaveLength(0)
    expect((u.need('mcp-editor-content') as HTMLTextAreaElement).value).toBe(draft)

    // "Keep editing" dismisses the warning, editor stays with the draft
    u.clickTestId('mcp-editor-unsaved-cancel')
    expect(u.byTestId('mcp-editor-unsaved')).toBeNull()
    expect(u.puts()).toHaveLength(0)

    // closing again still warns — only the explicit discard closes it
    u.clickTestId('mcp-editor-close')
    await waitFor(() => expect(u.byTestId('mcp-editor-unsaved')).toBeTruthy())
    u.clickTestId('mcp-editor-unsaved-confirm')
    expect(u.byTestId('mcp-editor')).toBeNull()
    expect(u.puts()).toHaveLength(0)

    // the discarded draft is gone for good: reopening shows the file bytes
    u.clickTestId('mcp-edit-global')
    await waitFor(() => expect(u.byTestId('mcp-editor-content')).toBeTruthy())
    await waitFor(() =>
      expect((u.need('mcp-editor-content') as HTMLTextAreaElement).value).toBe(RAW_GLOBAL),
    )
  })

  test('switching layers with unsaved changes warns and only switches after the discard', async () => {
    const u = await ready(setup())
    await u.openEditor('global')
    u.setValue('mcp-editor-content', `${RAW_GLOBAL}\n// wip`)

    u.clickTestId('mcp-edit-agent')
    await waitFor(() => expect(u.byTestId('mcp-editor-unsaved')).toBeTruthy())
    expect(u.need('mcp-editor-unsaved').textContent).toContain(tr('mcp.editor.unsavedSwitch'))
    // still on the dirty editor — the switch was held back
    expect(u.need('mcp-editor-title').textContent).toContain(tr('mcp.layer.global'))
    expect(u.callsOf('raw').map((call) => call.layer)).toEqual(['global'])

    u.clickTestId('mcp-editor-unsaved-cancel')
    expect(u.byTestId('mcp-editor-unsaved')).toBeNull()
    expect(u.need('mcp-editor-title').textContent).toContain(tr('mcp.layer.global'))

    u.clickTestId('mcp-edit-agent')
    await waitFor(() => expect(u.byTestId('mcp-editor-unsaved')).toBeTruthy())
    u.clickTestId('mcp-editor-unsaved-confirm')
    await waitFor(() => expect(u.callsOf('raw').map((call) => call.layer)).toContain('agent'))
    expect(u.need('mcp-editor-title').textContent).toContain(tr('mcp.layer.agent'))
    expect(u.puts()).toHaveLength(0)
  })
})

describe('AgentMCPSection — raw editor: reachable from the empty panel', () => {
  test('zero servers still render warnings, the layers block and a working Edit affordance', async () => {
    const u = await ready(
      setup({
        inventory: {
          servers: [],
          warnings: ['mcp.json:12: invalid character "{" after top-level value'],
          // exactly two layers, per the empty-panel contract
          layers: [
            { layer: 'global', path: '/home/u/.lele/mcp.json', exists: true },
            {
              layer: 'agent',
              path: '/home/u/.lele/workspace-coder/mcp.json',
              exists: false,
              aliased_with: ['project'],
            },
          ],
        },
      }),
    )

    // The empty state renders — as a ROW inside the panel, never a replacement:
    expect(u.need('mcp-empty').textContent).toContain(tr('mcp.empty.title'))
    expect(u.need('mcp-empty').textContent).toContain(tr('mcp.empty.desc'))
    expect(u.byTestId('mcp-rows')).toBeNull()

    // …the broken-config diagnostic is NOT swallowed by the empty state:
    expect(u.need('mcp-warnings').textContent).toContain(
      'mcp.json:12: invalid character "{" after top-level value',
    )

    // …and the layers block survives, with a clickable Edit affordance:
    expect(u.need('mcp-layers')).toBeTruthy()
    expect((u.need('mcp-edit-global') as HTMLButtonElement).disabled).toBe(false)

    // The raw route is reachable from the empty panel: click Edit → GET raw fires.
    expect(u.callsOf('raw')).toHaveLength(0)
    u.clickTestId('mcp-edit-global')
    await waitFor(() => expect(u.callsOf('raw')).toHaveLength(1))
    expect(u.callsOf('raw')[0]).toEqual({ kind: 'raw', agentId: 'coder', layer: 'global' })
    await waitFor(() => expect(u.byTestId('mcp-editor-content')).toBeTruthy())
  })

  test('a missing file still offers Edit from the empty panel and shows the create-hint', async () => {
    const u = await ready(setup({ inventory: { servers: [] } }))

    expect(u.need('mcp-empty').textContent).toContain(tr('mcp.empty.title'))
    // the layer row exists:false but its Edit button is enabled (path present)
    expect((u.need('mcp-edit-agent') as HTMLButtonElement).disabled).toBe(false)

    await u.openEditor('agent')
    await waitFor(() =>
      expect(u.need('mcp-editor-create-hint').textContent).toBe(tr('mcp.editor.createHint')),
    )
    // reachability only — nothing is written in this test
    expect(u.puts()).toHaveLength(0)
  })
})
