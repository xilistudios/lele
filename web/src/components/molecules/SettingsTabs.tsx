import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'

type SettingsTab =
  | 'general'
  | 'session'
  | 'channels'
  | 'native'
  | 'groups'
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
  const { groupsEnabled } = useAppLogicContext()

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'general', label: t('settings.tabs.general') },
    { id: 'session', label: t('settings.tabs.session') },
    { id: 'channels', label: t('settings.tabs.channels') },
    { id: 'native', label: t('settings.tabs.native') },
    ...(groupsEnabled ? [{ id: 'groups' as const, label: t('settings.tabs.groups') }] : []),
    { id: 'tools', label: t('settings.tabs.tools') },
    { id: 'system', label: t('settings.tabs.system') },
    { id: 'advanced', label: t('settings.tabs.advanced') },
    { id: 'diagnostics', label: t('settings.tabs.diagnostics') },
  ]

  return (
    <nav className="w-full md:w-[200px] flex-shrink-0 border-b md:border-b-0 md:border-r border-border bg-background-secondary p-2 md:p-4 overflow-x-auto md:overflow-x-visible no-scrollbar">
      <div className="flex md:flex-col gap-1 md:space-y-1 min-w-max md:min-w-0">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onTabChange(tab.id)}
            type="button"
            className={`rounded-md px-3 py-1.5 md:py-2.5 text-center md:text-left text-xs md:text-sm font-medium transition-colors whitespace-nowrap flex-shrink-0 ${
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
