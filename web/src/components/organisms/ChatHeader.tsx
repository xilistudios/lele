import { memo, useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { useSubagents } from '../../hooks/useSubagents'
import { getModeTheme } from '../../lib/modeTheme'
import { formatSessionTitle } from '../../lib/utils'
import { ConnectionIndicator } from '../atoms/ConnectionIndicator'
import { ContextIndicator } from '../atoms/ContextIndicator'
import { ChevronLeftIcon, SidebarToggleIcon } from '../atoms/Icons'
import { SubagentsIndicator } from '../atoms/SubagentsIndicator'
import { SubagentsSidebar } from './SubagentsSidebar'

export const ChatHeader = memo(function ChatHeader() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { currentAgent, wsStatus, currentSessionKey, onToggleSidebar, chatMode } =
    useAppLogicContext()
  const { apiUrl } = useAuthContext()
  const { currentSession, parentSession } = useChatPageContext()
  const [subagentsSidebarOpen, setSubagentsSidebarOpen] = useState(false)

  const { subagents, loading } = useSubagents(currentSessionKey)
  const modeTheme = getModeTheme(chatMode)
  const ModeIcon = modeTheme.Icon

  const handleToggleSubagents = useCallback(() => {
    setSubagentsSidebarOpen((prev) => !prev)
  }, [])

  const handleCloseSubagents = useCallback(() => {
    setSubagentsSidebarOpen(false)
  }, [])

  const handleSelectSubagent = useCallback(
    (sessionKey: string) => {
      if (currentSessionKey) {
        // Only navigate — let ChatRoute's first useEffect handle onSelectSession.
        // Calling onSelectSession here creates a race condition: the state updates
        // (currentSessionKey → subagent) may be committed before the URL changes,
        // causing the useEffect to see mismatched state/URL and reset the session.
        navigate(
          `/chat/${encodeURIComponent(currentSessionKey)}/subagent/${encodeURIComponent(sessionKey)}`,
        )
      }
    },
    [currentSessionKey, navigate],
  )

  const currentTitle = currentSession
    ? formatSessionTitle(currentSession.key, currentSession.name)
    : t('chat.session')

  const parentTitle = parentSession
    ? formatSessionTitle(parentSession.key, parentSession.name)
    : ''

  return (
    <>
      <div className="flex items-center justify-between border-b border-border px-4 py-3 md:px-6">
        <div className="flex items-center gap-2 md:gap-3 min-w-0">
          <button
            type="button"
            onClick={onToggleSidebar}
            className="flex md:hidden items-center justify-center rounded-md p-1.5 text-text-secondary hover:bg-surface-hover hover:text-text-primary transition-colors mr-1"
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
              <p className="truncate text-[11px] text-text-tertiary">
                {currentAgent?.name ?? t('chat.default')}
              </p>
              <span
                className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${modeTheme.chip}`}
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
          <ConnectionIndicator status={wsStatus} apiUrl={apiUrl} />
        </div>
      </div>

      <SubagentsSidebar
        subagents={subagents}
        loading={loading}
        isOpen={subagentsSidebarOpen}
        onClose={handleCloseSubagents}
        onSelectSubagent={handleSelectSubagent}
      />
    </>
  )
})
