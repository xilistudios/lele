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
      <span className="rounded bg-[#2a2a2a] px-2 py-0.5 font-mono text-[11px] text-[#aaa]">
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
    <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-3">
      <div className="flex items-center gap-3 min-w-0">
        <button
          type="button"
          onClick={onToggleSidebar}
          className="hidden md:flex text-[#888] transition-colors hover:text-white"
          aria-label={t('chat.toggleSidebar')}
        >
          <SidebarToggleIcon />
        </button>

        <div className="min-w-0">
          {parentSession && (
            <button
              type="button"
              onClick={() => navigate(`/chat/${encodeURIComponent(parentSession.key)}`)}
              className="flex items-center text-[#888] transition-colors hover:text-white mr-2"
              aria-label={t('chat.backTo', { title: parentTitle })}
            >
              <ChevronLeftIcon />
            </button>
          )}
          <h2 className="truncate text-sm font-medium text-white">{currentTitle}</h2>
          <p className="truncate text-[11px] text-[#666]">
            {currentAgent?.name ?? t('chat.default')}
          </p>
        </div>

        <div className="flex items-center gap-3">
          {(isStreaming || toolStatus) && (
            <div className="flex items-center gap-1.5 text-xs text-[#666]">
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
              className="rounded-md border border-[#5a2b2b] px-3 py-1 text-xs text-[#f0b4b4] transition-colors hover:bg-[#351717]"
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