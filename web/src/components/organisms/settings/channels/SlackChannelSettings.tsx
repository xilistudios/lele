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

export function SlackChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.slackChannel')}
      isRestartRequired={isRestartRequired('channels.slack')}
    >
      <SettingsField
        label={t('settings.fields.slackEnabled')}
        path="channels.slack.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.slack.enabled')}
      >
        <BooleanInput
          id="channels.slack.enabled"
          value={ch.slack.enabled}
          onChange={(v) => updateField('channels.slack.enabled', v)}
        />
      </SettingsField>
      {ch.slack.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.slackBotToken')}
            path="channels.slack.bot_token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.slack.bot_token')}
          >
            <SecretInput
              id="channels.slack.bot_token"
              value={ch.slack.bot_token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.slack.bot_token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.slackAppToken')}
            path="channels.slack.app_token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.slack.app_token')}
          >
            <SecretInput
              id="channels.slack.app_token"
              value={ch.slack.app_token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.slack.app_token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.slackAllowFrom')}
            path="channels.slack.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.slack.allow_from')}
          >
            <StringListEditor
              id="channels.slack.allow_from"
              value={ch.slack.allow_from || []}
              onChange={(v) => updateField('channels.slack.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
