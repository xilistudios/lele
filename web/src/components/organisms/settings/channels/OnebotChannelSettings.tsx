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

export function OnebotChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, updateSecretField, isRestartRequired, t } =
    useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.onebotChannel')}
      isRestartRequired={isRestartRequired('channels.onebot')}
    >
      <SettingsField
        label={t('settings.fields.onebotEnabled')}
        path="channels.onebot.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.enabled')}
      >
        <BooleanInput
          id="channels.onebot.enabled"
          value={ch.onebot.enabled}
          onChange={(v) => updateField('channels.onebot.enabled', v)}
        />
      </SettingsField>
      {ch.onebot.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.onebotWsUrl')}
            path="channels.onebot.ws_url"
            isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.ws_url')}
          >
            <TextInput
              id="channels.onebot.ws_url"
              value={ch.onebot.ws_url}
              onChange={(v) => updateField('channels.onebot.ws_url', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.onebotAccessToken')}
            path="channels.onebot.access_token"
            isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.access_token')}
          >
            <SecretInput
              id="channels.onebot.access_token"
              value={ch.onebot.access_token}
              onChange={(v: SecretValue) =>
                updateSecretField('channels.onebot.access_token', v.mode, v.value, v.env_name)
              }
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.onebotReconnectInterval')}
            path="channels.onebot.reconnect_interval"
            isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.reconnect_interval')}
          >
            <NumberInput
              id="channels.onebot.reconnect_interval"
              value={ch.onebot.reconnect_interval}
              onChange={(v) => updateField('channels.onebot.reconnect_interval', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.onebotGroupTriggerPrefix')}
            path="channels.onebot.group_trigger_prefix"
            isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.group_trigger_prefix')}
          >
            <StringListEditor
              id="channels.onebot.group_trigger_prefix"
              value={ch.onebot.group_trigger_prefix || []}
              onChange={(v) => updateField('channels.onebot.group_trigger_prefix', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.onebotAllowFrom')}
            path="channels.onebot.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.onebot.allow_from')}
          >
            <StringListEditor
              id="channels.onebot.allow_from"
              value={ch.onebot.allow_from || []}
              onChange={(v) => updateField('channels.onebot.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
