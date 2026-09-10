import { useTranslation } from 'react-i18next'
import type { ProviderModelConfig } from '../../../lib/types'
import { RemoveButton } from '../../atoms/RemoveButton'
import { BooleanInput, NumberInput, SettingsField } from '../../molecules'
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
                      [key]: { ...m, reasoning: { ...m.reasoning, enable: v } },
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
