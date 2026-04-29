import { type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { useAppLogicContext } from '../../contexts/AppLogicContext'
import { useAuthContext } from '../../contexts/AuthContext'
import { SettingsProvider } from '../../contexts/SettingsContext'
import { useSettingsConfig } from '../../hooks/useSettingsConfig'
import { SettingsFooter, SettingsHeader } from '../molecules'
import { Sidebar } from '../organisms/Sidebar'

type Props = {
  titleKey: string
  children: ReactNode
}

export function EntitySettingsPage({ titleKey, children }: Props) {
  const { t } = useTranslation()
  const { api } = useAuthContext()
  const { sidebarOpen, onToggleSidebar } = useAppLogicContext()

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
          mobileOpen={sidebarOpen}
          onClose={() => onToggleSidebar()}
        />
        <main className="flex flex-1 flex-col overflow-hidden">
          <SettingsHeader
            title={t(titleKey)}
            onToggleSidebar={onToggleSidebar}
            configPath={settingsState.metadata?.config_path}
          />

          <div className="flex flex-1 flex-col overflow-hidden">
            <div className="flex-1 overflow-y-auto p-6">
              {children}
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
