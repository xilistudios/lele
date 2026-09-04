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
import {
  finalizeStreamingAssistantsForSession,
  hasStreamingMessageForSession,
} from './streamingOpsLocal'
import { useChatHistory } from './useChatHistory'
import { useChatSessions } from './useChatSessions'
import { useMessageQueue } from './useMessageQueue'
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
  // Mobile drawer state — independent from desktop collapse. Always starts closed.
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false)
  const [chatMode, setChatMode] = useState<ChatMode>(() => {
    return (localStorage.getItem('lele_chat_mode') as ChatMode) || 'agent'
  })
  const [parentSessionKey, setParentSessionKey] = useState<string | null>(null)
  const [thinkLevel, setThinkLevel] = useState('default')
  // Server folder attached to the current session (injected into the agent
  // context by the backend). '' means "no folder".
  const [sessionFolder, setSessionFolder] = useState('')
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
  const queueHook = useMessageQueue()

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
      // Load sessions and agents IN PARALLEL. The sessions list powers the
      // sidebar and the agent list is needed to resolve the session's agent.
      // Previously these were loaded sequentially (sessions first, then agents
      // with retries), which serialized two network round-trips on every
      // bootstrap.
      const [sessionKey, agentsResult] = await Promise.all([
        sessionsHook.refreshSessions().catch((err) => {
          setError((err as Error).message)
          return null
        }),
        api.agents().catch((err) => {
          console.warn('[useAppLogic] Failed to load agents:', err)
          return { agents: [] as Agent[] }
        }),
      ])

      const agentsList = agentsResult?.agents ?? []
      setAgents(agentsList)

      // Resolve agent for current session
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

    // Rehydrate the session folder the same way thinking level is rehydrated.
    // A 404 (session without folder yet) silently resolves to "no folder".
    // Guard against the session changing while the request is in flight: the
    // response must only apply to the session that was current when it started.
    const folderKey = sessionsHook.currentSessionKey
    api
      .sessionFolder(folderKey)
      .then((res) => {
        if (sessionsHook.currentSessionKeyRef.current !== folderKey) return
        setSessionFolder(res.folder ?? '')
      })
      .catch(() => {
        if (sessionsHook.currentSessionKeyRef.current !== folderKey) return
        setSessionFolder('')
      })
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

  // No session selected → no folder chip (e.g. after deleting the last chat).
  useEffect(() => {
    if (!sessionsHook.currentSessionKey) {
      setSessionFolder('')
    }
  }, [sessionsHook.currentSessionKey])

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
    setSessionFolder('')
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
      // Optimistic update: bump updated timestamp immediately so the sidebar
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

  const handleSelectFolder = useCallback(
    async (folder: string) => {
      if (!sessionsHook.currentSessionKey) return
      try {
        const res = await api.updateSessionFolder(sessionsHook.currentSessionKey, folder)
        setSessionFolder(res.folder ?? folder)
      } catch (err) {
        setError((err as Error).message)
      }
    },
    [api, sessionsHook.currentSessionKey],
  )

  const handleClearFolder = useCallback(async () => {
    if (!sessionsHook.currentSessionKey) {
      setSessionFolder('')
      return
    }
    try {
      await api.updateSessionFolder(sessionsHook.currentSessionKey, '')
    } catch (err) {
      setError((err as Error).message)
      return
    }
    setSessionFolder('')
  }, [api, sessionsHook.currentSessionKey])

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

  const handleOpenMobileSidebar = useCallback(() => {
    setMobileSidebarOpen(true)
  }, [])

  const handleCloseMobileSidebar = useCallback(() => {
    setMobileSidebarOpen(false)
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
      // Backstop for the session-scoped streaming term of isProcessing: the
      // backend is no longer processing this session (HTTP ground truth), so
      // any assistant still flagged streaming:true for it is stale (e.g. a
      // placeholder orphaned by a lost message.complete). Finalize them here,
      // otherwise the loading dots would stay lit until a page reload.
      messagesHook.setStreamingMessages((prev) =>
        finalizeStreamingAssistantsForSession(prev, sessionKey),
      )
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

  // Loading is scoped PER SESSION: the first term only counts streaming
  // messages whose sessionKey matches the session currently being viewed
  // (an orphan placeholder left over from a background conversation, or from
  // a missed message.complete after a chat switch, must not light the dots
  // here). If no session is selected the term is false. Per-session spinners
  // in the sidebar are driven independently by processingSessions.
  const isProcessing =
    hasStreamingMessageForSession(messagesHook.streamingMessages, sessionsHook.currentSessionKey) ||
    (sessionsHook.currentSessionKey
      ? processingSessions.has(sessionsHook.currentSessionKey)
      : false) ||
    hasActiveGroup

  // ── Message queue ───────────────────────────────────────────────────────
  // Enter-submit while the agent is busy must not lose the message: it lands in
  // the per-session FIFO queue and is replayed through the normal send path when
  // the turn ends. Slash commands are NOT special-cased here — the backend runs
  // them when the message is delivered, so queueing them is enough.
  //
  // The busy check reads isProcessing through a ref so the callback identity
  // stays stable across streaming ticks (isProcessing is derived from hot state
  // and would otherwise rebuild onMessage every tick).
  const busyRef = useRef(isProcessing)
  busyRef.current = isProcessing

  const currentSessionKeyRef = useRef(sessionsHook.currentSessionKey)
  currentSessionKeyRef.current = sessionsHook.currentSessionKey

  const sendOrQueue = useCallback(
    (content: string, attachments: string[]): boolean => {
      const sessionKey = currentSessionKeyRef.current
      if (busyRef.current && sessionKey) {
        // False when this session's queue is at QUEUE_CAP: the caller keeps the
        // draft so the message is not lost.
        const accepted = queueHook.enqueueMessage(sessionKey, content, attachments)
        // The queue entry now owns those attachments (they are sent with it on
        // flush). Clearing avoids the second queued message inheriting the first
        // one's files, mirroring what handleSend does after a real send.
        if (accepted && attachments.length > 0) messagesHook.setPendingAttachments([])
        return accepted
      }
      void handleSend(content, attachments)
      return true
    },
    [handleSend, queueHook.enqueueMessage, messagesHook.setPendingAttachments],
  )

  // Auto-flush: one queued message per processing falling edge. The replayed
  // message raises isProcessing again, so the rest of the queue drains on the
  // following turn ends — paced by real agent turns instead of firing at once.
  //
  // prev is tracked per session key so switching chats does not look like a
  // falling edge of the session we just left.
  const prevQueueProcessingRef = useRef<{ key: string | null; value: boolean } | null>(null)
  const flushingIdRef = useRef<string | null>(null)
  useEffect(() => {
    const sessionKey = sessionsHook.currentSessionKey
    const previous = prevQueueProcessingRef.current
    const wasProcessing = previous !== null && previous.key === sessionKey && previous.value
    const switchedToThisSession = previous === null || previous.key !== sessionKey

    // Never flush while the agent is busy on this session. The state IS recorded
    // here so the next run can detect the true→false (falling) edge.
    if (!sessionKey || isProcessing) {
      prevQueueProcessingRef.current = { key: sessionKey, value: isProcessing }
      return
    }

    // handleSend needs the session's agent, otherwise it returns early and the
    // popped message would be lost. Leave the transition unconsumed so this
    // effect retries as soon as currentAgentId arrives (it is a dependency).
    if (!currentAgentId) return

    prevQueueProcessingRef.current = { key: sessionKey, value: isProcessing }

    // Trigger: this session's turn just ended, or the user switched onto an
    // idle session that still holds a backlog.
    if (!wasProcessing && !switchedToThisSession) return

    // A dequeue is already in flight (its message.ack may not have arrived yet).
    if (flushingIdRef.current) return

    const next = queueHook.peekNext(sessionKey)
    if (!next || next.sessionKey !== sessionKey) return

    flushingIdRef.current = next.id
    queueHook.dequeueNext(sessionKey)
    void handleSend(next.content, next.attachments).finally(() => {
      if (flushingIdRef.current === next.id) flushingIdRef.current = null
    })
  }, [
    isProcessing,
    sessionsHook.currentSessionKey,
    currentAgentId,
    handleSend,
    queueHook.peekNext,
    queueHook.dequeueNext,
  ])

  return {
    error,
    agents,
    currentAgent,
    diagnostics,
    diagnosticsOpen,
    sidebarOpen,
    mobileSidebarOpen,
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
    onSend: sendOrQueue,
    queuedMessages: queueHook.queuedMessages,
    removeQueuedMessage: queueHook.removeQueuedMessage,
    clearQueue: queueHook.clearQueue,
    queueCount: queueHook.queueCount,
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
    sessionFolder,
    onSelectFolder: handleSelectFolder,
    onClearFolder: handleClearFolder,
    onUploadAttachments: handleUploadAttachments,
    onAttachmentsChange: messagesHook.setPendingAttachments,
    onLogout: handleLogout,
    onToggleDiagnostics: handleToggleDiagnostics,
    onToggleSidebar: handleToggleSidebar,
    onOpenMobileSidebar: handleOpenMobileSidebar,
    onCloseMobileSidebar: handleCloseMobileSidebar,
    onSelectMode: selectMode,
    loadMore: chatHistory.loadMore,
    hasMore: chatHistory.hasMore,
    isLoadingMore: chatHistory.isLoadingMore,
    typingIndicator: messagesHook.typingIndicator,
    sendTyping: messagesHook.sendTyping,
  }
}
