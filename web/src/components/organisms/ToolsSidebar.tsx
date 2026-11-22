import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ConnectionIndicator } from '../atoms/ConnectionIndicator'
import { IconButton } from '../atoms/IconButton'
import { ClockIcon, CloseIcon, LockIcon, SkillsIcon, TerminalIcon } from '../atoms/Icons'

type ToolsSidebarProps = {
  isOpen: boolean
  onClose: () => void
  connectionStatus: 'disconnected' | 'connecting' | 'connected'
  apiUrl?: string
}

const ANIMATION_DURATION_MS = 300

type ToolsRow = { path: string; labelKey: string; icon: typeof SkillsIcon }

const TOOLS_ROWS: ToolsRow[] = [
  { path: '/skills', labelKey: 'sidebar.skills', icon: SkillsIcon },
  { path: '/cron', labelKey: 'sidebar.cron', icon: ClockIcon },
  { path: '/background-exec', labelKey: 'sidebar.backgroundExecs', icon: TerminalIcon },
  { path: '/secrets', labelKey: 'sidebar.secrets', icon: LockIcon },
]

export function ToolsSidebar({ isOpen, onClose, connectionStatus, apiUrl }: ToolsSidebarProps) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [visible, setVisible] = useState(false)
  const [animate, setAnimate] = useState(false)
  const rafRef = useRef<number>(0)

  useEffect(() => {
    if (isOpen) {
      setVisible(true)
      rafRef.current =
        typeof requestAnimationFrame !== 'undefined'
          ? requestAnimationFrame(() => {
              rafRef.current =
                typeof requestAnimationFrame !== 'undefined'
                  ? requestAnimationFrame(() => setAnimate(true))
                  : 0
            })
          : 0
      return () => {
        if (typeof cancelAnimationFrame !== 'undefined') cancelAnimationFrame(rafRef.current)
      }
    }
    setAnimate(false)
    const timer = setTimeout(() => setVisible(false), ANIMATION_DURATION_MS)
    return () => {
      clearTimeout(timer)
      if (typeof cancelAnimationFrame !== 'undefined') cancelAnimationFrame(rafRef.current)
    }
  }, [isOpen])

  useEffect(() => {
    if (!isOpen) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [isOpen, onClose])

  const handleSelect = useCallback(
    (path: string) => {
      navigate(path)
      onClose()
    },
    [navigate, onClose],
  )

  if (!visible) return null

  return (
    <>
      <button
        type="button"
        className={`fixed inset-0 z-40 bg-overlay transition-opacity duration-300 ease-out ${animate ? 'opacity-100' : 'opacity-0 pointer-events-none'}`}
        onClick={onClose}
        aria-label={t('common.close')}
      />

      <aside
        data-testid="tools-sidebar"
        aria-label={t('chat.tools')}
        className={`bg-background-secondary fixed right-0 top-0 z-50 h-full w-80 max-w-[90vw] safe-area-y border-l border-border shadow-lg flex flex-col transition-transform duration-300 ease-out ${animate ? 'translate-x-0' : 'translate-x-full'}`}
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h3 className="text-sm font-medium text-text-primary">{t('chat.tools')}</h3>
          <IconButton
            onClick={onClose}
            className="rounded p-1 text-text-tertiary transition-colors hover:bg-surface-hover hover:text-text-secondary"
            ariaLabel={t('common.close')}
          >
            <CloseIcon size={16} />
          </IconButton>
        </div>

        <nav className="flex flex-1 flex-col gap-1 overflow-y-auto p-2">
          {TOOLS_ROWS.map((row) => (
            <button
              key={row.path}
              type="button"
              onClick={() => handleSelect(row.path)}
              aria-label={t(row.labelKey)}
              className="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-left text-sm text-text-secondary transition-colors hover:bg-surface-hover hover:text-text-primary"
            >
              <span className="flex h-8 w-8 flex-none items-center justify-center rounded-md bg-background-tertiary text-text-secondary">
                <row.icon size={16} />
              </span>
              <span>{t(row.labelKey)}</span>
            </button>
          ))}
        </nav>

        <div className="flex items-center gap-2 border-t border-border px-4 py-3">
          <span className="text-xs text-text-tertiary">{t('common.status')}</span>
          <div className="ml-auto">
            <ConnectionIndicator status={connectionStatus} apiUrl={apiUrl} />
          </div>
        </div>
      </aside>
    </>
  )
}
