import { useCallback, useEffect, useState } from 'react'
import { AuthView } from './components/AuthView'
import { ChatPage } from './components/pages/ChatPage'
import { useAuth } from './hooks/useAuth'
import { useChatSessions } from './hooks/useChatSessions'
import { useMessages } from './hooks/useMessages'
import { useModels } from './hooks/useModels'
import { usePollingFallback } from './hooks/usePollingFallback'
import { useSocket } from './hooks/useSocket'
import { clearCurrentSessionKey } from './lib/storage'
import type {
  Agent,
  AgentDetails,
  ChannelInfo,
  ChatMessage,
  ConfigResponse,
  SystemStatus,
  ToolInfo,
} from './lib/types'

const defaultApiUrl =
  import.meta.env.VITE_LELE_API_URL ??
  `${window.location.protocol}//${window.location.hostname}:18793`

type DiagnosticsState = {
  status: SystemStatus | null
  channels: ChannelInfo[]
  tools: ToolInfo[]
  config: ConfigResponse | null
  agentInfo: AgentDetails | null
}

function App() {
  const { api, apiUrl, session, persistSession, handleAuth, ensureSession } = useAuth(defaultApiUrl)
  const [error, setError] = useState<string | null>(null)
  const [agents, setAgents] = useState<Agent[]>([])
  const [currentAgentId, setCurrentAgentId] = useState<string | null>(null)
  const [diagnostics, setDiagnostics] = useState<DiagnosticsState>({
    status: null,
    channels: [],
    tools: [],
    config: null,
    agentInfo: null,
  })
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false)

  const token = session?.token ?? null
  const clientId = session?.client_id ?? null

  const {
    sessions,
    currentSessionKey,
    currentSessionKeyRef,
    refreshSessions,
    selectSession,
    createSession,
    deleteSession,
    clearSession,
  } = useChatSessions(api, token, clientId)

  const { modelState, loadModels, selectModel } = useModels(api, token)

  const {
    messages,
    messagesRef,
    toolStatus,
    approvalRequest,
    pendingAttachments,
    loadHistory,
    handleEvent,
    sendMessage,
    approveRequest,
    setPendingAttachments,
    clearMessages,
  } = useMessages(api, token, currentSessionKey, currentSessionKeyRef)

  const {
    status: wsStatus,
    send: wsSend,
    close: wsClose,
  } = useSocket(apiUrl, token, {
    onEvent: handleEvent,
  })

  usePollingFallback({
    api,
    currentSessionKey,
    wsStatus,
    onMessages: (newMessages: ChatMessage[]) => {
      if (currentSessionKeyRef.current === currentSessionKey) {
        messagesRef.current = newMessages
      }
    },
    sessionToken: token ?? undefined,
    toChatMessages: (history, sessionKey) => {
      return history.flatMap((message, index) => {
        const base: ChatMessage = {
          id: `${sessionKey}:${index}:${message.role}`,
          role: message.role,
          content: message.content,
          streaming: false,
          createdAt: new Date().toISOString(),
          sessionKey,
        }
        return [base]
      })
    },
  })

  useEffect(() => {
    if (!session) return

    const initSession = async () => {
      const validSession = await ensureSession(session)
      if (!validSession) {
        setError('Session expired')
        return
      }

      try {
        const agentsResult = await api.agents()
        setAgents(agentsResult.agents)
        if (agentsResult.agents.length > 0 && !currentAgentId) {
          setCurrentAgentId(agentsResult.agents[0].id)
        }

        await refreshSessions()
        setError(null)
      } catch (err) {
        setError((err as Error).message)
      }
    }

    initSession()
  }, [session, api, ensureSession, currentAgentId, refreshSessions])

  useEffect(() => {
    if (!currentAgentId || !token) return

    const loadAgentData = async () => {
      try {
        const [info, statusResult, channelsResult, toolsResult, configResult] = await Promise.all([
          api.agentInfo(currentAgentId),
          api.systemStatus(),
          api.channels(),
          api.tools(),
          api.config(),
        ])

        setDiagnostics({
          status: statusResult,
          channels: channelsResult.channels,
          tools: toolsResult.tools,
          config: configResult,
          agentInfo: info,
        })
      } catch (err) {
        console.error('Failed to load agent data:', err)
      }
    }

    loadAgentData()
  }, [currentAgentId, token, api])

  useEffect(() => {
    if (!currentSessionKey || !currentAgentId || !token) return

    wsSend('subscribe', { session_key: currentSessionKey, agent_id: currentAgentId })
    loadHistory(currentSessionKey)
    loadModels(currentAgentId, currentSessionKey, messagesRef.current.length > 0)
  }, [currentSessionKey, currentAgentId, token, wsSend, loadHistory, loadModels, messagesRef])

  const handleAuthSubmit = useCallback(
    async (input: { apiUrl: string; pin: string; deviceName: string }) => {
      try {
        setError(null)
        await handleAuth(input)
      } catch (err) {
        setError((err as Error).message)
      }
    },
    [handleAuth],
  )

  const handleLogout = useCallback(() => {
    wsClose()
    clearMessages()
    persistSession(null)
    clearCurrentSessionKey()
    setAgents([])
    setCurrentAgentId(null)
    setDiagnostics({
      status: null,
      channels: [],
      tools: [],
      config: null,
      agentInfo: null,
    })
    setError(null)
  }, [wsClose, clearMessages, persistSession])

  const handleSend = useCallback(
    (content: string, attachments: string[]) => {
      if (!currentSessionKey || !currentAgentId) return

      sendMessage(content, attachments, currentSessionKey, currentAgentId)
      wsSend('send', {
        session_key: currentSessionKey,
        agent_id: currentAgentId,
        content,
        attachments,
      })
      setPendingAttachments([])
    },
    [currentSessionKey, currentAgentId, sendMessage, wsSend, setPendingAttachments],
  )

  const handleApprove = useCallback(
    (approved: boolean) => {
      if (!approvalRequest) return
      const result = approveRequest(approved, approvalRequest.id)
      wsSend('approve', result)
    },
    [approvalRequest, approveRequest, wsSend],
  )

  const handleCancel = useCallback(() => {
    wsSend('cancel', {})
  }, [wsSend])

  const handleSelectSession = useCallback(
    (sessionKey: string) => {
      wsSend('unsubscribe', {})
      selectSession(sessionKey)
      clearMessages()
    },
    [wsSend, selectSession, clearMessages],
  )

  const handleCreateSession = useCallback(() => {
    wsSend('unsubscribe', {})
    const newKey = createSession()
    if (newKey) {
      clearMessages()
    }
  }, [wsSend, createSession, clearMessages])

  const handleDeleteSession = useCallback(
    async (sessionKey: string) => {
      await deleteSession(sessionKey)
    },
    [deleteSession],
  )

  const handleClearSession = useCallback(async () => {
    if (!currentSessionKey) return
    await clearSession(currentSessionKey)
    clearMessages()
  }, [currentSessionKey, clearSession, clearMessages])

  const handleSelectAgent = useCallback((agentId: string) => {
    setCurrentAgentId(agentId)
  }, [])

  const handleSelectModel = useCallback(
    async (model: string) => {
      if (!currentSessionKey) return
      await selectModel(model, currentSessionKey)
    },
    [currentSessionKey, selectModel],
  )

  const handleUploadAttachments = useCallback(
    async (files: File[]): Promise<string[]> => {
      if (!token) return []
      try {
        const result = await api.uploadFiles(files)
        return result.files.map((f) => f.path)
      } catch (err) {
        setError((err as Error).message)
        return []
      }
    },
    [token, api],
  )

  const handleToggleDiagnostics = useCallback(() => {
    setDiagnosticsOpen((current) => !current)
  }, [])

  const currentAgent = agents.find((a) => a.id === currentAgentId) ?? null
  const isStreaming = messages.some((m) => m.streaming)

  if (!session?.token) {
    return <AuthView apiUrl={apiUrl} error={error} onSubmit={handleAuthSubmit} />
  }

  return (
    <ChatPage
      apiUrl={apiUrl}
      deviceName={session.device_name ?? ''}
      wsStatus={wsStatus}
      sessions={sessions}
      agents={agents}
      currentSessionKey={currentSessionKey}
      currentAgent={currentAgent}
      diagnostics={diagnostics}
      diagnosticsOpen={diagnosticsOpen}
      error={error}
      modelState={modelState}
      messages={messages}
      approvalRequest={approvalRequest}
      pendingAttachments={pendingAttachments}
      toolStatus={toolStatus}
      isStreaming={isStreaming}
      onApprove={handleApprove}
      onUploadAttachments={handleUploadAttachments}
      onAttachmentsChange={setPendingAttachments}
      onCancel={handleCancel}
      onClearSession={() => void handleClearSession()}
      onCreateSession={handleCreateSession}
      onLogout={handleLogout}
      onSend={handleSend}
      onSelectAgent={handleSelectAgent}
      onSelectModel={(model) => void handleSelectModel(model)}
      onSelectSession={handleSelectSession}
      onDeleteSession={(key) => void handleDeleteSession(key)}
      onToggleDiagnostics={handleToggleDiagnostics}
    />
  )
}

export default App
