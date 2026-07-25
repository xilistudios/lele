import { useSettings } from '../../../contexts/SettingsContext'
import { isDirtyPath } from '../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  NamedItemCard,
  NumberInput,
  SelectInput,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../molecules'

export function GroupsSettings() {
  const { draftConfig, dirtyPaths, updateField, t } = useSettings()

  if (!draftConfig) return null
  const list = draftConfig.groups?.list || []

  const agentOptions = (draftConfig.agents?.list || []).map((a: { id: string }) => ({
    value: a.id,
    label: a.id,
  }))

  const addProfile = () => {
    updateField('groups.list', [...list, { id: '', participants: [], strategy: 'round_robin' }])
  }

  const removeProfile = (index: number) => {
    updateField(
      'groups.list',
      list.filter((_g: unknown, i: number) => i !== index),
    )
  }

  const strategyOptions = [
    { value: 'round_robin', label: 'round_robin' },
    { value: 'moa', label: 'moa' },
    { value: 'moderator', label: 'moderator' },
    { value: 'pipeline', label: 'pipeline' },
  ]

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.groupsList')}>
        {/* Header with Add button */}
        <div className="flex items-center justify-between mb-4">
          <p className="text-sm text-text-secondary">
            {t('settings.descriptions.groupsList')}
          </p>
          <button
            type="button"
            onClick={addProfile}
            className="inline-flex items-center gap-2 rounded-lg bg-cta-primary px-4 py-2 text-sm font-medium text-text-on-accent hover:bg-cta-hover transition-all duration-200 shadow-sm hover:shadow-md"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <title>Add profile</title>
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('settings.addGroupProfile')}
          </button>
        </div>

        {/* Empty state */}
        {list.length === 0 && (
          <div className="flex flex-col items-center justify-center py-12 border-2 border-dashed border-border rounded-xl bg-background-secondary/20">
            <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-interaction-primary/20 to-brand-morado/20 flex items-center justify-center mb-4">
              <svg
                width="32"
                height="32"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                className="text-interaction-primary"
              >
                <title>Group icon</title>
                <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
                <circle cx="9" cy="7" r="4" />
                <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
                <path d="M16 3.13a4 4 0 0 1 0 7.75" />
              </svg>
            </div>
            <p className="text-sm text-text-secondary mb-2">{t('settings.noGroupProfiles')}</p>
            <button
              type="button"
              onClick={addProfile}
              className="inline-flex items-center gap-2 rounded-lg bg-cta-primary px-4 py-2 text-sm font-medium text-text-on-accent hover:bg-cta-hover transition-all duration-200"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>Add profile</title>
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
              {t('settings.addGroupProfile')}
            </button>
          </div>
        )}

        {/* Group profile cards */}
        {list.map(
          (
            group: {
              id: string
              participants: string[]
              strategy: string
              moderator?: string
              rounds?: number
              max_turns?: number
              max_tokens_per_turn?: number
              total_token_budget?: number
              stop_keywords?: string[]
              parallel?: boolean
            },
            index: number,
          ) => {
            const isModified = Array.from(dirtyPaths).some((p: string) =>
              p.startsWith(`groups.list.${index}.`),
            )

            return (
              <NamedItemCard
                key={group.id || `new-${index}`}
                title={
                  <div className="flex items-center gap-2 sm:gap-3 min-w-0 flex-1">
                    {/* Group avatar */}
                    <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-interaction-primary to-brand-morado flex items-center justify-center text-xs text-text-on-accent font-medium flex-shrink-0">
                      {group.id
                        ? group.id.charAt(0).toUpperCase()
                        : '#'}
                    </div>

                    <div className="flex flex-col min-w-0 flex-1">
                      <div className="flex items-center gap-1.5 sm:gap-2 min-w-0">
                        <span className="font-medium text-sm text-text-primary truncate">
                          {group.id || t('settings.noGroupProfiles')}
                        </span>
                        {isModified && (
                          <span className="rounded-full bg-state-info-light text-state-info px-1.5 py-0.5 text-[9px] sm:text-xs font-medium flex-shrink-0">
                            {t('settings.modifiedBadge')}
                          </span>
                        )}
                      </div>
                      <span className="text-text-tertiary text-xs sm:text-sm truncate">
                        {group.strategy} · {group.participants.length} {t('groups.participants').toLowerCase()}
                      </span>
                    </div>
                  </div>
                }
                onRemove={() => removeProfile(index)}
                removeLabel={t('settings.removeGroupProfile')}
              >
                {/* Section: General */}
                <div className="pb-4 mb-5 border-b border-border-light">
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.general')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.groupId')}
                    description={t('settings.descriptions.groupId')}
                    path={`groups.list.${index}.id`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.id`)}
                  >
                    <TextInput
                      id={`groups.list.${index}.id`}
                      value={group.id || ''}
                      onChange={(v) => updateField(`groups.list.${index}.id`, v)}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupStrategy')}
                    description={t('settings.descriptions.groupStrategy')}
                    path={`groups.list.${index}.strategy`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.strategy`)}
                  >
                    <SelectInput
                      id={`groups.list.${index}.strategy`}
                      value={group.strategy || 'round_robin'}
                      onChange={(v) => updateField(`groups.list.${index}.strategy`, v)}
                      options={strategyOptions}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupParticipants')}
                    description={t('settings.descriptions.groupParticipants')}
                    path={`groups.list.${index}.participants`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.participants`)}
                  >
                    <StringListEditor
                      id={`groups.list.${index}.participants`}
                      value={group.participants || []}
                      onChange={(v) => updateField(`groups.list.${index}.participants`, v)}
                      options={agentOptions}
                      placeholder={t('settings.selectAgent')}
                      emptyLabel={t('settings.noOtherAgents')}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupModerator')}
                    description={t('settings.descriptions.groupModerator')}
                    path={`groups.list.${index}.moderator`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.moderator`)}
                  >
                    <SelectInput
                      id={`groups.list.${index}.moderator`}
                      value={group.moderator || ''}
                      onChange={(v) => updateField(`groups.list.${index}.moderator`, v || undefined)}
                      options={[
                        { value: '', label: t('settings.none') },
                        ...(group.participants || []).map((p: string) => ({
                          value: p,
                          label: p,
                        })),
                      ]}
                    />
                  </SettingsField>
                </div>

                {/* Section: Limits */}
                <div className="pb-4 mb-5 border-b border-border-light">
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.limits')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.groupRounds')}
                    description={t('settings.descriptions.groupRounds')}
                    path={`groups.list.${index}.rounds`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.rounds`)}
                  >
                    <NumberInput
                      id={`groups.list.${index}.rounds`}
                      value={group.rounds ?? 0}
                      min={0}
                      onChange={(v) =>
                        updateField(`groups.list.${index}.rounds`, v === 0 ? undefined : v)
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupMaxTurns')}
                    description={t('settings.descriptions.groupMaxTurns')}
                    path={`groups.list.${index}.max_turns`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.max_turns`)}
                  >
                    <NumberInput
                      id={`groups.list.${index}.max_turns`}
                      value={group.max_turns ?? 0}
                      min={0}
                      onChange={(v) =>
                        updateField(`groups.list.${index}.max_turns`, v === 0 ? undefined : v)
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupMaxTokensPerTurn')}
                    description={t('settings.descriptions.groupMaxTokensPerTurn')}
                    path={`groups.list.${index}.max_tokens_per_turn`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.max_tokens_per_turn`)}
                  >
                    <NumberInput
                      id={`groups.list.${index}.max_tokens_per_turn`}
                      value={group.max_tokens_per_turn ?? 0}
                      min={0}
                      onChange={(v) =>
                        updateField(
                          `groups.list.${index}.max_tokens_per_turn`,
                          v === 0 ? undefined : v,
                        )
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupTotalTokenBudget')}
                    description={t('settings.descriptions.groupTotalTokenBudget')}
                    path={`groups.list.${index}.total_token_budget`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.total_token_budget`)}
                  >
                    <NumberInput
                      id={`groups.list.${index}.total_token_budget`}
                      value={group.total_token_budget ?? 0}
                      min={0}
                      onChange={(v) =>
                        updateField(
                          `groups.list.${index}.total_token_budget`,
                          v === 0 ? undefined : v,
                        )
                      }
                    />
                  </SettingsField>
                </div>

                {/* Section: Behavior */}
                <div>
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.behavior')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.groupParallel')}
                    description={t('settings.descriptions.groupParallel')}
                    path={`groups.list.${index}.parallel`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.parallel`)}
                  >
                    <BooleanInput
                      id={`groups.list.${index}.parallel`}
                      value={group.parallel || false}
                      onChange={(v) =>
                        updateField(`groups.list.${index}.parallel`, v ? true : undefined)
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.groupStopKeywords')}
                    description={t('settings.descriptions.groupStopKeywords')}
                    path={`groups.list.${index}.stop_keywords`}
                    isDirty={isDirtyPath(dirtyPaths, `groups.list.${index}.stop_keywords`)}
                  >
                    <StringListEditor
                      id={`groups.list.${index}.stop_keywords`}
                      value={group.stop_keywords || []}
                      onChange={(v) =>
                        updateField(
                          `groups.list.${index}.stop_keywords`,
                          v.length ? v : undefined,
                        )
                      }
                    />
                  </SettingsField>
                </div>
              </NamedItemCard>
            )
          },
        )}
      </SettingsSection>
    </div>
  )
}
