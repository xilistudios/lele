import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AuthView } from './components/AuthView'
import { ChatView } from './components/ChatView'
import { createApiClient } from './lib/api'
import { LeleSocket } from './lib/socket'
import {
  clearCurrentSessionKey,
  clearSession,
  loadApiUrl,
  loadCurrentSessionKey,
  loadSession,
  saveApiUrl,
  saveCurrentSessionKey,
  saveSession,
} from './lib/storage'
import type {
  Agent,
  AgentDetails,
  ApprovalRequest,
  AuthSession,
  ChannelInfo,
  ChatMessage,
  ChatSession,
  ConfigResponse,
  HistoryToolCall,
  ModelGroup,
  SystemStatus,
  ToolInfo,
  ToolStatus,
} from './lib/types'

const defaultApiUrl =
  import.meta.env.VITE_LELE_API_URL ??
  `${window.location.protocol}//${window.location.hostname}:18793`

const generateUUID = () => {
  return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, (c) => {
    const r = (Math.random() * 16) | 0
    const v = c === 'x' ? r : (r & 0x3) | 0x8
    return v.toString(16)
  })
}

const buildDefaultSessionKey = (auth: Pick<AuthSession, 'client_id'>) => `native:${auth.client_id}`

const formatToolCallArgs = (toolCall: HistoryToolCall) => {
  if (typeof toolCall.arguments === 'undefined') {
    return toolCall.name ?? ''
  }

  return toolCall.name
    ? `${toolCall.name} ${JSON.stringify(toolCall.arguments)}`
    : JSON.stringify(toolCall.arguments)
}

const toChatMessages = (
  history: Array<{
    role: 'user' | 'assistant' | 'tool'
    content: string
    tool_calls?: HistoryToolCall[]
    tool_call_id?: string
  }>,
  sessionKey: string,
): ChatMessage[] =>
  history.flatMap((message, index) => {
    const baseMessage: ChatMessage = {
      id: `${sessionKey}:${index}:${message.role}`,
      role: message.role,
      content: message.content,
      streaming: false,
      createdAt: new Date().toISOString(),
      sessionKey,
    }

    if (message.role === 'assistant' && message.tool_calls?.length) {
      return [
        baseMessage,
        ...message.tool_calls.map((toolCall, toolIndex) => ({
          id: `${sessionKey}:${index}:tool:${toolCall.id || toolIndex}`,
          role: 'tool' as const,
          content: '',
          streaming: false,
          createdAt: new Date().toISOString(),
          sessionKey,
          toolName: toolCall.name ?? toolCall.id,
          toolArgs: formatToolCallArgs(toolCall),
          toolStatus: 'completed' as const,
        })),
      ]
    }

    if (message.role === 'tool') {
      return [
        {
          ...baseMessage,
          role: 'tool',
          toolName: message.tool_call_id ?? 'tool',
          toolResult: message.content,
          toolStatus: 'completed',
        },
      ]
    }

    return [baseMessage]
  })

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

