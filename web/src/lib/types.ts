export type Agent = {
  id: string
  name: string
  workspace: string
  model: string
  default?: boolean
}

export type Attachment = {
  name?: string
  path?: string
  mime_type?: string
  kind?: string
  caption?: string
}

export type ChatSession = {
  key: string
  created: string
  updated: string
  message_count: number
}

export type ToolInfo = {
  name: string
  description: string
  enabled: boolean
}

export type ChannelInfo = {
  name: string
  enabled: boolean
  running: boolean
}

export type SystemAgentStatus = {
  id: string
  name: string
  status: string
}

export type SystemStatus = {
  status: string
  uptime: string
  agents: SystemAgentStatus[]
  channels: ChannelInfo[]
  version: string
}

export type AgentStatusResponse = {
  id: string
  status: string
  active_sessions: number
}

export type AgentDetails = Agent & {
  status?: string
  active_sessions?: number
}

export type ConfigResponse = {
  config: Record<string, unknown>
}

export type ToolsResponse = {
  tools: ToolInfo[]
}

export type ModelsResponse = {
  agent_id?: string
  model?: string
  models: string[]
}

export type SessionModelResponse = {
  session_key: string
  agent_id?: string
  model: string
  models: string[]
}

export type ChannelsResponse = {
  channels: ChannelInfo[]
}

export type ChatSessionsResponse = {
  sessions: ChatSession[]
}

export type AuthSession = {
  token: string
  refresh_token: string
  expires: string
  client_id: string
  device_name?: string
}

export type ChatMessage = {
  id: string
  role: 'user' | 'assistant'
  content: string
  streaming: boolean
  createdAt: string
  attachments?: Attachment[]
  sessionKey?: string
}

export type ToolStatus = {
  tool: string
  action: string
}

export type ApprovalRequest = {
  id: string
  command: string
  reason: string
}

export type AuthPairResponse = AuthSession

export type AuthRefreshResponse = {
  token: string
  refresh_token: string
  expires: string
}

export type AuthStatusResponse = {
  valid: boolean
  client_id: string
  device_name: string
  expires: string
}

export type AgentsResponse = {
  agents: Agent[]
}

export type SendMessageRequest = {
  content: string
  session_key?: string
  agent_id?: string
  attachments?: string[]
}

export type SendMessageResponse = {
  message_id: string
  session_key: string
}

export type HistoryResponse = {
  session_key: string
  messages: Array<{ role: 'user' | 'assistant'; content: string }>
}

export type ApiErrorResponse = {
  code?: string
  message?: string
}

export type ClientEvent =
  | {
      event: 'welcome'
      data: {
        client_id: string
        device_name: string
        session_key: string
        status: string
        agents: Agent[]
        server_time: string
      }
    }
  | { event: 'message.ack'; data: { message_id: string; session_key: string } }
  | { event: 'message.stream'; data: { message_id: string; chunk: string; done: boolean } }
  | {
      event: 'message.complete'
      data: { message_id: string; content: string; attachments?: Attachment[] }
    }
  | { event: 'tool.executing'; data: ToolStatus }
  | { event: 'tool.result'; data: { tool: string; result: string } }
  | { event: 'approval.request'; data: ApprovalRequest }
  | { event: 'approve.ack'; data: { request_id: string; approved: string } }
  | { event: 'subscribe.ack'; data: { session_key: string } }
  | { event: 'unsubscribe.ack'; data: { session_key: string } }
  | { event: 'cancel.ack'; data: { status: string } }
  | { event: 'pong'; data: { time: string } }
  | { event: 'attachments'; data: Attachment[] }
  | { event: 'error'; data: { code: string; message: string } }

export type ApprovalDecision = {
  request_id: string
  approved: boolean
}
