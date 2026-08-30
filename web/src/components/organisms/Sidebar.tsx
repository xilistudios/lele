import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useLocation, useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { useIsMobile } from '../../hooks/useIsMobile'
import { getModeTheme } from '../../lib/modeTheme'
import { IconButton } from '../atoms/IconButton'
import {
  AgentsIcon,
  ChatBubbleIcon,
  ClockIcon,
  HistoryIcon,
  LockIcon,
  LogoutIcon,
  MoreIcon,
  PlusCircleIcon,
  ProvidersIcon,
  SearchIcon,
  SettingsIcon,
  SidebarToggleIcon,
  SkillsIcon,
  TerminalIcon,
  TrashIcon,
  UserIcon,
} from '../atoms/Icons'
import { Logo } from '../atoms/Logo'
import { Popover } from '../atoms/Popover'
import { ModeSelector } from '../molecules/ModeSelector'
import { SessionItem } from '../molecules/SessionItem'
import { QuickChatPanel } from './QuickChatPanel'

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
    chatMode,
    onCreateSession,
    onDeleteSession,
    onToggleSidebar,
    onLogout,
  } = useAppLogicContext()
  const isMobile = useIsMobile()

  const deviceName = session?.device_name ?? 'lele'

  const isOnChatPage = ['/', '/chat/'].some((prefix) => {
    if (prefix === '/') return location.pathname === '/'
    return location.pathname.startsWith(prefix)
  })

  const selectedKey = parentSessionKey ?? currentSessionKey

  const sortedSessions = useMemo(() => {
    const visible = sessions.filter(
      (s) => s.kind !== 'subagent' && (s.mode || 'agent') === chatMode,
    )
    return [...visible].sort(
      (b, a) => new Date(a.updated).getTime() - new Date(b.updated).getTime(),
    )
  }, [sessions, selectedKey, chatMode])

  // Only show current session on chat pages
  const currentSession = isOnChatPage
    ? (sortedSessions.find((s) => s.key === selectedKey) ?? sortedSessions[0] ?? null)
    : null

  const [panelOpen, setPanelOpen] = useState(false)
  const [panelFocusSearch, setPanelFocusSearch] = useState(false)
  const [recentExpanded, setRecentExpanded] = useState(false)

  const openPanel = (focusSearch = false) => {
    setPanelFocusSearch(focusSearch)
    setPanelOpen(true)
  }

  const recentSessions = recentExpanded
    ? sortedSessions
    : sortedSessions.slice(0, MAX_VISIBLE_SESSIONS)

  const handleSessionSelect = (key: string) => {
    navigate(`/chat/${encodeURIComponent(key)}`)
    if (isMobile) onClose()
  }

  const handleLogoutClick = useCallback(async () => {
    if (isMobile) onClose()
    await onLogout()
    navigate('/pair', { replace: true })
  }, [onLogout, navigate, isMobile, onClose])

  const isActiveRoute = (path: string) => location.pathname === path

  const navItems = [
    {
      path: '/chats',
      label: t('sidebar.chats'),
      icon: ChatBubbleIcon,
    },
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
    {
      path: '/background-exec',
      label: t('sidebar.backgroundExecs'),
      icon: TerminalIcon,
    },
    {
      path: '/cron',
      label: t('sidebar.cron'),
      icon: ClockIcon,
    },
    {
      path: '/secrets',
      label: t('sidebar.secrets'),
      icon: LockIcon,
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
        aria-hidden={!mobileOpen}
      />

      <aside
        className={`fixed inset-y-0 left-0 z-50 flex flex-col border-r border-border bg-background-secondary transition-all duration-300 ease-in-out md:relative md:translate-x-0 ${
          mobileOpen ? 'glass-effect translate-x-0' : '-translate-x-full'
        } ${collapsed ? 'w-[60px]' : 'w-[280px]'}`}
      >
        <div
          className={`flex items-center px-4 py-3 ${collapsed ? 'justify-center' : 'justify-between'}`}
        >
          {!collapsed && <Logo collapsed={collapsed} />}
          {collapsed && (
            <div className="hidden md:flex group relative items-center justify-center">
              <IconButton
                onClick={onToggleSidebar}
                ariaLabel={t('sidebar.expand')}
                className="flex items-center justify-center h-10 w-10"
              >
                <SidebarToggleIcon size={16} />
              </IconButton>
              <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                {t('sidebar.expand')}
              </span>
            </div>
          )}
          {!collapsed && (
            <div className="hidden md:flex group relative items-center justify-center">
              <IconButton
                onClick={onToggleSidebar}
                ariaLabel={t('sidebar.collapse')}
                className="flex items-center justify-center h-10 w-10"
              >
                <SidebarToggleIcon size={16} />
              </IconButton>
              <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                {t('sidebar.collapse')}
              </span>
            </div>
          )}
          {/* Mobile-only close button */}
          <IconButton
            onClick={onClose}
            ariaLabel={t('common.close')}
            className={`flex md:hidden items-center justify-center h-10 w-10 ${collapsed ? 'md:hidden' : ''}`}
          >
            <svg
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              role="img"
              aria-label={t('common.close')}
            >
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </IconButton>
        </div>

        <div
          className={`px-2 ${collapsed ? 'flex flex-col items-center gap-1' : 'flex flex-col gap-1'}`}
        >
          {collapsed ? (
            <>
              <div className="group relative flex items-center justify-center">
                <IconButton
                  onClick={() => onCreateSession()}
                  ariaLabel={t('chat.newChat')}
                  variant="nav"
                  className="flex items-center justify-center h-10 w-10"
                >
                  <PlusCircleIcon size={24} />
                </IconButton>
                <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                  {t('chat.newChat')}
                </span>
              </div>
              <div className="group relative flex items-center justify-center">
                <IconButton
                  onClick={() => openPanel(true)}
                  ariaLabel={t('chat.search')}
                  variant="nav"
                  className="flex items-center justify-center h-10 w-10"
                >
                  <SearchIcon size={16} />
                </IconButton>
                <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                  {t('chat.search')}
                </span>
              </div>
            </>
          ) : (
            <>
              <button
                type="button"
                onClick={() => onCreateSession()}
                aria-label={t('chat.newChat')}
                className="flex items-center gap-2 w-full rounded-md px-2 py-1 text-sm text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary"
              >
                <div className="h-10 w-10 flex items-center justify-center">
                  <PlusCircleIcon size={24} />
                </div>
                <span className="leading-none">{t('chat.newChat')}</span>
              </button>
              <button
                type="button"
                onClick={() => openPanel(true)}
                aria-label={t('chat.search')}
                className="flex items-center gap-2 w-full rounded-md px-2 py-1 text-sm text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary"
              >
                <div className="h-10 w-10 flex items-center justify-center">
                  <SearchIcon size={16} />
                </div>
                <span>{t('chat.search')}</span>
              </button>
            </>
          )}
        </div>

        {!collapsed && (
          <div className="px-3 pt-1 pb-1">
            <ModeSelector />
          </div>
        )}

        <div className={collapsed ? 'px-2' : 'flex min-h-0 flex-1 flex-col px-3 py-3'}>
          {collapsed ? (
            <Popover
              block
              tooltip={t('chat.recent')}
              trigger={
                <div className="group relative flex items-center justify-center">
                  <IconButton
                    ariaLabel={t('chat.recent')}
                    variant="nav"
                    className="flex items-center justify-center h-10 w-10"
                  >
                    <HistoryIcon size={16} />
                  </IconButton>
                  <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                    {t('chat.recent')}
                  </span>
                </div>
              }
              popoverWidth={220}
              popoverHeight={280}
            >
              <div className="pb-2 mb-2 border-b border-border">
                <p className="text-[10px] text-text-secondary px-1 uppercase tracking-wider">
                  {t('chat.recentChats')}
                </p>
              </div>
              <div className="flex flex-col gap-1 max-h-[200px] overflow-y-auto">
                {sortedSessions.length === 0 ? (
                  <p className="text-xs text-text-tertiary px-3 py-2">{t('chat.noSessions')}</p>
                ) : (
                  <>
                    {recentSessions.map((s) => (
                      <button
                        key={s.key}
                        type="button"
                        onClick={() => handleSessionSelect(s.key)}
                        className={`flex items-center rounded-md px-3 py-2 text-sm transition-colors ${
                          s.key === currentSession?.key
                            ? getModeTheme(s.mode).selectedItem
                            : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
                        }`}
                      >
                        <span className="truncate">{s.name || s.key}</span>
                      </button>
                    ))}
                  </>
                )}
              </div>
              {sortedSessions.length > MAX_VISIBLE_SESSIONS && (
                <button
                  type="button"
                  onClick={() => setRecentExpanded((v) => !v)}
                  className="flex items-center justify-center gap-1 w-full mt-2 pt-2 border-t border-border text-xs text-brand-rosa hover:text-brand-rosa/80 transition-colors px-2 py-1"
                >
                  <span>{recentExpanded ? t('chat.showLess') : t('chat.showMore')}</span>
                  {!recentExpanded && (
                    <span className="text-text-tertiary">
                      ({sortedSessions.length - MAX_VISIBLE_SESSIONS})
                    </span>
                  )}
                </button>
              )}
            </Popover>
          ) : (
            <>
              <div className="flex shrink-0 items-center justify-between px-1 py-1">
                <p className="text-[10px] uppercase tracking-wider text-text-tertiary">
                  {t('chat.recent')}
                </p>
                <Popover
                  tooltip={t('chat.more')}
                  trigger={
                    <button
                      type="button"
                      className="flex items-center justify-center rounded p-0.5 text-text-tertiary hover:text-text-secondary hover:bg-surface-hover transition-colors"
                      aria-label={t('chat.more')}
                    >
                      <MoreIcon size={12} />
                    </button>
                  }
                  popoverWidth={200}
                  popoverHeight={60}
                >
                  <div className="flex flex-col gap-1">
                    <button
                      type="button"
                      className="flex items-center gap-2 w-full whitespace-nowrap rounded-md px-3 py-2 text-sm text-red-400 hover:bg-surface-hover hover:text-red-300 transition-colors"
                    >
                      <TrashIcon size={14} />
                      <span>{t('chat.deleteAllChats')}</span>
                    </button>
                  </div>
                </Popover>
              </div>
              {sortedSessions.length > 0 && (
                <>
                  <nav className="mt-2 min-h-0 flex-1 space-y-0.5 overflow-y-auto">
                    {recentSessions.map((s) => (
                      <SessionItem
                        key={s.key}
                        sessionKey={s.key}
                        sessionName={s.name}
                        selected={s.key === currentSession?.key}
                        isProcessing={processingSessions.has(s.key)}
                        onSelect={() => handleSessionSelect(s.key)}
                        onDelete={() => onDeleteSession(s.key)}
                        collapsed={false}
                        mode={s.mode}
                      />
                    ))}
                  </nav>
                  {sortedSessions.length > MAX_VISIBLE_SESSIONS && (
                    <button
                      type="button"
                      onClick={() => setRecentExpanded((v) => !v)}
                      className="flex shrink-0 items-center justify-center gap-1 w-full mt-2 pt-2 border-t border-border text-xs text-brand-rosa hover:text-brand-rosa/80 transition-colors px-2 py-1"
                    >
                      <span>{recentExpanded ? t('chat.showLess') : t('chat.showMore')}</span>
                      {!recentExpanded && (
                        <span className="text-text-tertiary">
                          ({sortedSessions.length - MAX_VISIBLE_SESSIONS})
                        </span>
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
          className={
            collapsed
              ? 'flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-2'
              : 'flex shrink-0 flex-col gap-1 px-2 pt-1'
          }
        >
          {collapsed ? (
            <>
              {navItems.map((item) => (
                <div key={item.path} className="group relative flex items-center justify-center">
                  <IconButton
                    onClick={() => {
                      navigate(item.path)
                      if (isMobile) onClose()
                    }}
                    title={item.label}
                    ariaLabel={item.label}
                    variant="nav"
                    className={`flex items-center justify-center h-10 w-10 ${
                      isActiveRoute(item.path) ? 'text-brand-rosa bg-surface-selected' : ''
                    }`}
                  >
                    <item.icon size={16} />
                  </IconButton>
                  <span className="absolute left-full top-1/2 -translate-y-1/2 ml-2 px-2 py-1 rounded bg-surface-hover text-xs font-medium text-text-secondary transition-opacity duration-100 pointer-events-none whitespace-nowrap opacity-0 group-hover:opacity-100">
                    {item.label}
                  </span>
                </div>
              ))}
            </>
          ) : (
            <>
              {navItems.map((item) => (
                <button
                  key={item.path}
                  type="button"
                  onClick={() => {
                    navigate(item.path)
                    if (isMobile) onClose()
                  }}
                  aria-label={item.label}
                  className={`flex items-center gap-2 w-full rounded-md px-2 py-1 text-sm transition-colors hover:bg-surface-hover ${
                    isActiveRoute(item.path)
                      ? 'bg-surface-selected text-brand-rosa border border-brand-rosa/30'
                      : 'text-text-secondary hover:text-text-primary'
                  }`}
                >
                  <div className="h-10 w-10 flex items-center justify-center">
                    <item.icon size={16} />
                  </div>
                  <span>{item.label}</span>
                </button>
              ))}
            </>
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
                className={`flex items-center rounded-md hover-highlight-group ${collapsed ? 'w-full justify-center py-2' : 'gap-2 w-full py-2 px-2'}`}
                aria-label={collapsed ? t('chat.deviceMenu') : undefined}
              >
                <div
                  className="flex flex-shrink-0 items-center justify-center bg-surface-hover text-text-primary h-7 w-7 rounded"
                  style={{ transform: 'rotate(45deg)' }}
                >
                  <div style={{ transform: 'rotate(-45deg)' }}>
                    <UserIcon size={12} />
                  </div>
                </div>
                {!collapsed && (
                  <div className="min-w-0 flex-1 text-left">
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
                className="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-text-muted transition-colors hover:bg-surface-hover hover:text-red-400"
                onClick={handleLogoutClick}
              >
                <LogoutIcon />
                <span>{t('chat.logout')}</span>
              </button>
            </div>
          </Popover>
        </div>
      </aside>

      <QuickChatPanel
        isOpen={panelOpen}
        onClose={() => setPanelOpen(false)}
        initialFocusSearch={panelFocusSearch}
      />
    </>
  )
}
