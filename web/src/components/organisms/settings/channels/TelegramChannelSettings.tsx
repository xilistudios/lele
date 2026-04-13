import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import type { SecretValue } from '../../../../lib/types'
import {
  BooleanInput,
  SecretInput,
  SelectInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../../molecules'

export function TelegramChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.telegramChannel')}
      isRestartRequired={isRestartRequired('channels.telegram')}
    >
      <SettingsField
        label={t('settings.fields.telegramEnabled')}
        path="channels.telegram.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.enabled')}
      >
        <BooleanInput
          id="channels.telegram.enabled"
          value={ch.telegram.enabled}
          onChange={(v) => updateField('channels.telegram.enabled', v)}
        />
      </SettingsField>
      {ch.telegram.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.telegramToken')}
            path="channels.telegram.token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.token')}
          >
            <SecretInput
              id="channels.telegram.token"
              value={ch.telegram.token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.telegram.token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.telegramProxy')}
            path="channels.telegram.proxy"
            isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.proxy')}
          >
            <TextInput
              id="channels.telegram.proxy"
              value={ch.telegram.proxy || ''}
              onChange={(v) => updateField('channels.telegram.proxy', v || undefined)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.telegramAllowFrom')}
            path="channels.telegram.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.allow_from')}
          >
            <StringListEditor
              id="channels.telegram.allow_from"
              value={ch.telegram.allow_from || []}
              onChange={(v) => updateField('channels.telegram.allow_from', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.telegramVerbose')}
            path="channels.telegram.verbose"
            isDirty={isDirtyPath(dirtyPaths, 'channels.telegram.verbose')}
          >
            <SelectInput
              id="channels.telegram.verbose"
              value={ch.telegram.verbose || 'off'}
              onChange={(v) => updateField('channels.telegram.verbose', v)}
              options={[
                { value: 'off', label: t('settings.options.off') },
                { value: 'basic', label: t('settings.options.basic') },
                { value: 'full', label: t('settings.options.full') },
              ]}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
