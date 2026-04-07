import { useTranslation } from 'react-i18next'
import type {
  Agent,
  AgentDetails,
  ApprovalRequest,
  ChannelInfo,
  ChatMessage,
  ChatSession,
  ConfigResponse,
  ModelGroup,
  SystemStatus,
  ToolInfo,
  ToolStatus,
} from '../../lib/types'
import { ErrorBanner } from '../atoms/ErrorBanner'
import { ChatComposer } from '../molecules/ChatComposer'
import { ChatHeader } from '../organisms/ChatHeader'
import { DiagnosticsPanel } from '../organisms/DiagnosticsPanel'
import { MessageList } from '../organisms/MessageList'
import { Sidebar } from '../organisms/Sidebar'

type ModelState = {
  current: string
  available: string[]
  groups: ModelGroup[]
}

type DiagnosticsState = {
  status: SystemStatus | null
  channels: ChannelInfo[]
  tools: ToolInfo[]
  config: ConfigResponse | null
  agentInfo: AgentDetails | null
}

type Props = {
  apiUrl: string
  deviceName: string
  wsStatus: 'disconnected' | 'connecting' | 'connected'
  sessions: ChatSession[]
  agents: Agent[]
  currentSessionKey: string | null
  currentAgent: Agent | null
  diagnostics: DiagnosticsState
  diagnosticsOpen: boolean
  error: string | null
  modelState: ModelState
  messages: ChatMessage[]
  approvalRequest: ApprovalRequest | null
  pendingAttachments: string[]
  toolStatus: ToolStatus | null
  isStreaming: boolean
  onApprove: (approved: boolean) => void
  onUploadAttachments: (files: File[]) => Promise<string[]>
  onAttachmentsChange: (attachments: string[]) => void
  onCancel: () => void
  onClearSession: () => void
  onCreateSession: () => void
  onLogout: () => void
  onSend: (content: string, attachments: string[]) => void
  onSelectAgent: (agentId: string) => void
  onSelectModel: (model: string) => void
  onSelectSession: (sessionKey: string) => void
  onDeleteSession: (sessionKey: string) => void
  onToggleDiagnostics: () => void
}

export function ChatPage({
  apiUrl,
  deviceName,
  wsStatus,
  sessions,
  agents,
  currentSessionKey,
  currentAgent,
  diagnostics,
  diagnosticsOpen,
  error,
  modelState,
  messages,
  approvalRequest,
  pendingAttachments,
  toolStatus,
  isStreaming,
  onApprove,
  onUploadAttachments,
  onAttachmentsChange,
  onCancel,
  onClearSession,
  onCreateSession,
  onLogout,
  onSend,
  onSelectAgent,
  onSelectModel,
  onSelectSession,
  onDeleteSession,
  onToggleDiagnostics,
}: Props) {
  const { t } = useTranslation()
  const hasConversation = messages.length > 0
  const canCancel = isStreaming || Boolean(toolStatus)
  const currentSession = sessions.find((s) => s.key === currentSessionKey) ?? sessions[0] ?? null
  const availableModels =
    modelState.available.length > 0
      ? modelState.available
      : currentAgent?.model
        ? [currentAgent.model]
        : [t('chat.default')]
  const groupedModels = modelState.groups.filter((group) => group.models.length > 0)
  const hasGroupedModels = groupedModels.length > 0
  const selectedModel = modelState.current || availableModels[0]

  return (
    <div className="flex h-screen overflow-hidden bg-[#1a1a1a] text-[#e0e0e0]">
      <Sidebar
        deviceName={deviceName}
        apiUrl={apiUrl}
        wsStatus={wsStatus}
        sessions={sessions}
        currentSessionKey={currentSessionKey}
        onCreateSession={onCreateSession}
        onClearSession={onClearSession}
        onSelectSession={onSelectSession}
        onDeleteSession={onDeleteSession}
        onLogout={onLogout}
        onToggleDiagnostics={onToggleDiagnostics}
      />

      <main className="flex flex-1 flex-col overflow-hidden">
        <ChatHeader
          currentSession={currentSession}
          currentAgent={currentAgent}
          toolStatus={toolStatus}
          isStreaming={isStreaming}
          canCancel={canCancel}
          onCancel={onCancel}
        />

        {error && <ErrorBanner message={error} />}
        {diagnosticsOpen && <DiagnosticsPanel diagnostics={diagnostics} />}

        <div className="flex-1 overflow-y-auto px-6 py-4">
          <MessageList
            messages={messages}
            approvalRequest={approvalRequest}
            onApprove={onApprove}
          />
        </div>

        <div className="border-t border-[#2e2e2e] px-6 py-4">
          <div className="mx-auto max-w-3xl">
            <ChatComposer
              attachments={pendingAttachments}
              availableModels={availableModels.map((model) => ({ value: model, label: model }))}
              canCancel={canCancel}
              disabled={false}
              groupedModels={
                hasGroupedModels
                  ? groupedModels.map((group) => ({
                      label: group.provider,
                      options: group.models,
                    }))
                  : undefined
              }
              hasConversation={hasConversation}
              onAttachmentsChange={onAttachmentsChange}
              onCancel={onCancel}
              onSelectAgent={onSelectAgent}
              onSelectModel={onSelectModel}
              onSend={onSend}
              onUploadAttachments={onUploadAttachments}
              selectedAgent={currentAgent?.id ?? ''}
              agents={agents.map((agent) => ({ value: agent.id, label: agent.name }))}
              selectedModel={selectedModel}
            />
          </div>
        </div>
      </main>
    </div>
  )
}
