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
  SubagentsSettings,
  SystemSettings,
  ToolsSettings,
  UpdatesSettings,
} from '../organisms/settings'
import { AgentsListPage } from './AgentsListPage'

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
      return <AgentsListPage />
    case 'subagents':
      return <SubagentsSettings />
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
