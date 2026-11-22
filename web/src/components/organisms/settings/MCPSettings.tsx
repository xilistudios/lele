import { useState } from 'react'
import { useSettings } from '../../../contexts/SettingsContext'
import { SelectInput } from '../../molecules'
import { AgentMCPSection } from '../agents/AgentMCPSection'

type Option = {
  value: string
  label: string
}

function agentOption(id: string, name?: string): Option {
  return { value: id, label: name ? `${name} (${id})` : id }
}

export function MCPSettings() {
  const { draftConfig, remoteConfig, t } = useSettings()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  if (!draftConfig) return null

  const list = draftConfig.agents?.list ?? []
  const options: Option[] = list.length
    ? list.map((entry) => agentOption(entry.id, entry.name))
    : [{ value: 'main', label: 'main' }]
  const fallbackId = list.find((entry) => entry.default)?.id ?? list[0]?.id ?? 'main'
  const activeId =
    selectedId && options.some((option) => option.value === selectedId) ? selectedId : fallbackId
  const savedIds = new Set((remoteConfig?.agents?.list ?? []).map((entry) => entry.id))
  const unsaved = list.length > 0 && !savedIds.has(activeId)

  return (
    <div data-testid="mcp-settings-page" className="space-y-4">
      <div className="flex flex-wrap items-end gap-3">
        <div className="w-56">
          <label htmlFor="mcp-agent-select" className="text-sm font-medium text-text-secondary">
            {t('settings.mcpPage.agentLabel')}
          </label>
          <SelectInput
            id="mcp-agent-select"
            value={activeId}
            onChange={setSelectedId}
            options={options}
          />
        </div>
      </div>

      {list.length === 0 && (
        <p className="text-xs text-text-tertiary">{t('settings.mcpPage.implicitMain')}</p>
      )}

      {unsaved && (
        <div
          data-testid="mcp-unsaved-agent"
          className="max-w-[820px] rounded-lg border border-state-warning/40 bg-state-warning/10 p-3.5 text-xs text-state-warning"
        >
          {t('settings.mcpPage.unsavedAgent')}
        </div>
      )}

      <p className="max-w-[820px] text-xs text-text-tertiary">{t('settings.mcpPage.intro')}</p>

      <AgentMCPSection key={activeId} agentId={activeId} />
    </div>
  )
}
