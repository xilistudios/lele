import { useCallback, useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import i18n from '../i18n'
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
import { useOnPageVisible } from './usePageVisible'
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
  const [mobileSidebarOpen, setMobileSidebarOpen] = useState(false)
  const [chatMode, setChatMode] = useState<ChatMode>(() => {
    return (localStorage.getItem('lele_chat_mode') as ChatMode) || 'agent'
  })
  const [parentSessionKey, setParentSessionKey] = useState<string | null>(null)
  const [thinkLevel, setThinkLevel] = useState('default')
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
    setError,
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

  const prevWsStatusRef = useRef(wsStatus)
  const hadWsConnectedRef = useRef(false)
  const bootstrapOutcomeRef = useRef<'pending' | 'ok' | 'failed'>('pending')
  // biome-ignore lint/correctness/useExhaustiveDependencies: the reconnect actions use stable dispatchers/current-session reads that must not re-trigger the transition-guarded effect
  useEffect(() => {
    if (prevWsStatusRef.current !== 'connected' && wsStatus === 'connected') {
      subscribedSessionRef.current = null
      const isReconnect = hadWsConnectedRef.current
      hadWsConnectedRef.current = true
      if (isReconnect || bootstrapOutcomeRef.current === 'failed') {
        sessionsHook.refreshSessions().catch((err) => {
          console.warn('[useAppLogic] Failed to refresh sessions on reconnect:', err)
        })
        chatHistory.invalidateHistory()
        messagesHook.setStreamingMessages((prev) =>
          finalizeStreamingAssistantsForSession(prev, sessionsHook.currentSessionKey),
        )
      }
    }
    prevWsStatusRef.current = wsStatus
  }, [wsStatus, sessionsHook.refreshSessions, chatHistory.invalidateHistory])

  useOnPageVisible(() => {
    void sessionsHook.refreshSessions().catch((err) => {
      console.warn('[useAppLogic] Failed to refresh sessions on tab focus:', err)
    })
  })

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

      bootstrapOutcomeRef.current = sessionKey === null ? 'failed' : 'ok'

      const agentsList = agentsResult?.agents ?? []
      setAgents(agentsList)

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
    loadModels(currentAgentId, sessionsHook.currentSessionKey, hasConversation).catch(() => {})

    const thinkingKey = sessionsHook.currentSessionKey
    api
      .sessionThinking(thinkingKey)
      .then((res) => {
        if (sessionsHook.currentSessionKeyRef.current !== thinkingKey) return
        setThinkLevel(res.level)
      })
      .catch(() => {
        if (sessionsHook.currentSessionKeyRef.current !== thinkingKey) return
      })

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

  useEffect(() => {
    if (!sessionsHook.currentSessionKey) {
      setSessionFolder('')
    }
  }, [sessionsHook.currentSessionKey])

  const handleLogout = useCallback(async () => {
    await api.logout()
    subscribedSessionRef.current = null
    modelLoadKeyRef.current = null
    wsClose()
    messagesHook.clearAll()
    sessionsHook.reset()
    bootstrapOutcomeRef.current = 'pending'
    persistSession(null)
    clearCurrentSessionKey()
    setAgents([])
    setCurrentAgentId(null)
    setSessionFolder('')
    setDiagnostics({ status: null, channels: [], tools: [], config: null, agentInfo: null })
    setError(null)
  }, [api, wsClose, messagesHook.clearAll, persistSession, sessionsHook.reset])

  const handleSend = useCallback(
    async (content: string, attachments: string[]): Promise<boolean> => {
      let sessionKey = sessionsHook.currentSessionKey
      if (!sessionKey) {
        sessionKey = await sessionsHook.createSession(chatMode)
        if (!sessionKey) {
          setError('Failed to create session')
          return false
        }
      }
      if (!currentAgentId) {
        setError('No agent selected')
        return false
      }

      const accepted = await messagesHook.sendMessage(
        content,
        attachments,
        sessionKey,
        currentAgentId,
      )
      if (!accepted) {
        return false
      }
      messagesHook.setPendingAttachments([])
      const title = content
        .replace(/[\n\r\t]+/g, ' ')
        .replace(/[.,!?;:'"`]+/g, '')
        .trim()
        .slice(0, 50)
      touchSession(sessionKey, title || undefined, chatMode)
      return true
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

      messagesHook.approveRequest(approved, requestId, command)

      if (sessionKey) {
        api.approve(sessionKey, requestId, approved).catch((err) => {
          console.warn('[Approve] HTTP failed, falling back to WS:', err)
          wsSend('approve', { request_id: requestId, approved })
        })
      } else {
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
    const sessionKey = sessionsHook.currentSessionKey
    wsSend('cancel', sessionKey ? { session_key: sessionKey } : {})
  }, [wsSend, sessionsHook.currentSessionKey])

  const handleSelectSession = useCallback(
    async (sessionKey: string, options?: { parentSessionKey?: string | null }) => {
      if (
        sessionsHook.currentSessionKey === sessionKey &&
        parentSessionKey === (options?.parentSessionKey ?? null)
      ) {
        return
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
      const sessionKey = await sessionsHook.createSession(targetMode)
      if (sessionKey) {
        navigate(`/chat/${encodeURIComponent(sessionKey)}`, { replace: true })

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
      const previousAgentId = currentAgentIdRef.current
      setCurrentAgentId(agentId)
      if (sessionsHook.currentSessionKey) {
        try {
          await api.updateSessionAgent(sessionsHook.currentSessionKey, agentId)
        } catch (err) {
          setCurrentAgentId(previousAgentId)
          setError((err as Error).message)
        }
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
      if (!token) {
        setError(i18n.t('chat.notAuthenticated'))
        return []
      }
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
      messagesHook.setProcessingSessions((prev: Set<string>) => {
        if (!prev.has(sessionKey)) return prev
        const next = new Set(prev)
        next.delete(sessionKey)
        return next
      })
      messagesHook.setStreamingMessages((prev) =>
        finalizeStreamingAssistantsForSession(prev, sessionKey),
      )
    }

    prevProcessingRef.current = chatHistory.processing
  }, [chatHistory.processing, sessionsHook.currentSessionKey])

  const currentAgent = agents.find((a) => a.id === currentAgentId) ?? null
  const processingSessions = messagesHook.processingSessions
  const hasActiveGroup = Array.from(messagesHook.groups.values()).some(
    (g) => g.status === 'started',
  )

  const isProcessing =
    hasStreamingMessageForSession(messagesHook.streamingMessages, sessionsHook.currentSessionKey) ||
    (sessionsHook.currentSessionKey
      ? processingSessions.has(sessionsHook.currentSessionKey)
      : false) ||
    hasActiveGroup

  const busyRef = useRef(isProcessing)
  busyRef.current = isProcessing

  const currentSessionKeyRef = useRef(sessionsHook.currentSessionKey)
  currentSessionKeyRef.current = sessionsHook.currentSessionKey

  const sendOrQueue = useCallback(
    async (content: string, attachments: string[]): Promise<boolean> => {
      const sessionKey = currentSessionKeyRef.current
      if (busyRef.current && sessionKey) {
        const accepted = queueHook.enqueueMessage(sessionKey, content, attachments)
        if (accepted && attachments.length > 0) messagesHook.setPendingAttachments([])
        return accepted
      }
      return handleSend(content, attachments)
    },
    [handleSend, queueHook.enqueueMessage, messagesHook.setPendingAttachments],
  )

  const prevQueueProcessingRef = useRef<{ key: string | null; value: boolean } | null>(null)
  const flushingIdRef = useRef<string | null>(null)
  const forceSendRef = useRef<{
    content: string
    attachments: string[]
    sessionKey: string
  } | null>(null)

  const restoreQueued = useCallback(
    (msg: { sessionKey: string; content: string; attachments: string[] }) => {
      queueHook.enqueueMessage(msg.sessionKey, msg.content, msg.attachments)
    },
    [queueHook.enqueueMessage],
  )

  useEffect(() => {
    const sessionKey = sessionsHook.currentSessionKey
    const previous = prevQueueProcessingRef.current
    const wasProcessing = previous !== null && previous.key === sessionKey && previous.value
    const switchedToThisSession = previous === null || previous.key !== sessionKey

    if (!sessionKey || isProcessing) {
      prevQueueProcessingRef.current = { key: sessionKey, value: isProcessing }
      return
    }

    if (!currentAgentId) return

    prevQueueProcessingRef.current = { key: sessionKey, value: isProcessing }

    if (!wasProcessing && !switchedToThisSession) return

    if (flushingIdRef.current) return

    const forced = forceSendRef.current
    if (forced) {
      if (forced.sessionKey !== sessionKey) return
      forceSendRef.current = null
      flushingIdRef.current = 'force-send'
      void handleSend(forced.content, forced.attachments)
        .then((ok) => {
          if (!ok) restoreQueued(forced)
        })
        .finally(() => {
          if (flushingIdRef.current === 'force-send') flushingIdRef.current = null
        })
      return
    }

    const next = queueHook.peekNext(sessionKey)
    if (!next || next.sessionKey !== sessionKey) return

    flushingIdRef.current = next.id
    queueHook.dequeueNext(sessionKey)
    void handleSend(next.content, next.attachments)
      .then((ok) => {
        if (!ok) restoreQueued(next)
      })
      .finally(() => {
        if (flushingIdRef.current === next.id) flushingIdRef.current = null
      })
  }, [
    isProcessing,
    sessionsHook.currentSessionKey,
    currentAgentId,
    handleSend,
    queueHook.peekNext,
    queueHook.dequeueNext,
    restoreQueued,
  ])

  const sendNowQueuedMessage = useCallback(
    (id: string) => {
      const msg = queueHook.takeQueuedMessage(id)
      if (!msg) return
      if (busyRef.current) {
        if (forceSendRef.current) {
          restoreQueued(msg)
          return
        }
        forceSendRef.current = {
          content: msg.content,
          attachments: msg.attachments,
          sessionKey: msg.sessionKey,
        }
        handleCancel()
        return
      }
      void handleSend(msg.content, msg.attachments).then((ok) => {
        if (!ok) restoreQueued(msg)
      })
    },
    [queueHook.takeQueuedMessage, handleCancel, handleSend, restoreQueued],
  )

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
    sendNowQueuedMessage,
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
    isHistoryLoading: chatHistory.isLoading,
    typingIndicator: messagesHook.typingIndicator,
    sendTyping: messagesHook.sendTyping,
    streamMetrics: messagesHook.streamMetrics,
  }
}
