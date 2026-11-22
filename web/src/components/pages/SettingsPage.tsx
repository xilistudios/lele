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

/**
 * `/settings` — the settings shell (sidebar + header + scrollable content +
 * save footer) and the single owner of the settings draft.
 *
 * Children are rendered through `<Outlet/>`:
 *
 *   /settings/:tab                 → `SettingsTabContent` (one tab)
 *   /settings/agents/:id/:tab?     → `AgentConfigPage` (agent detail)
 *
 * Why the draft lives HERE and not in each page: `useSettingsConfig` holds the
 * unsaved draft in component state, and navigating between routes that mount
 * *different* instances would remount the hook and silently drop it. Keeping
 * one instance in the layout is what lets the user edit an agent, go back to
 * the list, open another one and still save everything in one go.
 *
 * The tab strip is hidden while an agent detail page is open: that page owns
 * its own tab strip, and stacking two of them would leave the content a narrow
 * column on desktop. The agent page header's breadcrumb ("‹ Agents / id") is
 * the way back.
 */
export function SettingsPage() {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, mobileSidebarOpen, onCloseMobileSidebar, onOpenMobileSidebar } =
    useAppLogicContext()
  const navigate = useNavigate()

  // `/settings/:tab` matches exactly one segment; `/settings/agents/coder` does
  // not, which is precisely the "agent detail" case.
  const tabMatch = useMatch('/settings/:tab')
  const activeTab: SettingsTab | null = isSettingsTab(tabMatch?.params.tab)
    ? (tabMatch?.params.tab as SettingsTab)
    : null
  const isAgentDetail = !!useMatch('/settings/agents/:agentId/:tab?')

  // A `:tab` segment that is not a known tab is a redirect, never a broken
  // page: the address bar must always describe what is on screen.
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
              <div className="flex-1 overflow-y-auto p-4 md:p-6">
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
