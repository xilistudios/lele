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
  name?: string
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
  model_groups?: ModelGroup[]
}

export type SessionModelResponse = {
  session_key: string
  agent_id?: string
  model: string
  models: string[]
  model_groups?: ModelGroup[]
}

export type ModelOption = {
  value: string
  label: string
}

export type ModelGroup = {
  provider: string
  models: ModelOption[]
}

export type SessionNameResponse = {
  session_key: string
  name: string
}

export type ChannelsResponse = {
  channels: ChannelInfo[]
}

export type ChatSessionsResponse = {
  sessions: ChatSession[]
}

export type HistoryToolCall = {
  id: string
  type?: string
  name?: string
  arguments?: Record<string, unknown>
  thought_signature?: string
}

export type AuthSession = {
  token: string
  refresh_token: string
  expires: string
  client_id: string
  device_name?: string
}

export type ToolMessageStatus = 'executing' | 'completed' | 'error'

export type ChatMessage = {
  id: string
  role: 'user' | 'assistant' | 'tool'
  content: string
  streaming: boolean
  createdAt: string
  attachments?: Attachment[]
  sessionKey?: string
  toolName?: string
  toolArgs?: string
  toolResult?: string
  toolStatus?: ToolMessageStatus
}

export type ToolStatus = {
  session_key?: string
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
  messages: Array<{
    role: 'user' | 'assistant' | 'tool'
    content: string
    tool_calls?: HistoryToolCall[]
    tool_call_id?: string
  }>
}

export type ApiErrorResponse = {
  code?: string
  message?: string
}

export type UploadedFile = {
  id: string
  path: string
  name: string
  mime_type: string
  size: number
}

export type FileUploadResponse = {
  files: UploadedFile[]
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
  | {
      event: 'message.stream'
      data: { message_id: string; session_key?: string; chunk: string; done: boolean }
    }
  | {
      event: 'message.complete'
      data: {
        message_id: string
        session_key?: string
        content: string
        attachments?: Attachment[]
      }
    }
  | {
      event: 'messages.catchup'
      data: {
        session_key: string
        message_count: number
        catchup_count: number
        is_initial: boolean
        messages: Array<{
          role: 'user' | 'assistant' | 'tool'
          content: string
          tool_call_id?: string
          tool_calls?: HistoryToolCall[]
        }>
      }
    }
  | { event: 'tool.executing'; data: ToolStatus }
  | { event: 'tool.result'; data: { session_key?: string; tool: string; result: string } }
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
