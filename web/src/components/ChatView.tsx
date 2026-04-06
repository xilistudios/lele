import { useEffect, useRef, useState } from 'react'
import type { ChangeEvent, FormEvent, KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import type {
  Agent,
  AgentDetails,
  ApprovalRequest,
  AuthSession,
  ChannelInfo,
  ChatMessage,
  ChatSession,
  ConfigResponse,
  ModelGroup,
  SystemStatus,
  ToolInfo,
  ToolStatus,
} from '../lib/types'
import { ApprovalInline } from './ApprovalModal'
import { MessageBubble } from './MessageBubble'
import { SearchableSelect } from './SearchableSelect'

type DiagnosticsState = {
  status: SystemStatus | null
  channels: ChannelInfo[]
  tools: ToolInfo[]
  config: ConfigResponse | null
  agentInfo: AgentDetails | null
}

type ModelState = {
  current: string
  available: string[]
  groups: ModelGroup[]
}

type Props = {
  apiUrl: string
  auth: AuthSession
  agents: Agent[]
  sessions: ChatSession[]
  currentAgentId: string | null
  currentSessionKey: string | null
  diagnostics: DiagnosticsState
  diagnosticsOpen: boolean
  error: string | null
  modelState: ModelState
  messages: ChatMessage[]
  approvalRequest: ApprovalRequest | null
  pendingAttachments: string[]
  toolStatus: ToolStatus | null
  wsStatus: 'disconnected' | 'connecting' | 'connected'
  onApprove: (approved: boolean) => void
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

const formatSessionTitle = (sessionKey: string, sessionName?: string) => {
  if (sessionName && sessionName.trim()) {
    return sessionName
  }
  const parts = sessionKey.split(':')
  return parts.length > 2 ? `Session ${parts[parts.length - 1]}` : sessionKey
}

const formatMessageCount = (count: number, t: ReturnType<typeof useTranslation>['t']) =>
  count === 1 ? t('chat.messageCount_one', { count }) : t('chat.messageCount_other', { count })

export function ChatView({
  apiUrl,
  auth,
  agents,
  sessions,
  currentAgentId,
  currentSessionKey,
  diagnostics,
  diagnosticsOpen,
  error,
  modelState,
  messages,
  approvalRequest,
  pendingAttachments,
  toolStatus,
  wsStatus,
  onApprove,
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
  const [draft, setDraft] = useState('')
  const [showAllSessions, setShowAllSessions] = useState(false)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const currentAgent = agents.find((a) => a.id === currentAgentId) ?? agents[0] ?? null
  const currentSession =
    sessions.find((session) => session.key === currentSessionKey) ?? sessions[0] ?? null
  const isStreaming = messages.some((m) => m.streaming)
  const canCancel = isStreaming || Boolean(toolStatus)
  const hasConversation = messages.length > 0
  const availableModels =
    modelState.available.length > 0
      ? modelState.available
      : currentAgent?.model
        ? [currentAgent.model]
        : [t('chat.default')]
  const groupedModels = modelState.groups.filter((group) => group.models.length > 0)
  const hasGroupedModels = groupedModels.length > 0
  const selectedModel = modelState.current || availableModels[0]

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const submit = (e?: FormEvent) => {
    e?.preventDefault()
    const content = draft.trim()
    if (!content) {
      return
    }

    onSend(content, pendingAttachments)
    setDraft('')
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto'
    }
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      submit()
    }
  }

  const handleTextareaChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setDraft(e.target.value)
    e.target.style.height = 'auto'
    e.target.style.height = `${Math.min(e.target.scrollHeight, 200)}px`
  }

  const handleAttachmentInput = (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? [])
    const paths = files
      .map((file) => (file as File & { path?: string }).path ?? file.name)
      .filter((path) => path.length > 0)

    onAttachmentsChange(paths)
  }

  return (
    <div className="flex h-screen overflow-hidden bg-[#1a1a1a] text-[#e0e0e0]">
      <aside className="flex w-[280px] flex-shrink-0 flex-col border-r border-[#2e2e2e] bg-[#222222]">
        <div className="flex items-center gap-2 border-b border-[#2e2e2e] px-4 py-3">
          <div className="flex h-7 w-7 items-center justify-center rounded bg-[#3a3a3a] text-xs font-bold text-white">
            {auth.device_name?.[0]?.toUpperCase() ?? 'L'}
          </div>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-medium text-white">{auth.device_name ?? 'lele'}</p>
            <p className="truncate text-[10px] text-[#666]">{apiUrl.replace(/^https?:\/\//, '')}</p>
          </div>
          <button
            onClick={onLogout}
            title={t('chat.logout')}
            type="button"
            className="text-[#555] transition-colors hover:text-[#aaa]"
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
              <polyline points="16 17 21 12 16 7" />
              <line x1="21" y1="12" x2="9" y2="12" />
            </svg>
          </button>
        </div>

        <div className="space-y-2 border-b border-[#2e2e2e] px-3 py-3">
          <button
            onClick={onCreateSession}
            type="button"
            className="flex w-full items-center gap-2 rounded-md border border-[#3a3a3a] px-3 py-2 text-xs text-[#bbb] transition-colors hover:bg-[#2a2a2a] hover:text-white"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            {t('chat.newSession')}
          </button>
          <button
            onClick={onClearSession}
            type="button"
            className="flex w-full items-center gap-2 rounded-md border border-[#3a3a3a] px-3 py-2 text-xs text-[#999] transition-colors hover:bg-[#2a2a2a] hover:text-white"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
            >
              <path d="M3 6h18" />
              <path d="M8 6V4h8v2" />
              <path d="M19 6l-1 14H6L5 6" />
            </svg>
            {t('chat.clearSession')}
          </button>
        </div>

        <div className="border-b border-[#2e2e2e] px-3 py-3">
          <p className="px-1 text-[10px] uppercase tracking-[0.2em] text-[#666]">
            {t('chat.sessions')}
          </p>
          <nav className="mt-2 space-y-0.5 overflow-y-auto max-h-[240px]">
            {(showAllSessions ? sessions : sessions.slice(0, 5)).map((session) => (
              <div
                key={session.key}
                onClick={() => onSelectSession(session.key)}
                role="button"
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault()
                    onSelectSession(session.key)
                  }
                }}
                className={`group flex w-full items-start gap-2 rounded-md px-3 py-2 text-left transition-colors cursor-pointer ${
                  session.key === currentSession?.key
                    ? 'bg-[#2e2e2e] text-white'
                    : 'text-[#999] hover:bg-[#272727] hover:text-[#ccc]'
                }`}
              >
                <span className="mt-0.5 text-xs text-[#555]">#</span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs leading-5">
                    {formatSessionTitle(session.key, session.name)}
                  </span>
                  <span className="block text-[10px] text-[#666]">
                    {formatMessageCount(session.message_count, t)}
                  </span>
                </span>
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    onDeleteSession(session.key)
                  }}
                  type="button"
                  title={t('chat.deleteSession')}
                  className="opacity-0 group-hover:opacity-100 ml-auto text-[#666] hover:text-[#f0b4b4] transition-opacity"
                >
                  <svg
                    width="12"
                    height="12"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                  >
                    <path d="M3 6h18" />
                    <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                  </svg>
                </button>
              </div>
            ))}
          </nav>
          {sessions.length > 5 && (
            <button
              onClick={() => setShowAllSessions(!showAllSessions)}
              type="button"
              className="mt-2 w-full text-center text-xs text-[#666] hover:text-[#999] transition-colors"
            >
              {showAllSessions ? t('chat.showLess') : t('chat.showMore')}
            </button>
          )}
        </div>

        <div className="flex items-center justify-between border-t border-[#2e2e2e] px-4 py-3">
          <div className="flex items-center gap-1.5">
            <span
              className={`h-1.5 w-1.5 rounded-full ${
                wsStatus === 'connected'
                  ? 'bg-emerald-400'
                  : wsStatus === 'connecting'
                    ? 'bg-yellow-400'
                    : 'bg-[#555]'
              }`}
            />
            <span className="text-[10px] text-[#555]">{t(`chat.${wsStatus}`)}</span>
          </div>
          <div className="flex items-center gap-3 text-[#444]">
            <button
              type="button"
              title={t('chat.settings')}
              className="transition-colors hover:text-[#888]"
              onClick={onToggleDiagnostics}
            >
              <svg
                width="14"
                height="14"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
              >
                <circle cx="12" cy="12" r="3" />
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z" />
              </svg>
            </button>
          </div>
        </div>
      </aside>

      <main className="flex flex-1 flex-col overflow-hidden">
        <div className="flex items-center justify-between border-b border-[#2e2e2e] px-6 py-3">
          <div className="min-w-0">
            <h2 className="truncate text-sm font-medium text-white">
              {currentSession
                ? formatSessionTitle(currentSession.key, currentSession.name)
                : t('chat.session')}
            </h2>
            <p className="truncate text-[11px] text-[#666]">
              {currentAgent?.name ?? t('chat.default')}
            </p>
          </div>
          <div className="flex items-center gap-3">
            {(isStreaming || toolStatus) && (
              <div className="flex items-center gap-1.5 text-xs text-[#666]">
                {toolStatus ? (
                  <>
                    <span className="rounded bg-[#2a2a2a] px-2 py-0.5 font-mono text-[11px] text-[#aaa]">
                      {toolStatus.tool}
                    </span>
                    <span>{toolStatus.action}</span>
                  </>
                ) : (
                  <svg
                    className="h-3 w-3 animate-spin"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                  >
                    <path d="M21 12a9 9 0 1 1-6.219-8.56" />
                  </svg>
                )}
              </div>
            )}
            {canCancel ? (
              <button
                type="button"
                className="rounded-md border border-[#5a2b2b] px-3 py-1 text-xs text-[#f0b4b4] transition-colors hover:bg-[#351717]"
                onClick={onCancel}
              >
                {t('chat.cancel')}
              </button>
            ) : null}
          </div>
        </div>

        {error ? (
          <div className="mx-6 mt-3 rounded border border-red-900/50 bg-red-950/30 px-4 py-2 text-xs text-red-300">
            {error}
          </div>
        ) : null}

        {diagnosticsOpen ? (
          <section className="mx-6 mt-3 rounded-lg border border-[#2e2e2e] bg-[#202020] p-4 text-xs text-[#bbb]">
            <div className="grid gap-4 md:grid-cols-2">
              <div className="space-y-2">
                <p className="text-[10px] uppercase tracking-[0.2em] text-[#666]">
                  {t('chat.systemStatus')}
                </p>
                <p>{diagnostics.status?.status ?? '-'}</p>
                <p>{diagnostics.status?.uptime ?? '-'}</p>
                <p>{diagnostics.status?.version ?? '-'}</p>
              </div>
              <div className="space-y-2">
                <p className="text-[10px] uppercase tracking-[0.2em] text-[#666]">
                  {t('chat.agentInfo')}
                </p>
                <p>{diagnostics.agentInfo?.name ?? '-'}</p>
                <p>{diagnostics.agentInfo?.model ?? '-'}</p>
                <p>{diagnostics.agentInfo?.workspace ?? '-'}</p>
                <p>{diagnostics.agentInfo?.status ?? '-'}</p>
              </div>
              <div className="space-y-2">
                <p className="text-[10px] uppercase tracking-[0.2em] text-[#666]">
                  {t('chat.channels')}
                </p>
                {diagnostics.channels.map((channel) => (
                  <p key={channel.name}>
                    {channel.name} · {channel.running ? t('chat.running') : t('chat.stopped')}
                  </p>
                ))}
              </div>
              <div className="space-y-2">
                <p className="text-[10px] uppercase tracking-[0.2em] text-[#666]">
                  {t('chat.tools')}
                </p>
                {diagnostics.tools.map((tool) => (
                  <p key={tool.name}>
                    {tool.name} · {tool.enabled ? t('chat.enabled') : t('chat.disabled')}
                  </p>
                ))}
              </div>
            </div>
            <details className="mt-4 rounded border border-[#2a2a2a] bg-[#1a1a1a] p-3">
              <summary className="cursor-pointer text-[10px] uppercase tracking-[0.2em] text-[#666]">
                {t('chat.config')}
              </summary>
              <pre className="mt-3 overflow-x-auto text-[11px] leading-5 text-[#999]">
                {JSON.stringify(diagnostics.config?.config ?? {}, null, 2)}
              </pre>
            </details>
          </section>
        ) : null}

        <div className="flex-1 overflow-y-auto px-6 py-4">
          {messages.length === 0 ? (
            <div className="flex h-full items-center justify-center">
              <p className="text-sm text-[#444]">{t('chat.startConversation')}</p>
            </div>
          ) : (
            <div className="mx-auto max-w-3xl space-y-1">
              {messages.map((message, index) => (
                <MessageBubble
                  key={message.id}
                  message={message}
                  isLast={index === messages.length - 1}
                />
              ))}
              {approvalRequest ? (
                <ApprovalInline
                  request={approvalRequest}
                  onApprove={() => onApprove(true)}
                  onReject={() => onApprove(false)}
                />
              ) : null}
              <div ref={messagesEndRef} />
            </div>
          )}
        </div>

        <div className="border-t border-[#2e2e2e] px-6 py-4">
          <div className="mx-auto max-w-3xl">
            {pendingAttachments.length > 0 ? (
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
            ) : null}
            <form onSubmit={submit}>
              <div className="rounded-lg border border-[#3a3a3a] bg-[#222] transition-colors focus-within:border-[#555]">
                <textarea
                  ref={textareaRef}
                  className="min-h-[44px] max-h-[200px] w-full resize-none bg-transparent px-4 pb-2 pt-3 text-sm text-white outline-none placeholder:text-[#444]"
                  placeholder={t('chat.messagePlaceholder')}
                  value={draft}
                  onChange={handleTextareaChange}
                  onKeyDown={handleKeyDown}
                  rows={1}
                />
                <div className="flex items-center justify-between px-3 pb-2 pt-1">
                  <div className="flex items-center gap-3">
                    <input
                      ref={fileInputRef}
                      className="hidden"
                      multiple
                      type="file"
                      onChange={handleAttachmentInput}
                    />
                    <button
                      type="button"
                      className="text-[#555] transition-colors hover:text-[#888]"
                      title={t('chat.attach')}
                      onClick={() => fileInputRef.current?.click()}
                    >
                      <svg
                        width="14"
                        height="14"
                        viewBox="0 0 24 24"
                        fill="none"
                        stroke="currentColor"
                        strokeWidth="2"
                      >
                        <path d="M21.44 11.05 12.25 20.24a6 6 0 0 1-8.49-8.49l9.2-9.19a4 4 0 1 1 5.65 5.66l-9.2 9.19a2 2 0 1 1-2.82-2.83l8.48-8.48" />
                      </svg>
                    </button>
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
                        options={agents.map((agent) => ({ value: agent.id, label: agent.name }))}
                        placeholder={currentAgent?.name ?? t('chat.agent')}
                        searchAriaLabel={`${t('chat.agent')} buscar`}
                        searchPlaceholder={t('chat.agent')}
                        value={currentAgentId ?? ''}
                      />
                    </div>
                  </div>
                  <button
                    type="submit"
                    disabled={!draft.trim()}
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
