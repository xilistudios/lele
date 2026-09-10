import '../../../test/setup'
import { describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, waitFor } from '@testing-library/react'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../../hooks/useSettingsConfig'
import type { CatalogModelsResponse, ProviderModelsResponse } from '../../../lib/types'
import type { ApiClient } from '../../../services/http/client'
import {
  type AddModelPayload,
  ModelSearchInput,
  isOpenAICompatible,
  normalizeProviderType,
} from './ModelSearchInput'
import { ProviderModelsEditor, buildModelConfig } from './ProviderModelsEditor'

const LIVE_MODELS: ProviderModelsResponse = {
  provider: 'openai',
  models: [
    {
      id: 'gpt-5',
      object: 'model',
      created: 1,
      owned_by: 'openai',
      context_window: 400000,
      max_output: 128000,
      vision: true,
      thinking_levels: ['low', 'medium', 'high'],
      reasoning: true,
    },
  ],
}

const CATALOG_MODELS: CatalogModelsResponse = {
  provider: 'xai',
  models: [
    {
      id: 'grok-4',
      name: 'Grok 4',
      context_window: 256000,
      max_output: 32768,
      vision: true,
      thinking_levels: ['high'],
      reasoning: true,
    },
    {
      id: 'grok-code-fast',
      context_window: 256000,
    },
  ],
}

function makeApi(overrides: Partial<ApiClient> = {}): ApiClient {
  return {
    models: async () => ({ models: [] }),
    providerModels: async () => LIVE_MODELS,
    catalogModels: async () => CATALOG_MODELS,
    ...overrides,
  } as unknown as ApiClient
}

function makeSettingsState(): SettingsConfigState {
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
    hasErrors: false,
  }
}

function renderSearch(options: {
  providerName?: string
  providerType?: string | undefined
  existingModels?: string[]
  api?: ApiClient
}) {
  const onAddModel = mock((_model: AddModelPayload) => {})
  const api = options.api ?? makeApi()
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={makeSettingsState()} api={api}>
        <ModelSearchInput
          providerName={options.providerName ?? 'openai'}
          providerType={options.providerType}
          existingModels={options.existingModels ?? []}
          onAddModel={onAddModel}
        />
      </SettingsProvider>
    </QueryClientProvider>,
  )
  const input = utils.container.querySelector('input[type="text"]') as HTMLInputElement
  return { ...utils, onAddModel, input, api, queryClient }
}

describe('normalizeProviderType / isOpenAICompatible', () => {
  test('maps common aliases onto catalog ids', () => {
    expect(normalizeProviderType('GPT')).toBe('openai')
    expect(normalizeProviderType('grok')).toBe('xai')
    expect(normalizeProviderType('X.AI')).toBe('xai')
    expect(normalizeProviderType('lm-studio')).toBe('lmstudio')
    expect(normalizeProviderType('ollama-cloud')).toBe('ollama_cloud')
    expect(normalizeProviderType('mimo')).toBe('xiaomi')
    expect(normalizeProviderType('kimi_for_coding')).toBe('kimi_for_coding')
  })

  test('includes the new hermes providers as openai-compatible', () => {
    for (const type of [
      'xai',
      'nous',
      'lmstudio',
      'stepfun',
      'minimax',
      'minimax_cn',
      'vercel',
      'opencode',
      'opencode_go',
      'huggingface',
      'novita',
      'xiaomi',
      'tencent_tokenhub',
      'arcee',
      'gmi',
      'cerebras',
      'together',
      'fireworks',
      'mistral',
      'siliconflow',
      'perplexity',
      'ollama_cloud',
      'kimi_for_coding',
      'alibaba_coding_plan',
      'qwen_portal',
    ]) {
      expect(isOpenAICompatible(type)).toBe(true)
    }
    expect(isOpenAICompatible(undefined)).toBe(false)
    expect(isOpenAICompatible('unknown-type')).toBe(false)
  })
})

