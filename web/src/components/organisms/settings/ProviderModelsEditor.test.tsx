/**
 * ProviderModelsEditor — per-model reasoning overrides.
 *
 * The backend (config.ReasoningConfig in Go) accepts effort, summary,
 * max_tokens and exclude on top of the pre-existing enable toggle; this suite
 * pins what the editor writes for each of them:
 * - current values are rendered from the stored ReasoningConfig;
 * - a change keeps every sibling field of the model (immutable update);
 * - picking the "default" option PRUNES the key instead of writing an empty
 *   or explicit value, so an unset override stays absent in the saved config;
 * - a model without a `reasoning` object gets one created on first write
 *   instead of crashing on the `...undefined` spread.
 *
 * Harness rules: tests boot in Spanish (`src/test/i18n.ts`), so copy is
 * asserted through `tr(key)` — never hardcoded prose. The editor embeds
 * ModelSearchInput, which needs the settings context and a react-query client,
 * hence the wrapper below (same harness as ModelSearchInput.test.tsx).
 */

import '../../../test/setup'
import { describe, expect, mock, test } from 'bun:test'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render } from '@testing-library/react'
import '../../../test/i18n'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import type { SettingsConfigState } from '../../../hooks/useSettingsConfig'
import type { ApiClient } from '../../../services/http/client'
import i18n from '../../../test/i18n'
import { ProviderModelsEditor } from './ProviderModelsEditor'

const tr = (key: string) => i18n.t(key) as string

const NAME = 'openai'
const KEY = 'm1'

type Models = Record<string, Record<string, unknown>>

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
    isReloading: false,
    hasErrors: false,
  }
}

/** The editor only reads the model list from the API on user interaction. */
function makeApi(): ApiClient {
  return {
    models: async () => ({ models: [] }),
    providerModels: async () => ({ provider: NAME, models: [] }),
    catalogModels: async () => ({ provider: NAME, models: [] }),
  } as unknown as ApiClient
}

function renderEditor(models: Models) {
  const onChange = mock((_v: Models) => {})
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={queryClient}>
      <SettingsProvider settingsState={makeSettingsState()} api={makeApi()}>
        <ProviderModelsEditor
          name={NAME}
          models={models as never}
          onChange={onChange as never}
          providerType="openai"
        />
      </SettingsProvider>
    </QueryClientProvider>,
  )

  const path = (suffix: string) => `providers.${NAME}.models.${KEY}.${suffix}`
  const select = (suffix: string) =>
    utils.container.querySelector(`select[id="${path(suffix)}"]`) as HTMLSelectElement
  const numberInput = (suffix: string) =>
    utils.container.querySelector(`input[id="${path(suffix)}"]`) as HTMLInputElement
  /** The single model object the editor wrote back on the last change. */
  const lastModel = () => {
    const calls = onChange.mock.calls
    return (calls[calls.length - 1]?.[0] as Models)[KEY] as {
      context_window?: number
      max_tokens?: number
      temperature?: number
      vision?: boolean
      thinking_type?: string
      reasoning?: {
        enable?: boolean
        effort?: string
        summary?: string
        max_tokens?: number
        exclude?: boolean
      }
    }
  }

  return { ...utils, onChange, select, numberInput, lastModel, path }
}

