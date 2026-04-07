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
import { AttachmentInput } from '../molecules/AttachmentInput'
import { SearchableSelect } from '../molecules/SearchableSelect'
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
            {pendingAttachments.length > 0 && (
              <div className="mb-3 flex flex-wrap gap-2">
                {pendingAttachments.map((attachment) => (
                  <span
                    key={attachment}
                    className="rounded-full border border-[#3a3a3a] bg-[#222] px-3 py-1 text-xs text-[#bbb]"
                  >
                    {attachment.split('/').pop() ?? attachment}
                  </span>
                ))}
              </div>
            )}
            <form
              onSubmit={(e) => {
                e.preventDefault()
                onSend('', pendingAttachments)
              }}
            >
              <div className="rounded-lg border border-[#3a3a3a] bg-[#222] transition-colors focus-within:border-[#555]">
                <textarea
                  className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-3 text-sm text-white outline-none placeholder:text-[#444]"
                  placeholder={t('chat.messagePlaceholder')}
onKeyDown={(e) => {
                     if (e.key === 'Enter' && !e.shiftKey) {
                       e.preventDefault()
                       const content = (e.target as HTMLTextAreaElement).value.trim()
                       if (content) {
                         onSend(content, pendingAttachments)
                         ;(e.target as HTMLTextAreaElement).value = ''
                       }
                     }
                   }}
                  onChange={(e) => {
                    e.target.style.height = 'auto'
                    e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`
                  }}
                  rows={1}
                />
                <div className="flex items-center justify-between px-3 pb-2 pt-1">
                  <div className="flex items-center gap-3">
                    <AttachmentInput
                      onUpload={onUploadAttachments}
                      onAttach={onAttachmentsChange}
                    />
                    <div className="flex items-center gap-2 text-[10px] text-[#555]">
                      <SearchableSelect
                        ariaLabel={t('chat.model')}
                        buttonLabel={t('chat.model')}
                        emptyLabel={t('chat.default')}
                        groups={
                          hasGroupedModels
                            ? groupedModels.map((group) => ({
                                label: group.provider,
                                options: group.models,
                              }))
                            : undefined
                        }
                        onChange={onSelectModel}
                        options={
                          hasGroupedModels
                            ? undefined
                            : availableModels.map((model) => ({ value: model, label: model }))
                        }
                        placeholder={selectedModel}
                        searchAriaLabel={`${t('chat.model')} buscar`}
                        searchPlaceholder={t('chat.model')}
                        value={selectedModel}
                      />
                      <SearchableSelect
                        ariaLabel={t('chat.agent')}
                        buttonLabel={t('chat.agent')}
                        disabled={hasConversation}
                        emptyLabel={t('chat.agentLocked')}
                        onChange={onSelectAgent}
options={agents.map((agent) => ({
                           value: agent.id,
                           label: agent.name,
                         }))}
                        placeholder={currentAgent?.name ?? t('chat.agent')}
                        searchAriaLabel={`${t('chat.agent')} buscar`}
                        searchPlaceholder={t('chat.agent')}
                        value={currentAgent?.id ?? ''}
                      />
                    </div>
                  </div>
                  <button
                    type="submit"
                    aria-label={t('chat.send')}
                    className="flex h-7 w-7 items-center justify-center rounded-md bg-white text-black transition-colors hover:bg-[#ddd] disabled:opacity-20"
                  >
                    <svg
                      width="12"
                      height="12"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="2.5"
                      aria-hidden="true"
                    >
                      <line x1="12" y1="19" x2="12" y2="5" />
                      <polyline points="5 12 12 5 19 12" />
                    </svg>
                  </button>
                </div>
              </div>
            </form>
          </div>
        </div>
      </main>
    </div>
  )
}
