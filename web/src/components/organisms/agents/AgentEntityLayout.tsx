import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router-dom'
import { useAppLogicContext } from '../../../contexts/AppLogicContext'
import { useAuthContext } from '../../../contexts/AuthContext'
import { SettingsProvider } from '../../../contexts/SettingsContext'
import { useSettingsConfig } from '../../../hooks/useSettingsConfig'
import { SettingsFooter, SettingsHeader } from '../../molecules'
import { Sidebar } from '../Sidebar'

type Props = {
  /** i18n key for the SettingsHeader title. Defaults to 'sidebar.agents'. */
  titleKey?: string
}

/**
 * Route layout for the agents section (`/agents`, `/agents/:agentId`,
 * `/agents/:agentId/:tab`).
 *
 * Replicates the body of `EntitySettingsPage` (Sidebar + SettingsHeader +
 * scrollable content + SettingsFooter wrapped in a SettingsProvider) with two
 * differences:
 *
 * 1. It renders `<Outlet/>` instead of `children`, so it works as a
 *    react-router parent-route layout.
 * 2. `useSettingsConfig(api)` is instantiated HERE, exactly once, and the
 *    resulting state survives navigation between the agents list and any
 *    agent detail tab. If each child page mounted its own settings hook (as
 *    they would with `EntitySettingsPage`), navigating to `/agents/coder`
 *    would remount the hook and silently drop the unsaved draft.
 *
 * `EntitySettingsPage` is intentionally NOT modified: other pages
 * (Providers, Skills, …) depend on its children-based contract.
 */
export function AgentEntityLayout({ titleKey = 'sidebar.agents' }: Props) {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, mobileSidebarOpen, onCloseMobileSidebar, onOpenMobileSidebar } =
    useAppLogicContext()

  // Single source of truth for the settings draft shared by all agent routes.
  const settingsState = useSettingsConfig(api)

  const handleSave = async () => {
    const isValid = await settingsState.validate()
    if (isValid) {
      await settingsState.save()
    }
  }

  return (
    <SettingsProvider settingsState={settingsState} api={api}>
      <div className="flex h-screen overflow-hidden bg-background-primary text-text-primary">
        <Sidebar
          collapsed={!sidebarOpen}
          mobileOpen={mobileSidebarOpen}
          onClose={() => onCloseMobileSidebar()}
        />
        <main className="flex flex-1 flex-col overflow-hidden">
          <SettingsHeader
            title={t(titleKey)}
            configPath={settingsState.metadata?.config_path}
            onOpenMobileSidebar={onOpenMobileSidebar}
          />

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
        </main>
      </div>
    </SettingsProvider>
  )
}
