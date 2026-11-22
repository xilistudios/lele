import '../../../test/setup'
import { describe, expect, mock, test } from 'bun:test'
import { act, fireEvent, render, waitFor } from '@testing-library/react'
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

type ProvidersMap = Record<string, EditableNamedProviderConfig>

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

function makeApi(overrides: Partial<ApiClient> = {}): ApiClient {
  return {
    models: async () => ({ models: [], model_groups: [] }),
    catalogProviders: async () => ({ providers: [] }),
    ...overrides,
  } as unknown as ApiClient
}

const catalogApi = (providers: CatalogProvider[]) =>
  makeApi({ catalogProviders: async () => ({ providers }) })

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

const flush = () => new Promise((r) => setTimeout(r, 0))

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

const tilesIn = (container: HTMLElement) =>
  Array.from(container.querySelectorAll<HTMLButtonElement>('[data-provider-type]'))

const tileType = (tile: HTMLElement) => tile.getAttribute('data-provider-type') ?? ''

const tileByType = (container: HTMLElement, type: string) =>
  container.querySelector<HTMLButtonElement>(`[data-provider-type="${type}"]`)

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

const keyInput = (container: HTMLElement) =>
  one<HTMLInputElement>(container, '#new-provider-api-key')

const searchInput = (container: HTMLElement) =>
  one<HTMLInputElement>(
    container,
    `input[aria-label="${tr('settings.addProviderModal.searchPlaceholder')}"]`,
  )

const advancedToggle = (container: HTMLElement) =>
  one<HTMLButtonElement>(container, '[aria-controls="new-provider-advanced"]')

const buttonByText = (container: HTMLElement, text: string) => {
  const found = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(
    (b) => b.textContent?.trim() === text,
  )
  if (!found) throw new Error(`no button labelled "${text}"`)
  return found
}

const addButton = (container: HTMLElement) =>
  buttonByText(container, tr('settings.addProviderModal.add'))

const written = (modal: Modal, call = 0): ProvidersMap => {
  const calls = modal.updateField.mock.calls as unknown as [string, ProvidersMap][]
  if (calls.length <= call) throw new Error(`updateField called ${calls.length} time(s)`)
  expect(calls[call][0]).toBe('providers')
  return calls[call][1]
}

