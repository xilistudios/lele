import { useTranslation } from 'react-i18next'
import { useParams } from 'react-router-dom'
import { useSettings } from '../../contexts/SettingsContext'
import { type SettingsTab, isSettingsTab } from '../../lib/settingsTabs'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'
import {
  AdvancedSettings,
  ChannelSettings,
  GeneralSettings,
  GroupsSettings,
  LogsViewer,
  NativeClientsSettings,
  ProvidersSettings,
  SessionSettings,
  SystemSettings,
  ToolsSettings,
  UpdatesSettings,
} from '../organisms/settings'
import { AgentsListPage } from './AgentsListPage'

/**
 * `/settings/:tab` — the content of one Settings tab.
 *
 * Rendered through `<Outlet/>` by `SettingsPage`, which owns the layout
 * (sidebar, header, footer) and the single `useSettingsConfig` draft shared by
 * every tab — including the agents list and the agent detail pages.
 *
 * An unknown `:tab` never reaches a broken page: `SettingsPage` normalises the
 * URL to `/settings/general` with `replace: true` (this component renders
 * `null` for the single frame before that redirect lands).
 */
export function SettingsTabContent() {
  const { t } = useTranslation()
  const { tab } = useParams<{ tab?: string }>()
  const { draftConfig } = useSettings()

  const activeTab: SettingsTab | null = isSettingsTab(tab) ? tab : null

  if (!draftConfig) {
    return (
      <div className="flex h-64 items-center justify-center">
        <div className="text-sm text-text-secondary">{t('common.loading')}</div>
      </div>
    )
  }

  if (!activeTab) return null

  switch (activeTab) {
    case 'general':
      return <GeneralSettings />
    case 'agents':
      // Master list of agents. It reads and writes the same draft as every
      // other tab, so unsaved edits and the shared footer Save behave like the
      // rest of the page. Opening an agent goes to
      // /settings/agents/:agentId/:tab, which the same layout hosts.
      return <AgentsListPage />
    case 'providers':
      return <ProvidersSettings />
    case 'session':
      return <SessionSettings />
    case 'channels':
      return <ChannelSettings />
    case 'native':
      return <NativeClientsSettings />
    case 'groups':
      return <GroupsSettings />
    case 'tools':
      return <ToolsSettings />
    case 'system':
      return <SystemSettings />
    case 'updates':
      return <UpdatesSettings />
    case 'advanced':
      return <AdvancedSettings />
    case 'diagnostics':
      return <DiagnosticsPanel />
    case 'logs':
      return <LogsViewer />
    default:
      return null
  }
}
