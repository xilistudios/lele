import { useTranslation } from 'react-i18next'
import { SidebarToggleIcon } from '../atoms/Icons'

type Props = {
  configPath?: string
  title?: string
  onOpenMobileSidebar?: () => void
}

export function SettingsHeader({ configPath, title, onOpenMobileSidebar }: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-b border-border px-4 py-4 md:px-6">
      <div className="flex items-center gap-2 md:gap-4">
        {onOpenMobileSidebar && (
          <button
            type="button"
            onClick={onOpenMobileSidebar}
            className="flex md:hidden items-center justify-center rounded-md p-1.5 text-text-secondary hover:bg-surface-hover hover:text-text-primary transition-colors mr-1"
            aria-label={t('chat.toggleSidebar')}
          >
            <SidebarToggleIcon size={20} />
          </button>
        )}
        <h1 className="text-base font-semibold text-text-primary md:text-xl">{title ?? t('chat.settings')}</h1>
        {configPath && (
          <span className="hidden max-w-[40vw] truncate text-xs text-text-tertiary md:inline" title={configPath}>
            {configPath}
          </span>
        )}
      </div>
    </div>
  )
}
