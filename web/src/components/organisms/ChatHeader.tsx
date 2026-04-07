import { useTranslation } from 'react-i18next'
import { Spinner } from '../atoms/Spinner'

type Props = {
  currentSession: { key: string; name?: string } | null
  currentAgent: { name: string } | null
  toolStatus: { tool: string; action: string } | null
  isStreaming: boolean
  canCancel: boolean
  onCancel: () => void
}

const formatSessionTitle = (sessionKey: string, sessionName?: string) => {
  if (sessionName?.trim()) return sessionName
  const parts = sessionKey.split(':')
  return parts.length > 2 ? `Session ${parts[parts.length - 1]}` : sessionKey
}

export function ChatHeader({
  currentSession,
  currentAgent,
  toolStatus,
  isStreaming,
  canCancel,
  onCancel,
}: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-3">
      <div className="min-w-0">
        <h2 className="truncate text-sm font-medium text-white">
          {currentSession
            ? formatSessionTitle(currentSession.key, currentSession.name)
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
  )
}
