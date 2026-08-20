import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useSettings } from '../../../contexts/SettingsContext'
import { getAgentModelPrimary, isDirtyPath } from '../../../hooks/useSettingsHelpers'
import {
  BooleanInput,
  NamedItemCard,
  NumberInput,
  SearchableSelect,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../molecules'
import { AddAgentModal } from './AddAgentWizard'

export function AgentsSettings() {
  const {
    draftConfig,
    dirtyPaths,
    updateField,
    t,
    getOptionsForAgent,
    getGroupsForAgent,
    isLoadingModels,
  } = useSettings()

  const navigate = useNavigate()
  const [showWizard, setShowWizard] = useState(false)

  if (!draftConfig) return null
  const list = draftConfig.agents.list || []

  const removeAgent = (index: number) => {
    updateField(
      'agents.list',
      list.filter((_a: unknown, i: number) => i !== index),
    )
  }

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.agentsList')}>
        {/* Header with Add button */}
        <div className="flex items-center justify-between mb-4">
          <p className="text-sm text-text-secondary">{t('settings.descriptions.agentsList')}</p>
          <button
            type="button"
            onClick={() => setShowWizard(true)}
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
              <title>Add agent</title>
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('settings.addAgentModal.addButton')}
          </button>
        </div>

        {/* Empty state */}
        {list.length === 0 && (
          <div className="flex flex-col items-center justify-center py-12 border-2 border-dashed border-border rounded-xl bg-background-secondary/20">
            <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-interaction-primary/20 to-brand-morado/20 flex items-center justify-center mb-4">
              {' '}
              <svg
                width="32"
                height="32"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                className="text-interaction-primary"
              >
                <title>Agent icon</title>
                <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
                <circle cx="12" cy="7" r="4" />
              </svg>
            </div>
            <p className="text-sm text-text-secondary mb-2">{t('settings.noAgents')}</p>
            <button
              type="button"
              onClick={() => setShowWizard(true)}
              className="inline-flex items-center gap-2 rounded-lg bg-cta-primary px-4 py-2 text-sm font-medium text-text-on-accent hover:bg-cta-hover transition-all duration-200"
            >
              {' '}
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>Add agent</title>
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
              {t('settings.addAgentModal.addButton')}
            </button>
          </div>
        )}

        {/* Agent cards */}
        {list.map(
          (
            agent: {
              id: string
              name?: string
              description?: string
              default?: boolean
              workspace?: string
              model?: { primary?: string; fallbacks?: string[] }
              skills?: string[]
              subagents?: { allow_agents?: string[] }
              temperature?: number
              max_iterations?: number
              max_tokens?: number
              context_window?: number
              supports_images?: boolean
              reasoning?: { enable?: boolean }
            },
            index: number,
          ) => {
            const isModified = Array.from(dirtyPaths).some((p: string) =>
              p.startsWith(`agents.list.${index}.`),
            )

            return (
              <NamedItemCard
                key={agent.id}
                title={
                  <div className="flex items-center gap-2 sm:gap-3 min-w-0 flex-1">
                    {/* Agent avatar */}
                    <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-interaction-primary to-brand-morado flex items-center justify-center text-xs text-text-on-accent font-medium flex-shrink-0">
                      {agent.name
                        ? agent.name.charAt(0).toUpperCase()
                        : agent.id.charAt(0).toUpperCase()}
                    </div>

                    <div className="flex flex-col min-w-0 flex-1">
                      <div className="flex items-center gap-1.5 sm:gap-2 min-w-0">
                        <span className="font-medium text-sm text-text-primary truncate">
                          {agent.id}
                        </span>
                        {agent.default && (
                          <span className="rounded-full bg-accent-subtle text-accent-primary px-1.5 py-0.5 text-[9px] sm:text-xs font-medium flex-shrink-0">
                            {t('settings.defaultBadge')}
                          </span>
                        )}
                        {isModified && (
                          <span className="rounded-full bg-state-info-light text-state-info px-1.5 py-0.5 text-[9px] sm:text-xs font-medium flex-shrink-0">
                            {t('settings.modifiedBadge')}
                          </span>
                        )}
                      </div>
                      {agent.name && (
                        <span className="text-text-tertiary text-xs sm:text-sm truncate">
                          {agent.name}
                        </span>
                      )}
                    </div>

                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(`/settings/agent/${encodeURIComponent(agent.id)}`)
                      }}
                      className="ml-auto rounded-lg px-2 sm:px-2.5 py-1 text-xs font-medium text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-colors flex items-center gap-1.5 flex-shrink-0"
                      title={t('settings.agentFilesTooltip')}
                    >
                      <svg
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                        className="flex-shrink-0"
                      >
                        <title>Edit files</title>
                        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                        <polyline points="14 2 14 8 20 8" />
                        <line x1="16" y1="13" x2="8" y2="13" />
                        <line x1="16" y1="17" x2="8" y2="17" />
                      </svg>
                      <span className="hidden sm:inline">{t('settings.agentFilesButton')}</span>
                    </button>
                  </div>
                }
                onRemove={() => removeAgent(index)}
                removeLabel={t('settings.removeAgent')}
              >
                {/* Section: General */}
                <div className="pb-4 mb-5 border-b border-border-light">
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.general')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.agentName')}
                    description={t('settings.descriptions.agentName')}
                    path={`agents.list.${index}.name`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.name`)}
                  >
                    <TextInput
                      id={`agents.list.${index}.name`}
                      value={agent.name || ''}
                      onChange={(v) => updateField(`agents.list.${index}.name`, v || undefined)}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentDescription')}
                    description={t('settings.descriptions.agentDescription')}
                    path={`agents.list.${index}.description`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.description`)}
                  >
                    <TextInput
                      id={`agents.list.${index}.description`}
                      value={agent.description || ''}
                      placeholder={t('settings.placeholders.agentDescription')}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.description`, v || undefined)
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentDefault')}
                    description={t('settings.descriptions.agentDefault')}
                    path={`agents.list.${index}.default`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.default`)}
                  >
                    <BooleanInput
                      id={`agents.list.${index}.default`}
                      value={agent.default || false}
                      onChange={(v) => updateField(`agents.list.${index}.default`, v)}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentWorkspace')}
                    description={t('settings.descriptions.agentWorkspace')}
                    path={`agents.list.${index}.workspace`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.workspace`)}
                  >
                    <TextInput
                      id={`agents.list.${index}.workspace`}
                      value={agent.workspace || ''}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.workspace`, v || undefined)
                      }
                    />
                  </SettingsField>
                </div>

                {/* Section: Model */}
                <div className="pb-4 mb-5 border-b border-border-light">
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.model')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.agentModelPrimary')}
                    description={t('settings.descriptions.agentModelPrimary')}
                    path={`agents.list.${index}.model.primary`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.model.primary`)}
                  >
                    <SearchableSelect
                      ariaLabel={t('settings.fields.agentModelPrimary')}
                      buttonLabel={t('settings.fields.agentModelPrimary')}
                      direction="down"
                      emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
                      groups={getGroupsForAgent}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.model`, {
                          ...agent.model,
                          primary: v,
                        })
                      }
                      options={getOptionsForAgent}
                      placeholder={getAgentModelPrimary(agent.model) || t('settings.selectModel')}
                      searchAriaLabel={`${t('settings.fields.agentModelPrimary')} search`}
                      searchPlaceholder={t('settings.fields.agentModelPrimary')}
                      value={getAgentModelPrimary(agent.model)}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentModelFallbacks')}
                    description={t('settings.descriptions.agentModelFallbacks')}
                    path={`agents.list.${index}.model.fallbacks`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.model.fallbacks`)}
                  >
                    <StringListEditor
                      id={`agents.list.${index}.model.fallbacks`}
                      value={agent.model?.fallbacks || []}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.model`, {
                          primary: agent.model?.primary || '',
                          fallbacks: v,
                        })
                      }
                      options={getOptionsForAgent}
                      groups={getGroupsForAgent}
                      emptyLabel={isLoadingModels ? t('settings.loading') : t('settings.noModels')}
                      loading={isLoadingModels}
                    />
                  </SettingsField>
                </div>

                {/* Section: Behavior */}
                <div className="pb-4 mb-5 border-b border-border-light">
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.behavior')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.agentTemperature')}
                    description={t('settings.descriptions.agentTemperature')}
                    path={`agents.list.${index}.temperature`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.temperature`)}
                  >
                    <NumberInput
                      id={`agents.list.${index}.temperature`}
                      value={agent.temperature ?? 0.7}
                      min={0}
                      max={2}
                      step={0.1}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.temperature`, v === 0.7 ? undefined : v)
                      }
                    />
                  </SettingsField>
                </div>
                {/* Section: Skills */}
                <div>
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.skills')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.agentSkills')}
                    description={t('settings.descriptions.agentSkills')}
                    path={`agents.list.${index}.skills`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.skills`)}
                  >
                    <StringListEditor
                      id={`agents.list.${index}.skills`}
                      value={agent.skills || []}
                      onChange={(v) => updateField(`agents.list.${index}.skills`, v)}
                    />
                  </SettingsField>
                </div>

                {/* Section: Subagents */}
                <div>
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.subagents')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.agentSubagentsEnabled')}
                    description={t('settings.descriptions.agentSubagentsEnabled')}
                    path={`agents.list.${index}.subagents`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.subagents`)}
                  >
                    <BooleanInput
                      id={`agents.list.${index}.subagents.enabled`}
                      value={
                        agent.subagents?.allow_agents !== undefined &&
                        agent.subagents?.allow_agents !== null
                      }
                      onChange={(v) => {
                        if (v) {
                          // Enable subagents - create empty config with all other agents as options
                          const otherAgents = list
                            .filter((a: { id: string }) => a.id !== agent.id)
                            .map((a: { id: string }) => a.id)
                          updateField(`agents.list.${index}.subagents`, {
                            allow_agents: otherAgents.length > 0 ? otherAgents : [],
                          })
                        } else {
                          // Disable subagents - remove the config
                          updateField(`agents.list.${index}.subagents`, undefined)
                        }
                      }}
                    />
                  </SettingsField>

                  {agent.subagents?.allow_agents !== undefined && (
                    <SettingsField
                      label={t('settings.fields.agentSubagentsAllowed')}
                      description={t('settings.descriptions.agentSubagentsAllowed')}
                      path={`agents.list.${index}.subagents.allow_agents`}
                      isDirty={isDirtyPath(
                        dirtyPaths,
                        `agents.list.${index}.subagents.allow_agents`,
                      )}
                    >
                      <StringListEditor
                        id={`agents.list.${index}.subagents.allow_agents`}
                        value={agent.subagents?.allow_agents || []}
                        onChange={(v) =>
                          updateField(`agents.list.${index}.subagents`, {
                            ...agent.subagents,
                            allow_agents: v,
                          })
                        }
                        options={list
                          .filter((a: { id: string }) => a.id !== agent.id)
                          .map((a: { id: string; name?: string }) => ({
                            value: a.id,
                            label: a.name ? `${a.id} (${a.name})` : a.id,
                          }))}
                        placeholder={t('settings.selectAgent')}
                        emptyLabel={t('settings.noOtherAgents')}
                      />
                    </SettingsField>
                  )}
                </div>
              </NamedItemCard>
            )
          },
        )}
      </SettingsSection>

      <AddAgentModal isOpen={showWizard} onClose={() => setShowWizard(false)} />
    </div>
  )
}
