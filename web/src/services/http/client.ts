import type {
  AgentDetails,
  AgentStatusResponse,
  AgentsResponse,
  AuthPairResponse,
  AuthRefreshResponse,
  AuthSession,
  AuthStatusResponse,
  ChannelsResponse,
  ChatSessionsResponse,
  ConfigResponse,
  ConfigUpdateResponse,
  ConfigValidateResponse,
  EditableConfig,
  FileUploadResponse,
  HistoryResponse,
  ModelsResponse,
  SendMessageRequest,
  SendMessageResponse,
  SessionModelResponse,
  SessionNameResponse,
  SystemStatus,
  ToolsResponse,
} from '../../lib/types'
import { endpoints } from './endpoints'
import { ApiError, parseApiError } from './errors'

const joinUrl = (baseUrl: string, path: string) => `${baseUrl.replace(/\/$/, '')}${path}`

const isJsonBody = (body: BodyInit | null | undefined) => body !== null && body !== undefined

const DEFAULT_MAX_RETRIES = 1
const DEFAULT_RETRY_DELAY = 1000

type TokenState = {
  token: string | null
  refreshToken: string | null
  onTokenRefresh?: (session: AuthSession) => void
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
        clearToken()
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
      clearToken()
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

        const response = await fetch(joinUrl(baseUrl, path), {
          ...init,
          headers,
        })

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

  return {
    setToken,
    clearToken,
    getToken,
    pair: (pin: string, device_name: string) =>
      request<AuthPairResponse>(endpoints.auth.pair, {
        method: 'POST',
        body: JSON.stringify({ pin, device_name }),
      }),
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
    history: (sessionKey: string) =>
      request<HistoryResponse>(endpoints.chat.history(sessionKey), { method: 'GET' }),
    sessions: () => request<ChatSessionsResponse>(endpoints.chat.sessions, { method: 'GET' }),
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
    sendMessage: (payload: SendMessageRequest) =>
      request<SendMessageResponse>(endpoints.chat.send, {
        method: 'POST',
        body: JSON.stringify(payload),
      }),
    clearSession: async (sessionKey: string) => {
      await request<unknown>(endpoints.chat.session(sessionKey), { method: 'DELETE' })
    },
    deleteSession: async (sessionKey: string) => {
      await request<unknown>(endpoints.chat.session(sessionKey, 'delete'), {
        method: 'DELETE',
      })
    },
    config: () => request<ConfigResponse>(endpoints.system.config, { method: 'GET' }),
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
