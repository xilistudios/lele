export const endpoints = {
  auth: {
    pair: '/api/v1/auth/pair',
    refresh: '/api/v1/auth/refresh',
    status: '/api/v1/auth/status',
  },
  agents: {
    list: '/api/v1/agents',
    info: (agentId: string) => `/api/v1/agents/${encodeURIComponent(agentId)}`,
    status: (agentId: string) => `/api/v1/agents/${encodeURIComponent(agentId)}/status`,
    files: (agentId: string, fileName?: string) => {
      const base = `/api/v1/agents/${encodeURIComponent(agentId)}/files`
      return fileName ? `${base}/${encodeURIComponent(fileName)}` : base
    },
  },
  chat: {
    send: '/api/v1/chat/send',
    history: (sessionKey: string, parentSessionKey?: string) => {
      if (parentSessionKey) {
        if (sessionKey.startsWith(parentSessionKey + ':')) {
          const subagentId = sessionKey.slice(parentSessionKey.length + 1)
          return `/api/v1/chat/sessions/${parentSessionKey}/history/${subagentId}`
        }
        if (sessionKey.startsWith('subagent:')) {
          const subagentId = sessionKey.slice(9)
          return `/api/v1/chat/sessions/${parentSessionKey}/history/${subagentId}`
        }
      }
      return `/api/v1/chat/sessions/${sessionKey}/history`
    },
    sessions: '/api/v1/chat/sessions',
    session: (
      sessionKey: string,
      subresource?: 'model' | 'name' | 'agent' | 'thinking' | 'context' | 'summary',
    ) => {
      const base = `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}`
      return subresource ? `${base}/${subresource}` : base
    },
    clear: (sessionKey: string) => `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}/clear`,
    compact: (sessionKey: string) => `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}/compact`,
  },
  system: {
    config: '/api/v1/config',
    configValidate: '/api/v1/config/validate',
    tools: '/api/v1/tools',
    channels: '/api/v1/channels',
    status: '/api/v1/status',
    models: '/api/v1/models',
  },
  providers: {
    models: (name: string) => `/api/v1/providers/${encodeURIComponent(name)}/models`,
  },
  files: {
    upload: '/api/v1/files/upload',
  },
  skills: {
    list: '/api/v1/skills',
    available: '/api/v1/skills/available',
    install: '/api/v1/skills',
    remove: (name: string) => `/api/v1/skills/${encodeURIComponent(name)}`,
  },
} as const
