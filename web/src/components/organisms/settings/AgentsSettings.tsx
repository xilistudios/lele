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

  const getThinkingEnabled = (agent: { reasoning?: { enable?: boolean } } | undefined): boolean => {
    return agent?.reasoning?.enable ?? false
  }

  const toggleThinking = (index: number, agent: Record<string, unknown>, enabled: boolean) => {
    const current = (agent as Record<string, unknown>).reasoning as
      | Record<string, unknown>
      | undefined
    updateField(`agents.list.${index}.reasoning`, {
      ...(current ?? {}),
      enable: enabled,
    })
  }

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.sections.agentsList')}>
        {/* Header with Add button */}
        <div className="flex items-center justify-between mb-4">
          <p className="text-sm text-text-secondary">
            {t('settings.descriptions.agentsList') ||
              'Create and manage AI agents with custom configurations'}
          </p>
          <button
            type="button"
            onClick={() => setShowWizard(true)}
            className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500 transition-all duration-200 shadow-sm hover:shadow-md"
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('settings.addAgentModal.addButton')}
          </button>
        </div>

        {/* Empty state */}
        {list.length === 0 && (
          <div className="flex flex-col items-center justify-center py-12 border-2 border-dashed border-border rounded-xl bg-background-secondary/20">
            <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-blue-500/20 to-purple-500/20 flex items-center justify-center mb-4">
              <svg
                width="32"
                height="32"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                className="text-blue-400"
              >
                <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2" />
                <circle cx="12" cy="7" r="4" />
              </svg>
            </div>
            <p className="text-sm text-text-secondary mb-2">
              {t('settings.noAgents')}
            </p>
            <button
              type="button"
              onClick={() => setShowWizard(true)}
              className="inline-flex items-center gap-2 rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-500 transition-all duration-200"
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
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
              default?: boolean
              workspace?: string
              model?: { primary?: string; fallbacks?: string[] }
              skills?: string[]
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
                  <div className="flex items-center gap-3">
                    {/* Agent avatar */}
                    <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-blue-500 to-purple-500 flex items-center justify-center text-xs text-white font-medium flex-shrink-0">
                      {agent.name ? agent.name.charAt(0).toUpperCase() : agent.id.charAt(0).toUpperCase()}
                    </div>
                    <span className="font-medium">{agent.id}</span>
                    {agent.name && (
                      <span className="text-text-tertiary text-sm">— {agent.name}</span>
                    )}
                    {agent.default && (
                      <span className="rounded-full bg-accent-subtle text-accent-primary px-2 py-0.5 text-xs font-medium">
                        {t('settings.defaultBadge')}
                      </span>
                    )}
                    {isModified && (
                      <span className="rounded-full bg-state-info-light text-state-info px-2 py-0.5 text-xs">
                        {t('settings.modifiedBadge')}
                      </span>
                    )}
                    <button
                      type="button"
                      onClick={(e) => {
                        e.stopPropagation()
                        navigate(`/settings/agent/${encodeURIComponent(agent.id)}`)
                      }}
                      className="ml-auto rounded-lg px-2.5 py-1 text-xs font-medium text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-colors flex items-center gap-1.5"
                      title="Edit context files"
                    >
                      <svg
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                      >
                        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
                        <polyline points="14 2 14 8 20 8" />
                        <line x1="16" y1="13" x2="8" y2="13" />
                        <line x1="16" y1="17" x2="8" y2="17" />
                      </svg>
                      Files
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

                  <SettingsField
                    label={t('settings.fields.agentMaxIterations')}
                    description={t('settings.descriptions.agentMaxIterations')}
                    path={`agents.list.${index}.max_iterations`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.max_iterations`)}
                  >
                    <NumberInput
                      id={`agents.list.${index}.max_iterations`}
                      value={agent.max_iterations ?? 10}
                      min={1}
                      max={100}
                      step={1}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.max_iterations`, v === 10 ? undefined : v)
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentMaxTokens')}
                    description={t('settings.descriptions.agentMaxTokens')}
                    path={`agents.list.${index}.max_tokens`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.max_tokens`)}
                  >
                    <NumberInput
                      id={`agents.list.${index}.max_tokens`}
                      value={agent.max_tokens ?? 4096}
                      min={256}
                      max={128000}
                      step={256}
                      onChange={(v) =>
                        updateField(`agents.list.${index}.max_tokens`, v === 4096 ? undefined : v)
                      }
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentContextWindow')}
                    description={t('settings.descriptions.agentContextWindow')}
                    path={`agents.list.${index}.context_window`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.context_window`)}
                  >
                    <NumberInput
                      id={`agents.list.${index}.context_window`}
                      value={agent.context_window ?? 128000}
                      min={4096}
                      max={2097152}
                      step={4096}
                      onChange={(v) =>
                        updateField(
                          `agents.list.${index}.context_window`,
                          v === 128000 ? undefined : v,
                        )
                      }
                    />
                  </SettingsField>
                </div>

                {/* Section: Features */}
                <div className="pb-4 mb-5 border-b border-border-light">
                  <div className="text-xs font-medium text-text-tertiary uppercase tracking-wider mb-4">
                    {t('settings.sections.features')}
                  </div>

                  <SettingsField
                    label={t('settings.fields.agentThinking')}
                    description={t('settings.descriptions.agentThinking')}
                    path={`agents.list.${index}.reasoning.enable`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.reasoning.enable`)}
                  >
                    <BooleanInput
                      id={`agents.list.${index}.reasoning.enable`}
                      value={getThinkingEnabled(
                        agent as Record<string, unknown> as {
                          reasoning?: { enable?: boolean }
                        },
                      )}
                      onChange={(v) => toggleThinking(index, agent as Record<string, unknown>, v)}
                    />
                  </SettingsField>

                  <SettingsField
                    label={t('settings.fields.agentSupportsImages')}
                    description={t('settings.descriptions.agentSupportsImages')}
                    path={`agents.list.${index}.supports_images`}
                    isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.supports_images`)}
                  >
                    <BooleanInput
                      id={`agents.list.${index}.supports_images`}
                      value={agent.supports_images ?? false}
                      onChange={(v) =>
                        updateField(
                          `agents.list.${index}.supports_images`,
                          v === false ? undefined : v,
                        )
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
              </NamedItemCard>
            )
          },
        )}
      </SettingsSection>

      <AddAgentModal isOpen={showWizard} onClose={() => setShowWizard(false)} />
    </div>
  )
}
