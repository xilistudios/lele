import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { SettingsProvider } from '../../contexts/SettingsContext'
import { useSettingsConfig } from '../../hooks/useSettingsConfig'
import { SettingsFooter, SettingsHeader, SettingsTabs } from '../molecules'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'
import { Sidebar } from '../organisms/Sidebar'
import {
  AdvancedSettings,
  AgentsSettings,
  ChannelSettings,
  GeneralSettings,
  ProvidersSettings,
  SessionSettings,
  SystemSettings,
  ToolsSettings,
} from '../organisms/settings'

type Props = {
  onLogout: () => void
}

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

export function SettingsPage({ onLogout }: Props) {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()
  const [activeTab, setActiveTab] = useState<SettingsTab>('general')

  const settingsState = useSettingsConfig(api)

  const handleSave = async () => {
    const isValid = await settingsState.validate()
    if (isValid) {
      await settingsState.save()
    }
  }

  const renderTabContent = () => {
    if (settingsState.isLoading) {
      return (
        <div className="flex h-64 items-center justify-center">
          <div className="text-sm text-[#888]">{t('common.loading')}</div>
        </div>
      )
    }

    switch (activeTab) {
      case 'general':
        return <GeneralSettings />
      case 'agents':
        return <AgentsSettings />
      case 'session':
        return <SessionSettings />
      case 'providers':
        return <ProvidersSettings />
      case 'channels':
        return <ChannelSettings />
      case 'tools':
        return <ToolsSettings />
      case 'system':
        return <SystemSettings />
      case 'advanced':
        return <AdvancedSettings />
      case 'diagnostics':
        return <DiagnosticsPanel />
      default:
        return null
    }
  }

  return (
    <SettingsProvider settingsState={settingsState} api={api}>
      <div className="flex h-screen overflow-hidden bg-[#1a1a1a] text-[#e0e0e0]">
        <Sidebar
          collapsed={!sidebarOpen}
          mobileOpen={sidebarOpen}
          onClose={() => onToggleSidebar()}
        />
        <main className="flex flex-1 flex-col overflow-hidden">
          <SettingsHeader
            onToggleSidebar={onToggleSidebar}
            onLogout={onLogout}
            configPath={settingsState.metadata?.config_path}
          />

          <div className="flex flex-1 overflow-hidden">
            <SettingsTabs activeTab={activeTab} onTabChange={setActiveTab} />

            <div className="flex flex-1 flex-col overflow-hidden">
              <div className="flex-1 overflow-y-auto p-6">{renderTabContent()}</div>

              <SettingsFooter
                saveState={settingsState.saveState}
                saveError={settingsState.saveError}
                hasErrors={settingsState.hasErrors}
                isDirty={settingsState.isDirty}
                validationErrorsCount={settingsState.validationErrors.length}
                onReset={settingsState.reset}
                onSave={handleSave}
              />
            </div>
          </div>
        </main>
      </div>
    </SettingsProvider>
  )
}
