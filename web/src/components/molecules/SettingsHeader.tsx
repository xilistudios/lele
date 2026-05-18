import { useTranslation } from 'react-i18next'
import { SidebarToggleIcon } from '../atoms/Icons'

type Props = {
  configPath?: string
  title?: string
  onToggleSidebar?: () => void
}

export function SettingsHeader({ configPath, title, onToggleSidebar }: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-b border-border px-6 py-4">
      <div className="flex items-center gap-4">
        {onToggleSidebar && (
          <button
            type="button"
            onClick={onToggleSidebar}
            className="text-text-secondary transition-colors hover:text-text-primary"
            aria-label="Toggle sidebar"
          >
            <SidebarToggleIcon />
          </button>
        )}
        <h1 className="text-xl font-semibold text-text-primary">
          {title ?? t('chat.settings')}
        </h1>
        {configPath && (
          <span className="text-xs text-text-tertiary">{configPath}</span>
        )}
>>>>>>> 8873ac6f45ee644b69ac9723bed9c7882dec1241
      </div>
    </div>
  )
}
