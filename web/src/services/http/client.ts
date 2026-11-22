import type {
  MCPAuthStart,
  MCPAuthStatus,
  MCPInventoryResponse,
  MCPLayerName,
  MCPQueryLayer,
  MCPRawFileResponse,
  MCPToggleResponse,
  MCPValidateResponse,
} from '../../lib/mcpTypes'
import { loadSession } from '../../lib/storage'
import type {
  AgentCatalogResponse,
  AgentCommandDeleteResponse,
  AgentCommandDetail,
  AgentCommandMutationResponse,
  AgentCommandUpdateRequest,
  AgentCommandWriteRequest,
  AgentCommandsResponse,
  AgentDetails,
  AgentFilesResponse,
  AgentSkillMutationResponse,
  AgentStatusResponse,
  AgentsResponse,
  ApproveResponse,
  AuthPINResponse,
  AuthPairResponse,
  AuthRefreshResponse,
  AuthSession,
  AuthStatusResponse,
  AvailableSkillsResponse,
  BackgroundExecOutputResponse,
  BackgroundExecStopResponse,
  BackgroundExecsResponse,
  CatalogModelsResponse,
  CatalogPrefetchResponse,
  CatalogProvidersResponse,
  ChannelsResponse,
  ChatCommandsResponse,
  ChatSessionsResponse,
  ClientEvent,
  ConfigResponse,
  ConfigUpdateResponse,
  ConfigValidateResponse,
  CreateSessionResponse,
  CronJob,
  CronJobInput,
  CronJobResponse,
  CronJobsResponse,
  EditableConfig,
  FileUploadResponse,
  FsListResponse,
  GroupsConfig,
  HistoryResponse,
  LogsDatesResponse,
  LogsResponse,
  ModelsResponse,
  ProviderModelsResponse,
  SafeClientInfo,
  ScanSkillsResponse,
  SecretDetailResponse,
  SecretInput,
  SecretMeta,
  SecretsAuditResponse,
  SecretsListResponse,
  SendMessageRequest,
  SendMessageResponse,
  SessionAgentResponse,
  SessionContextResponse,
  SessionFolderResponse,
  SessionModelResponse,
  SessionNameResponse,
  SessionSubagentsResponse,
  SessionThinkingResponse,
  SkillInstallBatchResponse,
  SkillInstallResponse,
  SkillInstallScope,
  SkillRemoveResponse,
  SkillsResponse,
  StreamMessageState,
  StreamStatusResponse,
  SystemStatus,
  SystemVersionInfo,
  ToolsResponse,
  UpdateCheckInfo,
  UpdateState,
} from '../../lib/types'
import { endpoints } from './endpoints'
import { ApiError, parseApiError } from './errors'

export function isFatalRefreshFailure(status: number, code?: string): boolean {
  if (status !== 400 && status !== 401) return false
  return code === 'refresh_error'
}

const ADOPTION_RETRY_DELAY_MS = 400

export function refreshExpires(expires: unknown): string {
  if (typeof expires === 'string' && Number.isFinite(Date.parse(expires))) {
    return expires
  }
  return new Date(Date.now() + 3600000).toISOString()
}

const joinUrl = (baseUrl: string, path: string) => `${baseUrl.replace(/\/$/, '')}${path}`

const isJsonBody = (body: BodyInit | null | undefined) => body !== null && body !== undefined

const DEFAULT_MAX_RETRIES = 1
const DEFAULT_RETRY_DELAY = 1000
const DEFAULT_REQUEST_TIMEOUT_MS = 30_000

type TokenState = {
  token: string | null
  refreshToken: string | null
  clientId: string
  onTokenRefresh?: (session: AuthSession) => void
  onAuthFailure?: (mayClearStorage: boolean) => void
}

export type ApiClientCredentials = {
  token: string
  refreshToken?: string
  clientId?: string
}

type SendMessageStreamOptions = {
  signal?: AbortSignal
  onDone?: () => void
}

const parseSSEBlock = (block: string): ClientEvent | null => {
  let eventName = ''
  const dataLines: string[] = []

  for (const rawLine of block.split(/\r?\n/)) {
    const line = rawLine.trimEnd()
    if (line.startsWith('event:')) {
      eventName = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trimStart())
    }
  }

  if (!eventName || dataLines.length === 0) {
    return null
  }

  try {
    return {
      event: eventName,
      data: JSON.parse(dataLines.join('\n')),
    } as ClientEvent
  } catch {
    return {
      event: eventName,
      data: { raw: dataLines.join('\n') },
    } as unknown as ClientEvent
  }
}

