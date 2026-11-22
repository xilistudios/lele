import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useParams } from 'react-router-dom'
import { useSettings } from '../../contexts/SettingsContext'
import { getDefaultModel } from '../../hooks/useSettingsHelpers'
import { AGENT_TABS, type AgentTab, isAgentDirty, isAgentTab } from '../../lib/agentDirty'
import { Button } from '../atoms/Button'
import { AgentsIcon } from '../atoms/Icons'
import { AgentCommandsSection } from '../organisms/agents/AgentCommandsSection'
import { AgentFilesSection } from '../organisms/agents/AgentFilesSection'
import { AgentGeneralSection } from '../organisms/agents/AgentGeneralSection'
import { AgentMCPSection } from '../organisms/agents/AgentMCPSection'
import { AgentModelSection } from '../organisms/agents/AgentModelSection'
import { AgentPageHeader } from '../organisms/agents/AgentPageHeader'
import {
  AGENT_TAB_PANEL_ID,
  AgentSettingsTabs,
  agentTabId,
} from '../organisms/agents/AgentSettingsTabs'
import { AgentSkillsSection } from '../organisms/agents/AgentSkillsSection'
import { AgentSubagentsSection } from '../organisms/agents/AgentSubagentsSection'
import { AgentToolsSection } from '../organisms/agents/AgentToolsSection'

const FULL_WIDTH_TABS: ReadonlySet<AgentTab> = new Set<AgentTab>([
  'skills',
  'mcp',
  'commands',
  'tools',
])

function DetailSkeleton() {
  const { t } = useTranslation()
  return (
    // biome-ignore lint/a11y/useSemanticElements: spec §5.1 requires a status live region around the skeletons; <output> is form-only and would not announce a load
    <div
      role="status"
      aria-busy="true"
      data-testid="agent-config-loading"
      className="flex flex-col gap-5 lg:flex-row"
    >
      <span className="sr-only">{t('common.loading')}</span>
      <div className="w-full lg:w-[200px]">
        <div className="h-4 w-1/2 animate-pulse rounded-md bg-background-tertiary" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex animate-pulse items-center gap-4 pb-4">
          <div className="h-12 w-12 rounded-xl bg-background-tertiary" />
          <div className="flex-1 space-y-2">
            <div className="h-4 w-1/3 rounded-md bg-background-tertiary" />
            <div className="h-3 w-1/2 rounded-md bg-background-tertiary" />
          </div>
        </div>
        <div className="space-y-4 rounded-lg border border-border bg-background-primary p-4 md:p-6">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="space-y-2">
              <div className="h-3 w-[30%] rounded-md bg-background-tertiary" />
              <div className="h-9 w-full rounded-md bg-background-tertiary" />
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function AgentNotFound({ agentId }: { agentId: string }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  return (
    <div
      className="mx-auto flex max-w-2xl flex-col items-center justify-center py-16 text-center"
      data-testid="agent-config-not-found"
    >
      <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-background-tertiary text-text-muted">
        <AgentsIcon size={48} />
      </div>
      <h2 className="mt-4 text-base font-medium text-text-primary">
        {t('settings.agentPage.notFoundTitle', { id: agentId })}
      </h2>
      <p className="mx-auto mt-1 max-w-md text-xs text-text-secondary">
        {t('settings.agentPage.notFoundDesc')}
      </p>
      <div className="mt-5">
        <Button variant="secondary" size="md" onClick={() => navigate('/settings/agents')}>
          {t('settings.agentPage.backToList')}
        </Button>
      </div>
    </div>
  )
}

export function AgentConfigPage() {
  const navigate = useNavigate()
  const { agentId, tab } = useParams<{ agentId: string; tab?: string }>()
  const { draftConfig, dirtyPaths, validationErrors, isLoading } = useSettings()

  const list = draftConfig?.agents?.list ?? []
  const agentIndex = list.findIndex((entry) => entry.id === agentId)
  const agent = agentIndex >= 0 ? list[agentIndex] : undefined

  const activeTab: AgentTab = isAgentTab(tab) ? tab : 'general'

  useEffect(() => {
    if (isAgentTab(tab)) return
    const valid = AGENT_TABS.includes(activeTab) ? activeTab : 'general'
    navigate(`/settings/agents/${agentId}/${valid}`, { replace: true })
  }, [tab, agentId, activeTab, navigate])

  if (isLoading) return <DetailSkeleton />
  if (!agent || agentIndex < 0) return <AgentNotFound agentId={agentId ?? ''} />

  const isModified = isAgentDirty(dirtyPaths, agentIndex)

  const section = (() => {
    switch (activeTab) {
      case 'model':
        return <AgentModelSection key={agent.id} agent={agent} index={agentIndex} />
      case 'skills':
        return (
          <AgentSkillsSection key={agent.id} agent={agent} index={agentIndex} agentId={agent.id} />
        )
      case 'mcp':
        return <AgentMCPSection key={agent.id} agentId={agent.id} />
      case 'commands':
        return <AgentCommandsSection key={agent.id} agent={agent} agentId={agent.id} />
      case 'tools':
        return (
          <AgentToolsSection key={agent.id} agent={agent} index={agentIndex} agentId={agent.id} />
        )
      case 'subagents':
        return (
          <AgentSubagentsSection key={agent.id} agent={agent} index={agentIndex} agents={list} />
        )
      case 'files':
        return <AgentFilesSection key={agent.id} agent={agent} agentId={agent.id} />
      default:
        return <AgentGeneralSection key={agent.id} agent={agent} index={agentIndex} agents={list} />
    }
  })()

  return (
    <div className="flex flex-col gap-4 lg:flex-row lg:gap-0" data-testid="agent-config-page">
      <AgentSettingsTabs
        agentIndex={agentIndex}
        activeTab={activeTab}
        dirtyPaths={dirtyPaths}
        validationErrors={validationErrors}
        onTabChange={(next) => navigate(`/settings/agents/${agentId}/${next}`)}
      />
      <div
        id={AGENT_TAB_PANEL_ID}
        role="tabpanel"
        aria-labelledby={agentTabId(activeTab)}
        // biome-ignore lint/a11y/noNoninteractiveTabindex: §6 tabs pattern, focusable panel
        tabIndex={0}
        className="min-w-0 flex-1"
      >
        <div key={activeTab} className="animate-step-enter">
          <AgentPageHeader
            agent={agent}
            isModified={isModified}
            defaultsModel={getDefaultModel(draftConfig)}
          />
          <div className={FULL_WIDTH_TABS.has(activeTab) ? '' : 'max-w-[820px]'}>{section}</div>
        </div>
      </div>
    </div>
  )
}
