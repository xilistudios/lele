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

export function DingtalkChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.dingtalkChannel')}
      isRestartRequired={isRestartRequired('channels.dingtalk')}
    >
      <SettingsField
        label={t('settings.fields.dingtalkEnabled')}
        path="channels.dingtalk.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.enabled')}
      >
        <BooleanInput
          id="channels.dingtalk.enabled"
          value={ch.dingtalk.enabled}
          onChange={(v) => updateField('channels.dingtalk.enabled', v)}
        />
      </SettingsField>
      {ch.dingtalk.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.dingtalkClientId')}
            path="channels.dingtalk.client_id"
            isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.client_id')}
          >
            <SecretInput
              id="channels.dingtalk.client_id"
              value={ch.dingtalk.client_id}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.dingtalk.client_id', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.dingtalkClientSecret')}
            path="channels.dingtalk.client_secret"
            isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.client_secret')}
          >
            <SecretInput
              id="channels.dingtalk.client_secret"
              value={ch.dingtalk.client_secret}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.dingtalk.client_secret', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.dingtalkAllowFrom')}
            path="channels.dingtalk.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.dingtalk.allow_from')}
          >
            <StringListEditor
              id="channels.dingtalk.allow_from"
              value={ch.dingtalk.allow_from || []}
              onChange={(v) => updateField('channels.dingtalk.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
