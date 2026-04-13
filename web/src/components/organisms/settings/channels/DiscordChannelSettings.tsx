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

export function DiscordChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.discordChannel')}
      isRestartRequired={isRestartRequired('channels.discord')}
    >
      <SettingsField
        label={t('settings.fields.discordEnabled')}
        path="channels.discord.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.discord.enabled')}
      >
        <BooleanInput
          id="channels.discord.enabled"
          value={ch.discord.enabled}
          onChange={(v) => updateField('channels.discord.enabled', v)}
        />
      </SettingsField>
      {ch.discord.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.discordToken')}
            path="channels.discord.token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.discord.token')}
          >
            <SecretInput
              id="channels.discord.token"
              value={ch.discord.token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.discord.token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.discordAllowFrom')}
            path="channels.discord.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.discord.allow_from')}
          >
            <StringListEditor
              id="channels.discord.allow_from"
              value={ch.discord.allow_from || []}
              onChange={(v) => updateField('channels.discord.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
