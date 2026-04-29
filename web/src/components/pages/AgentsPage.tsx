import { AgentsSettings } from '../organisms/settings'
import { EntitySettingsPage } from '../molecules/EntitySettingsPage'

export function AgentsPage() {
  return (
    <EntitySettingsPage titleKey="sidebar.agents">
      <AgentsSettings />
    </EntitySettingsPage>
  )
}
