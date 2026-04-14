import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  KeyValueEditor,
  NumberInput,
  SettingsField,
  SettingsSection,
  TextInput,
} from '../../molecules'

export function SessionSettings() {
  const { draftConfig, dirtyPaths, updateField, t } = useSettings()

  if (!draftConfig?.session) return null
  const session = draftConfig.session

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.session')}>
        <SettingsField
          label={t('settings.fields.ephemeral')}
          path="session.ephemeral"
          isDirty={isDirtyPath(dirtyPaths, 'session.ephemeral')}
        >
          <BooleanInput
            id="session.ephemeral"
            value={session.ephemeral}
            onChange={(v) => updateField('session.ephemeral', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.ephemeralThreshold')}
          path="session.ephemeral_threshold"
          description={t('settings.descriptions.ephemeralThreshold')}
          isDirty={isDirtyPath(dirtyPaths, 'session.ephemeral_threshold')}
        >
          <NumberInput
            id="session.ephemeral_threshold"
            value={session.ephemeral_threshold}
            onChange={(v) => updateField('session.ephemeral_threshold', v)}
            min={60}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.dmScope')}
          path="session.dm_scope"
          isDirty={isDirtyPath(dirtyPaths, 'session.dm_scope')}
          description={t('settings.descriptions.dmScope')}
        >
          <TextInput
            id="session.dm_scope"
            value={session.dm_scope || ''}
            onChange={(v) => updateField('session.dm_scope', v || undefined)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.identityLinks')}
          path="session.identity_links"
          isDirty={isDirtyPath(dirtyPaths, 'session.identity_links')}
          description={t('settings.descriptions.identityLinks')}
        >
          <KeyValueEditor
            id="session.identity_links"
            value={session.identity_links || {}}
            onChange={(v) => updateField('session.identity_links', v)}
            keyPlaceholder={t('settings.identityKeyPlaceholder')}
          />
        </SettingsField>
      </SettingsSection>
    </div>
  )
}
