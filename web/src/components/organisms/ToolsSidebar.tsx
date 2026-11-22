import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ConnectionIndicator } from '../atoms/ConnectionIndicator'
import { IconButton } from '../atoms/IconButton'
import { ClockIcon, CloseIcon, LockIcon, SkillsIcon, TerminalIcon } from '../atoms/Icons'
import { SideDrawer } from '../atoms/SideDrawer'

type ToolsSidebarProps = {
  isOpen: boolean
  onClose: () => void
  connectionStatus: 'disconnected' | 'connecting' | 'connected'
  apiUrl?: string
}

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

  const handleSelect = useCallback(
    (path: string) => {
      navigate(path)
      onClose()
    },
    [navigate, onClose],
  )

  return (
    <SideDrawer
      isOpen={isOpen}
      onClose={onClose}
      ariaLabel={t('chat.tools')}
      testId="tools-sidebar"
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
    </SideDrawer>
  )
}
