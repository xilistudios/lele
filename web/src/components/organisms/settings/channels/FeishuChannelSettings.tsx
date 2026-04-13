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

export function FeishuChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.feishuChannel')}
      isRestartRequired={isRestartRequired('channels.feishu')}
    >
      <SettingsField
        label={t('settings.fields.feishuEnabled')}
        path="channels.feishu.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.enabled')}
      >
        <BooleanInput
          id="channels.feishu.enabled"
          value={ch.feishu.enabled}
          onChange={(v) => updateField('channels.feishu.enabled', v)}
        />
      </SettingsField>
      {ch.feishu.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.feishuAppId')}
            path="channels.feishu.app_id"
            isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.app_id')}
          >
            <SecretInput
              id="channels.feishu.app_id"
              value={ch.feishu.app_id}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.feishu.app_id', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.feishuAppSecret')}
            path="channels.feishu.app_secret"
            isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.app_secret')}
          >
            <SecretInput
              id="channels.feishu.app_secret"
              value={ch.feishu.app_secret}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.feishu.app_secret', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.feishuEncryptKey')}
            path="channels.feishu.encrypt_key"
            isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.encrypt_key')}
          >
            <SecretInput
              id="channels.feishu.encrypt_key"
              value={ch.feishu.encrypt_key}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.feishu.encrypt_key', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.feishuVerificationToken')}
            path="channels.feishu.verification_token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.verification_token')}
          >
            <SecretInput
              id="channels.feishu.verification_token"
              value={ch.feishu.verification_token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.feishu.verification_token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.feishuAllowFrom')}
            path="channels.feishu.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.feishu.allow_from')}
          >
            <StringListEditor
              id="channels.feishu.allow_from"
              value={ch.feishu.allow_from || []}
              onChange={(v) => updateField('channels.feishu.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
