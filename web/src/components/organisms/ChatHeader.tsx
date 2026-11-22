import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { NO_POLL_MS, useSubagents } from '../../hooks/useSubagents'
import { getModeTheme } from '../../lib/modeTheme'
import {
  resolveHeaderAgentName,
  resolveHeaderTitle,
  selectSubagentForSession,
} from '../../lib/subagentHeader'
import { formatSessionTitle } from '../../lib/utils'
import { ContextIndicator } from '../atoms/ContextIndicator'
import { IconButton } from '../atoms/IconButton'
import { ChevronLeftIcon, SidebarToggleIcon, ToolsIcon } from '../atoms/Icons'
import { SubagentsIndicator } from '../atoms/SubagentsIndicator'
import { SubagentsSidebar } from './SubagentsSidebar'
import { ToolsSidebar } from './ToolsSidebar'

export const ChatHeader = memo(function ChatHeader() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const {
    currentAgent,
    wsStatus,
    currentSessionKey,
    parentSessionKey,
    agents,
    onOpenMobileSidebar,
    chatMode,
    isProcessing,
  } = useAppLogicContext()
  const { apiUrl } = useAuthContext()
  const { currentSession, parentSession } = useChatPageContext()
  const [subagentsSidebarOpen, setSubagentsSidebarOpen] = useState(false)
  const [toolsSidebarOpen, setToolsSidebarOpen] = useState(false)

  const { subagents, loading, refresh } = useSubagents(currentSessionKey, 5000, isProcessing)

  const { subagents: parentSubagents } = useSubagents(parentSessionKey, NO_POLL_MS, false)

  const matchedSubagent = useMemo(
    () => selectSubagentForSession(parentSubagents, currentSessionKey),
    [parentSubagents, currentSessionKey],
  )

  const modeTheme = getModeTheme(chatMode)
  const ModeIcon = modeTheme.Icon

  useEffect(() => {
    if (subagentsSidebarOpen) {
      void refresh()
    }
  }, [subagentsSidebarOpen, refresh])

  const handleToggleSubagents = useCallback(() => {
    setToolsSidebarOpen(false)
    setSubagentsSidebarOpen((prev) => !prev)
  }, [])

  const handleToggleTools = useCallback(() => {
    setSubagentsSidebarOpen(false)
    setToolsSidebarOpen((prev) => !prev)
  }, [])

  const handleCloseSubagents = useCallback(() => {
    setSubagentsSidebarOpen(false)
  }, [])

  const handleSelectSubagent = useCallback(
    (sessionKey: string) => {
      if (currentSessionKey) {
        navigate(
          `/chat/${encodeURIComponent(currentSessionKey)}/subagent/${encodeURIComponent(sessionKey)}`,
        )
      }
    },
    [currentSessionKey, navigate],
  )

  const baseTitle = currentSession
    ? formatSessionTitle(currentSession.key, currentSession.name)
    : t('chat.session')

  const currentTitle = resolveHeaderTitle(matchedSubagent, baseTitle)

  const headerAgentName = resolveHeaderAgentName(
    matchedSubagent,
    agents,
    currentAgent?.name ?? t('chat.default'),
  )

  const parentTitle = parentSession ? formatSessionTitle(parentSession.key, parentSession.name) : ''

  return (
    <>
      <div className="flex shrink-0 items-center justify-between border-b border-border px-4 py-3 md:px-6">
        <div className="flex items-center gap-2 md:gap-3 min-w-0">
          <button
            type="button"
            onClick={onOpenMobileSidebar}
            className="flex md:hidden items-center justify-center rounded-md p-2.5 md:p-1.5 text-text-secondary hover:bg-surface-hover hover:text-text-primary transition-colors mr-1"
            aria-label={t('chat.toggleSidebar')}
          >
            <SidebarToggleIcon size={20} />
          </button>
          <div className="min-w-0">
            {parentSession && (
              <button
                type="button"
                onClick={() => navigate(`/chat/${encodeURIComponent(parentSession.key)}`)}
                className="flex items-center text-text-secondary transition-colors hover:text-text-primary mr-2"
                aria-label={t('chat.backTo', { title: parentTitle })}
              >
                <ChevronLeftIcon />
              </button>
            )}
            <h2 className="truncate text-sm font-medium text-text-primary">{currentTitle}</h2>
            <div className="flex items-center gap-2">
              <p className="truncate text-xs text-text-tertiary">{headerAgentName}</p>
              <span
                className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-2xs font-medium ${modeTheme.chip}`}
              >
                <ModeIcon size={11} />
                {t(modeTheme.labelKey)}
              </span>
            </div>
          </div>
        </div>

        <div className="flex items-center gap-1">
          <SubagentsIndicator count={subagents.length} onClick={handleToggleSubagents} />
          <ContextIndicator />
          <IconButton
            onClick={handleToggleTools}
            ariaLabel={t('chat.tools')}
            title={t('chat.tools')}
            ariaExpanded={toolsSidebarOpen}
            className={`flex items-center justify-center h-9 w-9 ${
              toolsSidebarOpen ? 'text-accent-text bg-surface-selected' : ''
            }`}
          >
            <ToolsIcon />
          </IconButton>
        </div>
      </div>

      <SubagentsSidebar
        subagents={subagents}
        loading={loading}
        isOpen={subagentsSidebarOpen}
        onClose={handleCloseSubagents}
        onSelectSubagent={handleSelectSubagent}
      />

      <ToolsSidebar
        isOpen={toolsSidebarOpen}
        onClose={() => setToolsSidebarOpen(false)}
        connectionStatus={wsStatus}
        apiUrl={apiUrl}
      />
    </>
  )
})
