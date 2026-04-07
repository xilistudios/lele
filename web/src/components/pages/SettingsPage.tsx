import { useTranslation } from 'react-i18next'
import type {
  AgentDetails,
  ChannelInfo,
  ConfigResponse,
  SystemStatus,
  ToolInfo,
} from '../../lib/types'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'

type DiagnosticsState = {
  status: SystemStatus | null
  channels: ChannelInfo[]
  tools: ToolInfo[]
  config: ConfigResponse | null
  agentInfo: AgentDetails | null
}

type Props = {
  diagnostics: DiagnosticsState
  onLogout: () => void
}

export function SettingsPage({ diagnostics, onLogout }: Props) {
  const { t } = useTranslation()

  return (
    <div className="flex h-screen overflow-hidden bg-[#1a1a1a] text-[#e0e0e0]">
      <main className="flex flex-1 flex-col overflow-hidden">
        <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-4">
          <h1 className="text-xl font-semibold text-white">{t('chat.settings')}</h1>
          <button
            onClick={onLogout}
            type="button"
            className="rounded-md bg-rose-600 px-4 py-2 text-sm text-white transition-colors hover:bg-rose-500"
          >
            {t('chat.logout')}
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6">
          <DiagnosticsPanel diagnostics={diagnostics} />
        </div>
      </main>
    </div>
  )
}
