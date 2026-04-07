import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ConnectionIndicator } from '../atoms/ConnectionIndicator'
import { SessionItem } from '../molecules/SessionItem'

type Props = {
  deviceName: string
  apiUrl: string
  wsStatus: 'disconnected' | 'connecting' | 'connected'
  sessions: Array<{ key: string; name?: string; message_count: number; updated: string }>
  currentSessionKey: string | null
  onCreateSession: () => void
  onClearSession: () => void
  onSelectSession: (key: string) => void
  onDeleteSession: (key: string) => void
  onLogout: () => void
  onToggleDiagnostics: () => void
}

export function formatSessionTitle(sessionKey: string, sessionName?: string) {
  if (sessionName?.trim()) return sessionName
  const parts = sessionKey.split(':')
  return parts.length > 2 ? `Session ${parts[parts.length - 1]}` : sessionKey
}

export function Sidebar({
  deviceName,
  apiUrl,
  wsStatus,
  sessions,
  currentSessionKey,
  onCreateSession,
  onClearSession,
  onDeleteSession,
}: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const sortedSessions = [...sessions].sort(
    (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
  )

  const currentSession =
    sortedSessions.find((s) => s.key === currentSessionKey) ?? sortedSessions[0] ?? null

  return (
    <aside className="flex w-[280px] flex-shrink-0 flex-col border-r border-[#2e2e2e] bg-[#222222]">
      <div className="flex items-center gap-2 border-b border-[#2e2e2e] px-4 py-3">
        <div className="flex h-7 w-7 items-center justify-center rounded bg-[#3a3a3a] text-xs font-bold text-white">
          {deviceName?.[0]?.toUpperCase() ?? 'L'}
        </div>
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-white">{deviceName ?? 'lele'}</p>
          <p className="truncate text-[10px] text-[#666]">{apiUrl.replace(/^https?:\/\//, '')}</p>
        </div>
        <button
          onClick={() => navigate('/pair')}
          title={t('chat.logout')}
          type="button"
          aria-label={t('chat.logout')}
          className="text-[#555] transition-colors hover:text-[#aaa]"
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
            <polyline points="16 17 21 12 16 7" />
            <line x1="21" y1="12" x2="9" y2="12" />
          </svg>
        </button>
      </div>

      <div className="space-y-2 border-b border-[#2e2e2e] px-3 py-3">
        <button
          onClick={onCreateSession}
          type="button"
          className="flex w-full items-center gap-2 rounded-md border border-[#3a3a3a] px-3 py-2 text-xs text-[#bbb] transition-colors hover:bg-[#2a2a2a] hover:text-white"
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          {t('chat.newSession')}
        </button>
        <button
          onClick={onClearSession}
          type="button"
          className="flex w-full items-center gap-2 rounded-md border border-[#3a3a3a] px-3 py-2 text-xs text-[#999] transition-colors hover:bg-[#2a2a2a] hover:text-white"
        >
          <svg
            width="12"
            height="12"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <path d="M3 6h18" />
            <path d="M8 6V4h8v2" />
            <path d="M19 6l-1 14H6L5 6" />
          </svg>
          {t('chat.clearSession')}
        </button>
      </div>

      <div className="border-b border-[#2e2e2e] px-3 py-3">
        <p className="px-1 text-[10px] uppercase tracking-[0.2em] text-[#666]">
          {t('chat.sessions')}
        </p>
        <nav className="mt-2 space-y-0.5 overflow-y-auto max-h-[240px]">
          {sortedSessions.slice(0, 5).map((session) => (
            <SessionItem
              key={session.key}
              sessionKey={session.key}
              sessionName={session.name}
              messageCount={session.message_count}
              selected={session.key === currentSession?.key}
              onSelect={() => navigate(`/chat/${session.key}`)}
              onDelete={() => onDeleteSession(session.key)}
            />
          ))}
        </nav>
      </div>

      <div className="flex items-center justify-between border-t border-[#2e2e2e] px-4 py-3">
        <div className="flex items-center gap-1.5">
          <ConnectionIndicator status={wsStatus} />
          <span className="text-[10px] text-[#555]">{t(`chat.${wsStatus}`)}</span>
        </div>
        <button
          type="button"
          title={t('chat.settings')}
          aria-label={t('chat.settings')}
          className="text-[#444] transition-colors hover:text-[#888]"
          onClick={() => navigate('/settings')}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <circle cx="12" cy="12" r="3" />
            <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
          </svg>
        </button>
      </div>
    </aside>
  )
}
