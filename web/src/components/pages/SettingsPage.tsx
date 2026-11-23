import { useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Outlet, useMatch, useNavigate } from 'react-router-dom'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { SettingsProvider } from '../../contexts/SettingsContext'
import { useSettingsConfig } from '../../hooks/useSettingsConfig'
import { type SettingsTab, isSettingsTab } from '../../lib/settingsTabs'
import { SettingsFooter, SettingsHeader, SettingsTabs } from '../molecules'
import { Sidebar } from '../organisms/Sidebar'

export function SettingsPage() {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, mobileSidebarOpen, onCloseMobileSidebar, onOpenMobileSidebar } =
    useAppLogicContext()
  const navigate = useNavigate()

  const tabMatch = useMatch('/settings/:tab')
  const activeTab: SettingsTab | null = isSettingsTab(tabMatch?.params.tab)
    ? (tabMatch?.params.tab as SettingsTab)
    : null
  const isAgentDetail = !!useMatch('/settings/agents/:agentId/:tab?')

  const rawTab = tabMatch?.params.tab
  useEffect(() => {
    if (rawTab !== undefined && !isSettingsTab(rawTab)) {
      navigate('/settings/general', { replace: true })
    }
  }, [rawTab, navigate])

  const settingsState = useSettingsConfig(api)

  const handleSave = async () => {
    const isValid = await settingsState.validate()
    if (isValid) {
      await settingsState.save()
    }
  }

  const handleTabChange = (tab: SettingsTab) => {
    navigate(`/settings/${tab}`)
  }

  return (
    <SettingsProvider settingsState={settingsState} api={api}>
      <div className="flex app-shell bg-background-primary text-text-primary">
        <Sidebar
          collapsed={!sidebarOpen}
          mobileOpen={mobileSidebarOpen}
          onClose={() => onCloseMobileSidebar()}
        />
        <main className="flex flex-1 flex-col overflow-hidden">
          <SettingsHeader
            title={isAgentDetail ? t('settings.tabs.agents') : undefined}
            configPath={settingsState.metadata?.config_path}
            onOpenMobileSidebar={onOpenMobileSidebar}
          />

          <div className="flex flex-col md:flex-row flex-1 overflow-hidden">
            {!isAgentDetail && (
              <SettingsTabs activeTab={activeTab ?? 'general'} onTabChange={handleTabChange} />
            )}

            <div className="flex flex-1 flex-col overflow-hidden">
              <div
                data-testid="settings-scroll-area"
                className="relative flex-1 overflow-y-auto p-4 md:p-6"
              >
                <Outlet />
              </div>

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
