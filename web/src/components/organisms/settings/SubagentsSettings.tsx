import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useSettings } from '../../../contexts/SettingsContext'
import { getErrorForPath, isDirtyPath } from '../../../hooks/useSettingsHelpers'
import type { EditableAgentDefaults } from '../../../lib/types'
import { NumberInput, SelectInput, SettingsField, SettingsSection } from '../../molecules'
import { AgentSubagentsSection } from '../agents/AgentSubagentsSection'

type DefaultFieldKey =
  | 'subagent_timeout_minutes'
  | 'subagent_max_concurrent'
  | 'subagent_max_retries'
  | 'subagent_max_iterations'
  | 'subagent_retention_minutes'

type DefaultField = {
  key: DefaultFieldKey
  labelKey: string
  helpKey: string
}

const DEFAULT_FIELDS: DefaultField[] = [
  { key: 'subagent_timeout_minutes', labelKey: 'subagentTimeout', helpKey: 'subagentTimeout' },
  {
    key: 'subagent_max_concurrent',
    labelKey: 'subagentMaxConcurrent',
    helpKey: 'subagentMaxConcurrent',
  },
  { key: 'subagent_max_retries', labelKey: 'subagentMaxRetries', helpKey: 'subagentMaxRetries' },
  {
    key: 'subagent_max_iterations',
    labelKey: 'subagentMaxIterations',
    helpKey: 'subagentMaxIterations',
  },
  {
    key: 'subagent_retention_minutes',
    labelKey: 'subagentRetention',
    helpKey: 'subagentRetention',
  },
]

function agentLabel(id: string, name?: string): string {
  return name ? `${name} (${id})` : id
}

export function SubagentsSettings() {
  const { draftConfig, updateField, dirtyPaths, validationErrors, t } = useSettings()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  if (!draftConfig) return null

  const defaults: Partial<EditableAgentDefaults> | undefined = draftConfig.agents?.defaults
  const list = draftConfig.agents?.list ?? []
  const fallbackId = list.find((entry) => entry.default)?.id ?? list[0]?.id ?? null
  const activeId =
    selectedId && list.some((entry) => entry.id === selectedId) ? selectedId : fallbackId
  const index = list.findIndex((entry) => entry.id === activeId)
  const agent = index >= 0 ? list[index] : undefined

  return (
    <div data-testid="subagents-settings-page" className="mx-auto w-full max-w-[820px] space-y-6">
      <SettingsSection
        title={t('settings.sections.subagentDefaults')}
        description={t('settings.descriptions.subagentDefaults')}
      >
        {DEFAULT_FIELDS.map(({ key, labelKey, helpKey }) => {
          const path = `agents.defaults.${key}`
          return (
            <SettingsField
              key={key}
              label={t(`settings.fields.${labelKey}`)}
              description={t(`settings.descriptions.${helpKey}`)}
              path={path}
              isDirty={isDirtyPath(dirtyPaths, path)}
              error={getErrorForPath(validationErrors, path)}
            >
              <NumberInput
                id={path}
                value={Number(defaults?.[key] ?? 0)}
                min={0}
                onChange={(value) => updateField(path, value)}
              />
            </SettingsField>
          )
        })}
      </SettingsSection>

      {agent ? (
        <>
          <div className="flex flex-wrap items-end gap-3">
            <div className="w-56">
              <label
                htmlFor="subagents-agent-select"
                className="text-sm font-medium text-text-secondary"
              >
                {t('settings.subagentsPage.agentLabel')}
              </label>
              <SelectInput
                id="subagents-agent-select"
                value={activeId ?? ''}
                onChange={setSelectedId}
                options={list.map((entry) => ({
                  value: entry.id,
                  label: agentLabel(entry.id, entry.name),
                }))}
              />
            </div>
            <Link
              data-testid="subagents-open-agent"
              to={`/settings/agents/${encodeURIComponent(agent.id)}/subagents`}
              className="text-xs text-accent-primary hover:underline"
            >
              {t('settings.subagentsPage.openAgent')}
            </Link>
          </div>

          <AgentSubagentsSection key={agent.id} agent={agent} index={index} agents={list} />
        </>
      ) : (
        <div
          data-testid="subagents-no-agents"
          className="rounded-lg border border-dashed border-border px-6 py-10 text-center text-sm text-text-secondary"
        >
          {t('settings.subagentsPage.noAgents')}
          <Link to="/settings/agents" className="ml-1 text-accent-primary hover:underline">
            {t('settings.tabs.agents')}
          </Link>
        </div>
      )}
    </div>
  )
}