describe('ProviderModelsEditor reasoning overrides', () => {
  test('renders the stored reasoning values', () => {
    const u = renderEditor({
      [KEY]: {
        context_window: 128000,
        temperature: 0.6,
        reasoning: {
          enable: true,
          effort: 'low',
          summary: 'detailed',
          max_tokens: 2048,
          exclude: true,
        },
      },
    })

    expect(u.select('reasoning.effort').value).toBe('low')
    expect(u.select('reasoning.summary').value).toBe('detailed')
    expect(u.numberInput('reasoning.max_tokens').value).toBe('2048')
    expect(u.select('reasoning.exclude').value).toBe('yes')
    // The pre-existing toggle is still there and still reflects the config.
    expect(u.getByText(tr('settings.fields.modelThinking'))).toBeTruthy()
  })

  test('renders the default option and empty values for an unset ReasoningConfig', () => {
    const u = renderEditor({ [KEY]: { context_window: 128000, reasoning: { enable: false } } })

    expect(u.select('reasoning.effort').value).toBe('')
    expect(u.select('reasoning.summary').value).toBe('')
    expect(u.numberInput('reasoning.max_tokens').value).toBe('0')
    expect(u.select('reasoning.exclude').value).toBe('')
    // Option labels are translated, not raw wire values, for effort.
    const labels = Array.from(u.select('reasoning.effort').options).map((o) => o.textContent)
    expect(labels).toEqual([
      tr('settings.options.reasoningDefault'),
      tr('settings.options.thinkingLow'),
      tr('settings.options.thinkingMedium'),
      tr('settings.options.thinkingHigh'),
    ])
  })

  test('changing effort keeps every other model field', () => {
    const u = renderEditor({
      [KEY]: {
        context_window: 128000,
        max_tokens: 4096,
        temperature: 0.6,
        vision: true,
        thinking_type: 'deepseek',
        reasoning: { enable: true, effort: 'low' },
      },
    })

    fireEvent.change(u.select('reasoning.effort'), { target: { value: 'high' } })

    expect(u.onChange).toHaveBeenCalledTimes(1)
    expect(u.lastModel()).toEqual({
      context_window: 128000,
      max_tokens: 4096,
      temperature: 0.6,
      vision: true,
      thinking_type: 'deepseek',
      reasoning: { enable: true, effort: 'high' },
    })
  })

  test('selecting the default effort prunes the key to undefined', () => {
    const u = renderEditor({ [KEY]: { reasoning: { enable: true, effort: 'medium' } } })

    fireEvent.change(u.select('reasoning.effort'), { target: { value: '' } })

    const reasoning = u.lastModel().reasoning ?? {}
    expect('effort' in reasoning && reasoning.effort !== undefined).toBe(false)
    expect(reasoning.enable).toBe(true)
  })

  test('changing the summary writes it and keeps effort', () => {
    const u = renderEditor({ [KEY]: { reasoning: { effort: 'high' } } })

    fireEvent.change(u.select('reasoning.summary'), { target: { value: 'concise' } })
    expect(u.lastModel().reasoning).toEqual({ effort: 'high', summary: 'concise' })

    fireEvent.change(u.select('reasoning.summary'), { target: { value: '' } })
    expect(u.lastModel().reasoning).toEqual({ effort: 'high', summary: undefined })
  })

  test('exclude maps yes → true, no → false and default → undefined', () => {
    const u = renderEditor({ [KEY]: { reasoning: { enable: true } } })

    fireEvent.change(u.select('reasoning.exclude'), { target: { value: 'yes' } })
    expect(u.lastModel().reasoning).toEqual({ enable: true, exclude: true })

    fireEvent.change(u.select('reasoning.exclude'), { target: { value: 'no' } })
    expect(u.lastModel().reasoning).toEqual({ enable: true, exclude: false })

    fireEvent.change(u.select('reasoning.exclude'), { target: { value: '' } })
    expect(u.lastModel().reasoning).toEqual({ enable: true, exclude: undefined })
  })

  test('max_tokens persists a positive cap', () => {
    // One write per render: the editor is fully controlled by its `models`
    // prop, which the mock onChange never feeds back, so a second change to the
    // same value React already tracks would not dispatch (value tracker).
    const u = renderEditor({ [KEY]: { reasoning: { effort: 'low' } } })

    fireEvent.change(u.numberInput('reasoning.max_tokens'), { target: { value: '1500' } })
    expect(u.lastModel().reasoning).toEqual({ effort: 'low', max_tokens: 1500 })
  })

  test('max_tokens 0 prunes the key', () => {
    const u = renderEditor({ [KEY]: { reasoning: { effort: 'low', max_tokens: 1500 } } })

    fireEvent.change(u.numberInput('reasoning.max_tokens'), { target: { value: '0' } })
    expect(u.lastModel().reasoning).toEqual({ effort: 'low', max_tokens: undefined })
  })

  test('a model without reasoning gets one created on the first write', () => {
    const u = renderEditor({ [KEY]: { context_window: 128000, temperature: 0.2 } })

    fireEvent.change(u.select('reasoning.effort'), { target: { value: 'medium' } })

    expect(u.lastModel()).toEqual({
      context_window: 128000,
      temperature: 0.2,
      reasoning: { effort: 'medium' },
    })
  })

  test('the reasoning fields expose their descriptions', () => {
    const u = renderEditor({ [KEY]: { reasoning: { enable: true } } })

    expect(u.getByText(tr('settings.descriptions.modelReasoningEffort'))).toBeTruthy()
    expect(u.getByText(tr('settings.descriptions.modelReasoningSummary'))).toBeTruthy()
    expect(u.getByText(tr('settings.descriptions.modelReasoningMaxTokens'))).toBeTruthy()
    expect(u.getByText(tr('settings.descriptions.modelReasoningExclude'))).toBeTruthy()
  })
})
