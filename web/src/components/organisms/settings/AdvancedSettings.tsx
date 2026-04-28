import { useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import { getErrorForPath, isDirtyPath } from '../../../hooks/useSettingsHelpers'
import type { EditableConfig } from '../../../lib/types'
import { AddButton } from '../../atoms/AddButton'
import { RemoveButton } from '../../atoms/RemoveButton'
import { NumberInput, SettingsField, SettingsSection, TextInput } from '../../molecules'

const CARD_CLS = 'rounded border border-border bg-background-primary p-4'

export function AdvancedSettings() {
  const {
    draftConfig,
    dirtyPaths,
    validationErrors,
    updateField,
    replaceDraft,
    isRestartRequired,
    t,
  } = useSettings()
  const [showRawJson, setShowRawJson] = useState(false)

  if (!draftConfig) return null
  const bindings = draftConfig.bindings || []

  const addBinding = () => {
    updateField('bindings', [...bindings, { agent_id: '', match: { channel: '' } }])
  }

  const removeBinding = (index: number) => {
    updateField(
      'bindings',
      bindings.filter((_b: unknown, i: number) => i !== index),
    )
  }

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.sections.gateway')}
        isRestartRequired={isRestartRequired('gateway')}
      >
        <SettingsField
          label={t('settings.fields.gatewayHost')}
          path="gateway.host"
          isDirty={isDirtyPath(dirtyPaths, 'gateway.host')}
          error={getErrorForPath(validationErrors, 'gateway.host')}
        >
          <TextInput
            id="gateway.host"
            value={draftConfig.gateway.host}
            onChange={(v) => updateField('gateway.host', v)}
          />
        </SettingsField>
        <SettingsField
          label={t('settings.fields.gatewayPort')}
          path="gateway.port"
          isDirty={isDirtyPath(dirtyPaths, 'gateway.port')}
          error={getErrorForPath(validationErrors, 'gateway.port')}
        >
          <NumberInput
            id="gateway.port"
            value={draftConfig.gateway.port}
            onChange={(v) => updateField('gateway.port', v)}
            min={1}
            max={65535}
          />
        </SettingsField>
      </SettingsSection>

      <SettingsSection title={t('settings.sections.bindings')}>
        <div className="mb-4">
          <AddButton onClick={addBinding}>{t('settings.addBinding')}</AddButton>
        </div>

        {bindings.length === 0 && (
          <p className="text-xs text-text-tertiary">{t('settings.noBindings')}</p>
        )}

        {bindings.map(
          (
            binding: {
              agent_id: string
              match: {
                channel: string
                account_id?: string
                guild_id?: string
                team_id?: string
                peer?: { kind: string; id: string }
              }
            },
            index: number,
          ) => (
            <div key={`${binding.agent_id}-${binding.match.channel}-${index}`} className={CARD_CLS}>
              <div className="mb-3 flex items-center justify-between">
                <span className="text-xs font-medium text-text-secondary">
                  {t('settings.sections.bindings')} #{index + 1}
                </span>
                <RemoveButton
                  onClick={() => removeBinding(index)}
                  ariaLabel={t('settings.removeBinding')}
                />
              </div>
              <div className="space-y-3">
                <SettingsField
                  label={t('settings.fields.bindingAgentId')}
                  path={`bindings.${index}.agent_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.agent_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.agent_id`}
                    value={binding.agent_id}
                    onChange={(v) => updateField(`bindings.${index}.agent_id`, v)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingChannel')}
                  path={`bindings.${index}.match.channel`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.channel`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.channel`}
                    value={binding.match.channel}
                    onChange={(v) => updateField(`bindings.${index}.match.channel`, v)}
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingAccountId')}
                  path={`bindings.${index}.match.account_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.account_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.account_id`}
                    value={binding.match.account_id || ''}
                    onChange={(v) =>
                      updateField(`bindings.${index}.match.account_id`, v || undefined)
                    }
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingGuildId')}
                  path={`bindings.${index}.match.guild_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.guild_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.guild_id`}
                    value={binding.match.guild_id || ''}
                    onChange={(v) =>
                      updateField(`bindings.${index}.match.guild_id`, v || undefined)
                    }
                  />
                </SettingsField>
                <SettingsField
                  label={t('settings.fields.bindingTeamId')}
                  path={`bindings.${index}.match.team_id`}
                  isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.team_id`)}
                >
                  <TextInput
                    id={`bindings.${index}.match.team_id`}
                    value={binding.match.team_id || ''}
                    onChange={(v) => updateField(`bindings.${index}.match.team_id`, v || undefined)}
                  />
                </SettingsField>
                {binding.match.peer && (
                  <>
                    <SettingsField
                      label={t('settings.fields.bindingPeerKind')}
                      path={`bindings.${index}.match.peer.kind`}
                      isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.peer.kind`)}
                    >
                      <TextInput
                        id={`bindings.${index}.match.peer.kind`}
                        value={binding.match.peer.kind}
                        onChange={(v) => updateField(`bindings.${index}.match.peer.kind`, v)}
                      />
                    </SettingsField>
                    <SettingsField
                      label={t('settings.fields.bindingPeerId')}
                      path={`bindings.${index}.match.peer.id`}
                      isDirty={isDirtyPath(dirtyPaths, `bindings.${index}.match.peer.id`)}
                    >
                      <TextInput
                        id={`bindings.${index}.match.peer.id`}
                        value={binding.match.peer.id}
                        onChange={(v) => updateField(`bindings.${index}.match.peer.id`, v)}
                      />
                    </SettingsField>
                  </>
                )}
              </div>
            </div>
          ),
        )}
      </SettingsSection>

      <SettingsSection title={t('settings.sections.rawJson')}>
        <div className="mb-3">
          <button
            type="button"
            onClick={() => setShowRawJson(!showRawJson)}
            className="rounded border border-border px-3 py-2 text-xs text-text-secondary transition-colors hover:bg-surface-hover"
          >
            {showRawJson ? t('settings.hideRawJson') : t('settings.showRawJson')}
          </button>
        </div>
        {showRawJson && (
          <textarea
            value={JSON.stringify(draftConfig, null, 2)}
            onChange={(e) => {
              try {
                const parsed = JSON.parse(e.target.value) as EditableConfig
                replaceDraft(parsed)
              } catch {
                // ignore invalid JSON while typing
              }
            }}
            className="h-[500px] w-full rounded border border-border bg-background-primary p-3 font-mono text-xs text-text-primary focus:border-interaction-primary focus:outline-none focus:ring-2 focus:ring-interaction-primary focus:ring-offset-2 focus:ring-offset-background-primary"
            spellCheck={false}
          />
        )}
      </SettingsSection>
    </div>
  )
}