describe('AddProviderModal', () => {
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

    expect(container.querySelectorAll('select')).toHaveLength(0)
    expect(container.querySelector('[role="progressbar"]')).toBeNull()
    expect(container.textContent).not.toContain('stepType')
    expect(container.textContent).not.toContain('stepConfig')
    expect(container.textContent).not.toContain('settings.addProviderModal.back')
    expect(container.textContent).not.toContain('settings.addProviderModal.next')

    const footer = one<HTMLDivElement>(container, 'div.justify-end')
    expect(footer.querySelectorAll('button')).toHaveLength(2)

    expect(keyInput(container).value).toBe('')
    expect(pressedTypes(container)).toEqual([])
  })

  test('renders at least the static tiles before the catalog resolves', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const { container } = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })

    expect(tilesIn(container).length).toBeGreaterThanOrEqual(PROVIDER_TYPES.length)
    for (const pt of PROVIDER_TYPES) {
      expect(tileByType(container, pt.value)).not.toBeNull()
    }
    expect(tileByType(container, 'xai')).toBeNull()

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

    expect(tilesIn(container)).toHaveLength(before + 1)
    const xai = tile(container, 'xai')
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
    expect(container.textContent).not.toContain(tr('settings.addProviderModal.noResults'))
  })

  test('filters tiles by label and type and shows the empty state', () => {
    const { container } = renderModal()
    const total = tilesIn(container).length
    const input = searchInput(container)

    fireEvent.change(input, { target: { value: 'groq' } })
    expect(tilesIn(container).map(tileType)).toEqual(['groq'])

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

    expect(keyInput(container).value).toBe('')
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
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
    expect(addButton(container).disabled).toBe(true)

    fireEvent.change(customType, { target: { value: 'my-gw' } })
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
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

    fireEvent.change(keyInput(container), { target: { value: 'sk-abc' } })
    expect(pressedTypes(container)).toEqual([])

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
    const literal = renderModal({ providers: {} })
    fireEvent.click(tile(literal.container, 'ollama'))
    expect(keyInput(literal.container).value).toBe('')
    fireEvent.click(addButton(literal.container))
    expect(written(literal).ollama.api_key).toEqual({ mode: 'empty', has_env_var: false })

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
    expect(Object.keys(written(modal))).toEqual(['anthropic', 'anthropic-2'])

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

    fireEvent.change(apiBase, { target: { value: 'https://gateway.internal/v1' } })
    fireEvent.click(tile(container, 'groq'))
    expect(apiBase.value).toBe('https://gateway.internal/v1')

    fireEvent.click(addButton(container))
    expect(written(modal).groq?.api_base).toBe('https://gateway.internal/v1')
  })

  test('zai and zai_coding_plan have distinct coding bases', () => {
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
    expect(uniqueProviderName('claude-code', 'Claude Code (CLI)', [])).toBe('claude-code')
    expect(uniqueProviderName('codex-cli', 'Codex CLI', [])).toBe('codex-cli')
    expect(uniqueProviderName('zai_coding_plan', 'zAI Coding Plan', [])).toBe('zai-coding-plan')
    expect(uniqueProviderName('opencode_go', 'OpenCode Zen', [])).toBe('opencode-go')
    expect(uniqueProviderName('custom', 'OpenAI Compatible', [])).toBe('custom')
    expect(uniqueProviderName('', 'My Gateway', [])).toBe('my-gateway')
    expect(uniqueProviderName('!!!', '???', [])).toBe('provider')
    expect(uniqueProviderName('anthropic', 'Anthropic', ['anthropic'])).toBe('anthropic-2')
    expect(uniqueProviderName('anthropic', 'Anthropic', ['anthropic', 'anthropic-2'])).toBe(
      'anthropic-3',
    )
    expect(uniqueProviderName('anthropic', 'Anthropic', ['anthropic-2', 'anthropic'])).toBe(
      'anthropic-3',
    )
  })

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
    await waitFor(() => {
      expect(tile(container, 'github_copilot').textContent).toContain(
        `github_copilot · ${tr('settings.addProviderModal.modelsCount', { count: 5 })}`,
      )
    })
    expect(tile(container, 'github_copilot').textContent).toContain('GitHub Copilot')

    fireEvent.click(tile(container, 'github_copilot'))
    fireEvent.click(addButton(container))
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
    expect(tile(container, 'opencode').textContent).toContain('OpenCode Zen')

    fireEvent.change(searchInput(container), { target: { value: 'zen' } })
    expect(tilesIn(container).map(tileType)).toEqual(['opencode'])
  })

  test('a detected provider with no tile yet gets a synthesized, selectable tile', async () => {
    const gate = deferred<CatalogProvidersResponse>()
    const modal = renderModal({ api: makeApi({ catalogProviders: () => gate.promise }) })
    const { container } = modal

    fireEvent.change(keyInput(container), { target: { value: 'xai-abcdef' } })

    const xai = tileByType(container, 'xai')
    expect(xai).not.toBeNull()
    expect(xai?.getAttribute('aria-pressed')).toBe('true')
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
    expect(written(modal).xai.type).toBe('xai')

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
    await act(async () => {
      await flush()
    })
    expect(tile(container, 'xai').textContent).toContain('xAI')
    expect(container.querySelectorAll('[data-provider-type="xai"]')).toHaveLength(1)
    expect(tilesIn(container)).toHaveLength(before)
    expect(tile(container, 'xai').getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(addButton(container))
    await act(async () => {
      await flush()
    })
    expect(written(modal).xai.api_base).toBe('https://api.x.ai/v1')
  })

  test('switching the secret mode freezes a key-detected selection', () => {
    const { container } = renderModal()

    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-x' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])

    fireEvent.click(buttonByText(container, tr('settings.envVariable')))

    expect(pressedTypes(container)).toEqual(['anthropic'])
    expect(tile(container, 'anthropic').getAttribute('aria-pressed')).toBe('true')
    expect(addButton(container).disabled).toBe(false)
  })

  test('the Advanced name placeholder shows the live derived name', () => {
    const modal = renderModal({ providers: { anthropic: stubProvider() } })
    const { container } = modal

    fireEvent.click(tile(container, 'anthropic'))
    fireEvent.click(advancedToggle(container))
    const nameField = one<HTMLInputElement>(container, '#new-provider-name')
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
    expect(Object.keys(written(modal))).toEqual(['anthropic', 'myprovider'])

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

    expect(Object.keys(written(modal))).toEqual(['anthropic', 'anthropic-2'])
    expect(written(modal).anthropic).toEqual(stubProvider())
    expect(written(modal)['anthropic-2'].type).toBe('anthropic')
  })

  test('PROVIDER_TYPES has unique values and a curated base for every entry', () => {
    const values = PROVIDER_TYPES.map((pt) => pt.value)
    expect(new Set(values).size).toBe(values.length)

    const intentionallyEmpty = ['custom', 'claude-code', 'codex-cli', 'vllm']
    for (const value of values) {
      if (intentionallyEmpty.includes(value)) continue
      expect(DEFAULT_API_BASE[value] ?? '').not.toBe('')
    }
  })

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

  test('a filter that hides the selected tile disables Add again', () => {
    const modal = renderModal()
    const { container } = modal

    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-abcdef' } })
    expect(pressedTypes(container)).toEqual(['anthropic'])
    expect(addButton(container).disabled).toBe(false)

    fireEvent.change(searchInput(container), { target: { value: 'groq' } })
    expect(tilesIn(container).map(tileType)).toEqual(['groq'])
    expect(pressedTypes(container)).toEqual([])
    expect(addButton(container).disabled).toBe(true)

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

    expect(container.querySelectorAll('[data-provider-type=""]')).toHaveLength(0)
    for (const t of tilesIn(container)) expect(tileType(t)).not.toBe('')
    expect(pressedTypes(container)).toEqual([])
    expect(tilesIn(container)).toHaveLength(PROVIDER_TYPES.length + 1)
    expect(container.textContent).not.toContain('Bogus')
  })

  test('the key hint area names the detected provider and forgets it when cleared', () => {
    const { container } = renderModal()
    const hintArea = one<HTMLElement>(container, '#new-provider-api-key-hint')

    expect(hintArea.querySelector('[data-detected-provider]')).toBeNull()
    expect(hintArea.textContent).not.toContain('Anthropic')

    fireEvent.change(keyInput(container), { target: { value: 'sk-ant-abcdef' } })
    expect(hintArea.textContent).toContain('Anthropic')
    expect(hintArea.querySelector('[data-detected-provider]')?.textContent).toBe('Anthropic')

    fireEvent.change(keyInput(container), { target: { value: '' } })
    expect(hintArea.querySelector('[data-detected-provider]')).toBeNull()
    expect(hintArea.textContent).not.toContain('Anthropic')
  })
  test('a provider the backend knows but the curated list omits is selectable', async () => {
    expect(PROVIDER_TYPES.map((pt) => pt.value)).not.toContain('mistral')

    const MISTRAL: CatalogProvider = {
      id: 'mistral',
      name: 'Mistral',
      type: 'openai',
      api_base: 'https://api.mistral.ai/v1',
      model_count: 9,
    }
    const modal = renderModal({ api: catalogApi([MISTRAL]) })
    const { container } = modal

    await waitFor(() => {
      expect(tileByType(container, 'mistral')).not.toBeNull()
    })

    fireEvent.click(tile(container, 'mistral'))
    expect(pressedTypes(container)).toEqual(['mistral'])
    expect(addButton(container).disabled).toBe(false)

    fireEvent.click(addButton(container))
    expect(written(modal).mistral.type).toBe('mistral')
    expect(written(modal).mistral.api_base).toBe('https://api.mistral.ai/v1')
  })

  test('the catalog api_base pre-fills the Advanced field for a catalog-only tile', async () => {
    const { container } = renderModal({ api: catalogApi([XAI_CATALOG]) })
    await waitFor(() => {
      expect(tileByType(container, 'xai')).not.toBeNull()
    })

    fireEvent.click(tile(container, 'xai'))
    fireEvent.click(advancedToggle(container))

    const apiBase = one<HTMLInputElement>(container, '#new-provider-api-base')
    expect(apiBase.value).toBe('https://api.x.ai/v1')
  })

  test('a non-empty curated base still wins over the catalog', async () => {
    const COPILOT: CatalogProvider = {
      id: 'github_copilot',
      name: 'GitHub Copilot',
      type: 'openai',
      api_base: 'https://api.githubcopilot.com/v1',
      model_count: 5,
    }
    const modal = renderModal({ api: catalogApi([COPILOT]) })
    const { container } = modal

    await waitFor(() => {
      expect(tile(container, 'github_copilot').textContent).toContain(
        `github_copilot · ${tr('settings.addProviderModal.modelsCount', { count: 5 })}`,
      )
    })

    fireEvent.click(tile(container, 'github_copilot'))
    fireEvent.click(addButton(container))
    expect(written(modal)['github-copilot'].api_base).toBe(DEFAULT_API_BASE.github_copilot)
  })

  test('base_less marks the providers whose endpoint cannot be derived', async () => {
    const BEDROCK: CatalogProvider = {
      id: 'bedrock',
      name: 'AWS Bedrock',
      type: 'openai',
      base_less: true,
    }
    const modal = renderModal({ api: catalogApi([BEDROCK]) })
    const { container } = modal

    await waitFor(() => {
      expect(tileByType(container, 'bedrock')).not.toBeNull()
    })

    fireEvent.click(tile(container, 'bedrock'))
    fireEvent.click(addButton(container))
    expect(written(modal).bedrock.api_base).toBe('')
  })
})
