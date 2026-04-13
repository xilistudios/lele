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

export function MaixcamChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, isRestartRequired, t } = useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.maixcamChannel')}
      isRestartRequired={isRestartRequired('channels.maixcam')}
    >
      <SettingsField
        label={t('settings.fields.maixcamEnabled')}
        path="channels.maixcam.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.enabled')}
      >
        <BooleanInput
          id="channels.maixcam.enabled"
          value={ch.maixcam.enabled}
          onChange={(v) => updateField('channels.maixcam.enabled', v)}
        />
      </SettingsField>
      {ch.maixcam.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.maixcamHost')}
            path="channels.maixcam.host"
            isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.host')}
          >
            <TextInput
              id="channels.maixcam.host"
              value={ch.maixcam.host}
              onChange={(v) => updateField('channels.maixcam.host', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.maixcamPort')}
            path="channels.maixcam.port"
            isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.port')}
          >
            <NumberInput
              id="channels.maixcam.port"
              value={ch.maixcam.port}
              onChange={(v) => updateField('channels.maixcam.port', v)}
              min={1}
              max={65535}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.maixcamAllowFrom')}
            path="channels.maixcam.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.maixcam.allow_from')}
          >
            <StringListEditor
              id="channels.maixcam.allow_from"
              value={ch.maixcam.allow_from || []}
              onChange={(v) => updateField('channels.maixcam.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
