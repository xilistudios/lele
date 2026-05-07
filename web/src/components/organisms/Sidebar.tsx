import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useIsMobile } from '../../hooks/useIsMobile'
import {
  AgentsIcon,
  ChatBubbleIcon,
  LogoutIcon,
  PlusCircleIcon,
  ProvidersIcon,
  SettingsIcon,
  SidebarToggleIcon,
  SkillsIcon,
} from '../atoms/Icons'
import { IconButton } from '../atoms/IconButton'
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
  const location = useLocation()
  const { session } = useAuthContext()
  const {
    sessions,
    currentSessionKey,
    parentSessionKey,
    processingSessions,
    onCreateSession,
    onDeleteSession,
    onToggleSidebar,
  } = useAppLogicContext()
  const isMobile = useIsMobile()

  const deviceName = session?.device_name ?? 'lele'

  const sortedSessions = useMemo(() => {
    const visible = sessions.filter((s) => !s.key.startsWith('subagent:'))
    return [...visible].sort(
      (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
    )
  }, [sessions])

  // Only show current session on chat pages
  const isOnChatPage = ['/', '/chat/'].some(prefix => {
    if (prefix === '/') return location.pathname === '/'
    return location.pathname.startsWith(prefix)
  })

  const selectedKey = parentSessionKey ?? currentSessionKey
  const currentSession = isOnChatPage
    ? (sortedSessions.find((s) => s.key === selectedKey) ?? sortedSessions[0] ?? null)
    : null

  // Expanded state - shows all sessions when true
  const [expanded, setExpanded] = useState(false)

  const prevCountRef = useRef(sessions.length)

  // Only reset expanded when sessions are removed (user deleted one),
  // not when a new session arrives while the user is browsing the expanded list.
  useEffect(() => {
    if (sessions.length < prevCountRef.current) {
      setExpanded(false)
    }
    prevCountRef.current = sessions.length
  }, [sessions.length])

  const visibleSessions = expanded
    ? sortedSessions
    : sortedSessions.slice(0, MAX_VISIBLE_SESSIONS)

  const handleSessionSelect = (key: string) => {
    navigate(`/chat/${encodeURIComponent(key)}`)
    if (isMobile) onClose()
  }

  const isActiveRoute = (path: string) => location.pathname === path

  const navItems = [
    {
      path: '/agents',
      label: t('sidebar.agents'),
      icon: AgentsIcon,
    },
    {
      path: '/providers',
      label: t('sidebar.providers'),
      icon: ProvidersIcon,
    },
    {
      path: '/skills',
      label: t('sidebar.skills'),
      icon: SkillsIcon,
    },
  ]

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
          className={`flex items-center px-4 py-3 ${collapsed ? 'justify-center' : 'justify-between'}`}
        >
          {!collapsed && <Logo collapsed={collapsed} />}
          {collapsed && (
            <div className="hidden md:flex group relative">
              <IconButton
                onClick={onToggleSidebar}
                ariaLabel={t('sidebar.expand')}
              >
                <SidebarToggleIcon />
              </IconButton>
              <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                {t('sidebar.expand')}
              </span>
            </div>
          )}
          {!collapsed && (
            <div className="hidden md:flex group relative">
              <IconButton
                onClick={onToggleSidebar}
                ariaLabel={t('sidebar.collapse')}
              >
                <SidebarToggleIcon />
              </IconButton>
              <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                {t('sidebar.collapse')}
              </span>
            </div>
          )}
        </div>

        <div className={`px-2 py-3 ${collapsed ? 'flex justify-center' : ''}`}>
          {collapsed ? (
            <div className="group relative py-3">
              <IconButton
                onClick={onCreateSession}
                ariaLabel={t('chat.newChat')}
                variant="nav"
              >
                <PlusCircleIcon size={32} />
              </IconButton>
              <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                {t('chat.newChat')}
              </span>
            </div>
          ) : (
            <IconButton
              onClick={onCreateSession}
              ariaLabel={t('chat.newChat')}
              variant="nav-full"
            >
              <PlusCircleIcon size={28} />
              <span>{t('chat.newChat')}</span>
            </IconButton>
          )}
        </div>

        <div className={`${collapsed ? 'px-2' : 'px-3'} py-3`}>
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
              <div className="pb-2 mb-2">
                <p className="text-[10px] text-text-secondary px-1 uppercase tracking-wider">
                  {t('chat.recentChats')}
                </p>
              </div>
              <div className="flex flex-col gap-1 max-h-[200px] overflow-y-auto">
                {sortedSessions.length === 0 ? (
                  <p className="text-xs text-text-tertiary px-3 py-2">{t('chat.noSessions')}</p>
                ) : (
                  <>
                    {visibleSessions.map((s) => (
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
                    ))}
                    {sortedSessions.length > MAX_VISIBLE_SESSIONS && (
                      <button
                        type="button"
                        onClick={() => setExpanded((v) => !v)}
                        className="text-xs text-brand-rosa hover:text-brand-rosa/80 px-3 py-1 mt-1 border-t border-border pt-2"
                      >
                        {expanded ? t('chat.showLess') : t('chat.showMore')} ({sortedSessions.length - MAX_VISIBLE_SESSIONS})
                      </button>
                    )}
                  </>
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
                <>
                  <nav className="mt-2 space-y-0.5 overflow-y-auto max-h-[240px]">
                    {visibleSessions.map((s) => (
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
                  {sortedSessions.length > MAX_VISIBLE_SESSIONS && (
                    <button
                      type="button"
                      onClick={() => setExpanded((v) => !v)}
                      className="flex items-center justify-center gap-1 w-full mt-2 pt-2 border-t border-border text-xs text-brand-rosa hover:text-brand-rosa/80 transition-colors px-2 py-1"
                    >
                      <svg
                        className={`w-3 h-3 transition-transform duration-200 ${expanded ? 'rotate-180' : ''}`}
                        fill="none"
                        viewBox="0 0 24 24"
                        stroke="currentColor"
                        strokeWidth={2}
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
                      </svg>
                      <span>{expanded ? t('chat.showLess') : t('chat.showMore')}</span>
                      {!expanded && (
                        <span className="text-text-tertiary">({sortedSessions.length - MAX_VISIBLE_SESSIONS})</span>
                      )}
                    </button>
                  )}
                </>
              )}
            </>
          )}
        </div>

        {/* Agents & Providers navigation */}
        <nav
          className={`px-2 py-3 ${collapsed ? 'flex flex-col items-center gap-1' : ''}`}
        >
          {collapsed ? (
            <>
              {navItems.map((item) => (
                <IconButton
                  key={item.path}
                  onClick={() => {
                    navigate(item.path)
                    if (isMobile) onClose()
                  }}
                  title={item.label}
                  ariaLabel={item.label}
                  variant="nav"
                  className={`justify-center py-2 ${
                    isActiveRoute(item.path)
                      ? 'text-brand-rosa bg-surface-selected'
                      : ''
                  }`}
                >
                  <item.icon size={16} />
                </IconButton>
              ))}
            </>
          ) : (
            navItems.map((item) => (
              <IconButton
                key={item.path}
                onClick={() => {
                  navigate(item.path)
                  if (isMobile) onClose()
                }}
                title={item.label}
                ariaLabel={item.label}
                variant="nav-full"
                className={
                  isActiveRoute(item.path)
                    ? 'bg-surface-selected text-brand-rosa border border-brand-rosa/30'
                    : ''
                }
              >
                <item.icon size={16} />
                <span>{item.label}</span>
              </IconButton>
            ))
          )}
        </nav>

        <div
          className={`mt-auto border-t border-border px-2 py-3 ${collapsed ? 'flex justify-center' : ''}`}
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
              <IconButton
                title={t('chat.settings')}
                ariaLabel={t('chat.settings')}
                variant="nav-full"
                className="px-3 py-2"
                onClick={() => {
                  navigate('/settings/general')
                  if (isMobile) onClose()
                }}
              >
                <SettingsIcon />
                <span>{t('chat.settings')}</span>
              </IconButton>
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
