import { useCallback, useEffect, useRef, useState } from 'react'
import type { ApiClient } from '../lib/api'
import { clearCurrentSessionKey } from '../lib/storage'
import type {
  Agent,
  AgentDetails,
  ChannelInfo,
  ChatMessage,
  ConfigResponse,
  HistoryResponse,
  SystemStatus,
  ToolInfo,
} from '../lib/types'
import { useChatSessions } from './useChatSessions'
import { useMessages } from './useMessages'
import { useModels } from './useModels'
import { usePollingFallback } from './usePollingFallback'
import type { SocketStatus } from './useSocket'

type DiagnosticsState = {
  status: SystemStatus | null
  channels: ChannelInfo[]
  tools: ToolInfo[]
  config: ConfigResponse | null
  agentInfo: AgentDetails | null
}

type SendFn = (event: string, data: Record<string, unknown>) => void

export function useAppLogic(
  api: ApiClient,
  token: string | null,
  clientId: string | null,
  wsStatus: SocketStatus,
  wsSend: SendFn,
  wsClose: () => void,
  persistSession: (session: null) => void,
) {
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
  const [sidebarOpen, setSidebarOpen] = useState(true)

  const sessionsHook = useChatSessions(api, token, clientId)
  const { modelState, loadModels, selectModel } = useModels(api, token)
  const messagesHook = useMessages(
    api,
    token,
    sessionsHook.currentSessionKey,
    sessionsHook.currentSessionKeyRef,
  )

  const wsStatusRef = useRef(wsStatus)
  wsStatusRef.current = wsStatus

  const agentsRef = useRef(agents)
  useEffect(() => {
    agentsRef.current = agents
  }, [agents])

  usePollingFallback({
    api,
    currentSessionKey: sessionsHook.currentSessionKey,
    wsStatus,
    onMessages: (newMessages: ChatMessage[]) => {
      if (sessionsHook.currentSessionKeyRef.current === sessionsHook.currentSessionKey) {
        messagesHook.setMessages(newMessages)
      }
    },
    sessionToken: token ?? undefined,
    toChatMessages: (history: HistoryResponse['messages'], sessionKey: string) => {
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

  const currentAgentIdRef = useRef(currentAgentId)
  useEffect(() => {
    currentAgentIdRef.current = currentAgentId
  }, [currentAgentId])

  useEffect(() => {
    if (!token) return

    const initSession = async () => {
      try {
        const agentsResult = await api.agents()
        setAgents(agentsResult.agents)

        const sessionKey = await sessionsHook.refreshSessions()

        if (sessionKey && !currentAgentIdRef.current) {
          try {
            const agentResult = await api.sessionAgent(sessionKey)
            const validAgent = agentsResult.agents.find((a) => a.id === agentResult.agent_id)
            if (validAgent) {
              setCurrentAgentId(agentResult.agent_id)
            } else if (agentsResult.agents.length > 0) {
              setCurrentAgentId(agentsResult.agents[0].id)
            }
          } catch {
            if (agentsResult.agents.length > 0) {
              setCurrentAgentId(agentsResult.agents[0].id)
            }
          }
        } else if (!currentAgentIdRef.current && agentsResult.agents.length > 0) {
          setCurrentAgentId(agentsResult.agents[0].id)
        }

        setError(null)
      } catch (err) {
        setError((err as Error).message)
      }
    }

    initSession()
  }, [token, api, sessionsHook.refreshSessions])

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
    if (!sessionsHook.currentSessionKey || !currentAgentId || !token) return
    if (wsStatus !== 'connected') return

    wsSend('subscribe', { session_key: sessionsHook.currentSessionKey, agent_id: currentAgentId })
    messagesHook.loadHistory(sessionsHook.currentSessionKey)
    loadModels(
      currentAgentId,
      sessionsHook.currentSessionKey,
      messagesHook.messagesRef.current.length > 0,
    )
  }, [
    sessionsHook.currentSessionKey,
    currentAgentId,
    token,
    wsStatus,
    wsSend,
    loadModels,
    messagesHook.loadHistory,
    messagesHook.messagesRef,
  ])

  const handleLogout = useCallback(() => {
    wsClose()
    messagesHook.clearMessages()
    persistSession(null)
    clearCurrentSessionKey()
    setAgents([])
    setCurrentAgentId(null)
    setDiagnostics({ status: null, channels: [], tools: [], config: null, agentInfo: null })
    setError(null)
  }, [wsClose, messagesHook.clearMessages, persistSession])

  const handleSend = useCallback(
    (content: string, attachments: string[]) => {
      if (!sessionsHook.currentSessionKey || !currentAgentId) return

      messagesHook.sendMessage(content, attachments, sessionsHook.currentSessionKey, currentAgentId)
      messagesHook.setPendingAttachments([])
      sessionsHook.refreshSessions()
    },
    [
      sessionsHook.currentSessionKey,
      currentAgentId,
      messagesHook.sendMessage,
      messagesHook.setPendingAttachments,
      sessionsHook.refreshSessions,
    ],
  )

  const handleApprove = useCallback(
    (approved: boolean) => {
      if (!messagesHook.approvalRequest) return
      const result = messagesHook.approveRequest(approved, messagesHook.approvalRequest.id)
      wsSend('approve', result)
    },
    [messagesHook.approvalRequest, messagesHook.approveRequest, wsSend],
  )

  const handleCancel = useCallback(() => {
    wsSend('cancel', {})
  }, [wsSend])

  const handleSelectSession = useCallback(
    async (sessionKey: string) => {
      wsSend('unsubscribe', {})
      sessionsHook.selectSession(sessionKey)
      messagesHook.clearMessages()
      try {
        const agentResult = await api.sessionAgent(sessionKey)
        const validAgent = agentsRef.current.find((a) => a.id === agentResult.agent_id)
        if (validAgent) {
          setCurrentAgentId(agentResult.agent_id)
        }
      } catch {}
    },
    [wsSend, sessionsHook.selectSession, messagesHook.clearMessages, api],
  )

  const handleCreateSession = useCallback(() => {
    wsSend('unsubscribe', {})
    const newKey = sessionsHook.createSession()
    if (newKey) {
      messagesHook.clearMessages()
    }
  }, [wsSend, sessionsHook.createSession, messagesHook.clearMessages])

  const handleDeleteSession = useCallback(
    async (sessionKey: string): Promise<string | null> => {
      return await sessionsHook.deleteSession(sessionKey)
    },
    [sessionsHook.deleteSession],
  )

  const handleClearSession = useCallback(async () => {
    if (!sessionsHook.currentSessionKey) return
    await sessionsHook.clearSession(sessionsHook.currentSessionKey)
    messagesHook.clearMessages()
  }, [sessionsHook.currentSessionKey, sessionsHook.clearSession, messagesHook.clearMessages])

  const handleSelectAgent = useCallback(
    async (agentId: string) => {
      setCurrentAgentId(agentId)
      if (sessionsHook.currentSessionKey) {
        try {
          await api.updateSessionAgent(sessionsHook.currentSessionKey, agentId)
        } catch {}
      }
    },
    [api, sessionsHook.currentSessionKey],
  )

  const handleSelectModel = useCallback(
    async (model: string) => {
      if (!sessionsHook.currentSessionKey) return
      await selectModel(model, sessionsHook.currentSessionKey)
    },
    [sessionsHook.currentSessionKey, selectModel],
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

  const handleToggleSidebar = useCallback(() => {
    setSidebarOpen((current) => !current)
  }, [])

  const currentAgent = agents.find((a) => a.id === currentAgentId) ?? null
  const isStreaming = messagesHook.messages.some((m) => m.streaming)

  return {
    error,
    agents,
    currentAgent,
    diagnostics,
    diagnosticsOpen,
    sidebarOpen,
    modelState,
    isStreaming,
    sessions: sessionsHook.sessions,
    currentSessionKey: sessionsHook.currentSessionKey,
    messages: messagesHook.messages,
    approvalRequest: messagesHook.approvalRequest,
    pendingAttachments: messagesHook.pendingAttachments,
    toolStatus: messagesHook.toolStatus,
    handleEvent: messagesHook.handleEvent,
    onSend: handleSend,
    onApprove: handleApprove,
    onCancel: handleCancel,
    onSelectSession: handleSelectSession,
    onCreateSession: handleCreateSession,
    onDeleteSession: handleDeleteSession,
    onClearSession: handleClearSession,
    onSelectAgent: handleSelectAgent,
    onSelectModel: (model: string) => void handleSelectModel(model),
    onUploadAttachments: handleUploadAttachments,
    onAttachmentsChange: messagesHook.setPendingAttachments,
    onLogout: handleLogout,
    onToggleDiagnostics: handleToggleDiagnostics,
    onToggleSidebar: handleToggleSidebar,
  }
}
