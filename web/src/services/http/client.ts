import type {
  AgentDetails,
  AgentFilesResponse,
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
  ChannelsResponse,
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
  GroupsConfig,
  HistoryResponse,
  LogsDatesResponse,
  LogsResponse,
  ModelsResponse,
  ProviderModelsResponse,
  SafeClientInfo,
  SecretDetailResponse,
  SecretInput,
  SecretMeta,
  SecretsAuditResponse,
  SecretsListResponse,
  SendMessageRequest,
  SendMessageResponse,
  SessionAgentResponse,
  SessionContextResponse,
  SessionModelResponse,
  SessionNameResponse,
  SessionSubagentsResponse,
  SessionThinkingResponse,
  SkillInstallBatchResponse,
  SkillInstallResponse,
  SkillRemoveResponse,
  SkillsResponse,
  ScanSkillsResponse,
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

const joinUrl = (baseUrl: string, path: string) => `${baseUrl.replace(/\/$/, '')}${path}`

const isJsonBody = (body: BodyInit | null | undefined) => body !== null && body !== undefined

const DEFAULT_MAX_RETRIES = 1
const DEFAULT_RETRY_DELAY = 1000
const DEFAULT_REQUEST_TIMEOUT_MS = 30_000

type TokenState = {
  token: string | null
  refreshToken: string | null
  onTokenRefresh?: (session: AuthSession) => void
  onAuthFailure?: () => void
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
    // Non-JSON data payload (e.g., plain text error from proxy)
    return {
      event: eventName,
      data: { raw: dataLines.join('\n') },
    } as unknown as ClientEvent
  }
}

export const createApiClient = (baseUrl: string) => {
  const tokenState: TokenState = {
    token: null,
    refreshToken: null,
    onTokenRefresh: undefined,
  }

  const setToken = (
    token: string,
    refreshToken: string,
    onRefresh?: (session: AuthSession) => void,
  ) => {
    tokenState.token = token
    tokenState.refreshToken = refreshToken
    tokenState.onTokenRefresh = onRefresh
  }

  const clearToken = () => {
    tokenState.token = null
    tokenState.refreshToken = null
    tokenState.onTokenRefresh = undefined
  }

  const getToken = () => tokenState.token

  const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

  const refreshToken = async (): Promise<string | null> => {
    if (!tokenState.refreshToken) return null

    try {
      const response = await fetch(joinUrl(baseUrl, endpoints.auth.refresh), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: tokenState.refreshToken }),
      })

      if (!response.ok) {
        const onFail = tokenState.onAuthFailure
        clearToken()
        onFail?.()
        return null
      }

      const data = (await response.json()) as AuthRefreshResponse
      tokenState.token = data.token
      tokenState.refreshToken = data.refresh_token

      if (tokenState.onTokenRefresh) {
        const session: AuthSession = {
          client_id: '',
          device_name: '',
          token: data.token,
          refresh_token: data.refresh_token,
          expires: new Date(Date.now() + 3600000).toISOString(),
        }
        tokenState.onTokenRefresh(session)
      }

      return data.token
    } catch {
      const onFail = tokenState.onAuthFailure
      clearToken()
      onFail?.()
      return null
    }
  }

  const requestWithRetry = async <T>(
    path: string,
    init: RequestInit = {},
    maxRetries: number = DEFAULT_MAX_RETRIES,
  ): Promise<T> => {
    let lastError: Error | null = null
    let retryCount = 0

    while (retryCount <= maxRetries) {
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

        if (response.status === 401 && tokenState.refreshToken && retryCount === 0) {
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
          if (error.status === 401 && retryCount > 0) {
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
          // Stream failed after ack — notify the UI so it can show an error indicator
          // on the incomplete message instead of silently hanging.
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
    setAuthFailureHandler: (handler: (() => void) | undefined) => {
      tokenState.onAuthFailure = handler
    },
    logout: async () => {
      // Revoke token server-side (fire-and-forget, don't block on errors)
      if (tokenState.token) {
        try {
          await fetch(joinUrl(baseUrl, endpoints.auth.logout), {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              Authorization: `Bearer ${tokenState.token}`,
            },
          })
        } catch {
          // Ignore network errors — we'll clear local state regardless
        }
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
    agentFile: (agentId: string, fileName: string) =>
      request<AgentFilesResponse>(endpoints.agents.files(agentId, fileName), { method: 'GET' }),
    agentFileSave: (agentId: string, fileName: string, content: string) =>
      request<AgentFilesResponse>(endpoints.agents.files(agentId, fileName), {
        method: 'PUT',
        body: JSON.stringify({ content }),
      }),
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
    sessionContext: (sessionKey: string) =>
      request<SessionContextResponse>(endpoints.chat.session(sessionKey, 'context'), {
        method: 'GET',
      }),
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
    installSkill: (url: string) =>
      request<SkillInstallResponse>(endpoints.skills.install, {
        method: 'POST',
        body: JSON.stringify({ url }),
      }),
    removeSkill: (name: string) =>
      request<SkillRemoveResponse>(endpoints.skills.remove(name), { method: 'DELETE' }),
    scanSkills: (repo: string) =>
      request<ScanSkillsResponse>(endpoints.skills.scan, {
        method: 'POST',
        body: JSON.stringify({ repo }),
      }),
    installSkillsBatch: (repo: string, skills: string[]) =>
      request<SkillInstallBatchResponse>(endpoints.skills.installBatch, {
        method: 'POST',
        body: JSON.stringify({ repo, skills }),
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
