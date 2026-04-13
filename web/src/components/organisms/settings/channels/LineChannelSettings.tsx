import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import type { SecretValue } from '../../../../lib/types'
import {
  BooleanInput,
  NumberInput,
  SecretInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../../molecules'

export function LineChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.lineChannel')}
      isRestartRequired={isRestartRequired('channels.line')}
    >
      <SettingsField
        label={t('settings.fields.lineEnabled')}
        path="channels.line.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.line.enabled')}
      >
        <BooleanInput
          id="channels.line.enabled"
          value={ch.line.enabled}
          onChange={(v) => updateField('channels.line.enabled', v)}
        />
      </SettingsField>
      {ch.line.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.lineChannelSecret')}
            path="channels.line.channel_secret"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.channel_secret')}
          >
            <SecretInput
              id="channels.line.channel_secret"
              value={ch.line.channel_secret}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.line.channel_secret', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.lineChannelAccessToken')}
            path="channels.line.channel_access_token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.channel_access_token')}
          >
            <SecretInput
              id="channels.line.channel_access_token"
              value={ch.line.channel_access_token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.line.channel_access_token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.lineWebhookHost')}
            path="channels.line.webhook_host"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.webhook_host')}
          >
            <TextInput
              id="channels.line.webhook_host"
              value={ch.line.webhook_host}
              onChange={(v) => updateField('channels.line.webhook_host', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.lineWebhookPort')}
            path="channels.line.webhook_port"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.webhook_port')}
          >
            <NumberInput
              id="channels.line.webhook_port"
              value={ch.line.webhook_port}
              onChange={(v) => updateField('channels.line.webhook_port', v)}
              min={1}
              max={65535}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.lineWebhookPath')}
            path="channels.line.webhook_path"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.webhook_path')}
          >
            <TextInput
              id="channels.line.webhook_path"
              value={ch.line.webhook_path}
              onChange={(v) => updateField('channels.line.webhook_path', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.lineAllowFrom')}
            path="channels.line.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.line.allow_from')}
          >
            <StringListEditor
              id="channels.line.allow_from"
              value={ch.line.allow_from || []}
              onChange={(v) => updateField('channels.line.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