describe('buildModelConfig', () => {
  test('prefills context/max/vision/reasoning from catalog metadata', () => {
    const cfg = buildModelConfig({
      id: 'gpt-5',
      context_window: 400000,
      max_output: 128000,
      vision: true,
      thinking_levels: ['low', 'medium', 'high'],
      reasoning: true,
    })
    expect(cfg.context_window).toBe(400000)
    expect(cfg.max_tokens).toBe(128000)
    expect(cfg.vision).toBe(true)
    expect(cfg.reasoning).toEqual({ enable: true, effort: 'medium' })
    expect(cfg.temperature).toBe(0.6)
  })

  test('falls back to defaults when metadata is missing', () => {
    const cfg = buildModelConfig({ id: 'local-model' })
    expect(cfg.context_window).toBe(120000)
    expect(cfg.max_tokens).toBe(8192)
    expect(cfg.vision).toBeUndefined()
    expect(cfg.reasoning).toBeUndefined()
  })

  test('caps max_tokens at the context window when only context is known', () => {
    const cfg = buildModelConfig({ id: 'tiny', context_window: 4096 })
    expect(cfg.context_window).toBe(4096)
    expect(cfg.max_tokens).toBe(4096)
  })

  test('enables reasoning when only thinking_levels are present', () => {
    const cfg = buildModelConfig({ id: 'thinker', thinking_levels: ['high'] })
    expect(cfg.reasoning).toEqual({ enable: true, effort: 'medium' })
  })
})

describe('ModelSearchInput', () => {
  test('prefers live provider models for openai-compatible types and prefills on select', async () => {
    const providerModels = mock(async () => LIVE_MODELS)
    const catalogModels = mock(async () => CATALOG_MODELS)
    const u = renderSearch({
      providerType: 'openai',
      api: makeApi({ providerModels, catalogModels } as Partial<ApiClient>),
    })

    fireEvent.focus(u.input)
    await waitFor(() => {
      expect(u.container.textContent).toContain('gpt-5')
    })
    expect(providerModels).toHaveBeenCalled()
    expect(catalogModels).not.toHaveBeenCalled()
    expect(u.container.textContent).toContain('400k')
    expect(u.container.textContent).toContain('vision')
    expect(u.container.textContent).toContain('low/medium/high')

    const rows = Array.from(u.container.querySelectorAll('button')).filter((b) =>
      b.textContent?.includes('gpt-5'),
    )
    expect(rows[0]).toBeTruthy()
    fireEvent.click(rows[0] as HTMLElement)

    expect(u.onAddModel).toHaveBeenCalledTimes(1)
    expect(u.onAddModel.mock.calls[0][0]).toEqual({
      id: 'gpt-5',
      context_window: 400000,
      max_output: 128000,
      vision: true,
      thinking_levels: ['low', 'medium', 'high'],
      reasoning: true,
    })
  })

  test('falls back to catalog when live fails, including non-compat types', async () => {
    const providerModels = mock(async () => {
      throw new Error('no key')
    })
    const catalogModels = mock(async () => CATALOG_MODELS)
    const u = renderSearch({
      providerName: 'my-xai',
      providerType: 'xai',
      api: makeApi({ providerModels, catalogModels } as Partial<ApiClient>),
    })

    fireEvent.focus(u.input)
    await waitFor(() => {
      expect(u.container.textContent).toContain('grok-4')
    })
    expect(catalogModels).toHaveBeenCalledWith('xai')
    expect(u.container.textContent).toContain('256k')
  })

  test('falls back to catalog when live fails for unknown types', async () => {
    const providerModels = mock(async () => {
      throw new Error('provider not found')
    })
    const catalogModels = mock(async () => CATALOG_MODELS)
    const u = renderSearch({
      providerName: 'weird',
      providerType: 'not-a-known-type',
      api: makeApi({ providerModels, catalogModels } as Partial<ApiClient>),
    })

    fireEvent.focus(u.input)
    await waitFor(() => {
      expect(catalogModels).toHaveBeenCalled()
    })
    expect(providerModels).toHaveBeenCalled()
    await waitFor(() => {
      expect(u.container.textContent).toContain('grok-4')
    })
  })

  test('manual add still works with a bare id payload', async () => {
    const u = renderSearch({ providerType: 'openai' })
    fireEvent.change(u.input, { target: { value: 'custom-model' } })
    fireEvent.keyDown(u.input, { key: 'Enter' })
    expect(u.onAddModel).toHaveBeenCalledWith({ id: 'custom-model' })
  })

  test('keyboard navigation selects the highlighted row', async () => {
    const u = renderSearch({ providerType: 'openai' })
    fireEvent.focus(u.input)
    await waitFor(() => {
      expect(u.container.textContent).toContain('gpt-5')
    })
    fireEvent.keyDown(u.input, { key: 'ArrowDown' })
    fireEvent.keyDown(u.input, { key: 'Enter' })
    expect(u.onAddModel).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'gpt-5', context_window: 400000 }),
    )
  })

  test('existing models are hidden from suggestions', async () => {
    const u = renderSearch({ providerType: 'openai', existingModels: ['gpt-5'] })
    fireEvent.focus(u.input)
    await waitFor(() => {
      expect(u.container.textContent).not.toContain('400k')
    })
    expect(u.container.textContent).not.toContain('gpt-5')
  })
})

