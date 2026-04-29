import { EntitySettingsPage } from '../molecules/EntitySettingsPage'
import { AgentsSettings } from '../organisms/settings'

export function AgentsPage() {
  return (
    <EntitySettingsPage titleKey="sidebar.agents">
      <AgentsSettings />
    </EntitySettingsPage>
  )
}
