import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useChatPageContext } from '../../contexts/ChatPageContext'
import { formatSessionTitle } from '../../lib/utils'
import { Spinner } from '../atoms/Spinner'

export function ChatHeader() {
  const { t } = useTranslation()
  const { currentAgent, toolStatus, isStreaming, onCancel, onToggleSidebar } = useAppLogicContext()
  const { currentSession, canCancel } = useChatPageContext()

  return (
    <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-3">
      <div className="flex items-center gap-3 min-w-0">
        <button
          type="button"
          onClick={onToggleSidebar}
          className="hidden md:flex text-[#888] transition-colors hover:text-white"
          aria-label="Toggle sidebar"
        >
          <svg
            width="20"
            height="20"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
            <line x1="9" y1="3" x2="9" y2="21" />
          </svg>
        </button>
        <div className="min-w-0">
          <h2 className="truncate text-sm font-medium text-white">
            {currentSession
              ? formatSessionTitle(
                  currentSession.key,
                  currentSession.name,
                  currentSession.message_count,
                )
              : t('chat.session')}
          </h2>
          <p className="truncate text-[11px] text-[#666]">
            {currentAgent?.name ?? t('chat.default')}
          </p>
        </div>
        <div className="flex items-center gap-3">
          {(isStreaming || toolStatus) && (
            <div className="flex items-center gap-1.5 text-xs text-[#666]">
              {toolStatus ? (
                <>
                  <span className="rounded bg-[#2a2a2a] px-2 py-0.5 font-mono text-[11px] text-[#aaa]">
                    {toolStatus.tool}
                  </span>
                  <span>{toolStatus.action}</span>
                </>
              ) : (
                <Spinner size="sm" />
              )}
            </div>
          )}
          {canCancel ? (
            <button
              type="button"
              className="rounded-md border border-[#5a2b2b] px-3 py-1 text-xs text-[#f0b4b4] transition-colors hover:bg-[#351717]"
              onClick={onCancel}
            >
              {t('chat.cancel')}
            </button>
          ) : null}
        </div>
      </div>
    </div>
  )
}
