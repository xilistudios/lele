import { useTranslation } from 'react-i18next'

type Props = {
  onToggleSidebar: () => void
  onLogout: () => void
  configPath?: string
}

export function SettingsHeader({ onToggleSidebar, onLogout, configPath }: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-b border-border px-6 py-4">
      <div className="flex items-center gap-4">
        <button
          type="button"
          onClick={onToggleSidebar}
          className="text-text-secondary transition-colors hover:text-text-primary"
          aria-label="Toggle sidebar"
        >
          <svg
            width="20"
            height="20"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2"
            aria-hidden="true"
          >
            <line x1="3" y1="6" x2="21" y2="6" />
            <line x1="3" y1="12" x2="21" y2="12" />
            <line x1="3" y1="18" x2="21" y2="18" />
          </svg>
        </button>
        <h1 className="text-xl font-semibold text-text-primary">{t('chat.settings')}</h1>
        {configPath && <span className="text-xs text-text-tertiary">{configPath}</span>}
      </div>
      <button
        onClick={onLogout}
        type="button"
        className="rounded-md bg-state-error px-4 py-2 text-sm text-text-on-accent transition-colors hover:bg-[#FF7B7B]"
      >
        {t('chat.logout')}
      </button>
    </div>
  )
}
