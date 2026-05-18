import { EntitySettingsPage } from '../molecules/EntitySettingsPage'
import { ProvidersSettings } from '../organisms/settings'

export function ProvidersPage() {
  return (
    <EntitySettingsPage titleKey="sidebar.providers">
      <ProvidersSettings />
    </EntitySettingsPage>
  )
}
