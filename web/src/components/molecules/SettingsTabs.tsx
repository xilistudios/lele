import { useTranslation } from 'react-i18next'

type SettingsTab =
  | 'general'
  | 'agents'
  | 'session'
  | 'providers'
  | 'channels'
  | 'tools'
  | 'system'
  | 'advanced'
  | 'diagnostics'

type Props = {
  activeTab: SettingsTab
  onTabChange: (tab: SettingsTab) => void
}

export function SettingsTabs({ activeTab, onTabChange }: Props) {
  const { t } = useTranslation()

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'general', label: t('settings.tabs.general') },
    { id: 'agents', label: t('settings.tabs.agents') },
    { id: 'session', label: t('settings.tabs.session') },
    { id: 'providers', label: t('settings.tabs.providers') },
    { id: 'channels', label: t('settings.tabs.channels') },
    { id: 'tools', label: t('settings.tabs.tools') },
    { id: 'system', label: t('settings.tabs.system') },
    { id: 'advanced', label: t('settings.tabs.advanced') },
    { id: 'diagnostics', label: t('settings.tabs.diagnostics') },
  ]

  return (
    <nav className="w-[200px] flex-shrink-0 border-r border-border bg-background-secondary p-4">
      <div className="space-y-1">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onTabChange(tab.id)}
            type="button"
            className={`w-full rounded-md px-3 py-2.5 text-left text-sm transition-colors ${
              activeTab === tab.id
                ? 'bg-accent-primary text-text-on-accent shadow-sm'
                : 'text-text-secondary hover:bg-surface-hover hover:text-text-primary'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>
    </nav>
  )
}
