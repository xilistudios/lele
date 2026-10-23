/**
 * AddProviderModal — single-screen provider chooser (plan §4 T4).
 *
 * What this suite pins, in order: the two exported constants (1-2, kept
 * verbatim from the pre-refactor wizard suite), the one-screen DOM (3), the
 * static-then-catalog tile grid (4-6), the filter (7), tile selection (8-10),
 * the written payload (11-13), Advanced name/base overrides (14-15), the z.ai
 * base fix (16), the reset-on-open contract (17), `uniqueProviderName` (18),
 * the curated-over-catalog precedence (19-20), the synthesized tile for a
 * detected catalog-only provider (21-22), the secret-mode freeze (23), the
 * Advanced name field's placeholder/sanitizer/dedupe (24-26), the data
 * invariants (27), the disclosure-dependent grid cap (28) and the re-review
 * nits: filter-gated Add (29), id-less catalog entries (30) and the detected-
 * provider chip in the key hint area (31).
 *
 * Harness rules learned the hard way:
 * - Tests boot in Spanish (`src/test/i18n.ts`), so copy is asserted through
 *   `tr(key)` — never as hardcoded prose — or structurally (ids, `aria-pressed`,
 *   `data-provider-type`).
 * - Clicking a tile bumps the modal's SecretInput mount counter (that is how
 *   the native `autoFocus` fires), so every helper RE-QUERIES the DOM; holding a
 *   node reference across a tile click gives you a detached input.
 * - `catalogProviders` is driven by a deferred promise where the test cares
 *   about "before it resolves"; such a promise is always settled before the
 *   test ends so its `then` cannot leak into the next one.
 */

import '../../../test/setup'
import { describe, expect, mock, test } from 'bun:test'
import { fireEvent, render, waitFor } from '@testing-library/react'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../../hooks/useSettingsConfig'
import type {
  CatalogProvider,
  CatalogProvidersResponse,
  EditableConfig,
  EditableNamedProviderConfig,
} from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import i18n from '../../../test/i18n'
import {
  AddProviderModal,
  DEFAULT_API_BASE,
  PROVIDER_TYPES,
  uniqueProviderName,
} from './AddProviderModal'

const tr = (key: string, options?: Record<string, unknown>) => i18n.t(key, options) as string

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

type ProvidersMap = Record<string, EditableNamedProviderConfig>

/** An empty stub as a shipped config carries them — relevant for the dedupe. */
function stubProvider(): EditableNamedProviderConfig {
  return { type: '', api_key: { mode: 'empty', has_env_var: false }, api_base: '' }
}

function makeSettingsState(overrides: Partial<SettingsConfigState> = {}): SettingsConfigState {
  return {
    remoteConfig: null,
    draftConfig: null,
    metadata: null,
    dirtyPaths: new Set(),
    validationErrors: [],
    saveState: 'idle',
    saveError: null,
    updateField: () => {},
    updateSecretField: () => {},
    replaceDraft: () => {},
    reset: () => {},
    validate: async () => true,
    save: async () => true,
    isDirty: false,
    isLoading: false,
    isReloading: false,
    hasErrors: false,
    ...overrides,
  }
}

/**
 * `models` feeds `useAvailableModels` inside `SettingsProvider`;
 * `catalogProviders` feeds the tile grid and is overridden per test.
 */
function makeApi(overrides: Partial<ApiClient> = {}): ApiClient {
  return {
    models: async () => ({ models: [], model_groups: [] }),
    catalogProviders: async () => ({ providers: [] }),
    ...overrides,
  } as unknown as ApiClient
}

const catalogApi = (providers: CatalogProvider[]) =>
  makeApi({ catalogProviders: async () => ({ providers }) })

/** A realistic catalog payload: `type` is the transport hint, NOT the id (C1). */
const XAI_CATALOG: CatalogProvider = {
  id: 'xai',
  name: 'xAI',
  type: 'openai',
  api_base: 'https://api.x.ai/v1',
  model_count: 13,
}

