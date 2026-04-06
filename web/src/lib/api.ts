import type {
  AgentDetails,
  AgentStatusResponse,
  AgentsResponse,
  ApiErrorResponse,
  AuthPairResponse,
  AuthRefreshResponse,
  AuthStatusResponse,
  ChannelsResponse,
  ChatSessionsResponse,
  ConfigResponse,
  HistoryResponse,
  ModelsResponse,
  SendMessageRequest,
  SendMessageResponse,
  SessionModelResponse,
  SystemStatus,
  ToolsResponse,
} from './types'

class ApiError extends Error {
  constructor(
    message: string,
    public readonly status: number,
    public readonly code?: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

const joinUrl = (baseUrl: string, path: string) => `${baseUrl.replace(/\/$/, '')}${path}`

const parseError = async (response: Response): Promise<ApiError> => {
  let payload: ApiErrorResponse | null = null
  try {
    payload = (await response.json()) as ApiErrorResponse
  } catch {
    payload = null
  }

  return new ApiError(payload?.message ?? response.statusText, response.status, payload?.code)
}

export const createApiClient = (baseUrl: string) => {
  const request = async <T>(path: string, init: RequestInit = {}, token?: string) => {
    const response = await fetch(joinUrl(baseUrl, path), {
      ...init,
      headers: {
        ...(init.headers ?? {}),
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      },
    })

    if (!response.ok) {
      throw await parseError(response)
    }

    if (response.status === 204) {
      return undefined as T
    }

    const contentType = response.headers.get('content-type') ?? ''
    if (!contentType.includes('application/json')) {
      return undefined as T
    }

    return (await response.json()) as T
  }

  return {
    pair: (pin: string, device_name: string) =>
      request<AuthPairResponse>('/api/v1/auth/pair', {
        method: 'POST',
        body: JSON.stringify({ pin, device_name }),
      }),
    refresh: (refresh_token: string) =>
      request<AuthRefreshResponse>('/api/v1/auth/refresh', {
        method: 'POST',
        body: JSON.stringify({ refresh_token }),
      }),
    status: (token: string) =>
      request<AuthStatusResponse>('/api/v1/auth/status', { method: 'GET' }, token),
    agents: (token: string) => request<AgentsResponse>('/api/v1/agents', { method: 'GET' }, token),
    agentInfo: async (agentId: string, token: string) => {
      const [info, status] = await Promise.all([
        request<AgentDetails>(`/api/v1/agents/${encodeURIComponent(agentId)}`, { method: 'GET' }, token),
        request<AgentStatusResponse>(
          `/api/v1/agents/${encodeURIComponent(agentId)}?action=status`,
          { method: 'GET' },
          token,
        ),
      ])

      return {
        ...info,
        status: status.status,
        active_sessions: status.active_sessions,
      }
    },
    history: (sessionKey: string, token: string) =>
      request<HistoryResponse>(
        `/api/v1/chat/history?session_key=${encodeURIComponent(sessionKey)}`,
        { method: 'GET' },
        token,
      ),
    sessions: (token: string) =>
      request<ChatSessionsResponse>('/api/v1/chat/sessions', { method: 'GET' }, token),
    models: (agentId: string, sessionKey: string | null, token: string) => {
      const params = new URLSearchParams()
      if (agentId) {
        params.set('agent_id', agentId)
      }
      if (sessionKey) {
        params.set('session_key', sessionKey)
      }
      const query = params.toString()
      return request<ModelsResponse>(`/api/v1/models${query ? `?${query}` : ''}`, { method: 'GET' }, token)
    },
    sessionModel: (sessionKey: string, token: string) =>
      request<SessionModelResponse>(
        `/api/v1/chat/session/${encodeURIComponent(sessionKey)}?action=model`,
        { method: 'GET' },
        token,
      ),
    updateSessionModel: (sessionKey: string, model: string, token: string) =>
      request<SessionModelResponse>(
        `/api/v1/chat/session/${encodeURIComponent(sessionKey)}?action=model`,
        {
          method: 'PATCH',
          body: JSON.stringify({ model }),
        },
        token,
      ),
    sendMessage: (payload: SendMessageRequest, token: string) =>
      request<SendMessageResponse>(
        '/api/v1/chat/send',
        {
          method: 'POST',
          body: JSON.stringify(payload),
        },
        token,
      ),
    clearSession: async (sessionKey: string, token: string) => {
      await request<unknown>(
        `/api/v1/chat/session/${encodeURIComponent(sessionKey)}`,
        { method: 'DELETE' },
        token,
      )
    },
    config: (token: string) => request<ConfigResponse>('/api/v1/config', { method: 'GET' }, token),
    tools: (token: string) => request<ToolsResponse>('/api/v1/tools', { method: 'GET' }, token),
    channels: (token: string) =>
      request<ChannelsResponse>('/api/v1/channels', { method: 'GET' }, token),
    systemStatus: (token: string) => request<SystemStatus>('/api/v1/status', { method: 'GET' }, token),
    ApiError,
  }
}

export type ApiClient = ReturnType<typeof createApiClient>
