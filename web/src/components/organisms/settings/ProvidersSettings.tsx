import { useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import type { SecretValue } from '../../../lib/types'
import {
  AddItemInput,
  BooleanInput,
  NamedItemCard,
  SecretInput,
  SettingsField,
  SettingsSection,
  TextInput,
} from '../../molecules'
import { ProviderModelsEditor } from './ProviderModelsEditor'

export function ProvidersSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, t } = useSettings()
  const [newProviderName, setNewProviderName] = useState('')

  if (!draftConfig) return null
  const providers = draftConfig.providers || {}
  const names = Object.keys(providers)

  const addProvider = () => {
    const name = newProviderName.trim()
    if (!name) return
    updateField('providers', {
      ...providers,
      [name]: { api_key: { mode: 'empty', has_env_var: false }, api_base: '' },
    })
    setNewProviderName('')
  }

  const removeProvider = (name: string) => {
    const updated = { ...providers }
    delete updated[name]
    updateField('providers', updated)
  }

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.namedProviders')}>
        <AddItemInput
          value={newProviderName}
          onChange={setNewProviderName}
          onAdd={addProvider}
          placeholder={t('settings.providerNamePlaceholder')}
        />

        {names.length === 0 && <p className="text-xs text-[#666]">{t('settings.noProviders')}</p>}

        {names.map((name) => {
          const prov = providers[name]
          return (
            <NamedItemCard
              key={name}
              title={name}
              onRemove={() => removeProvider(name)}
              removeLabel={t('settings.removeProvider')}
            >
              <SettingsField
                label={t('settings.fields.providerType')}
                path={`providers.${name}.type`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.type`)}
              >
                <TextInput
                  id={`providers.${name}.type`}
                  value={prov.type || ''}
                  onChange={(v) => updateField(`providers.${name}.type`, v || undefined)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerApiBase')}
                path={`providers.${name}.api_base`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.api_base`)}
              >
                <TextInput
                  id={`providers.${name}.api_base`}
                  value={prov.api_base}
                  onChange={(v) => updateField(`providers.${name}.api_base`, v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerApiKey')}
                path={`providers.${name}.api_key`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.api_key`)}
              >
                <SecretInput
                  id={`providers.${name}.api_key`}
                  value={prov.api_key}
                  onChange={(v: SecretValue) =>
                    updateSecretField(`providers.${name}.api_key`, v.mode, v.value, v.env_name)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerProxy')}
                path={`providers.${name}.proxy`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.proxy`)}
              >
                <TextInput
                  id={`providers.${name}.proxy`}
                  value={prov.proxy || ''}
                  onChange={(v) => updateField(`providers.${name}.proxy`, v || undefined)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerAuthMethod')}
                path={`providers.${name}.auth_method`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.auth_method`)}
              >
                <TextInput
                  id={`providers.${name}.auth_method`}
                  value={prov.auth_method || ''}
                  onChange={(v) => updateField(`providers.${name}.auth_method`, v || undefined)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerConnectMode')}
                path={`providers.${name}.connect_mode`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.connect_mode`)}
              >
                <TextInput
                  id={`providers.${name}.connect_mode`}
                  value={prov.connect_mode || ''}
                  onChange={(v) => updateField(`providers.${name}.connect_mode`, v || undefined)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerWebSearch')}
                path={`providers.${name}.web_search`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.web_search`)}
              >
                <BooleanInput
                  id={`providers.${name}.web_search`}
                  value={prov.web_search || false}
                  onChange={(v) => updateField(`providers.${name}.web_search`, v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.providerModels')}
                path={`providers.${name}.models`}
                isDirty={isDirtyPath(dirtyPaths, `providers.${name}.models`)}
              >
                <ProviderModelsEditor
                  name={name}
                  models={prov.models || {}}
                  onChange={(v) => updateField(`providers.${name}.models`, v)}
                />
              </SettingsField>
            </NamedItemCard>
          )
        })}
      </SettingsSection>
    </div>
  )
}
