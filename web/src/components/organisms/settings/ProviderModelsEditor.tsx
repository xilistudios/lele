import { useTranslation } from 'react-i18next'
import type { ProviderModelConfig, ReasoningConfig } from '../../../lib/types'
import { RemoveButton } from '../../atoms/RemoveButton'
import { BooleanInput, NumberInput, SelectInput, SettingsField } from '../../molecules'
import { type AddModelPayload, ModelSearchInput } from './ModelSearchInput'

type ProviderModels = Record<string, ProviderModelConfig>

type Props = {
  name: string
  models: ProviderModels
  onChange: (v: ProviderModels) => void
  providerType?: string
}

const DEFAULT_CONTEXT_WINDOW = 120000
const DEFAULT_MAX_TOKENS = 8192

export function buildModelConfig(meta: AddModelPayload): ProviderModelConfig {
  const contextWindow =
    meta.context_window && meta.context_window > 0 ? meta.context_window : DEFAULT_CONTEXT_WINDOW
  const maxTokens =
    meta.max_output && meta.max_output > 0
      ? meta.max_output
      : Math.min(DEFAULT_MAX_TOKENS, contextWindow)
  const supportsThinking = Boolean(meta.reasoning || meta.thinking_levels?.length)

  return {
    context_window: contextWindow,
    max_tokens: maxTokens,
    temperature: 0.6,
    vision: meta.vision,
    reasoning: supportsThinking ? { enable: true, effort: 'medium' } : undefined,
  }
}

