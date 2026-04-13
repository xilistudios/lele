import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ApiClient } from '../lib/api'
import { clearCurrentSessionKey } from '../lib/storage'
import type {
  Agent,
  AgentDetails,
  ChannelInfo,
  ConfigResponse,
  SystemStatus,
  ToolInfo,
} from '../lib/types'
import { useChatHistory } from './useChatHistory'
import { useChatSessions } from './useChatSessions'
import { useMessages } from './useMessages'
import { useModels } from './useModels'
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
  const [parentSessionKey, setParentSessionKey] = useState<string | null>(null)
  const navigate = useNavigate()

  const sessionsHook = useChatSessions(api, token, clientId)
  const { modelState, loadModels, selectModel } = useModels(api, token)
  const messagesHook = useMessages(
    api,
    token,
    sessionsHook.currentSessionKey,
    sessionsHook.currentSessionKeyRef,
  )
  const chatHistory = useChatHistory(
    api,
    sessionsHook.currentSessionKey,
    token,
    messagesHook.streamingMessages,
  )

  const wsStatusRef = useRef(wsStatus)
  wsStatusRef.current = wsStatus

  const subscribedSessionRef = useRef<string | null>(null)
  const sessionAgentSeqRef = useRef(0)

  const agentsRef = useRef(agents)
  useEffect(() => {
    agentsRef.current = agents
  }, [agents])

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

    return () => {
      if (subscribedSessionRef.current) {
        wsSend('unsubscribe', { session_key: subscribedSessionRef.current })
        subscribedSessionRef.current = null
      }
    }
  }, [sessionsHook.currentSessionKey, currentAgentId, token, wsStatus, wsSend])

  useEffect(() => {
    if (!sessionsHook.currentSessionKey || !currentAgentId || !token) return
    if (wsStatus !== 'connected') return
    if (subscribedSessionRef.current === sessionsHook.currentSessionKey) return

    wsSend('subscribe', { session_key: sessionsHook.currentSessionKey, agent_id: currentAgentId })
    subscribedSessionRef.current = sessionsHook.currentSessionKey
    loadModels(currentAgentId, sessionsHook.currentSessionKey, chatHistory.messages.length > 0)
  }, [
    sessionsHook.currentSessionKey,
    currentAgentId,
    token,
    wsStatus,
    wsSend,
    loadModels,
    chatHistory.messages,
  ])

  const handleLogout = useCallback(() => {
    subscribedSessionRef.current = null
    wsClose()
    messagesHook.clearStreaming()
    persistSession(null)
    clearCurrentSessionKey()
    setAgents([])
    setCurrentAgentId(null)
    setDiagnostics({ status: null, channels: [], tools: [], config: null, agentInfo: null })
    setError(null)
  }, [wsClose, messagesHook.clearStreaming, persistSession])

  const handleSend = useCallback(
    async (content: string, attachments: string[]) => {
      if (!sessionsHook.currentSessionKey || !currentAgentId) return

      await messagesHook.sendMessage(
        content,
        attachments,
        sessionsHook.currentSessionKey,
        currentAgentId,
      )
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
    async (sessionKey: string, options?: { parentSessionKey?: string | null }) => {
      if (
        sessionsHook.currentSessionKey === sessionKey &&
        parentSessionKey === (options?.parentSessionKey ?? null)
      ) {
        return
      }
      if (sessionsHook.currentSessionKey) {
        wsSend('unsubscribe', { session_key: sessionsHook.currentSessionKey })
      }
      subscribedSessionRef.current = null
      setParentSessionKey(options?.parentSessionKey ?? null)
      sessionsHook.selectSession(sessionKey)
      messagesHook.clearStreaming()
      const requestSeq = ++sessionAgentSeqRef.current
      try {
        const agentResult = await api.sessionAgent(sessionKey)
        if (sessionAgentSeqRef.current !== requestSeq) {
          return
        }
        if (sessionsHook.currentSessionKeyRef.current !== sessionKey) {
          return
        }
        const validAgent = agentsRef.current.find((a) => a.id === agentResult.agent_id)
        if (validAgent) {
          setCurrentAgentId(agentResult.agent_id)
        }
      } catch {}
    },
    [
      wsSend,
      sessionsHook.selectSession,
      sessionsHook.currentSessionKey,
      sessionsHook.currentSessionKeyRef,
      messagesHook.clearStreaming,
      api,
      parentSessionKey,
    ],
  )

  const handleCreateSession = useCallback(() => {
    const currentSession = sessionsHook.sessions.find(
      (s) => s.key === sessionsHook.currentSessionKey,
    )

    if (currentSession && currentSession.message_count === 0 && sessionsHook.currentSessionKey) {
      navigate(`/chat/${encodeURIComponent(sessionsHook.currentSessionKey)}`)
      return
    }

    if (sessionsHook.currentSessionKey) {
      wsSend('unsubscribe', { session_key: sessionsHook.currentSessionKey })
    }
    subscribedSessionRef.current = null
    setParentSessionKey(null)
    const newKey = sessionsHook.createSession()
    if (newKey) {
      messagesHook.clearStreaming()
      navigate(`/chat/${encodeURIComponent(newKey)}`)
    }
  }, [wsSend, sessionsHook, messagesHook.clearStreaming, navigate])

  const handleDeleteSession = useCallback(
    async (sessionKey: string): Promise<string | null> => {
      return await sessionsHook.deleteSession(sessionKey)
    },
    [sessionsHook.deleteSession],
  )

  const handleClearSession = useCallback(async () => {
    if (!sessionsHook.currentSessionKey) return
    await sessionsHook.clearSession(sessionsHook.currentSessionKey)
    messagesHook.clearStreaming()
  }, [sessionsHook.currentSessionKey, sessionsHook.clearSession, messagesHook.clearStreaming])

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
  const isStreaming = messagesHook.streamingMessages.some((m) => m.streaming)

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
    parentSessionKey,
    messages: chatHistory.messages,
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
