import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  NumberInput,
  SelectInput,
  SettingsField,
  SettingsSection,
  TextInput,
} from '../../molecules'

export function SystemSettings() {
  const { draftConfig, dirtyPaths, updateField, isRestartRequired, t } = useSettings()

  if (!draftConfig) return null
  const config = draftConfig

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.sections.heartbeat')}
        isRestartRequired={isRestartRequired('heartbeat')}
      >
        <SettingsField
          label={t('settings.fields.heartbeatEnabled')}
          path="heartbeat.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'heartbeat.enabled')}
        >
          <BooleanInput
            id="heartbeat.enabled"
            value={config.heartbeat.enabled}
            onChange={(v) => updateField('heartbeat.enabled', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.heartbeatInterval')}
          path="heartbeat.interval"
          description={t('settings.descriptions.heartbeatInterval')}
          isDirty={isDirtyPath(dirtyPaths, 'heartbeat.interval')}
        >
          <NumberInput
            id="heartbeat.interval"
            value={config.heartbeat.interval}
            onChange={(v) => updateField('heartbeat.interval', v)}
            min={5}
          />
        </SettingsField>
      </SettingsSection>

      <SettingsSection
        title={t('settings.sections.devices')}
        isRestartRequired={isRestartRequired('devices')}
      >
        <SettingsField
          label={t('settings.fields.devicesEnabled')}
          path="devices.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'devices.enabled')}
        >
          <BooleanInput
            id="devices.enabled"
            value={config.devices.enabled}
            onChange={(v) => updateField('devices.enabled', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.monitorUsb')}
          path="devices.monitor_usb"
          isDirty={isDirtyPath(dirtyPaths, 'devices.monitor_usb')}
        >
          <BooleanInput
            id="devices.monitor_usb"
            value={config.devices.monitor_usb}
            onChange={(v) => updateField('devices.monitor_usb', v)}
          />
        </SettingsField>
      </SettingsSection>

      <SettingsSection title={t('settings.sections.logs')}>
        <SettingsField
          label={t('settings.fields.logsEnabled')}
          path="logs.enabled"
          isDirty={isDirtyPath(dirtyPaths, 'logs.enabled')}
        >
          <BooleanInput
            id="logs.enabled"
            value={config.logs.enabled}
            onChange={(v) => updateField('logs.enabled', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.logsPath')}
          path="logs.path"
          isDirty={isDirtyPath(dirtyPaths, 'logs.path')}
        >
          <TextInput
            id="logs.path"
            value={config.logs.path || ''}
            onChange={(v) => updateField('logs.path', v || undefined)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.logsMaxDays')}
          path="logs.max_days"
          isDirty={isDirtyPath(dirtyPaths, 'logs.max_days')}
        >
          <NumberInput
            id="logs.max_days"
            value={config.logs.max_days || 7}
            onChange={(v) => updateField('logs.max_days', v)}
            min={1}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.logsRotation')}
          path="logs.rotation"
          isDirty={isDirtyPath(dirtyPaths, 'logs.rotation')}
        >
          <SelectInput
            id="logs.rotation"
            value={config.logs.rotation || 'daily'}
            onChange={(v) => updateField('logs.rotation', v)}
            options={[
              { value: 'daily', label: t('settings.options.daily') },
              { value: 'weekly', label: t('settings.options.weekly') },
            ]}
          />
        </SettingsField>
      </SettingsSection>
    </div>
  )
}
