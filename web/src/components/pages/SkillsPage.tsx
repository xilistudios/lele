import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useAgentCatalog } from '../../hooks/useAgentCatalog'
import { invalidateAgentCatalog, useAgentSkills } from '../../hooks/useAgentSkills'
import { useSkills } from '../../hooks/useSkills'
import {
  AGENT_PARAM,
  type SkillsView,
  VIEW_PARAM,
  catalogEntryToSkillInfo,
  filterSkillsForView,
  parseSkillsView,
  resolveAgentId,
} from '../../lib/skillsScope'
import type { SkillInfo, SkillInstallScope } from '../../lib/types'
import { BackButton, Button } from '../atoms'
import { InstallSkillModal } from '../organisms/InstallSkillModal'
import { Sidebar } from '../organisms/Sidebar'
import { SkillsList } from '../organisms/SkillsList'
import { SkillsScopeBar } from '../organisms/SkillsScopeBar'

export function SkillsPage() {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const {
    sidebarOpen,
    mobileSidebarOpen,
    onCloseMobileSidebar,
    onOpenMobileSidebar,
    agents,
    currentAgent,
  } = useAppLogicContext()
  const queryClient = useQueryClient()

  const [searchParams, setSearchParams] = useSearchParams()
  const [modalOpen, setModalOpen] = useState(false)
  const [agentScope, setAgentScope] = useState<SkillInstallScope>('workspace')

  const view = parseSkillsView(searchParams.get(VIEW_PARAM))
  const requestedAgent = searchParams.get(AGENT_PARAM)
  const selectedAgentId = useMemo(
    () => resolveAgentId(requestedAgent, agents, currentAgent ?? null),
    [requestedAgent, agents, currentAgent],
  )
  const selectedAgent = agents.find((a) => a.id === selectedAgentId) ?? null

  const global = useSkills(api)
  const catalog = useAgentCatalog(api, view === 'agent' ? selectedAgentId : null)
  const agentMut = useAgentSkills(api, { agentId: selectedAgentId ?? '' })

  useEffect(() => {
    if (view !== 'agent' || agents.length === 0) return
    const next = new URLSearchParams(searchParams)
    next.set(VIEW_PARAM, 'agent')
    if (selectedAgentId) next.set(AGENT_PARAM, selectedAgentId)
    if (next.toString() !== searchParams.toString()) {
      setSearchParams(next, { replace: true })
    }
  }, [view, agents.length, selectedAgentId, searchParams, setSearchParams])

  // biome-ignore lint/correctness/useExhaustiveDependencies: both values are the TRIGGER, not inputs — the reset must run on change even though the body reads neither
  useEffect(() => {
    setAgentScope('workspace')
  }, [view, selectedAgentId])

  const setView = useCallback(
    (next: SkillsView) => {
      const params = new URLSearchParams(searchParams)
      params.set(VIEW_PARAM, next)
      setSearchParams(params, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  const setAgent = useCallback(
    (agentId: string) => {
      const params = new URLSearchParams(searchParams)
      params.set(VIEW_PARAM, 'agent')
      params.set(AGENT_PARAM, agentId)
      setSearchParams(params, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  const visibleSkills: SkillInfo[] =
    view === 'agent'
      ? (catalog.data?.skills ?? []).map(catalogEntryToSkillInfo)
      : filterSkillsForView(global.skills, view)

  const deletable = useMemo(
    () =>
      new Set(
        (catalog.data?.skills ?? []).filter((entry) => entry.deletable).map((entry) => entry.name),
      ),
    [catalog.data],
  )

  const refreshCatalog = useCallback(() => {
    if (selectedAgentId) invalidateAgentCatalog(queryClient, selectedAgentId)
  }, [queryClient, selectedAgentId])
  const refreshGlobalList = useCallback(async () => {
    await global.fetchSkills()
  }, [global.fetchSkills])

  const handleOpenModal = useCallback(() => {
    setModalOpen(true)
    global.fetchAvailableSkills()
  }, [global.fetchAvailableSkills])

  const handleCloseModal = useCallback(() => {
    setModalOpen(false)
  }, [])

  const handleInstall = useCallback(
    async (url: string) => {
      if (view === 'agent') {
        const ok = await agentMut.install(url, agentScope)
        if (ok) await refreshGlobalList()
      } else {
        await global.installSkill(url, view === 'global' ? 'global' : undefined)
        refreshCatalog()
      }
    },
    [view, agentScope, agentMut, refreshGlobalList, global.installSkill, refreshCatalog],
  )

  const handleScan = useCallback(
    async (repo: string) => {
      return await global.scanRepo(repo)
    },
    [global.scanRepo],
  )

  const handleInstallBatch = useCallback(
    async (repo: string, skills: string[]) => {
      if (view === 'agent') {
        const ok = await agentMut.installBatch(repo, skills, agentScope)
        if (ok) await refreshGlobalList()
      } else {
        await global.installBatch(repo, skills, view === 'global' ? 'global' : undefined)
        refreshCatalog()
      }
      setModalOpen(false)
    },
    [view, agentScope, agentMut, refreshGlobalList, global.installBatch, refreshCatalog],
  )

  const handleRemove = useCallback(
    async (name: string) => {
      if (view === 'agent') {
        await agentMut.remove(name)
        await refreshGlobalList()
      } else {
        await global.removeSkill(name)
        refreshCatalog()
      }
    },
    [view, agentMut, refreshGlobalList, global.removeSkill, refreshCatalog],
  )

  const handleToggle = useCallback(
    async (name: string, enabled: boolean) => {
      if (view === 'agent') {
        await agentMut.toggle(name, enabled)
        await refreshGlobalList()
      } else {
        await global.toggleSkill(name, enabled)
        refreshCatalog()
      }
    },
    [view, agentMut, refreshGlobalList, global.toggleSkill, refreshCatalog],
  )

  const handleRetry = useCallback(() => {
    if (view === 'agent') {
      void catalog.refetch()
    } else {
      void global.fetchSkills()
    }
  }, [view, catalog, global.fetchSkills])

  const targetSuffix =
    view === 'global'
      ? t('skills.scopeGlobal', 'Global (all agents)')
      : view === 'agent'
        ? selectedAgent?.name || selectedAgentId
        : null

  const destinationLabel =
    view === 'global'
      ? t('skills.installIntoTarget', {
          target: t('skills.scopeGlobal', 'Global (all agents)'),
        })
      : view === 'all'
        ? t('skills.installIntoTarget', {
            target: t('skills.targetDefaultWorkspace', 'Default workspace'),
          })
        : undefined

  const listError = view === 'agent' ? catalog.isError : !!global.error
  const listErrorText = view === 'agent' ? t('skills.catalogLoadError') : global.error

  return (
    <div className="flex app-shell bg-background-primary text-text-primary">
      <Sidebar
        collapsed={!sidebarOpen}
        mobileOpen={mobileSidebarOpen}
        onClose={() => onCloseMobileSidebar()}
      />
      <main className="flex flex-1 flex-col overflow-hidden">
        <header className="flex items-center justify-between border-b border-border px-4 py-3 md:px-6 md:py-4 shrink-0 bg-background-secondary">
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => onOpenMobileSidebar()}
              className="flex md:hidden p-1.5 rounded-lg text-text-tertiary hover:text-text-primary hover:bg-background-tertiary transition-colors"
              title={t('chat.toggleSidebar')}
            >
              {' '}
              <svg
                width="20"
                height="20"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <title>Toggle sidebar</title>
                <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
                <line x1="9" y1="3" x2="9" y2="21" />
              </svg>
            </button>
            <BackButton />
            <h1 className="text-base font-semibold text-text-primary">
              {t('skills.title', 'Skills')}
            </h1>
          </div>
          <Button
            type="button"
            variant="primary"
            size="md"
            onClick={handleOpenModal}
            disabled={view === 'agent' && !selectedAgentId}
            title={destinationLabel ?? t('skills.installSkill', 'Install Skill')}
            aria-label={destinationLabel ?? t('skills.installSkill', 'Install Skill')}
            data-testid="skills-install-button"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <title>Install</title>
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('skills.installSkill', 'Install Skill')}
            {targetSuffix && (
              <span className="hidden sm:inline text-2xs opacity-70">→ {targetSuffix}</span>
            )}
          </Button>
        </header>

        <div className="flex-1 overflow-y-auto p-6">
          <SkillsScopeBar
            view={view}
            onViewChange={setView}
            agents={agents}
            selectedAgentId={selectedAgentId}
            onAgentChange={setAgent}
          />

          {listError && (
            <div className="mb-4 flex items-center justify-between gap-3 rounded-lg border border-state-error/30 bg-state-error/10 px-4 py-2 text-xs text-state-error">
              <span>{listErrorText}</span>
              <button
                type="button"
                onClick={handleRetry}
                className="shrink-0 rounded border border-state-error/40 px-2 py-1 font-medium text-state-error transition-colors hover:bg-state-error/10"
              >
                {t('common.retry', 'Retry')}
              </button>
            </div>
          )}

          {view === 'agent' && agentMut.error && (
            <p
              role="alert"
              data-testid="skills-action-error"
              className="mb-4 rounded-lg border border-state-error/30 bg-state-error/10 px-4 py-2 text-xs text-state-error"
            >
              {agentMut.error}
            </p>
          )}

          <p
            className="mb-4 text-xs text-text-tertiary"
            aria-live="polite"
            data-testid="skills-count"
          >
            {t('skills.installedCount', { count: visibleSkills.length })}
          </p>

          {view === 'global' && (
            <p data-testid="skills-global-toggle-hint" className="mt-1 text-xs text-text-tertiary">
              {t(
                'skills.globalToggleHint',
                'Enable/disable is per agent — open the Global skill under an agent to change it.',
              )}
            </p>
          )}

          {view === 'agent' && agents.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-16 text-center">
              <h3 className="text-sm font-medium text-text-primary">
                {t('skills.noAgents', 'No agents configured')}
              </h3>
              <p className="mt-1 text-xs text-text-secondary max-w-sm">
                {t('skills.noAgentsDesc', 'Create an agent to manage its skills.')}
              </p>
              <Link to="/settings/agents" className="mt-3 text-sm text-focus hover:underline">
                {t('settings.tabs.agents', 'Agents')}
              </Link>
            </div>
          ) : (
            <SkillsList
              skills={visibleSkills}
              isLoading={view === 'agent' ? catalog.isLoading : global.isLoading}
              isRemoving={view === 'agent' ? agentMut.isRemoving : global.isRemoving}
              onRemove={handleRemove}
              onToggle={view === 'global' ? undefined : handleToggle}
              canRemove={
                view === 'global'
                  ? (skill: SkillInfo) => skill.source === 'global'
                  : view === 'agent'
                    ? (skill: SkillInfo) => deletable.has(skill.name)
                    : undefined
              }
              emptyTitle={
                view === 'global'
                  ? t('skills.noGlobalSkills', 'No global skills yet')
                  : view === 'agent'
                    ? t('skills.noAgentSkills', 'This agent has no skills')
                    : undefined
              }
              emptyDescription={
                view === 'global'
                  ? t(
                      'skills.noGlobalSkillsDesc',
                      'Choose this tab before installing to share a skill with every agent.',
                    )
                  : undefined
              }
            />
          )}
        </div>
      </main>

      <InstallSkillModal
        isOpen={modalOpen}
        onClose={handleCloseModal}
        availableSkills={global.availableSkills}
        isAvailableLoading={global.isAvailableLoading}
        isInstalling={view === 'agent' ? agentMut.isInstalling : global.isInstalling}
        isScanning={global.isScanning}
        scanResults={global.scanResults}
        onInstall={handleInstall}
        onScan={handleScan}
        onInstallBatch={handleInstallBatch}
        onClearScan={global.clearScanResults}
        scope={view === 'agent' ? agentScope : undefined}
        onScopeChange={view === 'agent' ? setAgentScope : undefined}
        workspacePath={view === 'agent' ? catalog.data?.workspace : undefined}
        destinationLabel={destinationLabel}
      />
    </div>
  )
}
