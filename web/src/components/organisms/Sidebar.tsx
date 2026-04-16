import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useIsMobile } from '../../hooks/useIsMobile'
import { LogoutIcon, PlusIcon, SettingsIcon } from '../atoms/Icons'
import { Popover } from '../atoms/Popover'
import { SessionItem } from '../molecules/SessionItem'

const MAX_VISIBLE_SESSIONS = 5

type SidebarProps = {
  collapsed: boolean
  mobileOpen: boolean
  onClose: () => void
}

export function Sidebar({ collapsed, mobileOpen, onClose }: SidebarProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { session } = useAuthContext()
  const { sessions, currentSessionKey, parentSessionKey, onCreateSession, onDeleteSession } =
    useAppLogicContext()
  const isMobile = useIsMobile()

  const deviceName = session?.device_name ?? 'lele'

  const sortedSessions = useMemo(() => {
    const visible = sessions.filter((s) => !s.key.startsWith('subagent:'))
    return [...visible].sort(
      (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
    )
  }, [sessions])

  const selectedKey = parentSessionKey ?? currentSessionKey
  const currentSession =
    sortedSessions.find((s) => s.key === selectedKey) ?? sortedSessions[0] ?? null

  const handleSessionSelect = (key: string) => {
    navigate(`/chat/${encodeURIComponent(key)}`)
    if (isMobile) onClose()
  }

  const handleSettingsClick = () => {
    navigate('/settings/general')
    if (isMobile) onClose()
  }

  return (
    <>
      <div
        className={`fixed inset-0 z-40 bg-black/50 transition-opacity duration-300 md:hidden ${
          mobileOpen ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0'
        }`}
        onClick={onClose}
        onKeyDown={(e) => e.key === 'Escape' && onClose()}
        role="button"
        tabIndex={0}
        aria-hidden={!mobileOpen}
      />

      <aside
        className={`fixed inset-y-0 left-0 z-50 flex flex-col border-r border-[#2e2e2e] bg-[#222222] transition-all duration-300 ease-in-out md:relative md:translate-x-0 ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        } ${collapsed ? 'w-[60px]' : 'w-[280px]'}`}
      >
        <div className={`border-b border-[#2e2e2e] px-4 py-3 ${collapsed ? 'flex justify-center' : ''}`}>
          {collapsed ? (
            <span className="text-lg font-bold uppercase tracking-wider text-pink-500 drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]">L</span>
          ) : (
            <span className="text-lg font-bold uppercase tracking-wider">
              <span className="text-pink-500 drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]">L</span>
              <span className="text-cyan-400 drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]">E</span>
              <span className="text-yellow-400 drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]">L</span>
              <span className="text-pink-500 drop-shadow-[1px_1px_0_rgba(0,0,0,0.8)]">E</span>
            </span>
          )}
        </div>

        <div
          className={`overflow-hidden transition-all duration-300 ease-in-out ${collapsed ? 'max-h-0 opacity-0 border-b-0' : 'max-h-24 opacity-100 border-b border-[#2e2e2e]'}`}
        >
          <div className="space-y-2 px-3 py-3">
            <button
              onClick={onCreateSession}
              type="button"
              className="flex w-full items-center gap-2 rounded-md border border-[#3a3a3a] px-3 py-2 text-xs text-[#bbb] transition-colors hover:bg-[#2a2a2a] hover:text-white"
            >
              <PlusIcon />
              {t('chat.newSession')}
            </button>
          </div>
        </div>

        <div className={`border-b border-[#2e2e2e] ${collapsed ? 'px-2' : 'px-3'} py-3`}>
          <div
            className={`overflow-hidden transition-all duration-300 ease-in-out ${collapsed ? 'max-h-0 opacity-0' : 'max-h-8 opacity-100'}`}
          >
            <p className="px-1 text-[10px] uppercase tracking-[0.2em] text-[#666]">
              {t('chat.sessions')}
            </p>
          </div>
          <nav
            className={`mt-2 space-y-0.5 overflow-y-auto transition-[max-height] duration-300 ease-in-out ${collapsed ? 'max-h-[300px]' : 'max-h-[240px]'}`}
          >
            {sortedSessions.slice(0, MAX_VISIBLE_SESSIONS).map((s) => (
              <SessionItem
                key={s.key}
                sessionKey={s.key}
                sessionName={s.name}
                messageCount={s.message_count}
                selected={s.key === currentSession?.key}
                onSelect={() => handleSessionSelect(s.key)}
                onDelete={() => onDeleteSession(s.key)}
                collapsed={collapsed}
              />
            ))}
          </nav>
        </div>

        <div
          className={`mt-auto border-t border-[#2e2e2e] px-4 py-3 ${collapsed ? 'flex justify-center' : ''}`}
        >
          <Popover
            trigger={
              <div
                className={`flex items-center ${collapsed ? 'justify-center' : 'gap-2'} cursor-pointer`}
              >
                <div className="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded bg-[#3a3a3a] text-xs font-bold text-white">
                  {deviceName?.[0]?.toUpperCase() ?? 'L'}
                </div>
                {!collapsed && (
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-white">{deviceName}</p>
                  </div>
                )}
              </div>
            }
            popoverWidth={150}
            popoverHeight={80}
          >
            <div className="flex flex-col gap-1">
              <button
                type="button"
                title={t('chat.settings')}
                aria-label={t('chat.settings')}
                className="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-[#bbb] transition-colors hover:bg-[#2a2a2a] hover:text-white"
                onClick={handleSettingsClick}
              >
                <SettingsIcon />
                <span>{t('chat.settings')}</span>
              </button>
              <button
                onClick={() => navigate('/pair')}
                title={t('chat.logout')}
                type="button"
                aria-label={t('chat.logout')}
                className="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-[#555] transition-colors hover:bg-[#2a2a2a] hover:text-[#aaa]"
              >
                <LogoutIcon />
                <span>{t('chat.logout')}</span>
              </button>
            </div>
          </Popover>
        </div>
      </aside>
    </>
  )
}
