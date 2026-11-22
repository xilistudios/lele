import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import type { SettingsTab } from '../../lib/settingsTabs'

type Props = {
  activeTab: SettingsTab
  onTabChange: (tab: SettingsTab) => void
}

export function SettingsTabs({ activeTab, onTabChange }: Props) {
  const { t } = useTranslation()
  const { groupsEnabled } = useAppLogicContext()

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: 'general', label: t('settings.tabs.general') },
    // Agents and Providers used to be top-level sidebar destinations. They are
    // configuration, so they live here now — same draft, same footer save.
    { id: 'agents', label: t('settings.tabs.agents') },
    { id: 'providers', label: t('settings.tabs.providers') },
    { id: 'session', label: t('settings.tabs.session') },
    { id: 'channels', label: t('settings.tabs.channels') },
    { id: 'native', label: t('settings.tabs.native') },
    ...(groupsEnabled ? [{ id: 'groups' as const, label: t('settings.tabs.groups') }] : []),
    { id: 'tools', label: t('settings.tabs.tools') },
    { id: 'system', label: t('settings.tabs.system') },
    { id: 'updates', label: t('settings.tabs.updates') },
    { id: 'advanced', label: t('settings.tabs.advanced') },
    { id: 'diagnostics', label: t('settings.tabs.diagnostics') },
    { id: 'logs', label: t('settings.tabs.logs') },
  ]

  return (
    <nav
      data-testid="settings-tabs"
      className="w-full md:w-[200px] flex-shrink-0 border-b md:border-b-0 md:border-r border-border bg-background-secondary p-2 md:p-4 overflow-x-auto md:overflow-x-visible no-scrollbar"
    >
      <div className="flex md:flex-col gap-1 md:space-y-1 min-w-max md:min-w-0">
        {tabs.map((tab) => (
          <button
            key={tab.id}
            onClick={() => onTabChange(tab.id)}
            type="button"
            data-testid={`settings-tab-${tab.id}`}
            aria-current={activeTab === tab.id ? 'page' : undefined}
            className={`rounded-md border px-3 py-1.5 md:py-2.5 text-center md:text-left text-xs md:text-sm font-medium transition-colors whitespace-nowrap flex-shrink-0 ${
              activeTab === tab.id
                ? 'border-accent-primary/30 bg-surface-selected text-accent-primary'
                : 'border-transparent text-text-secondary hover:bg-surface-hover hover:text-text-primary'
            }`}
          >
            {tab.label}
          </button>
        ))}
      </div>
    </nav>
  )
}
