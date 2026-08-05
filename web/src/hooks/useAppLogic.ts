import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import type { ApiClient } from '../lib/api'
import { wsDebug } from '../lib/debug'
import { clearCurrentSessionKey, loadSidebarOpen, saveSidebarOpen } from '../lib/storage'
import type {
  Agent,
  AgentDetails,
  ChannelInfo,
  ChatMode,
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

import type { ClientCommand } from '../services/ws/events'

type SendFn = (event: ClientCommand['event'], data: Record<string, unknown>) => void

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
  const [sidebarOpen, setSidebarOpen] = useState(() => loadSidebarOpen())
  const [chatMode, setChatMode] = useState<ChatMode>(() => {
    return (localStorage.getItem('lele_chat_mode') as ChatMode) || 'agent'
  })
  const [parentSessionKey, setParentSessionKey] = useState<string | null>(null)
  const [thinkLevel, setThinkLevel] = useState('default')
  const navigate = useNavigate()

  const sessionsHook = useChatSessions(api, token, clientId)
  const { touchSession } = sessionsHook
  const { modelState, loadModels, selectModel } = useModels(api, token)
  const messagesHook = useMessages(
    wsSend,
    sessionsHook.currentSessionKey,
    sessionsHook.currentSessionKeyRef,
    sessionsHook.refreshSessions,
    parentSessionKey,
  )
  const chatHistory = useChatHistory(
    api,
    sessionsHook.currentSessionKey,
    token,
    messagesHook.streamingMessages,
    parentSessionKey ?? undefined,
    messagesHook.hydrateGroups,
  )

  const wsStatusRef = useRef(wsStatus)
  wsStatusRef.current = wsStatus

  const hasMessagesRef = useRef(false)
  hasMessagesRef.current = chatHistory.messages.length > 0

  const subscribedSessionRef = useRef<string | null>(null)
  const sessionAgentSeqRef = useRef(0)
  const modelLoadKeyRef = useRef<string | null>(null)

  // Reset subscription tracking on WebSocket reconnection to force re-subscribe.
  // This fixes the bug where the frontend thinks it's subscribed but the backend
  // has already cleaned up the client (e.g., after >30s disconnect).
  const prevWsStatusRef = useRef(wsStatus)
  useEffect(() => {
    if (prevWsStatusRef.current !== 'connected' && wsStatus === 'connected') {
      subscribedSessionRef.current = null
      // Refresh sessions on reconnect to pick up changes made during disconnect
      sessionsHook.refreshSessions().catch((err) => {
        console.warn('[useAppLogic] Failed to refresh sessions on reconnect:', err)
      })
      // Refetch chat history to recover messages that arrived during the
      // disconnect window. The WS reconnected event includes in_progress
      // content, but completed messages are only available via HTTP.
      chatHistory.invalidateHistory()
    }
    prevWsStatusRef.current = wsStatus
  }, [wsStatus, sessionsHook.refreshSessions, chatHistory.invalidateHistory])

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
      // 1. Load sessions first (critical for sidebar)
      let sessionKey: string | null = null
      try {
        sessionKey = await sessionsHook.refreshSessions()
        setError(null)
      } catch (err) {
        setError((err as Error).message)
      }

      // 2. Load agents separately with retry (non-blocking for sessions)
      let agentsList: Agent[] = []
      for (let attempt = 0; attempt < 3; attempt++) {
        try {
          const agentsResult = await api.agents()
          agentsList = agentsResult.agents
          setAgents(agentsList)
          break
        } catch (err) {
          console.warn(`[useAppLogic] Failed to load agents (attempt ${attempt + 1}/3):`, err)
          if (attempt < 2) {
            await new Promise((resolve) => setTimeout(resolve, 1000))
          }
        }
      }

      // 3. Resolve agent for current session
      if (sessionKey && !currentAgentIdRef.current) {
        try {
          const agentResult = await api.sessionAgent(sessionKey)
          const validAgent = agentsList.find((a) => a.id === agentResult.agent_id)
          if (validAgent) {
            setCurrentAgentId(agentResult.agent_id)
          } else if (agentsList.length > 0) {
            setCurrentAgentId(agentsList[0].id)
          }
        } catch {
          if (agentsList.length > 0) {
            setCurrentAgentId(agentsList[0].id)
          }
        }
      } else if (!currentAgentIdRef.current && agentsList.length > 0) {
        setCurrentAgentId(agentsList[0].id)
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

    const alreadySubscribed = subscribedSessionRef.current === sessionsHook.currentSessionKey
    if (!alreadySubscribed) {
      // Don't unsubscribe from previous sessions — the backend manages stale
      // subscriptions via TTL, so we avoid unnecessary unsub/resub chatter and
      // keep receiving background events (e.g., message.complete) for sessions
      // that are still processing.
      wsDebug('[AppLogic] Subscribing to session', {
        sessionKey: sessionsHook.currentSessionKey,
        agentId: currentAgentId,
        wsStatus,
      })
      wsSend('subscribe', { session_key: sessionsHook.currentSessionKey, agent_id: currentAgentId })
      subscribedSessionRef.current = sessionsHook.currentSessionKey
    }

    const hasConversation = chatHistory.rawMessages.length > 0 || hasMessagesRef.current
    const modelLoadKey = `${sessionsHook.currentSessionKey}:${currentAgentId}:${hasConversation ? 'history' : 'empty'}`
    if (modelLoadKeyRef.current === modelLoadKey) {
      return
    }

    modelLoadKeyRef.current = modelLoadKey
    loadModels(currentAgentId, sessionsHook.currentSessionKey, hasConversation).catch(() => {
      // Model loading is best-effort; a failure here must not surface as an
      // unhandled rejection (it would crash the app / pollute unrelated flows).
    })

    api
      .sessionThinking(sessionsHook.currentSessionKey)
      .then((res) => {
        setThinkLevel(res.level)
      })
      .catch(() => {})
  }, [
    sessionsHook.currentSessionKey,
    currentAgentId,
    token,
    api,
    wsStatus,
    wsSend,
    loadModels,
    chatHistory.rawMessages.length,
  ])

  const handleLogout = useCallback(async () => {
    // Revoke token server-side first
    await api.logout()
    subscribedSessionRef.current = null
    modelLoadKeyRef.current = null
    wsClose()
    messagesHook.clearAll()
    persistSession(null)
    clearCurrentSessionKey()
    setAgents([])
    setCurrentAgentId(null)
    setDiagnostics({ status: null, channels: [], tools: [], config: null, agentInfo: null })
    setError(null)
  }, [api, wsClose, messagesHook.clearAll, persistSession])

  const handleSend = useCallback(
    async (content: string, attachments: string[]) => {
      let sessionKey = sessionsHook.currentSessionKey
      if (!sessionKey) {
        sessionKey = await sessionsHook.createSession(chatMode)
        if (!sessionKey) return
      }
      if (!currentAgentId) return

      await messagesHook.sendMessage(content, attachments, sessionKey, currentAgentId)
      messagesHook.setPendingAttachments([])
      // Optimistic update: bump message_count immediately so sidebar
      // shows the session activity instead of "New Chat"
      // Also generate a title from the first message so we don't show a UUID
      const title = content
        .replace(/[\n\r\t]+/g, ' ')
        .replace(/[.,!?;:'"`]+/g, '')
        .trim()
        .slice(0, 50)
      touchSession(sessionKey, title || undefined, chatMode)
    },
    [
      sessionsHook.currentSessionKey,
      currentAgentId,
      messagesHook.sendMessage,
      messagesHook.setPendingAttachments,
      touchSession,
      sessionsHook.createSession,
      chatMode,
    ],
  )

  const handleApprove = useCallback(
    async (approved: boolean) => {
      if (!messagesHook.approvalRequest) return
      const { id: requestId, command } = messagesHook.approvalRequest
      const sessionKey = sessionsHook.currentSessionKey

      // Optimistic UI update: show feedback immediately
      messagesHook.approveRequest(approved, requestId, command)

      // Send via HTTP as primary method (persists in history).
      // If HTTP fails, fall back to WebSocket (which also persists on the backend).
      if (sessionKey) {
        api.approve(sessionKey, requestId, approved).catch((err) => {
          console.warn('[Approve] HTTP failed, falling back to WS:', err)
          wsSend('approve', { request_id: requestId, approved })
        })
      } else {
        // No session key available, use WebSocket directly
        wsSend('approve', { request_id: requestId, approved })
      }
    },
    [
      messagesHook.approvalRequest,
      messagesHook.approveRequest,
      sessionsHook.currentSessionKey,
      api,
      wsSend,
    ],
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
      // Keep all session subscriptions active so we receive
      // events (like message.complete) for background sessions
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
      sessionsHook.selectSession,
      sessionsHook.currentSessionKey,
      sessionsHook.currentSessionKeyRef,
      messagesHook.clearStreaming,
      api,
      parentSessionKey,
    ],
  )

  const handleCreateSession = useCallback(
    async (modeOverride?: ChatMode) => {
      subscribedSessionRef.current = null
      setParentSessionKey(null)
      messagesHook.clearStreaming()
      const targetMode = modeOverride ?? chatMode
      // Await backend session registration before navigating, so the subsequent
      // WebSocket subscribe passes ownership validation on the first attempt.
      const sessionKey = await sessionsHook.createSession(targetMode)
      if (sessionKey) {
        navigate(`/chat/${sessionKey}`, { replace: true })

        // Set currentAgentId so the WebSocket subscription useEffect can fire.
        // Try to get the agent assigned to the new session; fall back to the
        // existing currentAgentId or the first available agent.
        try {
          const agentResult = await api.sessionAgent(sessionKey)
          const validAgent = agentsRef.current.find((a) => a.id === agentResult.agent_id)
          if (validAgent) {
            setCurrentAgentId(agentResult.agent_id)
          } else if (currentAgentIdRef.current) {
            setCurrentAgentId(currentAgentIdRef.current)
          } else if (agentsRef.current.length > 0) {
            setCurrentAgentId(agentsRef.current[0].id)
          }
        } catch {
          // New session may not have an agent assigned yet — use current or first agent
          if (currentAgentIdRef.current) {
            setCurrentAgentId(currentAgentIdRef.current)
          } else if (agentsRef.current.length > 0) {
            setCurrentAgentId(agentsRef.current[0].id)
          }
        }
      }
    },
    [messagesHook.clearStreaming, navigate, sessionsHook.createSession, api, chatMode],
  )

  const selectMode = useCallback((mode: ChatMode) => {
    setChatMode(mode)
    localStorage.setItem('lele_chat_mode', mode)
  }, [])

  // Reset chatMode to 'agent' if group mode is selected but groups are disabled
  const groupsEnabled = messagesHook.groupsEnabled
  useEffect(() => {
    if (chatMode === 'group' && !groupsEnabled) {
      setChatMode('agent')
      localStorage.setItem('lele_chat_mode', 'agent')
    }
  }, [chatMode, groupsEnabled])

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

  const handleSelectThinkLevel = useCallback(
    async (level: string) => {
      if (!sessionsHook.currentSessionKey) return
      await api.updateSessionThinking(sessionsHook.currentSessionKey, level)
      setThinkLevel(level)
    },
    [api, sessionsHook.currentSessionKey],
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
    setSidebarOpen((current) => {
      const newValue = !current
      saveSidebarOpen(newValue)
      return newValue
    })
  }, [])

  useEffect(() => {
    saveSidebarOpen(sidebarOpen)
  }, [sidebarOpen])

  const prevProcessingRef = useRef(false)
  // biome-ignore lint/correctness/useExhaustiveDependencies: refs are intentionally excluded, they hold stable values
  useEffect(() => {
    const sessionKey = sessionsHook.currentSessionKey
    if (!sessionKey) return

    if (chatHistory.processing) {
      // Agent is processing - ensure session is tracked in processingSessions.
      // This is a safety net for when message.ack hasn't fired yet (e.g., reconnect).
      if (!prevProcessingRef.current) {
        messagesHook.setProcessingSessions((prev: Set<string>) => {
          if (!prev.has(sessionKey)) {
            const next = new Set(prev)
            next.add(sessionKey)
            return next
          }
          return prev
        })
      }
    } else if (prevProcessingRef.current) {
      // Backend reports processing has ended (HTTP poll is the ground truth
      // after the deferred cleanup in runAgentLoop removes sessionCancels).
      // Clean up processingSessions as a safety net in case the WebSocket
      // message.complete event was lost (e.g., reconnect after the 30s buffer
      // window expired). Without this, the loading indicator stays stuck
      // indefinitely until the user reloads the page.
      //
      // We only act on the true→false transition (prevProcessingRef) to avoid
      // spurious removals on initial load when processing is already false.
      messagesHook.setProcessingSessions((prev: Set<string>) => {
        if (!prev.has(sessionKey)) return prev
        const next = new Set(prev)
        next.delete(sessionKey)
        return next
      })
    }

    prevProcessingRef.current = chatHistory.processing
  }, [chatHistory.processing, sessionsHook.currentSessionKey])

  const currentAgent = agents.find((a) => a.id === currentAgentId) ?? null
  const processingSessions = messagesHook.processingSessions
  // isProcessing covers both active SSE streaming and agent thinking/tool-calls.
  // It drives the loading indicator (dots) and scroll-pin behavior in MessageList.
  // processingSessions is the primary driver (WebSocket events: message.ack adds,
  // message.complete removes). The HTTP poll (chatHistory.processing) acts as a
  // safety net: when it transitions true→false, the session is removed from
  // processingSessions in case the WebSocket event was lost (e.g., reconnect
  // after the 30s buffer window expired).
  // hasActiveGroup ensures the loading indicator stays visible during group
  // execution, even after message.complete removes the session from
  // processingSessions (race condition between message.complete and
  // group.status events).
  const hasActiveGroup = Array.from(messagesHook.groups.values()).some(
    (g) => g.status === 'started',
  )

  const isProcessing =
    messagesHook.streamingMessages.some((m) => m.streaming) ||
    (sessionsHook.currentSessionKey
      ? processingSessions.has(sessionsHook.currentSessionKey)
      : false) ||
    hasActiveGroup

  return {
    error,
    agents,
    currentAgent,
    diagnostics,
    diagnosticsOpen,
    sidebarOpen,
    chatMode,
    modelState,
    thinkLevel,
    isProcessing,
    processingSessions,
    sessions: sessionsHook.sessions,
    currentSessionKey: sessionsHook.currentSessionKey,
    parentSessionKey,
    messages: chatHistory.messages,
    approvalRequest: messagesHook.approvalRequest,
    approvalResult: messagesHook.approvalResult,
    pendingAttachments: messagesHook.pendingAttachments,
    toolStatus: messagesHook.toolStatus,
    groups: messagesHook.groups,
    groupsEnabled,
    setProcessingSessions: messagesHook.setProcessingSessions,
    handleEvent: messagesHook.handleEvent,
    onSend: handleSend,
    retryMessage: messagesHook.retryMessage,
    onApprove: handleApprove,
    onCancel: handleCancel,
    onSelectSession: handleSelectSession,
    onCreateSession: handleCreateSession,
    createSession: sessionsHook.createSession,
    onDeleteSession: handleDeleteSession,
    onClearSession: handleClearSession,
    onSelectAgent: handleSelectAgent,
    onSelectModel: (model: string) => void handleSelectModel(model),
    onSelectThinkLevel: handleSelectThinkLevel,
    onUploadAttachments: handleUploadAttachments,
    onAttachmentsChange: messagesHook.setPendingAttachments,
    onLogout: handleLogout,
    onToggleDiagnostics: handleToggleDiagnostics,
    onToggleSidebar: handleToggleSidebar,
    onSelectMode: selectMode,
    loadMore: chatHistory.loadMore,
    hasMore: chatHistory.hasMore,
    isLoadingMore: chatHistory.isLoadingMore,
    typingIndicator: messagesHook.typingIndicator,
    sendTyping: messagesHook.sendTyping,
  }
}