export default function App() {
  const { t } = useTranslation()
  const [apiUrl, setApiUrl] = useState(() => loadApiUrl(defaultApiUrl))
  const [session, setSession] = useState<AuthSession | null>(() => loadSession())
  const [agents, setAgents] = useState<Agent[]>([])
  const [sessions, setSessions] = useState<ChatSession[]>([])
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [currentAgentId, setCurrentAgentId] = useState<string | null>(null)
  const [currentSessionKey, setCurrentSessionKey] = useState<string | null>(() =>
    loadCurrentSessionKey(),
  )
  const [wsStatus, setWsStatus] = useState<'disconnected' | 'connecting' | 'connected'>(
    'disconnected',
  )
  const [toolStatus, setToolStatus] = useState<ToolStatus | null>(null)
  const [approvalRequest, setApprovalRequest] = useState<ApprovalRequest | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [diagnostics, setDiagnostics] = useState<DiagnosticsState>({
    status: null,
    channels: [],
    tools: [],
    config: null,
    agentInfo: null,
  })
  const [diagnosticsOpen, setDiagnosticsOpen] = useState(false)
  const [pendingAttachments, setPendingAttachments] = useState<string[]>([])
  const [modelState, setModelState] = useState<ModelState>({
    current: '',
    available: [],
    groups: [],
  })
  const socketRef = useRef<LeleSocket | null>(null)
  const currentSessionKeyRef = useRef<string | null>(currentSessionKey)
  const sessionsRef = useRef<ChatSession[]>(sessions)
  const lastLoadedSessionKeyRef = useRef<string | null>(null)

  useEffect(() => {
    currentSessionKeyRef.current = currentSessionKey
  }, [currentSessionKey])

  useEffect(() => {
    sessionsRef.current = sessions
  }, [sessions])

  const api = useMemo(() => createApiClient(apiUrl), [apiUrl])

  const persistSession = (nextSession: AuthSession | null) => {
    setSession(nextSession)
    if (nextSession) {
      saveSession(nextSession)
      return
    }

    clearSession()
  }

  const persistCurrentSessionKey = (sessionKey: string | null) => {
    setCurrentSessionKey(sessionKey)
    if (sessionKey) {
      saveCurrentSessionKey(sessionKey)
      return
    }

    clearCurrentSessionKey()
  }

  const ensureAssistantPlaceholder = (messageId: string, sessionKey: string, chunk = '') => {
    setMessages((current) => {
      if (current.some((message) => message.id === messageId)) {
        return current.map((message) =>
          message.id === messageId
            ? {
                ...message,
                content: chunk ? `${message.content}${chunk}` : message.content,
                streaming: true,
                sessionKey,
              }
            : message,
        )
      }

      return [
        ...current,
        {
          id: messageId,
          role: 'assistant',
          content: chunk,
          streaming: true,
          createdAt: new Date().toISOString(),
          sessionKey,
        },
      ]
    })
  }

  const handleLogout = () => {
    socketRef.current?.close()
    socketRef.current = null
    persistSession(null)
    clearCurrentSessionKey()
    setAgents([])
    setSessions([])
    setMessages([])
    setCurrentAgentId(null)
    setCurrentSessionKey(null)
    setToolStatus(null)
    setApprovalRequest(null)
    setWsStatus('disconnected')
    setError(null)
    setPendingAttachments([])
    setModelState({ current: '', available: [], groups: [] })
    setDiagnostics({
      status: null,
      channels: [],
      tools: [],
      config: null,
      agentInfo: null,
    })
    setDiagnosticsOpen(false)
  }

  const ensureSession = async (baseSession: AuthSession): Promise<AuthSession | null> => {
    try {
      const status = await api.status(baseSession.token)
      if (status.valid) {
        if (status.device_name && status.device_name !== baseSession.device_name) {
          const nextSession = { ...baseSession, device_name: status.device_name }
          persistSession(nextSession)
          return nextSession
        }

        return baseSession
      }
    } catch {
      // Fall through to refresh when the status probe fails.
    }

    try {
      const refreshed = await api.refresh(baseSession.refresh_token)
      const nextSession: AuthSession = {
        ...baseSession,
        ...refreshed,
        client_id: baseSession.client_id,
        device_name: baseSession.device_name,
      }
      persistSession(nextSession)
      return nextSession
    } catch {
      handleLogout()
      return null
    }
  }

  const loadHistory = async (sessionKey: string, token: string) => {
    lastLoadedSessionKeyRef.current = sessionKey
    try {
      const history = await api.history(sessionKey, token)
      setMessages(toChatMessages(history.messages, history.session_key))
    } catch (error) {
      if (lastLoadedSessionKeyRef.current == sessionKey) {
        lastLoadedSessionKeyRef.current = null
      }
      throw error
    }
  }

  const refreshSessions = async (token: string, defaultSessionKey: string) => {
    const result = await api.sessions(token)
    const fallbackSessions =
      result.sessions.length > 0
        ? result.sessions
        : [
            {
              key: defaultSessionKey,
              created: new Date().toISOString(),
              updated: new Date().toISOString(),
              message_count: 0,
            },
          ]

    const nextSessions = [
      ...sessionsRef.current.filter(
        (sessionItem) =>
          sessionItem.key === currentSessionKeyRef.current &&
          !fallbackSessions.some((nextSession) => nextSession.key === sessionItem.key),
      ),
      ...fallbackSessions,
    ]

    setSessions(nextSessions)

    const availableKeys = new Set(nextSessions.map((item) => item.key))
    const fallbackKey = availableKeys.has(defaultSessionKey)
      ? defaultSessionKey
      : (nextSessions[0]?.key ?? null)
    const storedSessionKey = loadCurrentSessionKey()
    const nextSessionKey =
      currentSessionKeyRef.current && availableKeys.has(currentSessionKeyRef.current)
        ? currentSessionKeyRef.current
        : storedSessionKey && availableKeys.has(storedSessionKey)
          ? storedSessionKey
          : fallbackKey

    persistCurrentSessionKey(nextSessionKey)
    return nextSessionKey
  }

  const loadModels = async (token: string, agentId: string, sessionKey: string | null) => {
    const hasConversation = messages.length > 0
    const result =
      sessionKey && hasConversation
        ? await api.sessionModel(sessionKey, token)
        : await api.models(agentId, sessionKey, token)

    setModelState({
      current: result.model ?? '',
      available: result.models,
      groups: result.model_groups ?? [],
    })
  }

  useEffect(() => {
    if (!session?.token) {
      return
    }

    let active = true

    const bootstrap = async () => {
      const ensuredSession = await ensureSession(session)
      if (!active || !ensuredSession) {
        return
      }

      try {
        const defaultSessionKey = buildDefaultSessionKey(ensuredSession)
        const [nextAgents, nextStatus, nextChannels, nextTools, nextConfig] = await Promise.all([
          api.agents(ensuredSession.token),
          api.systemStatus(ensuredSession.token),
          api.channels(ensuredSession.token),
          api.tools(ensuredSession.token),
          api.config(ensuredSession.token),
        ])

        if (!active) {
          return
        }

        setAgents(nextAgents.agents)
        const nextAgentId =
          currentAgentId ??
          nextAgents.agents.find((agent) => agent.default)?.id ??
          nextAgents.agents[0]?.id ??
          ''
        setCurrentAgentId((current) => current ?? nextAgentId)
        setDiagnostics((current) => ({
          ...current,
          status: nextStatus,
          channels: nextChannels.channels,
          tools: nextTools.tools,
          config: nextConfig,
        }))

        const nextSessionKey =
          (await refreshSessions(ensuredSession.token, defaultSessionKey)) ?? defaultSessionKey
        await loadHistory(nextSessionKey, ensuredSession.token)
        await loadModels(ensuredSession.token, nextAgentId, nextSessionKey)
        setError(null)

        socketRef.current?.close()
        const socket = new LeleSocket(apiUrl, ensuredSession.token, {
          onConnecting: () => setWsStatus('connecting'),
          onOpen: () => {
            setWsStatus('connected')
            const openSessionKey = currentSessionKeyRef.current ?? nextSessionKey
            if (openSessionKey) {
              socket.send('subscribe', { session_key: openSessionKey })
            }
          },
          onClose: () => setWsStatus('disconnected'),
          onEvent: (event) => {
            switch (event.event) {
              case 'welcome':
                setAgents((current) => {
                  if (current.length > 0) {
                    return current
                  }
                  return event.data.agents
                })
                setCurrentAgentId(
                  (current) =>
                    current ??
                    event.data.agents.find((agent) => agent.default)?.id ??
                    event.data.agents[0]?.id ??
                    null,
                )
                if (!currentSessionKeyRef.current) {
                  persistCurrentSessionKey(event.data.session_key)
                }
                break
              case 'message.stream':
                if (
                  event.data.session_key &&
                  event.data.session_key !== currentSessionKeyRef.current
                ) {
                  break
                }
                ensureAssistantPlaceholder(
                  event.data.message_id,
                  event.data.session_key ?? currentSessionKeyRef.current ?? '',
                  event.data.chunk,
                )
                break
              case 'message.ack':
                ensureAssistantPlaceholder(event.data.message_id, event.data.session_key)
                break
              case 'message.complete':
                if (
                  event.data.session_key &&
                  event.data.session_key !== currentSessionKeyRef.current
                ) {
                  break
                }
                setMessages((current) =>
                  current.map((message) =>
                    message.id === event.data.message_id
                      ? {
                          ...message,
                          content: event.data.content,
                          attachments: event.data.attachments,
                          sessionKey:
                            event.data.session_key ??
                            currentSessionKeyRef.current ??
                            message.sessionKey,
                          streaming: false,
                        }
                      : message,
                  ),
                )
                setToolStatus(null)
                setPendingAttachments([])
                break
              case 'attachments':
                setMessages((current) => {
                  const lastAssistantIndex = [...current]
                    .reverse()
                    .findIndex((message) => message.role === 'assistant')

                  if (lastAssistantIndex < 0) {
                    return current
                  }

                  const targetIndex = current.length - lastAssistantIndex - 1
                  return current.map((message, index) =>
                    index === targetIndex
                      ? { ...message, attachments: event.data, streaming: false }
                      : message,
                  )
                })
                break
              case 'tool.executing':
                setToolStatus(event.data)
                const toolId = `tool-${event.data.tool}-${Date.now()}`
                setMessages((current) => [
                  ...current,
                  {
                    id: toolId,
                    role: 'tool',
                    content: '',
                    streaming: false,
                    createdAt: new Date().toISOString(),
                    sessionKey: currentSessionKeyRef.current ?? undefined,
                    toolName: event.data.tool,
                    toolArgs: event.data.action,
                    toolStatus: 'executing',
                  },
                ])
                break
              case 'tool.result':
                setToolStatus(null)
                setMessages((current) => {
                  const lastToolIndex = [...current]
                    .reverse()
                    .findIndex(
                      (message) => message.role === 'tool' && message.toolStatus === 'executing',
                    )
                  if (lastToolIndex < 0) {
                    return current
                  }
                  const targetIndex = current.length - lastToolIndex - 1
                  const isError =
                    event.data.result &&
                    (event.data.result.toLowerCase().includes('error') ||
                      event.data.result.toLowerCase().includes('failed'))
                  return current.map((message, index) =>
                    index === targetIndex
                      ? {
                          ...message,
                          toolResult: event.data.result,
                          toolStatus: isError ? 'error' : 'completed',
                        }
                      : message,
                  )
                })
                break
              case 'approval.request':
                setApprovalRequest(event.data)
                break
              case 'cancel.ack':
                setToolStatus(null)
                setMessages((current) =>
                  current.map((message) => ({ ...message, streaming: false })),
                )
                break
              case 'subscribe.ack':
                persistCurrentSessionKey(event.data.session_key)
                break
              case 'approve.ack':
              case 'unsubscribe.ack':
              case 'pong':
                break
              case 'error':
                setError(event.data.message)
                break
              default:
                break
            }
          },
        })

        socketRef.current = socket
        socket.connect()
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : t('errors.connectionFailed'))
        }
      }
    }

    void bootstrap()

    return () => {
      active = false
      socketRef.current?.close()
    }
  }, [api, apiUrl, session, t])

  useEffect(() => {
    if (!session?.token || !currentSessionKey) {
      return
    }

    if (lastLoadedSessionKeyRef.current === currentSessionKey) {
      return
    }

    void loadHistory(currentSessionKey, session.token).catch((err) => {
      setError(err instanceof Error ? err.message : t('errors.connectionFailed'))
    })
  }, [api, currentSessionKey, session, t])

  useEffect(() => {
    if (!session?.token || !currentAgentId) {
      setDiagnostics((current) => ({ ...current, agentInfo: null }))
      return
    }

    void api
      .agentInfo(currentAgentId, session.token)
      .then((agentInfo) => {
        setDiagnostics((current) => ({ ...current, agentInfo }))
      })
      .catch(() => {
        setDiagnostics((current) => ({ ...current, agentInfo: null }))
      })
  }, [api, currentAgentId, session])

  useEffect(() => {
    if (!session?.token || !currentAgentId) {
      setModelState({ current: '', available: [], groups: [] })
      return
    }

    void loadModels(session.token, currentAgentId, currentSessionKey).catch(() => {
      setModelState({ current: '', available: [], groups: [] })
    })
  }, [api, currentAgentId, currentSessionKey, messages.length, session])

  const handleAuth = async (input: { apiUrl: string; pin: string; deviceName: string }) => {
    try {
      setError(null)
      const nextApiUrl = input.apiUrl.trim()
      const nextApi = createApiClient(nextApiUrl)
      const sessionData = await nextApi.pair(input.pin, input.deviceName)
      const nextSession: AuthSession = {
        ...sessionData,
        device_name: input.deviceName.trim(),
      }
      const nextSessionKey = buildDefaultSessionKey(nextSession)
      setApiUrl(nextApiUrl)
      saveApiUrl(nextApiUrl)
      persistSession(nextSession)
      persistCurrentSessionKey(nextSessionKey)
      lastLoadedSessionKeyRef.current = null
      setMessages([])
      setPendingAttachments([])
      setModelState({ current: '', available: [], groups: [] })
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.authFailed'))
    }
  }

  const handleSend = async (content: string, attachments: string[]) => {
    if (!session?.token || !currentSessionKey) {
      return
    }

    const normalizedContent = content.trim()
    if (normalizedContent.length === 0) {
      setError(t('errors.messageRequired'))
      return
    }

    const userMessage: ChatMessage = {
      id: generateUUID(),
      role: 'user',
      content: normalizedContent,
      streaming: false,
      createdAt: new Date().toISOString(),
      sessionKey: currentSessionKey,
      attachments: attachments.map((path) => ({
        path,
        name: path.split('/').pop() ?? path,
        kind: 'file',
      })),
    }

    setMessages((current) => [...current, userMessage])
    setPendingAttachments([])
    setError(null)

    try {
      const response = await api.sendMessage(
        {
          content: normalizedContent,
          session_key: currentSessionKey,
          agent_id: currentAgentId ?? undefined,
          attachments: attachments.length > 0 ? attachments : undefined,
        },
        session.token,
      )

      persistCurrentSessionKey(response.session_key)
      socketRef.current?.send('subscribe', { session_key: response.session_key })
      ensureAssistantPlaceholder(response.message_id, response.session_key)
      await refreshSessions(session.token, response.session_key)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.sendFailed'))
    }
  }

  const handleApprove = async (approved: boolean) => {
    if (!approvalRequest) {
      return
    }

    socketRef.current?.send('approve', {
      request_id: approvalRequest.id,
      approved,
    })
    setApprovalRequest(null)
  }

  const handleClearSession = async () => {
    if (!session?.token || !currentSessionKey) {
      return
    }

    await api.clearSession(currentSessionKey, session.token)
    setMessages([])
    await refreshSessions(session.token, buildDefaultSessionKey(session))
  }

  const handleSelectSession = (sessionKey: string) => {
    persistCurrentSessionKey(sessionKey)
    setError(null)
    socketRef.current?.send('subscribe', { session_key: sessionKey })
  }

  const handleDeleteSession = async (sessionKey: string) => {
    if (!session?.token) {
      return
    }

    try {
      await api.deleteSession(sessionKey, session.token)
      setSessions((current) => current.filter((s) => s.key !== sessionKey))

      if (sessionKey === currentSessionKey) {
        const remainingSessions = sessions.filter((s) => s.key !== sessionKey)
        const nextSessionKey = remainingSessions.length > 0 ? remainingSessions[0].key : null
        persistCurrentSessionKey(nextSessionKey)
        lastLoadedSessionKeyRef.current = null
        setMessages([])

        if (nextSessionKey) {
          socketRef.current?.send('subscribe', { session_key: nextSessionKey })
        }
      }

      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.deleteSessionFailed'))
    }
  }

  const handleSelectAgent = (agentId: string) => {
    if (messages.length > 0) {
      return
    }
    setCurrentAgentId(agentId)
    if (session?.token) {
      void loadModels(session.token, agentId, currentSessionKey).catch(() => {
        setModelState({ current: '', available: [], groups: [] })
      })
    }
  }

  const handleSelectModel = async (model: string) => {
    if (!session?.token || !currentSessionKey) {
      return
    }

    try {
      const result = await api.updateSessionModel(currentSessionKey, model, session.token)
      setModelState({
        current: result.model,
        available: result.models,
        groups: result.model_groups ?? [],
      })
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : t('errors.connectionFailed'))
    }
  }

  const handleCreateSession = () => {
    if (!session?.client_id) {
      return
    }

    const sessionKey = `native:${session.client_id}:${Date.now()}`
    setSessions((current) => [
      {
        key: sessionKey,
        created: new Date().toISOString(),
        updated: new Date().toISOString(),
        message_count: 0,
      },
      ...current.filter((sessionItem) => sessionItem.key !== sessionKey),
    ])
    persistCurrentSessionKey(sessionKey)
    lastLoadedSessionKeyRef.current = null
    setMessages([])
    if (session?.token && currentAgentId) {
      void loadModels(session.token, currentAgentId, sessionKey).catch(() => {
        setModelState({ current: '', available: [], groups: [] })
      })
    }
    socketRef.current?.send('subscribe', { session_key: sessionKey })
  }

  const handleCancel = () => {
    socketRef.current?.send('cancel', {})
  }

  const handleToggleDiagnostics = () => {
    setDiagnosticsOpen((current) => !current)
  }

  const handleAttachmentsChange = (attachments: string[]) => {
    setPendingAttachments(attachments)
  }

  if (!session?.token) {
    return <AuthView apiUrl={apiUrl} error={error} onSubmit={handleAuth} />
  }

  return (
    <ChatView
      apiUrl={apiUrl}
      approvalRequest={approvalRequest}
      auth={session}
      agents={agents}
      currentAgentId={currentAgentId}
      currentSessionKey={currentSessionKey}
      diagnostics={diagnostics}
      diagnosticsOpen={diagnosticsOpen}
      error={error}
      modelState={modelState}
      messages={messages}
      onApprove={(approved) => void handleApprove(approved)}
      onAttachmentsChange={handleAttachmentsChange}
      onCancel={handleCancel}
      onClearSession={() => void handleClearSession()}
      onCreateSession={handleCreateSession}
      onLogout={handleLogout}
      onSelectAgent={handleSelectAgent}
      onSelectModel={(model) => void handleSelectModel(model)}
      onSelectSession={handleSelectSession}
      onDeleteSession={(sessionKey) => void handleDeleteSession(sessionKey)}
      onSend={(content, attachments) => void handleSend(content, attachments)}
      onToggleDiagnostics={handleToggleDiagnostics}
      pendingAttachments={pendingAttachments}
      sessions={sessions}
      toolStatus={toolStatus}
      wsStatus={wsStatus}
    />
  )
}
