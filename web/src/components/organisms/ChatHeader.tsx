import { memo } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { formatSessionTitle } from '../../lib/utils'
import { ConnectionIndicator } from '../atoms/ConnectionIndicator'
import { ChevronLeftIcon, SidebarToggleIcon } from '../atoms/Icons'
import { Spinner } from '../atoms/Spinner'

function ToolStatus({ tool, action }: { tool: string; action: string }) {
  return (
    <>
      <span className="rounded px-2 py-1 bg-surface-hover text-xs font-medium font-mono text-text-secondary">
        {tool}
      </span>
      <span>{action}</span>
    </>
  )
}

export const ChatHeader = memo(function ChatHeader() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { currentAgent, toolStatus, isStreaming, onCancel, onToggleSidebar, wsStatus } =
    useAppLogicContext()
  const { apiUrl } = useAuthContext()
  const { currentSession, parentSession, canCancel } = useChatPageContext()

  const currentTitle = currentSession
    ? formatSessionTitle(currentSession.key, currentSession.name, currentSession.message_count)
    : t('chat.session')

  const parentTitle = parentSession
    ? formatSessionTitle(parentSession.key, parentSession.name, parentSession.message_count)
    : ''

  return (
    <div className="flex items-center justify-between border-b border-border px-6 py-3">
      <div className="flex items-center gap-3 min-w-0">
        <button
          type="button"
          onClick={onToggleSidebar}
          className="hidden md:flex text-text-secondary transition-colors hover:text-text-primary"
          aria-label={t('chat.toggleSidebar')}
        >
          <SidebarToggleIcon />
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
          <p className="truncate text-[11px] text-text-tertiary">
            {currentAgent?.name ?? t('chat.default')}
          </p>
        </div>

        <div className="flex items-center gap-3">
          {(isStreaming || toolStatus) && (
            <div className="flex items-center gap-1.5 text-xs text-text-tertiary">
              {toolStatus ? (
                <ToolStatus tool={toolStatus.tool} action={toolStatus.action} />
              ) : (
                <Spinner size="sm" />
              )}
            </div>
          )}

          {canCancel && (
            <button
              type="button"
              className="rounded-md border border-border px-3 py-1 text-xs text-state-error transition-colors hover:bg-state-error-light hover:text-[#FF7B7B]"
              onClick={onCancel}
            >
              {t('chat.cancel')}
            </button>
          )}
        </div>
      </div>

      <ConnectionIndicator status={wsStatus} apiUrl={apiUrl} />
    </div>
  )
})
