import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AddButton } from '../../atoms/AddButton'
import { RemoveButton } from '../../atoms/RemoveButton'
import { BooleanInput, NumberInput, SettingsField } from '../../molecules'

type ProviderModels = Record<string, import('../../../lib/types').ProviderModelConfig>

type Props = {
  name: string
  models: ProviderModels
  onChange: (v: ProviderModels) => void
}

const INPUT_CLS =
  'w-full rounded border border-border bg-background-primary px-3 py-2 text-xs text-text-primary placeholder:text-text-tertiary focus:border-blue-500 focus:outline-none disabled:opacity-50'

export function ProviderModelsEditor({ name, models, onChange }: Props) {
  const { t } = useTranslation()
  const [newModelName, setNewModelName] = useState('')
  const modelNames = Object.keys(models)

  const addModel = () => {
    const key = newModelName.trim()
    if (!key) return
    onChange({ ...models, [key]: {} })
    setNewModelName('')
  }

  const removeModel = (key: string) => {
    const updated = { ...models }
    delete updated[key]
    onChange(updated)
  }

  return (
    <div className="space-y-3">
      <div className="flex gap-2">
        <input
          type="text"
          value={newModelName}
          onChange={(e) => setNewModelName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault()
              addModel()
            }
          }}
          placeholder={t('settings.modelNamePlaceholder')}
          className={INPUT_CLS}
        />
        <AddButton onClick={addModel} disabled={!newModelName.trim()}>
          {t('common.add')}
        </AddButton>
      </div>

      {modelNames.length === 0 && (
        <p className="text-xs text-text-tertiary">{t('settings.noModels')}</p>
      )}

      {modelNames.map((key) => {
        const m = models[key]
        return (
          <div key={key} className="rounded border border-border bg-background-secondary p-3">
            <div className="mb-2 flex items-center justify-between">
              <span className="font-mono text-xs font-medium text-white">{key}</span>
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
                    onChange({ ...models, [key]: { ...m, context_window: v || undefined } })
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
                    onChange({ ...models, [key]: { ...m, max_tokens: v || undefined } })
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
                    onChange({ ...models, [key]: { ...m, temperature: v || undefined } })
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
            </div>
          </div>
        )
      })}
    </div>
  )
}