describe('ProviderModelsEditor.addModel', () => {
  function renderEditor(options: {
    providerType?: string
    api?: ApiClient
    models?: Record<string, unknown>
  }) {
    const onChange = mock((_v: Record<string, unknown>) => {})
    const api = options.api ?? makeApi()
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const utils = render(
      <QueryClientProvider client={queryClient}>
        <SettingsProvider settingsState={makeSettingsState()} api={api}>
          <ProviderModelsEditor
            name="openai"
            models={(options.models ?? {}) as never}
            onChange={onChange as never}
            providerType={options.providerType}
          />
        </SettingsProvider>
      </QueryClientProvider>,
    )
    const input = utils.container.querySelector('input[type="text"]') as HTMLInputElement
    return { ...utils, onChange, input }
  }

  test('selecting a catalog model writes prefilled config instead of hardcoded defaults', async () => {
    const u = renderEditor({ providerType: 'openai' })
    fireEvent.focus(u.input)
    await waitFor(() => {
      expect(u.container.textContent).toContain('gpt-5')
    })
    const row = Array.from(u.container.querySelectorAll('button')).find((b) =>
      b.textContent?.includes('gpt-5'),
    )
    expect(row).toBeTruthy()
    fireEvent.click(row as HTMLElement)

    expect(u.onChange).toHaveBeenCalledTimes(1)
    const next = u.onChange.mock.calls[0][0] as Record<
      string,
      {
        context_window: number
        max_tokens: number
        temperature: number
        vision: boolean
        reasoning: { enable: boolean; effort: string }
      }
    >
    expect(next['gpt-5']).toEqual({
      context_window: 400000,
      max_tokens: 128000,
      temperature: 0.6,
      vision: true,
      reasoning: { enable: true, effort: 'medium' },
    })
  })

  test('manual add keeps legacy defaults', async () => {
    const u = renderEditor({ providerType: 'custom' })
    fireEvent.change(u.input, { target: { value: 'local-llama' } })
    fireEvent.keyDown(u.input, { key: 'Enter' })
    const next = u.onChange.mock.calls[0][0] as Record<
      string,
      {
        context_window: number
        max_tokens: number
        temperature: number
        vision?: boolean
        reasoning?: { enable: boolean; effort: string }
      }
    >
    expect(next['local-llama']).toEqual({
      context_window: 120000,
      max_tokens: 8192,
      temperature: 0.6,
      vision: undefined,
      reasoning: undefined,
    })
  })
})