function deferred<T>(): {
  promise: Promise<T>
  resolve: (value: T) => void
  reject: (reason?: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

/** Let queued microtasks (a settled catalog promise) run to completion. */
const flush = () => new Promise((r) => setTimeout(r, 0))

/**
 * Render the modal inside a settings context whose draft can grow and whose
 * open flag can flip, without ever unmounting `AddProviderModal` itself — that
 * is what makes "close and reopen" (17) and "add twice" (13) testable.
 */
function renderModal(opts: { api?: ApiClient; providers?: ProvidersMap; isOpen?: boolean } = {}) {
  const api = opts.api ?? makeApi()
  const onClose = mock(() => {})
  const updateField = mock((_path: string, _value: unknown) => {})
  const parent = { isOpen: opts.isOpen ?? true, providers: opts.providers ?? {} }

  const Host = () => (
    <SettingsProvider
      settingsState={makeSettingsState({
        draftConfig: { providers: parent.providers } as unknown as EditableConfig,
        updateField,
      })}
      api={api}
    >
      <AddProviderModal isOpen={parent.isOpen} onClose={onClose} />
    </SettingsProvider>
  )

  const utils = render(<Host />)
  return {
    ...utils,
    onClose,
    updateField,
    setIsOpen(isOpen: boolean) {
      parent.isOpen = isOpen
      utils.rerender(<Host />)
    },
    setProviders(providers: ProvidersMap) {
      parent.providers = providers
      utils.rerender(<Host />)
    },
  }
}

type Modal = ReturnType<typeof renderModal>

// ---------------------------------------------------------------------------
// Locators (locale-proof: ids, data attributes, aria — plus tr() for copy)
// ---------------------------------------------------------------------------

const tilesIn = (container: HTMLElement) =>
  Array.from(container.querySelectorAll<HTMLButtonElement>('[data-provider-type]'))

const tileType = (tile: HTMLElement) => tile.getAttribute('data-provider-type') ?? ''

const tileByType = (container: HTMLElement, type: string) =>
  container.querySelector<HTMLButtonElement>(`[data-provider-type="${type}"]`)

/** Clickable tile; throws with the type when the grid lost it. */
const tile = (container: HTMLElement, type: string) => {
  const found = tileByType(container, type)
  if (!found) throw new Error(`no tile with data-provider-type="${type}"`)
  return found
}

const pressedTypes = (container: HTMLElement) =>
  tilesIn(container)
    .filter((t) => t.getAttribute('aria-pressed') === 'true')
    .map(tileType)

const one = <T extends HTMLElement>(container: HTMLElement, selector: string) => {
  const found = container.querySelector<T>(selector)
  if (!found) throw new Error(`missing ${selector}`)
  return found
}

/** `#new-provider-api-key` is the literal input AND the env input (SecretInput). */
const keyInput = (container: HTMLElement) =>
  one<HTMLInputElement>(container, '#new-provider-api-key')

const searchInput = (container: HTMLElement) =>
  one<HTMLInputElement>(
    container,
    `input[aria-label="${tr('settings.addProviderModal.searchPlaceholder')}"]`,
  )

const advancedToggle = (container: HTMLElement) =>
  one<HTMLButtonElement>(container, '[aria-controls="new-provider-advanced"]')

/** A button found by its i18n-resolved text — never by hardcoded prose. */
const buttonByText = (container: HTMLElement, text: string) => {
  const found = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(
    (b) => b.textContent?.trim() === text,
  )
  if (!found) throw new Error(`no button labelled "${text}"`)
  return found
}

const addButton = (container: HTMLElement) =>
  buttonByText(container, tr('settings.addProviderModal.add'))

/** The `providers` object handed to `updateField` on the n-th write. */
const written = (modal: Modal, call = 0): ProvidersMap => {
  const calls = modal.updateField.mock.calls as unknown as [string, ProvidersMap][]
  if (calls.length <= call) throw new Error(`updateField called ${calls.length} time(s)`)
  expect(calls[call][0]).toBe('providers')
  return calls[call][1]
}

// ---------------------------------------------------------------------------

describe('AddProviderModal', () => {
  // 1-2. The OpenCode pins, kept verbatim from the pre-refactor suite (A7:
  // these two exports are a contract, not an implementation detail).
  test('offers OpenCode Zen and OpenCode Go in PROVIDER_TYPES', () => {
    const values = PROVIDER_TYPES.map((pt) => pt.value)
    expect(values).toContain('opencode')
    expect(values).toContain('opencode_go')
  })

  test('pre-fills the OpenCode api bases', () => {
    expect(DEFAULT_API_BASE.opencode).toBe('https://opencode.ai/zen/v1')
    expect(DEFAULT_API_BASE.opencode_go).toBe('https://opencode.ai/zen/go/v1')
  })

  test('renders one screen with no wizard chrome', () => {
    const { container } = renderModal()

    // The wizard is gone: no type <select>, no step progressbar, and the
    // deleted step keys can no longer leak into the DOM as raw i18n keys.
    expect(container.querySelectorAll('select')).toHaveLength(0)
    expect(container.querySelector('[role="progressbar"]')).toBeNull()
    expect(container.textContent).not.toContain('stepType')
    expect(container.textContent).not.toContain('stepConfig')
    expect(container.textContent).not.toContain('settings.addProviderModal.back')
    expect(container.textContent).not.toContain('settings.addProviderModal.next')

    // Footer is Cancel + Add only — there is no "Next" left to walk.
    const footer = one<HTMLDivElement>(container, 'div.justify-end')
    expect(footer.querySelectorAll('button')).toHaveLength(2)

    // C2 half 1: the key input is in the DOM the moment the modal opens.
    expect(keyInput(container).value).toBe('')
    expect(pressedTypes(container)).toEqual([])
  })

  test('renders at least the static tiles before the catalog resolves', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const { container } = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })

    // C5: assert the invariant, never a magic tile count.
    expect(tilesIn(container).length).toBeGreaterThanOrEqual(PROVIDER_TYPES.length)
    for (const pt of PROVIDER_TYPES) {
      expect(tileByType(container, pt.value)).not.toBeNull()
    }
    expect(tileByType(container, 'xai')).toBeNull()

    // Always settle the deferred before returning: a dangling `then` would
    // write into the next test's document.
    gate.resolve({ providers: [] })
    await flush()
  })

  test('appends catalog-only providers once the catalog resolves', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const { container } = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })
    const before = tilesIn(container).length

    gate.resolve({ providers: [XAI_CATALOG] })
    await waitFor(() => {
      expect(tileByType(container, 'xai')).not.toBeNull()
    })

    // 24 static + 1 catalog-only (the merge is a set union, C5).
    expect(tilesIn(container)).toHaveLength(before + 1)
    const xai = tile(container, 'xai')
    // C1: the tile is keyed by the catalog ID. Its `type` says "openai" — a
    // tile with data-provider-type="openai" here would be pinning the bug.
    expect(tileType(xai)).toBe('xai')
    expect(tileByType(container, 'openai')?.textContent).toContain('OpenAI')
    expect(xai.textContent).toContain('xAI')
    expect(xai.textContent).toContain(
      `xai · ${tr('settings.addProviderModal.modelsCount', { count: 13 })}`,
    )
  })

  test('a catalog failure leaves the static tiles and stays silent', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const { container } = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })
    const before = tilesIn(container).length

    gate.reject(new Error('offline'))
    await flush()

    expect(tilesIn(container)).toHaveLength(before)
    expect(tileByType(container, 'xai')).toBeNull()
    expect(container.textContent).not.toContain('offline')
    expect(container.querySelector('[role="alert"]')).toBeNull()
    // The empty state is for an empty FILTER, not for a failed fetch.
    expect(container.textContent).not.toContain(tr('settings.addProviderModal.noResults'))
  })

  test('filters tiles by label and type and shows the empty state', () => {
    const { container } = renderModal()
    const total = tilesIn(container).length
    const input = searchInput(container)

    // Matches both the label ("Groq") and the type ("groq").
    fireEvent.change(input, { target: { value: 'groq' } })
    expect(tilesIn(container).map(tileType)).toEqual(['groq'])

    // Matches the label only — proves the filter is not type-only.
    fireEvent.change(input, { target: { value: 'compatible' } })
    expect(tilesIn(container).map(tileType)).toEqual(['custom'])

    fireEvent.change(input, { target: { value: 'zzz' } })
    expect(tilesIn(container)).toHaveLength(0)
    expect(container.textContent).toContain(tr('settings.addProviderModal.noResults'))

    fireEvent.change(input, { target: { value: '' } })
    expect(tilesIn(container)).toHaveLength(total)
  })

  test('a tile is selected with aria-pressed and needs no api key to enable Add', async () => {
    const modal = renderModal({ api: catalogApi([XAI_CATALOG]) })
    const { container } = modal
    await waitFor(() => {
      expect(tileByType(container, 'xai')).not.toBeNull()
    })

    fireEvent.click(tile(container, 'xai'))
    expect(tile(container, 'xai').getAttribute('aria-pressed')).toBe('true')
    expect(pressedTypes(container)).toEqual(['xai'])

    // C3: the key never gates Add — keyless providers are first class.
    expect(keyInput(container).value).toBe('')
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
    // C1 again, this time on the wire: the written type is the catalog id.
    expect(written(modal)).toEqual({
      xai: {
        type: 'xai',
        api_key: { mode: 'empty', has_env_var: false },
        api_base: 'https://api.x.ai/v1',
        proxy: undefined,
        web_search: false,
      },
    })
    expect(modal.onClose).toHaveBeenCalledTimes(1)
  })

  test('the custom tile reveals a free-text type and gates Add', () => {
    const modal = renderModal()
    const { container } = modal

    fireEvent.click(tile(container, 'custom'))
    const customType = one<HTMLInputElement>(container, '#new-provider-custom-type')
    // spec §2.7: the custom tile has no type until the user writes one.
    expect(addButton(container).disabled).toBe(true)

    fireEvent.change(customType, { target: { value: 'my-gw' } })
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
    // The name comes from the typed type, not from the "OpenAI Compatible" brand.
    expect(written(modal)).toEqual({
      'my-gw': {
        type: 'my-gw',
        api_key: { mode: 'empty', has_env_var: false },
        api_base: '',
        proxy: undefined,
        web_search: false,
      },
    })
  })

  test('typing a known key prefix preselects the tile', () => {
    const { container } = renderModal()

    fireEvent.change(keyInput(container), { target: { value: 'gsk_abc' } })
    expect(pressedTypes(container)).toEqual(['groq'])

    // C8: mirror, not sticky — an ambiguous key selects nothing again.
    fireEvent.change(keyInput(container), { target: { value: 'sk-abc' } })
    expect(pressedTypes(container)).toEqual([])

    // A manual click freezes the choice (userPicked). The tile click remounts
    // the key field, so it must be re-queried before typing into it.
    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.change(keyInput(container), { target: { value: 'gsk_abc' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])
    expect(tileByType(container, 'groq')?.getAttribute('aria-pressed')).toBe('false')
  })

  test('adds the provider with the normalised secret and default api_base', () => {
    const modal = renderModal()
    const { container } = modal

    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-xyz' } })
    fireEvent.click(addButton(container))

    expect(modal.updateField.mock.calls).toHaveLength(1)
    expect(written(modal)).toEqual({
      anthropic: {
        type: 'anthropic',
        api_key: { mode: 'literal', value: 'sk-ant-xyz', has_env_var: false },
        api_base: 'https://api.anthropic.com/v1',
        proxy: undefined,
        web_search: false,
      },
    })
    // The written shape, keys and all: `proxy` is present and undefined.
    expect(Object.keys(written(modal).anthropic)).toEqual([
      'type',
      'api_key',
      'api_base',
      'proxy',
      'web_search',
    ])
    expect(modal.onClose).toHaveBeenCalledTimes(1)
  })

  test('an empty key is persisted as mode "empty"', () => {
    // Blank literal (C2 half 2): the modal starts in literal mode, so a
    // keyless add must be normalised instead of persisting `{mode:'literal',
    // value:''}` — ProvidersSettings only classifies `mode:'empty'` as empty.
    const literal = renderModal({ providers: {} })
    fireEvent.click(tile(literal.container, 'ollama'))
    expect(keyInput(literal.container).value).toBe('')
    fireEvent.click(addButton(literal.container))
    expect(written(literal).ollama.api_key).toEqual({ mode: 'empty', has_env_var: false })

    // Same rule for an env tab left with no variable name.
    const env = renderModal()
    fireEvent.click(buttonByText(env.container, tr('settings.envVariable')))
    fireEvent.click(tile(env.container, 'openai'))
    expect(keyInput(env.container).value).toBe('')
    fireEvent.click(addButton(env.container))
    expect(written(env).openai.api_key).toEqual({ mode: 'empty', has_env_var: false })
  })

  test('auto-derives a unique provider name', () => {
    const existing = { anthropic: stubProvider() }
    const modal = renderModal({ providers: existing })

    fireEvent.click(tile(modal.container, 'anthropic'))
    fireEvent.click(addButton(modal.container))
    // Dedupe runs against every existing key, empty stubs included (C7), and
    // the write spreads the draft instead of replacing it.
    expect(Object.keys(written(modal))).toEqual(['anthropic', 'anthropic-2'])

    // The draft grew; a second session must not overwrite `anthropic-2` either.
    modal.setProviders({ ...existing, 'anthropic-2': stubProvider() })
    modal.setIsOpen(false)
    modal.setIsOpen(true)
    fireEvent.click(tile(modal.container, 'anthropic'))
    fireEvent.click(addButton(modal.container))
    expect(Object.keys(written(modal, 1))).toEqual(['anthropic', 'anthropic-2', 'anthropic-3'])
  })

  test('an Advanced name edit wins over the derived name', () => {
    const modal = renderModal({ providers: { anthropic: stubProvider() } })
    const { container } = modal

    fireEvent.click(advancedToggle(container))
    fireEvent.change(one<HTMLInputElement>(container, '#new-provider-name'), {
      target: { value: 'prod-anthropic' },
    })
    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.click(addButton(container))

    expect(Object.keys(written(modal))).toEqual(['anthropic', 'prod-anthropic'])
    expect(written(modal)['prod-anthropic'].type).toBe('anthropic')
  })

  test('the Advanced api_base is prefilled from the tile and survives a tile switch', () => {
    const modal = renderModal()
    const { container } = modal

    fireEvent.click(tile(container, 'openai'))
    fireEvent.click(advancedToggle(container))
    const apiBase = one<HTMLInputElement>(container, '#new-provider-api-base')
    expect(apiBase.value).toBe(DEFAULT_API_BASE.openai)

    // apiBaseEdited: a hand-written endpoint is the user's, not the tile's.
    fireEvent.change(apiBase, { target: { value: 'https://gateway.internal/v1' } })
    fireEvent.click(tile(container, 'groq'))
    expect(apiBase.value).toBe('https://gateway.internal/v1')

    fireEvent.click(addButton(container))
    // The last click wins: the written entry is the groq one, with the hand-typed base.
    expect(written(modal).groq?.api_base).toBe('https://gateway.internal/v1')
  })

  test('zai and zai_coding_plan have distinct coding bases', () => {
    // C6: the coding plan lives under /api/coding/, not under /api/ like zai.
    expect(DEFAULT_API_BASE.zai).toBe('https://api.z.ai/api/paas/v4')
    expect(DEFAULT_API_BASE.zai_coding_plan).toBe('https://api.z.ai/api/coding/paas/v4')
    expect(DEFAULT_API_BASE.zai_coding_plan).toContain('/coding/')
    expect(DEFAULT_API_BASE.zai_coding_plan).not.toBe(DEFAULT_API_BASE.zai)
  })

  test('state resets on close and reopen', () => {
    const modal = renderModal()
    const { container } = modal

    fireEvent.click(advancedToggle(container))
    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.change(keyInput(container), { target: { value: 'sk-proj-secret' } })
    fireEvent.change(searchInput(container), { target: { value: 'ant' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])

    modal.setIsOpen(false)
    modal.setIsOpen(true)

    expect(keyInput(container).value).toBe('')
    expect(searchInput(container).value).toBe('')
    expect(pressedTypes(container)).toEqual([])
    expect(advancedToggle(container).getAttribute('aria-expanded')).toBe('false')
    expect(container.querySelector('#new-provider-name')).toBeNull()
    expect(tilesIn(container)).toHaveLength(PROVIDER_TYPES.length)
  })

  test('uniqueProviderName derives the config key from the type', () => {
    // R1: the type wins over the label, so the CLI entries keep their own name.
    expect(uniqueProviderName('claude-code', 'Claude Code (CLI)', [])).toBe('claude-code')
    expect(uniqueProviderName('codex-cli', 'Codex CLI', [])).toBe('codex-cli')
    expect(uniqueProviderName('zai_coding_plan', 'zAI Coding Plan', [])).toBe('zai-coding-plan')
    expect(uniqueProviderName('opencode_go', 'OpenCode Zen', [])).toBe('opencode-go')
    // Never reached by the modal (the caller passes the user-typed custom
    // type), but the slug of `custom` is `custom` — pinned, not special-cased.
    expect(uniqueProviderName('custom', 'OpenAI Compatible', [])).toBe('custom')
    // Fallback chain: type, then label, then `provider`.
    expect(uniqueProviderName('', 'My Gateway', [])).toBe('my-gateway')
    expect(uniqueProviderName('!!!', '???', [])).toBe('provider')
    // Dedupe: -2, -3, regardless of the order the taken names arrive in.
    expect(uniqueProviderName('anthropic', 'Anthropic', ['anthropic'])).toBe('anthropic-2')
    expect(uniqueProviderName('anthropic', 'Anthropic', ['anthropic', 'anthropic-2'])).toBe(
      'anthropic-3',
    )
    expect(uniqueProviderName('anthropic', 'Anthropic', ['anthropic-2', 'anthropic'])).toBe(
      'anthropic-3',
    )
  })

  // 19-20. FIX-1 (BLOCKER-1 + MAJOR-1): the curated static entry must WIN over
  // the catalog. The catalog may still enrich (`modelCount`) and must still
  // supply everything for catalog-only ids — that half is pinned by tests 5
  // and 8, which stay green through this change.
  test('the catalog cannot re-point the curated github_copilot base', async () => {
    const COPILOT: CatalogProvider = {
      id: 'github_copilot',
      name: 'GitHub Copilot',
      type: 'openai',
      api_base: 'https://api.githubcopilot.com/v1',
      model_count: 5,
    }
    const modal = renderModal({ api: catalogApi([COPILOT]) })
    const { container } = modal
    // The enriched count proves the catalog resolved; only precedence changed.
    await waitFor(() => {
      expect(tile(container, 'github_copilot').textContent).toContain(
        `github_copilot · ${tr('settings.addProviderModal.modelsCount', { count: 5 })}`,
      )
    })
    expect(tile(container, 'github_copilot').textContent).toContain('GitHub Copilot')

    fireEvent.click(tile(container, 'github_copilot'))
    fireEvent.click(addButton(container))
    // `localhost:4321` is the gRPC CLI bridge (`copilot.ClientOptions{CLIUrl}`
    // in pkg/providers/github_copilot_provider.go); the catalog's HTTP URL
    // produces a provider that cannot start. Catalog-first failed here.
    // (The config key is the slug, so `github_copilot` lands as `github-copilot`.)
    expect(written(modal)['github-copilot'].type).toBe('github_copilot')
    expect(written(modal)['github-copilot'].api_base).toBe('localhost:4321')
  })

  test('the catalog cannot rename a curated tile or break its search', async () => {
    const OPENCODE: CatalogProvider = {
      id: 'opencode',
      name: 'OpenCode',
      type: 'openai',
      api_base: 'https://opencode.ai/zen/v1',
      model_count: 40,
    }
    const { container } = renderModal({ api: catalogApi([OPENCODE]) })
    await waitFor(() => {
      expect(tile(container, 'opencode').textContent).toContain(
        `opencode · ${tr('settings.addProviderModal.modelsCount', { count: 40 })}`,
      )
    })
    // "OpenCode" is the deprecated umbrella name this very branch documents;
    // the curated "OpenCode Zen" must survive the merge.
    expect(tile(container, 'opencode').textContent).toContain('OpenCode Zen')

    // The query that matched before the fetch still matches after it: with a
    // catalog-first label this returned 0 tiles once the GET landed.
    fireEvent.change(searchInput(container), { target: { value: 'zen' } })
    expect(tilesIn(container).map(tileType)).toEqual(['opencode'])
  })

  // 21-22. FIX-2: a key-detected catalog-only provider (`xai`, `perplexity`)
  // gets a synthesized tile while the catalog is pending or failed, and the
  // real tile replaces it — without duplicating — once the catalog lands.
  test('a detected provider with no tile yet gets a synthesized, selectable tile', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const modal = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })
    const { container } = modal

    // Before the fix: `aria-pressed` stayed [], nothing in the grid looked
    // selected, yet Add was enabled and wrote an invisible provider.
    fireEvent.change(keyInput(container), { target: { value: 'xai-abcdef' } })

    const xai = tileByType(container, 'xai')
    expect(xai).not.toBeNull()
    expect(xai?.getAttribute('aria-pressed')).toBe('true')
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
    expect(written(modal).xai.type).toBe('xai')
    // No `api_base` assertion here: the curated table has no `xai` entry, so
    // comparing against `DEFAULT_API_BASE.xai ?? ''` would be `'' === ''` and
    // could never fail. The catalog path is test 22's job.

    gate.resolve({ providers: [] })
    await flush()
  })

  test('the real catalog tile replaces the synthesized one without duplicating it', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const modal = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })
    const { container } = modal

    fireEvent.change(keyInput(container), { target: { value: 'xai-abcdef' } })
    const before = tilesIn(container).length
    expect(tileByType(container, 'xai')).not.toBeNull()

    gate.resolve({ providers: [XAI_CATALOG] })
    // Wait on the catalog's own content: the synthesized tile already carries
    // `data-provider-type="xai"`, so a selector count would resolve instantly
    // and the api_base effect would not have run yet. "xAI" (the catalog name)
    // beats the synthesized "Xai" fallback only once the merge re-ran.
    await waitFor(() => {
      expect(tile(container, 'xai').textContent).toContain('xAI')
    })
    expect(container.querySelectorAll('[data-provider-type="xai"]')).toHaveLength(1)
    // Deduped through the same map: the grid did not grow a second xai tile.
    expect(tilesIn(container)).toHaveLength(before)
    expect(tile(container, 'xai').getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(addButton(container))
    expect(written(modal).xai.api_base).toBe('https://api.x.ai/v1')
  })

  // 23. FIX-3: flipping the secret mode must not silently drop a detected
  // selection (SecretInput clears `value` on literal -> env/empty).
  test('switching the secret mode freezes a key-detected selection', () => {
    const { container } = renderModal()

    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-x' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])

    fireEvent.click(buttonByText(container, tr('settings.envVariable')))

    // Before the fix: nothing was pressed and Add greyed out mid-flow even
    // though the user was clearly still adding Anthropic. C8 is untouched:
    // clearing the *text* within a mode still clears a detected pick (test 10).
    expect(pressedTypes(container)).toEqual(['anthropic'])
    expect(tile(container, 'anthropic').getAttribute('aria-pressed')).toBe('true')
    expect(addButton(container).disabled).toBe(false)
  })

  // 24-26. FIX-4: the Advanced name field shows the live derived default and
  // a typed name is sanitized + deduped at add time.
  test('the Advanced name placeholder shows the live derived name', () => {
    const modal = renderModal({ providers: { anthropic: stubProvider() } })
    const { container } = modal

    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.click(advancedToggle(container))
    const nameField = one<HTMLInputElement>(container, '#new-provider-name')
    // The draft stays empty ("auto"); the placeholder carries the identifier
    // the add would use — deduped against the existing stub. The old static
    // `namePlaceholder` ("mi-proveedor") showed nothing useful.
    expect(nameField.value).toBe('')
    expect(nameField.placeholder).toBe('anthropic-2')
  })

  test('a typed name is sanitized: dots removed without separator, empty falls back', () => {
    const modal = renderModal({ providers: { anthropic: stubProvider() } })
    const { container } = modal

    fireEvent.click(advancedToggle(container))
    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.change(one<HTMLInputElement>(container, '#new-provider-name'), {
      target: { value: 'my.provider' },
    })
    fireEvent.click(addButton(container))
    // A dotted key would split `providers.<name>.api_base` in setValueAtPath
    // (useSettingsConfig.ts) on the next edit; the sanitizer drops the dot
    // and inserts no separator.
    expect(Object.keys(written(modal))).toEqual(['anthropic', 'myprovider'])

    // A draft that sanitizes to nothing falls back to the derived name.
    fireEvent.click(advancedToggle(container))
    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.change(one<HTMLInputElement>(container, '#new-provider-name'), {
      target: { value: '...' },
    })
    fireEvent.click(addButton(container))
    expect(Object.keys(written(modal, 1))).toEqual(['anthropic', 'anthropic-2'])
  })

  test('a typed name colliding with an existing provider is deduped, never overwrites', () => {
    const modal = renderModal({ providers: { anthropic: stubProvider() } })
    const { container } = modal

    fireEvent.click(advancedToggle(container))
    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.change(one<HTMLInputElement>(container, '#new-provider-name'), {
      target: { value: 'anthropic' },
    })
    fireEvent.click(addButton(container))

    // Before the fix this silently replaced `anthropic` (key and all) with the
    // new entry; now the typed name runs through the same -2 rule.
    expect(Object.keys(written(modal))).toEqual(['anthropic', 'anthropic-2'])
    expect(written(modal).anthropic).toEqual(stubProvider())
    expect(written(modal)['anthropic-2'].type).toBe('anthropic')
  })

  // 27. FIX-5: the data invariants the pre-refactor suite pinned. (The zai /
  // zai_coding_plan pin above — test 16 — stays as it is.)
  test('PROVIDER_TYPES has unique values and a curated base for every entry', () => {
    const values = PROVIDER_TYPES.map((pt) => pt.value)
    // A duplicated value would be silently absorbed by the tiles Map, so the
    // UI cannot show it — the data invariant itself needs this pin.
    expect(new Set(values).size).toBe(values.length)

    // These four are intentionally base-less: the catalog (or the runtime)
    // supplies their endpoint, and the FIX-1 precedence lets an *empty*
    // curated value fall through to the catalog. Everything else needs a
    // non-empty DEFAULT_API_BASE entry, because a curated value now wins.
    const intentionallyEmpty = ['custom', 'claude-code', 'codex-cli', 'vllm']
    for (const value of values) {
      if (intentionallyEmpty.includes(value)) continue
      expect(DEFAULT_API_BASE[value] ?? '').not.toBe('')
    }
  })

  // 28. FIX-6: one scrollbar at a time — the grid cap yields to the Modal
  // body's own scroller while Advanced is expanded.
  test('the tile grid caps lower while Advanced is open', () => {
    const { container } = renderModal()

    expect(one<HTMLElement>(container, '[role="group"]').classList.contains('max-h-[38vh]')).toBe(
      true,
    )

    fireEvent.click(advancedToggle(container))
    const grid = one<HTMLElement>(container, '[role="group"]')
    expect(grid.classList.contains('max-h-[38vh]')).toBe(false)
    expect(grid.classList.contains('max-h-[24vh]')).toBe(true)
  })

  // 29-31. Re-review nits: the filter gates Add (NIT-1), a catalog entry with
  // no id produces no tile (NIT-2), and the key hint names the detected
  // provider where the user is looking (NIT-4).
  test('a filter that hides the selected tile disables Add again', () => {
    const modal = renderModal()
    const { container } = modal

    // Detect `anthropic` from the key, then filter the grid down to Groq: the
    // Anthropic tile is gone, so nothing visible is pressed.
    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-abcdef' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])
    expect(addButton(container).disabled).toBe(false)

    fireEvent.change(searchInput(container), { target: { value: 'groq' } })
    expect(tilesIn(container).map(tileType)).toEqual(['groq'])
    expect(pressedTypes(container)).toEqual([])
    // Before the fix: Add stayed enabled and wrote the invisible Anthropic.
    expect(addButton(container).disabled).toBe(true)

    // Clearing the query brings the pressed tile back, and with it Add.
    fireEvent.change(searchInput(container), { target: { value: '' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])
    expect(addButton(container).disabled).toBe(false)
  })

  test('a catalog entry without an id renders no tile', async () => {
    const BOGUS: CatalogProvider = { id: '', name: 'Bogus', api_base: '', model_count: 1 }
    const { container } = renderModal({ api: catalogApi([BOGUS, XAI_CATALOG]) })
    await waitFor(() => {
      expect(tileByType(container, 'xai')).not.toBeNull()
    })

    // The blank id is skipped, so no tile can match the unselected
    // `selectedType === ''` and render permanently pressed.
    expect(container.querySelectorAll('[data-provider-type=""]')).toHaveLength(0)
    for (const t of tilesIn(container)) expect(tileType(t)).not.toBe('')
    expect(pressedTypes(container)).toEqual([])
    // Skipped entirely, not rendered under some other key: 24 static + xai.
    expect(tilesIn(container)).toHaveLength(PROVIDER_TYPES.length + 1)
    expect(container.textContent).not.toContain('Bogus')
  })

  test('the key hint area names the detected provider and forgets it when cleared', () => {
    const { container } = renderModal()
    const hintArea = one<HTMLElement>(container, '#new-provider-api-key-hint')

    // No selection: the hint line carries no chip.
    expect(hintArea.querySelector('[data-detected-provider]')).toBeNull()
    expect(hintArea.textContent).not.toContain('Anthropic')

    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-abcdef' } })
    // The pressed tile may be scrolled out of the grid; the chip is next to the
    // field the user is typing in. Label comes from the tile, not a new key.
    expect(hintArea.textContent).toContain('Anthropic')
    expect(hintArea.querySelector('[data-detected-provider]')?.textContent).toBe('Anthropic')

    // The detection mirrors the key, so clearing the key clears the chip.
    fireEvent.change(keyInput(container), { target: { value: '' } })
    expect(hintArea.querySelector('[data-detected-provider]')).toBeNull()
    expect(hintArea.textContent).not.toContain('Anthropic')
  })
})
