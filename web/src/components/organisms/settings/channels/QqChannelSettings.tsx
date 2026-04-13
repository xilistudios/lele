import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import type { SecretValue } from '../../../../lib/types'
import {
  BooleanInput,
  SecretInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
} from '../../../molecules'

export function QqChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.qqChannel')}
      isRestartRequired={isRestartRequired('channels.qq')}
    >
      <SettingsField
        label={t('settings.fields.qqEnabled')}
        path="channels.qq.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.qq.enabled')}
      >
        <BooleanInput
          id="channels.qq.enabled"
          value={ch.qq.enabled}
          onChange={(v) => updateField('channels.qq.enabled', v)}
        />
      </SettingsField>
      {ch.qq.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.qqAppId')}
            path="channels.qq.app_id"
            isDirty={isDirtyPath(dirtyPaths, 'channels.qq.app_id')}
          >
            <SecretInput
              id="channels.qq.app_id"
              value={ch.qq.app_id}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.qq.app_id', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.qqAppSecret')}
            path="channels.qq.app_secret"
            isDirty={isDirtyPath(dirtyPaths, 'channels.qq.app_secret')}
          >
            <SecretInput
              id="channels.qq.app_secret"
              value={ch.qq.app_secret}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.qq.app_secret', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.qqAllowFrom')}
            path="channels.qq.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.qq.allow_from')}
          >
            <StringListEditor
              id="channels.qq.allow_from"
              value={ch.qq.allow_from || []}
              onChange={(v) => updateField('channels.qq.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
