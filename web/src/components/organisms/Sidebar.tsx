import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useIsMobile } from '../../hooks/useIsMobile'
import { ChatBubbleIcon, EditIcon, LogoutIcon, SettingsIcon } from '../atoms/Icons'
import { Logo } from '../atoms/Logo'
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
  const {
    sessions,
    currentSessionKey,
    parentSessionKey,
    processingSessions,
    onCreateSession,
    onDeleteSession,
  } = useAppLogicContext()
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
        className={`fixed inset-y-0 left-0 z-50 flex flex-col border-r border-border bg-background-secondary transition-all duration-300 ease-in-out md:relative md:translate-x-0 ${
          mobileOpen ? 'translate-x-0' : '-translate-x-full'
        } ${collapsed ? 'w-[60px]' : 'w-[280px]'}`}
      >
        <div
          className={`flex items-center border-b border-border px-4 py-3 ${collapsed ? 'justify-center' : ''}`}
        >
          <Logo collapsed={collapsed} />
        </div>

        <div className="border-b border-border px-2 py-3">
          <button
            onClick={onCreateSession}
            type="button"
            className={`flex w-full items-center gap-2 rounded-md text-xs text-text-secondary hover-highlight ${collapsed ? 'px-2 justify-center' : 'px-3 py-2'}`}
            style={collapsed ? { paddingTop: '12px', paddingBottom: '12px' } : undefined}
            title={collapsed ? t('chat.newChat') : undefined}
          >
            <EditIcon />
            {!collapsed && <span>{t('chat.newChat')}</span>}
          </button>
        </div>

        <div className={`border-b border-border ${collapsed ? 'px-2' : 'px-3'} py-3`}>
          {collapsed ? (
            <Popover
              block
              trigger={
                <div
                  // biome-ignore lint/a11y/useSemanticElements: div needed for Popover trigger compatibility
                  role="button"
                  tabIndex={0}
                  className="flex w-full items-center justify-center rounded-md px-2 text-text-secondary hover-highlight-group"
                  style={{ paddingTop: '12px', paddingBottom: '12px' }}
                  title={t('chat.recent')}
                  aria-label={t('chat.recent')}
                >
                  <ChatBubbleIcon />
                </div>
              }
              popoverWidth={200}
              popoverHeight={250}
            >
              <div className="border-b border-border pb-2 mb-2">
                <p className="text-[10px] text-text-secondary px-1 uppercase tracking-wider">
                  {t('chat.recentChats')}
                </p>
              </div>
              <div className="flex flex-col gap-1 max-h-[200px] overflow-y-auto">
                {sortedSessions.length === 0 ? (
                  <p className="text-xs text-text-tertiary px-3 py-2">{t('chat.noSessions')}</p>
                ) : (
                  sortedSessions.slice(0, MAX_VISIBLE_SESSIONS).map((s) => (
                    <button
                      key={s.key}
                      type="button"
                      onClick={() => handleSessionSelect(s.key)}
                      className={`flex items-center rounded-md px-3 py-2 text-sm transition-colors ${
                        s.key === currentSession?.key
                          ? 'bg-surface-selected text-brand-rosa border border-brand-rosa/30'
                          : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
                      }`}
                    >
                      <span className="truncate">{s.name || s.key}</span>
                    </button>
                  ))
                )}
              </div>
            </Popover>
          ) : (
            <>
              <div className="overflow-hidden max-h-8 opacity-100">
                <p className="px-1 text-[10px] uppercase tracking-wider text-text-tertiary">
                  {t('chat.recent')}
                </p>
              </div>
              {sortedSessions.length > 0 && (
                <nav className="mt-2 space-y-0.5 overflow-y-auto max-h-[240px]">
                  {sortedSessions.slice(0, MAX_VISIBLE_SESSIONS).map((s) => (
                    <SessionItem
                      key={s.key}
                      sessionKey={s.key}
                      sessionName={s.name}
                      messageCount={s.message_count}
                      selected={s.key === currentSession?.key}
                      isProcessing={processingSessions.has(s.key)}
                      onSelect={() => handleSessionSelect(s.key)}
                      onDelete={() => onDeleteSession(s.key)}
                      collapsed={false}
                    />
                  ))}
                </nav>
              )}
            </>
          )}
        </div>

        <div
          className={`mt-auto border-t border-border ${collapsed ? 'px-2' : 'px-2'} py-3 ${collapsed ? 'flex justify-center' : ''}`}
        >
          <Popover
            block
            tooltip={collapsed ? deviceName : undefined}
            trigger={
              <button
                type="button"
                className={`flex items-center rounded-md hover-highlight-group ${collapsed ? 'w-full justify-center py-2' : 'gap-1 w-full py-2 px-2'}`}
                aria-label={collapsed ? t('chat.deviceMenu') : undefined}
              >
                <div
                  className={`flex flex-shrink-0 items-center justify-center rounded ${collapsed ? 'h-6 w-6' : 'px-2 py-1'} bg-surface-hover text-xs font-medium text-text-primary`}
                >
                  {deviceName?.[0]?.toUpperCase() ?? 'L'}
                </div>
                {!collapsed && (
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-text-primary">{deviceName}</p>
                  </div>
                )}
              </button>
            }
            popoverWidth={150}
            popoverHeight={80}
          >
            <div className="flex flex-col gap-1">
              <button
                type="button"
                title={t('chat.settings')}
                aria-label={t('chat.settings')}
                className="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary"
                onClick={handleSettingsClick}
              >
                <SettingsIcon />
                <span>{t('chat.settings')}</span>
              </button>
              <button
                type="button"
                aria-label={t('chat.logout')}
                className="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-text-muted transition-colors cursor-not-allowed opacity-60"
                title="Función deshabilitada"
              >
                <span className="opacity-50">
                  <LogoutIcon />
                </span>
                <span>Cerrar sesión</span>
              </button>
            </div>
          </Popover>
        </div>
      </aside>
    </>
  )
}
