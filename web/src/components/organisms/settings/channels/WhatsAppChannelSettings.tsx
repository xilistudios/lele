import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../../molecules'

export function WhatsAppChannelSettings() {
  const { draftConfig, dirtyPaths, updateField, isRestartRequired, t } = useSettings()

  if (!draftConfig) return null
  const ch = draftConfig.channels

  return (
    <SettingsSection
      title={t('settings.sections.whatsappChannel')}
      isRestartRequired={isRestartRequired('channels.whatsapp')}
    >
      <SettingsField
        label={t('settings.fields.whatsappEnabled')}
        path="channels.whatsapp.enabled"
        isDirty={isDirtyPath(dirtyPaths, 'channels.whatsapp.enabled')}
      >
        <BooleanInput
          id="channels.whatsapp.enabled"
          value={ch.whatsapp.enabled}
          onChange={(v) => updateField('channels.whatsapp.enabled', v)}
        />
      </SettingsField>
      {ch.whatsapp.enabled && (
        <>
          <SettingsField
            label={t('settings.fields.whatsappBridgeUrl')}
            path="channels.whatsapp.bridge_url"
            isDirty={isDirtyPath(dirtyPaths, 'channels.whatsapp.bridge_url')}
          >
            <TextInput
              id="channels.whatsapp.bridge_url"
              value={ch.whatsapp.bridge_url}
              onChange={(v) => updateField('channels.whatsapp.bridge_url', v)}
            />
          </SettingsField>
          <SettingsField
            label={t('settings.fields.whatsappAllowFrom')}
            path="channels.whatsapp.allow_from"
            isDirty={isDirtyPath(dirtyPaths, 'channels.whatsapp.allow_from')}
          >
            <StringListEditor
              id="channels.whatsapp.allow_from"
              value={ch.whatsapp.allow_from || []}
              onChange={(v) => updateField('channels.whatsapp.allow_from', v)}
            />
          </SettingsField>
        </>
      )}
    </SettingsSection>
  )
}
