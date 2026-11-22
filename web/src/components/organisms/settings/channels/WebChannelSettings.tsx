import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import { BooleanInput, SettingsField, SettingsSection } from '../../../molecules'

export function WebChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, isRestartRequired, t } = useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels
  const webEnabled = ch.web?.enabled ?? true
  const nativeEnabled = ch.native?.enabled ?? false

  return (
    <SettingsSection
      title={t('settings.sections.webChannel')}
      isRestartRequired={isRestartRequired('channels.web')}
    >
      <SettingsField
        label={t('settings.fields.webEnabled')}
        path="channels.web.enabled"
        description={t('settings.descriptions.webEnabled')}
        isDirty={isDirtyPath(dirtyPaths, 'channels.web.enabled')}
      >
        <BooleanInput
          id="channels.web.enabled"
          value={webEnabled}
          onChange={(v) => updateField('channels.web.enabled', v)}
        />
      </SettingsField>
      {webEnabled && !nativeEnabled && (
        <p className="text-xs text-state-warning">{t('settings.descriptions.webNeedsNative')}</p>
      )}
    </SettingsSection>
  )
}