export function ProviderModelsEditor({ name, models, onChange, providerType }: Props) {
  const { t } = useTranslation()
  const modelNames = Object.keys(models)

  const THINKING_TYPES: Array<{ value: string; label: string }> = [
    { value: 'auto', label: 'auto' },
    { value: 'deepseek', label: 'deepseek' },
    { value: 'openai', label: 'openai' },
    { value: 'openrouter', label: 'openrouter' },
    { value: 'qwen', label: 'qwen' },
    { value: 'none', label: 'none' },
  ]

  const REASONING_EFFORTS: Array<{ value: string; label: string }> = [
    { value: '', label: t('settings.options.reasoningDefault') },
    { value: 'low', label: t('settings.options.thinkingLow') },
    { value: 'medium', label: t('settings.options.thinkingMedium') },
    { value: 'high', label: t('settings.options.thinkingHigh') },
    { value: 'xhigh', label: t('settings.options.thinkingXhigh') },
    { value: 'max', label: t('settings.options.thinkingMax') },
  ]

  const REASONING_SUMMARIES: Array<{ value: string; label: string }> = [
    { value: '', label: t('settings.options.reasoningDefault') },
    { value: 'auto', label: 'auto' },
    { value: 'detailed', label: 'detailed' },
    { value: 'concise', label: 'concise' },
  ]

  const REASONING_EXCLUDE: Array<{ value: string; label: string }> = [
    { value: '', label: t('settings.options.reasoningDefault') },
    { value: 'yes', label: t('common.yes') },
    { value: 'no', label: t('common.no') },
  ]

  const withReasoning = (m: ProviderModelConfig, patch: ReasoningConfig): ProviderModelConfig => ({
    ...m,
    reasoning: { ...m.reasoning, ...patch },
  })

  const addModel = (input: string | AddModelPayload) => {
    const meta: AddModelPayload = typeof input === 'string' ? { id: input } : input
    const trimmed = meta.id.trim()
    if (!trimmed) return
    onChange({
      ...models,
      [trimmed]: buildModelConfig({ ...meta, id: trimmed }),
    })
  }

  const removeModel = (key: string) => {
    const updated = { ...models }
    delete updated[key]
    onChange(updated)
  }

  return (
    <div className="space-y-3">
      <ModelSearchInput
        providerName={name}
        providerType={providerType}
        existingModels={modelNames}
        onAddModel={addModel}
      />

      {modelNames.length === 0 && (
        <p className="text-xs text-text-tertiary">{t('settings.noModels')}</p>
      )}

      {modelNames.map((key) => {
        const m = models[key]
        return (
          <div key={key} className="rounded border border-border bg-background-secondary p-3">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-xs font-medium text-text-primary">{key}</span>
              <RemoveButton onClick={() => removeModel(key)} ariaLabel={t('common.remove')} />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <SettingsField
                label={t('settings.fields.modelContextWindow')}
                path={`providers.${name}.models.${key}.context_window`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.context_window`}
                  value={m.context_window || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, context_window: v || undefined },
                    })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelMaxTokens')}
                path={`providers.${name}.models.${key}.max_tokens`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.max_tokens`}
                  value={m.max_tokens || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, max_tokens: v || undefined },
                    })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelTemperature')}
                path={`providers.${name}.models.${key}.temperature`}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.temperature`}
                  value={m.temperature || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: { ...m, temperature: v || undefined },
                    })
                  }
                  min={0}
                  max={2}
                  step={0.1}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelVision')}
                path={`providers.${name}.models.${key}.vision`}
              >
                <BooleanInput
                  id={`providers.${name}.models.${key}.vision`}
                  value={m.vision || false}
                  onChange={(v) => onChange({ ...models, [key]: { ...m, vision: v } })}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelThinking')}
                path={`providers.${name}.models.${key}.reasoning.enable`}
              >
                <BooleanInput
                  id={`providers.${name}.models.${key}.reasoning`}
                  value={m.reasoning?.enable || false}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: withReasoning(m, { enable: v }),
                    })
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelReasoningEffort')}
                path={`providers.${name}.models.${key}.reasoning.effort`}
                description={t('settings.descriptions.modelReasoningEffort')}
              >
                <SelectInput
                  id={`providers.${name}.models.${key}.reasoning.effort`}
                  value={m.reasoning?.effort ?? ''}
                  options={REASONING_EFFORTS}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: withReasoning(m, {
                        effort: v === '' ? undefined : (v as ReasoningConfig['effort']),
                      }),
                    })
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelReasoningSummary')}
                path={`providers.${name}.models.${key}.reasoning.summary`}
                description={t('settings.descriptions.modelReasoningSummary')}
              >
                <SelectInput
                  id={`providers.${name}.models.${key}.reasoning.summary`}
                  value={m.reasoning?.summary ?? ''}
                  options={REASONING_SUMMARIES}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: withReasoning(m, {
                        summary: v === '' ? undefined : (v as ReasoningConfig['summary']),
                      }),
                    })
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelReasoningMaxTokens')}
                path={`providers.${name}.models.${key}.reasoning.max_tokens`}
                description={t('settings.descriptions.modelReasoningMaxTokens')}
              >
                <NumberInput
                  id={`providers.${name}.models.${key}.reasoning.max_tokens`}
                  value={m.reasoning?.max_tokens || 0}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: withReasoning(m, { max_tokens: v || undefined }),
                    })
                  }
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelReasoningExclude')}
                path={`providers.${name}.models.${key}.reasoning.exclude`}
                description={t('settings.descriptions.modelReasoningExclude')}
              >
                <SelectInput
                  id={`providers.${name}.models.${key}.reasoning.exclude`}
                  value={
                    m.reasoning?.exclude === undefined ? '' : m.reasoning.exclude ? 'yes' : 'no'
                  }
                  options={REASONING_EXCLUDE}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: withReasoning(m, {
                        exclude: v === '' ? undefined : v === 'yes',
                      }),
                    })
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.modelThinkingType')}
                path={`providers.${name}.models.${key}.thinking_type`}
              >
                <SelectInput
                  id={`providers.${name}.models.${key}.thinking_type`}
                  value={m.thinking_type || 'auto'}
                  options={THINKING_TYPES}
                  onChange={(v) =>
                    onChange({
                      ...models,
                      [key]: {
                        ...m,
                        thinking_type:
                          v === 'auto' ? undefined : (v as ProviderModelConfig['thinking_type']),
                      },
                    })
                  }
                />
              </SettingsField>
            </div>
          </div>
        )
      })}
    </div>
  )
}
