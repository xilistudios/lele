import { useSettings } from '../../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../../hooks/useSettingsHelpers'
import { BooleanInput, SettingsField, SettingsSection } from '../../../molecules'

/**
 * Web UI serving toggle (`channels.web`).
 *
 * Own card instead of a field inside the native one: the Channels tab shows one
 * card per channel block, and a toggle that disappears with `channels.native`
 * would be a trap — with the Web UI off the settings page is unreachable, so
 * the control must stay visible no matter what the native channel does.
 *
 * `channels.web` is optional in the document: the save path prunes values that
 * equal their default, so an ABSENT block means enabled. Reading it as
 * `?? true` keeps an existing config from rendering as "off", and writing
 * `false` is what makes the block appear in the saved file.
 */
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
      {/* The SPA needs the native channel for its API and WebSocket: with
          native off, serving the interface only yields a page that cannot
          load. Warn instead of disabling the control — turning the Web UI off
          is exactly what an operator may want to do in that state. */}
      {webEnabled && !nativeEnabled && (
        <p className="text-xs text-state-warning">{t('settings.descriptions.webNeedsNative')}</p>
      )}
    </SettingsSection>
  )
}