export const createApiClient = (baseUrl: string, initial?: ApiClientCredentials) => {
  const tokenState: TokenState = {
    token: initial?.token ?? null,
    refreshToken: initial?.refreshToken || null,
    clientId: initial?.clientId ?? '',
    onTokenRefresh: undefined,
  }

  let refreshInFlight: Promise<string | null> | null = null
  let refreshBlockedUntil = 0
  const REFRESH_COOLDOWN_MS = 15_000

  const setToken = (
    token: string,
    refreshToken: string,
    onRefresh?: (session: AuthSession) => void,
    clientId?: string,
  ) => {
    tokenState.token = token
    tokenState.refreshToken = refreshToken
    tokenState.onTokenRefresh = onRefresh
    tokenState.clientId = clientId ?? ''
    refreshBlockedUntil = 0
  }

  const clearToken = () => {
    tokenState.token = null
    tokenState.refreshToken = null
    tokenState.clientId = ''
    tokenState.onTokenRefresh = undefined
  }

  const getToken = () => tokenState.token

  const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

  const blockRefreshFor = (ms: number) => {
    refreshBlockedUntil = Date.now() + Math.max(0, Math.min(ms, 60_000))
  }

  const retryAfterMs = (response: Response): number | null => {
    const raw = response.headers.get('retry-after')
    if (raw === null || raw.trim() === '') return null
    const seconds = Number(raw)
    if (!Number.isFinite(seconds) || seconds <= 0) return null
    return seconds * 1000
  }

  const adoptRotatedSession = (staleRefreshToken: string): string | null => {
    const stored = loadSession()
    if (!stored?.token || !stored.refresh_token) return null
    if (stored.refresh_token === staleRefreshToken) return null
    if (tokenState.clientId && stored.client_id && tokenState.clientId !== stored.client_id) {
      return null
    }
    tokenState.token = stored.token
    tokenState.refreshToken = stored.refresh_token
    tokenState.onTokenRefresh?.(stored)
    return stored.token
  }

  const runRefresh = async (): Promise<string | null> => {
    const attempted = tokenState.refreshToken
    if (!attempted) return null

    let response: Response
    try {
      response = await fetch(joinUrl(baseUrl, endpoints.auth.refresh), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: attempted }),
      })
    } catch {
      blockRefreshFor(REFRESH_COOLDOWN_MS)
      return null
    }

    if (response.ok) {
      const data = (await response.json()) as AuthRefreshResponse
      tokenState.token = data.token
      tokenState.refreshToken = data.refresh_token
      refreshBlockedUntil = 0

      if (tokenState.onTokenRefresh) {
        const session: AuthSession = {
          client_id: '',
          device_name: '',
          token: data.token,
          refresh_token: data.refresh_token,
          expires: refreshExpires(data.expires),
        }
        tokenState.onTokenRefresh(session)
      }

      return data.token
    }

    const failure = await parseApiError(response)

    if (!isFatalRefreshFailure(failure.status, failure.code)) {
      blockRefreshFor(retryAfterMs(response) ?? REFRESH_COOLDOWN_MS)
      return null
    }

    const adopted = adoptRotatedSession(attempted)
    if (adopted) return adopted

    await sleep(ADOPTION_RETRY_DELAY_MS)
    const adoptedLate = adoptRotatedSession(attempted)
    if (adoptedLate) return adoptedLate

    const stored = loadSession()
    const storedIsOurs =
      !stored ||
      stored.refresh_token === attempted ||
      stored.token === tokenState.token ||
      (!!tokenState.clientId && stored.client_id === tokenState.clientId)

    const onFail = tokenState.onAuthFailure
    clearToken()
    onFail?.(storedIsOurs)
    return null
  }

  const refreshToken = (): Promise<string | null> => {
    const current = tokenState.refreshToken
    if (current) {
      const adopted = adoptRotatedSession(current)
      if (adopted) return Promise.resolve(adopted)
    }

    if (Date.now() < refreshBlockedUntil) return Promise.resolve(null)
    if (!refreshInFlight) {
      refreshInFlight = runRefresh().finally(() => {
        refreshInFlight = null
      })
    }
    return refreshInFlight
  }

  const requestWithRetry = async <T>(
    path: string,
    init: RequestInit = {},
    maxRetries: number = DEFAULT_MAX_RETRIES,
  ): Promise<T> => {
    let lastError: Error | null = null
    let retryCount = 0
    let refreshTried = false
    let supersededReplayUsed = false

    while (retryCount <= maxRetries) {
      const attemptToken = tokenState.token
      try {
        const headers: Record<string, string> = {
          ...((init.headers as Record<string, string>) ?? {}),
        }

        if (tokenState.token) {
          headers.Authorization = `Bearer ${tokenState.token}`
        }

        if (isJsonBody(init.body) && !(init.body instanceof FormData)) {
          headers['Content-Type'] = 'application/json'
        }

        const existingSignal = init.signal
        const timeoutController = new AbortController()
        const timeoutId = setTimeout(() => timeoutController.abort(), DEFAULT_REQUEST_TIMEOUT_MS)

        let combinedSignal: AbortSignal
        if (existingSignal) {
          combinedSignal = AbortSignal.any([existingSignal, timeoutController.signal])
        } else {
          combinedSignal = timeoutController.signal
        }

        let response: Response
        try {
          response = await fetch(joinUrl(baseUrl, path), {
            ...init,
            headers,
            signal: combinedSignal,
          })
        } finally {
          clearTimeout(timeoutId)
        }

        if (
          response.status === 401 &&
          !supersededReplayUsed &&
          tokenState.token !== null &&
          tokenState.token !== attemptToken
        ) {
          supersededReplayUsed = true
          continue
        }

        if (response.status === 401 && tokenState.refreshToken && retryCount === 0) {
          refreshTried = true
          const newToken = await refreshToken()
          if (newToken) {
            retryCount++
            continue
          }
        }

        if (!response.ok) {
          throw await parseApiError(response)
        }

        if (response.status === 204) {
          return undefined as T
        }

        const contentType = response.headers.get('content-type') ?? ''
        if (!contentType.includes('application/json')) {
          return undefined as T
        }

        return (await response.json()) as T
      } catch (error) {
        lastError = error as Error

        if (error instanceof ApiError) {
          if (error.status >= 400 && error.status < 500 && error.status !== 401) {
            throw error
          }
          if (error.status === 401 && (retryCount > 0 || refreshTried)) {
            throw error
          }
        }

        if (retryCount < maxRetries) {
          retryCount++
          await sleep(DEFAULT_RETRY_DELAY * retryCount)
          continue
        }

        throw lastError
      }
    }

    throw lastError ?? new Error('Unknown error')
  }

  const request = async <T>(path: string, init: RequestInit = {}) => {
    return requestWithRetry<T>(path, init)
  }

  const sendMessageStream = async (
    payload: SendMessageRequest,
    onEvent: (event: ClientEvent) => void,
    options: SendMessageStreamOptions = {},
  ): Promise<SendMessageResponse> => {
    const startRequest = (token: string | null) => {
      const headers: Record<string, string> = {
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
      }
      if (token) {
        headers.Authorization = `Bearer ${token}`
      }
      return fetch(joinUrl(baseUrl, endpoints.chat.sendStream), {
        method: 'POST',
        headers,
        body: JSON.stringify({
          ...payload,
          session_key: payload.session_key || undefined,
        }),
        signal: options.signal,
      })
    }

    let response = await startRequest(tokenState.token)
    if (response.status === 401 && tokenState.refreshToken) {
      const newToken = await refreshToken()
      if (newToken) {
        response = await startRequest(newToken)
      }
    }

    if (!response.ok) {
      throw await parseApiError(response)
    }

    if (!response.body) {
      throw new Error('Streaming response body is unavailable')
    }

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    let ackResolved = false
    let resolveAck!: (response: SendMessageResponse) => void
    let rejectAck!: (error: Error) => void
    const ackPromise = new Promise<SendMessageResponse>((resolve, reject) => {
      resolveAck = resolve
      rejectAck = reject
    })

    const handleBlock = (block: string) => {
      const event = parseSSEBlock(block)
      if (!event) return

      onEvent(event)
      if (event.event === 'message.ack' && !ackResolved) {
        ackResolved = true
        resolveAck(event.data)
      }
    }

    const pump = async () => {
      try {
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          let separatorIndex = buffer.search(/\r?\n\r?\n/)
          while (separatorIndex >= 0) {
            const block = buffer.slice(0, separatorIndex)
            buffer = buffer.slice(separatorIndex + (buffer[separatorIndex] === '\r' ? 4 : 2))
            handleBlock(block)
            separatorIndex = buffer.search(/\r?\n\r?\n/)
          }
        }

        buffer += decoder.decode()
        if (buffer.trim()) {
          handleBlock(buffer)
        }

        if (!ackResolved) {
          rejectAck(new Error('Streaming response ended before message acknowledgement'))
        }
      } catch (error) {
        if (!ackResolved) {
          rejectAck(error as Error)
        } else {
          onEvent({
            event: 'stream.error',
            data: { error: (error as Error).message || 'Stream connection lost' },
          })
        }
      } finally {
        options.onDone?.()
      }
    }

    void pump()
    return ackPromise
  }

  return {
    setToken,
    clearToken,
    getToken,
    refreshNow: (): Promise<string | null> => refreshToken(),
    setAuthFailureHandler: (handler: ((mayClearStorage: boolean) => void) | undefined) => {
      tokenState.onAuthFailure = handler
    },
    logout: async () => {
      if (tokenState.token) {
        try {
          await fetch(joinUrl(baseUrl, endpoints.auth.logout), {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              Authorization: `Bearer ${tokenState.token}`,
            },
          })
        } catch {}
      }
      clearToken()
    },
    pair: (pin: string, device_name: string) =>
      request<AuthPairResponse>(endpoints.auth.pair, {
        method: 'POST',
        body: JSON.stringify({ pin, device_name }),
      }),
    getPIN: (deviceName: string) =>
      request<AuthPINResponse>(
        `${endpoints.auth.pin}?device_name=${encodeURIComponent(deviceName)}`,
        { method: 'GET' },
      ),
    refresh: (refresh_token: string) =>
      request<AuthRefreshResponse>(endpoints.auth.refresh, {
        method: 'POST',
        body: JSON.stringify({ refresh_token }),
      }),
    status: (token?: string) => {
      if (token) {
        tokenState.token = token
      }
      return request<AuthStatusResponse>(endpoints.auth.status, { method: 'GET' })
    },
    listClients: () => request<SafeClientInfo[]>(endpoints.auth.listClients, { method: 'GET' }),
    removeClient: (clientId: string) =>
      request<void>(endpoints.auth.removeClient(clientId), { method: 'DELETE' }),
    agents: () => request<AgentsResponse>(endpoints.agents.list, { method: 'GET' }),
    agentInfo: async (agentId: string) => {
      const [info, status] = await Promise.all([
        request<AgentDetails>(endpoints.agents.info(agentId), { method: 'GET' }),
        request<AgentStatusResponse>(endpoints.agents.status(agentId), { method: 'GET' }),
      ])

      return {
        ...info,
        status: status.status,
        active_sessions: status.active_sessions,
      }
    },
    agentFiles: (agentId: string) =>
      request<AgentFilesResponse>(endpoints.agents.files(agentId), { method: 'GET' }),
    getAgentCatalog: (agentId: string): Promise<AgentCatalogResponse> =>
      request<AgentCatalogResponse>(endpoints.agents.catalog(agentId), { method: 'GET' }),
    agentToggleSkill: (agentId: string, name: string, enabled: boolean) =>
      request<AgentSkillMutationResponse>(endpoints.agents.skills.toggle(agentId, name), {
        method: 'PUT',
        body: JSON.stringify({ enabled }),
      }),
    agentRemoveSkill: (agentId: string, name: string) =>
      request<AgentSkillMutationResponse>(endpoints.agents.skills.remove(agentId, name), {
        method: 'DELETE',
      }),
    agentInstallSkill: (agentId: string, url: string, scope?: SkillInstallScope) =>
      request<SkillInstallResponse>(endpoints.agents.skills.install(agentId), {
        method: 'POST',
        body: JSON.stringify({ url, scope }),
      }),
    agentInstallSkillsBatch: (
      agentId: string,
      repo: string,
      skills: string[],
      scope?: SkillInstallScope,
    ) =>
      request<SkillInstallBatchResponse>(endpoints.agents.skills.installBatch(agentId), {
        method: 'POST',
        body: JSON.stringify({ repo, skills, scope }),
      }),
    agentFile: (agentId: string, fileName: string) =>
      request<AgentFilesResponse>(endpoints.agents.files(agentId, fileName), { method: 'GET' }),
    agentFileSave: (agentId: string, fileName: string, content: string) =>
      request<AgentFilesResponse>(endpoints.agents.files(agentId, fileName), {
        method: 'PUT',
        body: JSON.stringify({ content }),
      }),
    agentCommands: (agentId: string) =>
      request<AgentCommandsResponse>(endpoints.agents.commands.list(agentId), { method: 'GET' }),
    agentCommand: (agentId: string, name: string) =>
      request<AgentCommandDetail>(endpoints.agents.commands.one(agentId, name), { method: 'GET' }),
    agentCommandCreate: (agentId: string, body: AgentCommandWriteRequest) =>
      request<AgentCommandMutationResponse>(endpoints.agents.commands.create(agentId), {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    agentCommandUpdate: (agentId: string, name: string, body: AgentCommandUpdateRequest) =>
      request<AgentCommandMutationResponse>(endpoints.agents.commands.update(agentId, name), {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    agentCommandRemove: (agentId: string, name: string) =>
      request<AgentCommandDeleteResponse>(endpoints.agents.commands.remove(agentId, name), {
        method: 'DELETE',
      }),
    mcpInventory: (agentId: string) =>
      request<MCPInventoryResponse>(endpoints.agents.mcp.inventory(agentId), { method: 'GET' }),
    mcpRaw: (agentId: string, layer: MCPLayerName) =>
      request<MCPRawFileResponse>(endpoints.agents.mcp.raw(agentId, layer), { method: 'GET' }),
    mcpPutRaw: (agentId: string, layer: MCPLayerName, body: { content: string }) =>
      request<MCPRawFileResponse>(endpoints.agents.mcp.putRaw(agentId, layer), {
        method: 'PUT',
        body: JSON.stringify(body),
      }),
    mcpToggle: (
      agentId: string,
      layer: MCPQueryLayer,
      name: string,
      enabled: boolean,
      force?: boolean,
    ) =>
      request<MCPToggleResponse>(endpoints.agents.mcp.toggle(agentId, layer, name, force), {
        method: 'PUT',
        body: JSON.stringify({ enabled }),
      }),
    mcpValidate: (agentId: string, body: { layer: MCPLayerName; content: string }) =>
      request<MCPValidateResponse>(endpoints.agents.mcp.validate(agentId), {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    mcpAuthStart: (agentId: string, name: string) =>
      request<MCPAuthStart>(endpoints.agents.mcp.auth.start(agentId, name), { method: 'POST' }),
    mcpAuthStatus: (agentId: string, flowId: string) =>
      request<MCPAuthStatus>(endpoints.agents.mcp.auth.status(agentId, flowId), { method: 'GET' }),
    mcpAuthComplete: (agentId: string, flowId: string, body: { code: string; state?: string }) =>
      request<MCPAuthStatus>(endpoints.agents.mcp.auth.complete(agentId, flowId), {
        method: 'POST',
        body: JSON.stringify(body),
      }),
    mcpAuthCancel: (agentId: string, flowId: string) =>
      request<void>(endpoints.agents.mcp.auth.cancel(agentId, flowId), { method: 'DELETE' }),
    mcpLogout: (agentId: string, name: string) =>
      request<void>(endpoints.agents.mcp.auth.logout(agentId, name), { method: 'DELETE' }),
    history: (sessionKey: string, parentSessionKey?: string, beforeId?: string, limit?: number) => {
      const params = new URLSearchParams()
      if (beforeId !== undefined && beforeId !== null) params.set('before_id', beforeId)
      if (limit !== undefined) params.set('limit', String(limit))
      const query = params.toString()
      const baseEndpoint = endpoints.chat.history(sessionKey, parentSessionKey)
      return request<HistoryResponse>(query ? `${baseEndpoint}?${query}` : baseEndpoint, {
        method: 'GET',
      })
    },
    sessions: (
      mode?: string,
      kind?: string,
      includeSystem?: boolean,
      options?: { offset?: number; limit?: number },
    ) => {
      const params = new URLSearchParams()
      if (mode) params.set('mode', mode)
      if (kind) params.set('kind', kind)
      if (includeSystem) params.set('include_system', 'true')
      if (options?.offset !== undefined && options?.offset !== null) {
        params.set('offset', String(options.offset))
      }
      if (options?.limit !== undefined && options?.limit !== null) {
        params.set('limit', String(options.limit))
      }
      const query = params.toString()
      return request<ChatSessionsResponse>(
        query ? `${endpoints.chat.sessions}?${query}` : endpoints.chat.sessions,
        {
          method: 'GET',
        },
      )
    },
    sessionsMeta: (
      mode?: string,
      kind?: string,
      includeSystem?: boolean,
      options?: { offset?: number; limit?: number },
    ) => {
      const params = new URLSearchParams()
      if (mode) params.set('mode', mode)
      if (kind) params.set('kind', kind)
      if (includeSystem) params.set('include_system', 'true')
      if (options?.offset !== undefined && options?.offset !== null) {
        params.set('offset', String(options.offset))
      }
      if (options?.limit !== undefined && options?.limit !== null) {
        params.set('limit', String(options.limit))
      }
      const query = params.toString()
      return request<ChatSessionsResponse>(
        query ? `${endpoints.chat.sessionsMeta}?${query}` : endpoints.chat.sessionsMeta,
        {
          method: 'GET',
        },
      )
    },
    createSession: (sessionKey: string, mode?: string) =>
      request<CreateSessionResponse>(endpoints.chat.sessions, {
        method: 'POST',
        body: JSON.stringify({ session_key: sessionKey, mode }),
      }),
    models: (agentId: string, sessionKey: string | null) => {
      const params = new URLSearchParams()
      if (agentId) params.set('agent_id', agentId)
      if (sessionKey) params.set('session_key', sessionKey)
      const query = params.toString()
      return request<ModelsResponse>(`${endpoints.system.models}${query ? `?${query}` : ''}`, {
        method: 'GET',
      })
    },
    sessionModel: (sessionKey: string) =>
      request<SessionModelResponse>(endpoints.chat.session(sessionKey, 'model'), {
        method: 'GET',
      }),
    updateSessionModel: (sessionKey: string, model: string) =>
      request<SessionModelResponse>(endpoints.chat.session(sessionKey, 'model'), {
        method: 'PATCH',
        body: JSON.stringify({ model }),
      }),
    updateSessionName: (sessionKey: string, name: string) =>
      request<SessionNameResponse>(endpoints.chat.session(sessionKey, 'name'), {
        method: 'PATCH',
        body: JSON.stringify({ name }),
      }),
    sessionAgent: (sessionKey: string) =>
      request<SessionAgentResponse>(endpoints.chat.session(sessionKey, 'agent'), {
        method: 'GET',
      }),
    updateSessionAgent: (sessionKey: string, agentId: string) =>
      request<SessionAgentResponse>(endpoints.chat.session(sessionKey, 'agent'), {
        method: 'PATCH',
        body: JSON.stringify({ agent_id: agentId }),
      }),
    sessionThinking: (sessionKey: string) =>
      request<SessionThinkingResponse>(endpoints.chat.session(sessionKey, 'thinking'), {
        method: 'GET',
      }),
    updateSessionThinking: (sessionKey: string, level: string) =>
      request<SessionThinkingResponse>(endpoints.chat.session(sessionKey, 'thinking'), {
        method: 'PATCH',
        body: JSON.stringify({ level }),
      }),
    sessionFolder: (sessionKey: string) =>
      request<SessionFolderResponse>(endpoints.chat.session(sessionKey, 'folder'), {
        method: 'GET',
      }),
    updateSessionFolder: (sessionKey: string, folder: string) =>
      request<SessionFolderResponse>(endpoints.chat.session(sessionKey, 'folder'), {
        method: 'PATCH',
        body: JSON.stringify({ folder }),
      }),
    fsList: (path: string) => {
      const params = new URLSearchParams()
      if (path) params.set('path', path)
      const query = params.toString()
      return request<FsListResponse>(`${endpoints.fs.list}${query ? `?${query}` : ''}`, {
        method: 'GET',
      })
    },
    sessionContext: (sessionKey: string) =>
      request<SessionContextResponse>(endpoints.chat.session(sessionKey, 'context'), {
        method: 'GET',
      }),
    chatCommands: (agentId?: string) =>
      request<ChatCommandsResponse>(endpoints.chat.commands(agentId), { method: 'GET' }),
    sessionSubagents: (sessionKey: string) =>
      request<SessionSubagentsResponse>(endpoints.chat.session(sessionKey, 'subagents'), {
        method: 'GET',
      }),
    sendMessage: (payload: SendMessageRequest) =>
      request<SendMessageResponse>(endpoints.chat.send, {
        method: 'POST',
        body: JSON.stringify({
          ...payload,
          session_key: payload.session_key || undefined,
        }),
      }),
    sendMessageStream,
    approve: (sessionKey: string, requestId: string, approved: boolean) =>
      request<ApproveResponse>(endpoints.chat.approve(sessionKey), {
        method: 'POST',
        body: JSON.stringify({ request_id: requestId, approved }),
      }),
    clearSession: async (sessionKey: string) => {
      await request<unknown>(endpoints.chat.clear(sessionKey), { method: 'POST' })
    },
    deleteSession: async (sessionKey: string) => {
      await request<unknown>(endpoints.chat.session(sessionKey), { method: 'DELETE' })
    },
    config: () => request<ConfigResponse>(endpoints.system.config, { method: 'GET' }),
    getConfig: async (): Promise<{ groups?: GroupsConfig } & Record<string, unknown>> => {
      const resp = await request<ConfigResponse>(endpoints.system.config, { method: 'GET' })
      return resp.config as { groups?: GroupsConfig } & Record<string, unknown>
    },
    saveConfig: (config: EditableConfig) =>
      request<ConfigUpdateResponse>(endpoints.system.config, {
        method: 'PUT',
        body: JSON.stringify({ config }),
      }),
    validateConfig: (config: EditableConfig) =>
      request<ConfigValidateResponse>(endpoints.system.configValidate, {
        method: 'POST',
        body: JSON.stringify({ config }),
      }),
    tools: () => request<ToolsResponse>(endpoints.system.tools, { method: 'GET' }),
    channels: () => request<ChannelsResponse>(endpoints.system.channels, { method: 'GET' }),
    systemStatus: () => request<SystemStatus>(endpoints.system.status, { method: 'GET' }),
    systemVersion: () => request<SystemVersionInfo>(endpoints.system.version, { method: 'GET' }),
    updatesCheck: () => request<UpdateCheckInfo>(endpoints.system.updatesCheck, { method: 'GET' }),
    updatesApply: (version?: string, restart?: boolean) =>
      request<{ status: string }>(endpoints.system.updatesApply, {
        method: 'POST',
        body: JSON.stringify({ version, restart }),
      }),
    updatesStatus: () => request<UpdateState>(endpoints.system.updatesStatus, { method: 'GET' }),
    updatesRollback: () =>
      request<{ restored: string }>(endpoints.system.updatesRollback, { method: 'POST' }),
    systemRestart: () => request<{ status: string }>(endpoints.system.restart, { method: 'POST' }),
    skills: () => request<SkillsResponse>(endpoints.skills.list, { method: 'GET' }),
    availableSkills: () =>
      request<AvailableSkillsResponse>(endpoints.skills.available, { method: 'GET' }),
    installSkill: (url: string, scope?: SkillInstallScope) =>
      request<SkillInstallResponse>(endpoints.skills.install, {
        method: 'POST',
        body: JSON.stringify({ url, scope }),
      }),
    removeSkill: (name: string) =>
      request<SkillRemoveResponse>(endpoints.skills.remove(name), { method: 'DELETE' }),
    scanSkills: (repo: string) =>
      request<ScanSkillsResponse>(endpoints.skills.scan, {
        method: 'POST',
        body: JSON.stringify({ repo }),
      }),
    installSkillsBatch: (repo: string, skills: string[], scope?: SkillInstallScope) =>
      request<SkillInstallBatchResponse>(endpoints.skills.installBatch, {
        method: 'POST',
        body: JSON.stringify({ repo, skills, scope }),
      }),
    toggleSkill: (name: string, enabled: boolean) =>
      request<{ name: string; enabled: boolean; message: string }>(endpoints.skills.toggle(name), {
        method: 'PUT',
        body: JSON.stringify({ enabled }),
      }),
    streamStatus: (sessionKey: string) =>
      request<StreamStatusResponse>(endpoints.chat.streams(sessionKey), { method: 'GET' }),
    streamState: (sessionKey: string, messageID: string) =>
      request<StreamMessageState>(endpoints.chat.streamState(sessionKey, messageID), {
        method: 'GET',
      }),
    providerModels: (providerName: string) =>
      request<ProviderModelsResponse>(endpoints.providers.models(providerName), {
        method: 'GET',
      }),
    catalogModels: (provider?: string) =>
      request<CatalogModelsResponse>(endpoints.catalog.models(provider), { method: 'GET' }),
    catalogProviders: () =>
      request<CatalogProvidersResponse>(endpoints.catalog.providers, { method: 'GET' }),
    catalogPrefetch: () =>
      request<CatalogPrefetchResponse>(endpoints.catalog.prefetch, { method: 'POST' }),
    backgroundExecs: {
      list: (includeCompleted?: boolean) =>
        request<BackgroundExecsResponse>(
          `${endpoints.backgroundExec.list}${includeCompleted ? '?include_completed=true' : ''}`,
          { method: 'GET' },
        ),
      output: (id: string, tail?: number) =>
        request<BackgroundExecOutputResponse>(
          `${endpoints.backgroundExec.output(id)}${tail ? `?tail=${tail}` : ''}`,
          { method: 'GET' },
        ),
      stop: (id: string) =>
        request<BackgroundExecStopResponse>(endpoints.backgroundExec.stop(id), {
          method: 'POST',
        }),
    },
    cron: {
      list: (includeDisabled = true) =>
        request<CronJobsResponse>(
          `${endpoints.cron.list}${includeDisabled ? '?include_disabled=true' : ''}`,
          { method: 'GET' },
        ),
      get: (id: string) => request<CronJobResponse>(endpoints.cron.get(id), { method: 'GET' }),
      create: (input: CronJobInput) =>
        request<CronJobResponse>(endpoints.cron.create, {
          method: 'POST',
          body: JSON.stringify(input),
        }),
      update: (id: string, input: CronJobInput) =>
        request<CronJobResponse>(endpoints.cron.update(id), {
          method: 'PUT',
          body: JSON.stringify(input),
        }),
      remove: (id: string) =>
        request<{ id: string; removed: boolean }>(endpoints.cron.remove(id), {
          method: 'DELETE',
        }),
      enable: (id: string) =>
        request<CronJobResponse>(endpoints.cron.enable(id), { method: 'POST' }),
      disable: (id: string) =>
        request<CronJobResponse>(endpoints.cron.disable(id), { method: 'POST' }),
      run: (id: string) =>
        request<{ id: string; ran: boolean; job: CronJob }>(endpoints.cron.run(id), {
          method: 'POST',
        }),
    },
    secrets: {
      list: () => request<SecretsListResponse>(endpoints.secrets.list, { method: 'GET' }),
      get: (name: string) =>
        request<SecretDetailResponse>(endpoints.secrets.get(name), { method: 'GET' }),
      create: (input: SecretInput) =>
        request<{ secret: SecretMeta }>(endpoints.secrets.create, {
          method: 'POST',
          body: JSON.stringify(input),
        }),
      update: (name: string, input: Omit<SecretInput, 'name'>) =>
        request<{ secret: SecretMeta }>(endpoints.secrets.update(name), {
          method: 'PUT',
          body: JSON.stringify(input),
        }),
      remove: (name: string) =>
        request<{ deleted: string }>(endpoints.secrets.remove(name), { method: 'DELETE' }),
      status: () =>
        request<SecretsListResponse['status']>(endpoints.secrets.status, { method: 'GET' }),
      audit: () => request<SecretsAuditResponse>(endpoints.secrets.audit, { method: 'GET' }),
    },
    logs: {
      list: (params?: { level?: string; date?: string; lines?: number }) => {
        const query = new URLSearchParams()
        if (params?.level) query.set('level', params.level)
        if (params?.date) query.set('date', params.date)
        if (params?.lines) query.set('lines', String(params.lines))
        const qs = query.toString()
        return request<LogsResponse>(`${endpoints.logs.list}${qs ? `?${qs}` : ''}`, {
          method: 'GET',
        })
      },
      dates: () => request<LogsDatesResponse>(endpoints.logs.dates, { method: 'GET' }),
    },
    fileBlob: async (path: string, signal?: AbortSignal): Promise<Blob> => {
      const url = `${joinUrl(baseUrl, endpoints.files.viewSecure)}?path=${encodeURIComponent(path)}`

      const headers: Record<string, string> = {}
      if (tokenState.token) {
        headers.Authorization = `Bearer ${tokenState.token}`
      }

      const timeoutController = new AbortController()
      const timeoutId = setTimeout(() => timeoutController.abort(), DEFAULT_REQUEST_TIMEOUT_MS)

      let combinedSignal: AbortSignal
      if (signal) {
        combinedSignal = AbortSignal.any([signal, timeoutController.signal])
      } else {
        combinedSignal = timeoutController.signal
      }

      let response: Response
      try {
        response = await fetch(url, { method: 'GET', headers, signal: combinedSignal })
      } finally {
        clearTimeout(timeoutId)
      }

      if (response.status === 401 && tokenState.refreshToken) {
        const newToken = await refreshToken()
        if (newToken) {
          headers.Authorization = `Bearer ${newToken}`
          const retryTimeoutController = new AbortController()
          const retryTimeoutId = setTimeout(
            () => retryTimeoutController.abort(),
            DEFAULT_REQUEST_TIMEOUT_MS,
          )
          let retrySignal: AbortSignal
          if (signal) {
            retrySignal = AbortSignal.any([signal, retryTimeoutController.signal])
          } else {
            retrySignal = retryTimeoutController.signal
          }
          try {
            const retryResponse = await fetch(url, {
              method: 'GET',
              headers,
              signal: retrySignal,
            })
            if (!retryResponse.ok) {
              throw await parseApiError(retryResponse)
            }
            return await retryResponse.blob()
          } finally {
            clearTimeout(retryTimeoutId)
          }
        }
      }

      if (!response.ok) {
        throw await parseApiError(response)
      }

      return await response.blob()
    },
    uploadFiles: async (files: File[]) => {
      const formData = new FormData()
      for (const file of files) {
        formData.append('files', file)
      }

      const headers: Record<string, string> = {}
      if (tokenState.token) {
        headers.Authorization = `Bearer ${tokenState.token}`
      }

      const response = await fetch(joinUrl(baseUrl, endpoints.files.upload), {
        method: 'POST',
        headers,
        body: formData,
      })

      if (response.status === 401 && tokenState.refreshToken) {
        const newToken = await refreshToken()
        if (newToken) {
          headers.Authorization = `Bearer ${newToken}`
          const retryResponse = await fetch(joinUrl(baseUrl, endpoints.files.upload), {
            method: 'POST',
            headers,
            body: formData,
          })

          if (!retryResponse.ok) {
            throw await parseApiError(retryResponse)
          }

          return (await retryResponse.json()) as FileUploadResponse
        }
      }

      if (!response.ok) {
        throw await parseApiError(response)
      }

      return (await response.json()) as FileUploadResponse
    },
    ApiError,
  }
}

export type ApiClient = ReturnType<typeof createApiClient>
