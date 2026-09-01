export const endpoints = {
  auth: {
    pin: '/api/v1/auth/pin',
    pair: '/api/v1/auth/pair',
    refresh: '/api/v1/auth/refresh',
    status: '/api/v1/auth/status',
    logout: '/api/v1/auth/logout',
    listClients: '/api/v1/auth/clients',
    removeClient: (clientId: string) => `/api/v1/auth/clients/${encodeURIComponent(clientId)}`,
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
    sendStream: '/api/v1/chat/send/stream',
    history: (sessionKey: string, parentSessionKey?: string) => {
      if (parentSessionKey) {
        if (sessionKey.startsWith(`${parentSessionKey}:`)) {
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
    sessionsMeta: '/api/v1/chat/sessions/meta',
    session: (
      sessionKey: string,
      subresource?:
        | 'model'
        | 'name'
        | 'agent'
        | 'thinking'
        | 'folder'
        | 'context'
        | 'summary'
        | 'subagents',
    ) => {
      const base = `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}`
      return subresource ? `${base}/${subresource}` : base
    },
    clear: (sessionKey: string) => `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}/clear`,
    compact: (sessionKey: string) =>
      `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}/compact`,
    approve: (sessionKey: string) =>
      `/api/v1/chat/sessions/${encodeURIComponent(sessionKey)}/approve`,
    streams: (sessionKey: string) => `/api/v1/chat/streams/${encodeURIComponent(sessionKey)}`,
    streamState: (sessionKey: string, messageID: string) =>
      `/api/v1/chat/streams/${encodeURIComponent(sessionKey)}/${encodeURIComponent(messageID)}`,
  },
  system: {
    config: '/api/v1/config',
    configValidate: '/api/v1/config/validate',
    tools: '/api/v1/tools',
    channels: '/api/v1/channels',
    status: '/api/v1/status',
    models: '/api/v1/models',
    version: '/api/v1/system/version',
    updatesCheck: '/api/v1/system/updates/check',
    updatesApply: '/api/v1/system/updates/apply',
    updatesStatus: '/api/v1/system/updates/status',
    updatesRollback: '/api/v1/system/updates/rollback',
    restart: '/api/v1/system/restart',
  },
  providers: {
    models: (name: string) => `/api/v1/providers/${encodeURIComponent(name)}/models`,
  },
  files: {
    upload: '/api/v1/files/upload',
  },
  fs: {
    list: '/api/v1/fs/list',
  },
  skills: {
    list: '/api/v1/skills',
    available: '/api/v1/skills/available',
    install: '/api/v1/skills',
    remove: (name: string) => `/api/v1/skills/${encodeURIComponent(name)}`,
    scan: '/api/v1/skills/scan',
    installBatch: '/api/v1/skills/install-batch',
    toggle: (name: string) => `/api/v1/skills/${encodeURIComponent(name)}/toggle`,
    workspaceConfig: '/api/v1/skills/workspace-config',
  },
  backgroundExec: {
    list: '/api/v1/background-exec',
    output: (id: string) => `/api/v1/background-exec/${encodeURIComponent(id)}/output`,
    stop: (id: string) => `/api/v1/background-exec/${encodeURIComponent(id)}/stop`,
    stream: (id: string) => `/api/v1/background-exec/${encodeURIComponent(id)}/stream`,
  },
  cron: {
    list: '/api/v1/cron',
    create: '/api/v1/cron',
    get: (id: string) => `/api/v1/cron/${encodeURIComponent(id)}`,
    update: (id: string) => `/api/v1/cron/${encodeURIComponent(id)}`,
    remove: (id: string) => `/api/v1/cron/${encodeURIComponent(id)}`,
    enable: (id: string) => `/api/v1/cron/${encodeURIComponent(id)}/enable`,
    disable: (id: string) => `/api/v1/cron/${encodeURIComponent(id)}/disable`,
    run: (id: string) => `/api/v1/cron/${encodeURIComponent(id)}/run`,
  },
  secrets: {
    list: '/api/v1/secrets',
    create: '/api/v1/secrets',
    get: (name: string) => `/api/v1/secrets/${encodeURIComponent(name)}`,
    remove: (name: string) => `/api/v1/secrets/${encodeURIComponent(name)}`,
    status: '/api/v1/secrets/status',
    audit: '/api/v1/secrets/audit',
  },
  logs: {
    list: '/api/v1/logs',
    dates: '/api/v1/logs/dates',
  },
} as const
