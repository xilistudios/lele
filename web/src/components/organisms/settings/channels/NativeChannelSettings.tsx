import { useSettings } from '../../../../contexts/SettingsContext'
import { getErrorForPath, isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  NumberInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../../molecules'

export function NativeChannelSettings() {
  const { draftConfig, dirtyPaths, validationErrors, updateField, isRestartRequired, t } =
    useSettings()

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
          <SettingsField
            label={t('settings.fields.nativeRateLimitEnabled')}
            path="channels.native.rate_limit.enabled"
            isDirty={isDirtyPath(dirtyPaths, 'channels.native.rate_limit.enabled')}
            description={t('settings.fields.nativeRateLimitEnabledHint')}
            error={getErrorForPath(validationErrors, 'channels.native.rate_limit.enabled')}
          >
            <BooleanInput
              id="channels.native.rate_limit.enabled"
              value={ch.native.rate_limit.enabled}
              onChange={(v) => updateField('channels.native.rate_limit.enabled', v)}
            />
          </SettingsField>
          {ch.native.rate_limit.enabled && (
            <>
              <SettingsField
                label={t('settings.fields.nativeRateLimitPin')}
                path="channels.native.rate_limit.pin_per_minute"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.rate_limit.pin_per_minute')}
                error={getErrorForPath(
                  validationErrors,
                  'channels.native.rate_limit.pin_per_minute',
                )}
              >
                <NumberInput
                  id="channels.native.rate_limit.pin_per_minute"
                  value={ch.native.rate_limit.pin_per_minute}
                  onChange={(v) => updateField('channels.native.rate_limit.pin_per_minute', v)}
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeRateLimitPair')}
                path="channels.native.rate_limit.pair_per_minute"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.rate_limit.pair_per_minute')}
                error={getErrorForPath(
                  validationErrors,
                  'channels.native.rate_limit.pair_per_minute',
                )}
              >
                <NumberInput
                  id="channels.native.rate_limit.pair_per_minute"
                  value={ch.native.rate_limit.pair_per_minute}
                  onChange={(v) => updateField('channels.native.rate_limit.pair_per_minute', v)}
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeRateLimitRefresh')}
                path="channels.native.rate_limit.refresh_per_minute"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.rate_limit.refresh_per_minute')}
                error={getErrorForPath(
                  validationErrors,
                  'channels.native.rate_limit.refresh_per_minute',
                )}
              >
                <NumberInput
                  id="channels.native.rate_limit.refresh_per_minute"
                  value={ch.native.rate_limit.refresh_per_minute}
                  onChange={(v) => updateField('channels.native.rate_limit.refresh_per_minute', v)}
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeRateLimitApi')}
                path="channels.native.rate_limit.api_per_minute"
                isDirty={isDirtyPath(dirtyPaths, 'channels.native.rate_limit.api_per_minute')}
                error={getErrorForPath(
                  validationErrors,
                  'channels.native.rate_limit.api_per_minute',
                )}
              >
                <NumberInput
                  id="channels.native.rate_limit.api_per_minute"
                  value={ch.native.rate_limit.api_per_minute}
                  onChange={(v) => updateField('channels.native.rate_limit.api_per_minute', v)}
                  min={0}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.nativeRateLimitWs')}
                path="channels.native.rate_limit.ws_messages_per_minute"
                isDirty={isDirtyPath(
                  dirtyPaths,
                  'channels.native.rate_limit.ws_messages_per_minute',
                )}
                error={getErrorForPath(
                  validationErrors,
                  'channels.native.rate_limit.ws_messages_per_minute',
                )}
              >
                <NumberInput
                  id="channels.native.rate_limit.ws_messages_per_minute"
                  value={ch.native.rate_limit.ws_messages_per_minute}
                  onChange={(v) =>
                    updateField('channels.native.rate_limit.ws_messages_per_minute', v)
                  }
                  min={0}
                />
              </SettingsField>
            </>
          )}
        </>
      )}
    </SettingsSection>
  )
}
