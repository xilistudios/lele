import { useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import { getAgentModelPrimary, isDirtyPath } from '../../../hooks/useSettingsHelpers'
import {
  AddItemInput,
  BooleanInput,
  NamedItemCard,
  NumberInput,
  SearchableSelect,
  SettingsField,
  SettingsSection,
  StringListEditor,
  TextInput,
} from '../../molecules'

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

  const [newAgentId, setNewAgentId] = useState('')

  if (!draftConfig) return null
  const list = draftConfig.agents.list || []

  const addAgent = () => {
    const id = newAgentId.trim()
    if (!id) return
    updateField('agents.list', [...list, { id, default: false, skills: [] }])
    setNewAgentId('')
  }

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
        <AddItemInput
          value={newAgentId}
          onChange={setNewAgentId}
          onAdd={addAgent}
          placeholder={t('settings.agentIdPlaceholder')}
        />

        {list.length === 0 && (
          <p className="text-xs text-text-tertiary">{t('settings.noAgents')}</p>
        )}

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
                    <span>{agent.id}</span>
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
                  </div>
                }
                onRemove={() => removeAgent(index)}
                removeLabel={t('settings.removeAgent')}
              >
                {/* Section: General */}
                <div className="pb-4 mb-5 border-b border-border/40">
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
                <div className="pb-4 mb-5 border-b border-border/40">
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
                <div className="pb-4 mb-5 border-b border-border/40">
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
                <div className="pb-4 mb-5 border-b border-border/40">
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
    </div>
  )
}
