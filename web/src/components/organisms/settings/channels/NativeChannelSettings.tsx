import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  NumberInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../../molecules'

export function NativeChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, isRestartRequired, t } = useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.nativeChannel')}
      isRestartRequired={isRestartRequired('channels.native')}
    >
      <SettingsField
        label={t('settings.fields.nativeEnabled')}
        path="channels.native.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.native.enabled')}
      >
        <BooleanInput
          id="channels.native.enabled"
          value={ch.native.enabled}
          onChange={(v) => updateField('channels.native.enabled', v)}
        />
      </SettingsField>
      {ch.native.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.nativeHost')}
            path="channels.native.host"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.host')}
          >
            <TextInput
              id="channels.native.host"
              value={ch.native.host}
              onChange={(v) => updateField('channels.native.host', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativePort')}
            path="channels.native.port"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.port')}
          >
            <NumberInput
              id="channels.native.port"
              value={ch.native.port}
              onChange={(v) => updateField('channels.native.port', v)}
              min={1}
              max={65535}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.maxClients')}
            path="channels.native.max_clients"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.max_clients')}
          >
            <NumberInput
              id="channels.native.max_clients"
              value={ch.native.max_clients}
              onChange={(v) => updateField('channels.native.max_clients', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativeTokenExpiryDays')}
            path="channels.native.token_expiry_days"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.token_expiry_days')}
          >
            <NumberInput
              id="channels.native.token_expiry_days"
              value={ch.native.token_expiry_days}
              onChange={(v) => updateField('channels.native.token_expiry_days', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativePinExpiryMinutes')}
            path="channels.native.pin_expiry_minutes"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.pin_expiry_minutes')}
          >
            <NumberInput
              id="channels.native.pin_expiry_minutes"
              value={ch.native.pin_expiry_minutes}
              onChange={(v) => updateField('channels.native.pin_expiry_minutes', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativeSessionExpiryDays')}
            path="channels.native.session_expiry_days"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.session_expiry_days')}
          >
            <NumberInput
              id="channels.native.session_expiry_days"
              value={ch.native.session_expiry_days}
              onChange={(v) => updateField('channels.native.session_expiry_days', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativeMaxUploadSizeMb')}
            path="channels.native.max_upload_size_mb"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.max_upload_size_mb')}
          >
            <NumberInput
              id="channels.native.max_upload_size_mb"
              value={ch.native.max_upload_size_mb}
              onChange={(v) => updateField('channels.native.max_upload_size_mb', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativeUploadTtlHours')}
            path="channels.native.upload_ttl_hours"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.upload_ttl_hours')}
          >
            <NumberInput
              id="channels.native.upload_ttl_hours"
              value={ch.native.upload_ttl_hours}
              onChange={(v) => updateField('channels.native.upload_ttl_hours', v)}
              min={1}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.nativeCorsOrigins')}
            path="channels.native.cors_origins"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.cors_origins')}
          >
            <StringListEditor
              id="channels.native.cors_origins"
              value={ch.native.cors_origins || []}
              onChange={(v) => updateField('channels.native.cors_origins', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
