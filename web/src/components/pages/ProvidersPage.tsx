import { ProvidersSettings } from '../organisms/settings'
import { EntitySettingsPage } from '../molecules/EntitySettingsPage'

export function ProvidersPage() {
  return (
    <EntitySettingsPage titleKey="sidebar.providers">
      <ProvidersSettings />
    </EntitySettingsPage>
  )
}
