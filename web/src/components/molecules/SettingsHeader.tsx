import { useTranslation } from 'react-i18next'

type Props = {
  onToggleSidebar: () => void
  onLogout: () => void
  configPath?: string
}

export function SettingsHeader({ onToggleSidebar, onLogout, configPath }: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-4">
      <div className="flex items-center gap-4">
        <button
          type="button"
          onClick={onToggleSidebar}
          className="text-[#888] transition-colors hover:text-white"
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
        <h1 className="text-xl font-semibold text-white">{t('chat.settings')}</h1>
        {configPath && <span className="text-xs text-[#666]">{configPath}</span>}
      </div>
      <button
        onClick={onLogout}
        type="button"
        className="rounded-md bg-rose-600 px-4 py-2 text-sm text-white transition-colors hover:bg-rose-500"
      >
        {t('chat.logout')}
      </button>
    </div>
  )
}
