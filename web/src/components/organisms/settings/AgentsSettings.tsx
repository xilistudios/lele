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
            },
            index: number,
          ) => (
            <NamedItemCard
              key={agent.id}
              title={agent.id}
              onRemove={() => removeAgent(index)}
              removeLabel={t('settings.removeAgent')}
            >
              <SettingsField
                label={t('settings.fields.agentName')}
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
                path={`agents.list.${index}.workspace`}
                isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.workspace`)}
              >
                <TextInput
                  id={`agents.list.${index}.workspace`}
                  value={agent.workspace || ''}
                  onChange={(v) => updateField(`agents.list.${index}.workspace`, v || undefined)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.agentModelPrimary')}
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
                  searchAriaLabel={`${t('settings.fields.agentModelPrimary')} buscar`}
                  searchPlaceholder={t('settings.fields.agentModelPrimary')}
                  value={getAgentModelPrimary(agent.model)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.agentModelFallbacks')}
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
              <SettingsField
                label={t('settings.fields.agentSkills')}
                path={`agents.list.${index}.skills`}
                isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.skills`)}
              >
                <StringListEditor
                  id={`agents.list.${index}.skills`}
                  value={agent.skills || []}
                  onChange={(v) => updateField(`agents.list.${index}.skills`, v)}
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.agentTemperature')}
                path={`agents.list.${index}.temperature`}
                isDirty={isDirtyPath(dirtyPaths, `agents.list.${index}.temperature`)}
              >
                <NumberInput
                  id={`agents.list.${index}.temperature`}
                  value={agent.temperature ?? 0}
                  min={0}
                  max={2}
                  step={0.1}
                  onChange={(v) =>
                    updateField(`agents.list.${index}.temperature`, v === 0 ? undefined : v)
                  }
                />
              </SettingsField>
              <SettingsField
                label={t('settings.fields.agentThinking')}
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
            </NamedItemCard>
          ),
        )}
      </SettingsSection>
    </div>
  )
}
