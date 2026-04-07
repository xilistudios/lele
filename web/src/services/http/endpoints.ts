export const endpoints = {
  auth: {
    pair: '/api/v1/auth/pair',
    refresh: '/api/v1/auth/refresh',
    status: '/api/v1/auth/status',
  },
  agents: {
    list: '/api/v1/agents',
    info: (agentId: string) => `/api/v1/agents/${encodeURIComponent(agentId)}`,
    status: (agentId: string) => `/api/v1/agents/${encodeURIComponent(agentId)}?action=status`,
  },
  chat: {
    send: '/api/v1/chat/send',
    history: (sessionKey: string) =>
      `/api/v1/chat/history?session_key=${encodeURIComponent(sessionKey)}`,
    sessions: '/api/v1/chat/sessions',
    session: (sessionKey: string, action?: 'model' | 'name' | 'delete') => {
      const suffix = action ? `?action=${action}` : ''
      return `/api/v1/chat/session/${encodeURIComponent(sessionKey)}${suffix}`
    },
  },
  system: {
    config: '/api/v1/config',
    configValidate: '/api/v1/config/validate',
    tools: '/api/v1/tools',
    channels: '/api/v1/channels',
    status: '/api/v1/status',
    models: '/api/v1/models',
  },
  files: {
    upload: '/api/v1/files/upload',
  },
} as const
