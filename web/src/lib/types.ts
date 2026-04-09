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

export type SecretMode = 'literal' | 'env' | 'empty'

export type SecretValue = {
  mode: SecretMode
  value?: string
  env_name?: string
  env_default?: string
  has_env_var: boolean
}

export type EditableAgentDefaults = {
  workspace: string
  restrict_to_workspace: boolean
  provider: string
  model: string
  model_fallbacks?: string[]
  image_model?: string
  image_model_fallbacks?: string[]
  max_tokens: number
  temperature?: number
  max_tool_iterations: number
}

export type AgentModelConfig = {
  primary?: string
  fallbacks?: string[]
}

export type SubagentsConfig = {
  allow_agents?: string[]
  model?: AgentModelConfig
}

export type EditableAgentConfig = {
  id: string
  default?: boolean
  name?: string
  workspace?: string
  model?: AgentModelConfig
  skills?: string[]
  subagents?: SubagentsConfig
  temperature?: number
}

export type EditableAgentsConfig = {
  defaults: EditableAgentDefaults
  list?: EditableAgentConfig[]
}

export type EditableSessionConfig = {
  dm_scope?: string
  identity_links?: Record<string, string[]>
  ephemeral: boolean
  ephemeral_threshold: number
}

export type BindingMatch = {
  kind: string
  id: string
}

export type BindingMatchContainer = {
  channel: string
  account_id?: string
  peer?: BindingMatch
  guild_id?: string
  team_id?: string
}

export type AgentBinding = {
  agent_id: string
  match: BindingMatchContainer
}

export type EditableWhatsAppConfig = {
  enabled: boolean
  bridge_url: string
  allow_from: string[]
}

export type EditableTelegramConfig = {
  enabled: boolean
  token: SecretValue
  proxy?: string
  allow_from: string[]
  verbose?: 'off' | 'basic' | 'full'
}

export type EditableFeishuConfig = {
  enabled: boolean
  app_id: SecretValue
  app_secret: SecretValue
  encrypt_key: SecretValue
  verification_token: SecretValue
  allow_from: string[]
}

export type EditableDiscordConfig = {
  enabled: boolean
  token: SecretValue
  allow_from: string[]
}

export type EditableMaixCamConfig = {
  enabled: boolean
  host: string
  port: number
  allow_from: string[]
}

export type EditableQQConfig = {
  enabled: boolean
  app_id: SecretValue
  app_secret: SecretValue
  allow_from: string[]
}

export type EditableDingTalkConfig = {
  enabled: boolean
  client_id: SecretValue
  client_secret: SecretValue
  allow_from: string[]
}

export type EditableSlackConfig = {
  enabled: boolean
  bot_token: SecretValue
  app_token: SecretValue
  allow_from: string[]
}

export type EditableLINEConfig = {
  enabled: boolean
  channel_secret: SecretValue
  channel_access_token: SecretValue
  webhook_host: string
  webhook_port: number
  webhook_path: string
  allow_from: string[]
}

export type EditableOneBotConfig = {
  enabled: boolean
  ws_url: string
  access_token: SecretValue
  reconnect_interval: number
  group_trigger_prefix: string[]
  allow_from: string[]
}

export type EditableNativeConfig = {
  enabled: boolean
  host: string
  port: number
  token_expiry_days: number
  pin_expiry_minutes: number
  max_clients: number
  cors_origins: string[]
  session_expiry_days: number
  max_upload_size_mb: number
  upload_ttl_hours: number
}

export type EditableChannelsConfig = {
  whatsapp: EditableWhatsAppConfig
  telegram: EditableTelegramConfig
  feishu: EditableFeishuConfig
  discord: EditableDiscordConfig
  maixcam: EditableMaixCamConfig
  qq: EditableQQConfig
  dingtalk: EditableDingTalkConfig
  slack: EditableSlackConfig
  line: EditableLINEConfig
  onebot: EditableOneBotConfig
  native: EditableNativeConfig
}

export type ReasoningConfig = {
  effort?: 'low' | 'medium' | 'high'
  summary?: 'auto' | 'detailed' | 'concise'
}

export type ProviderModelConfig = {
  context_window?: number
  model?: string
  max_tokens?: number
  temperature?: number
  vision?: boolean
  reasoning?: ReasoningConfig
}

export type EditableNamedProviderConfig = {
  type?: string
  api_key: SecretValue
  api_base: string
  proxy?: string
  auth_method?: string
  connect_mode?: string
  web_search?: boolean
  models?: Record<string, ProviderModelConfig>
}

export type EditableProvidersConfig = Record<string, EditableNamedProviderConfig>

export type EditableBraveConfig = {
  enabled: boolean
  api_key: SecretValue
  max_results: number
}

export type EditableDuckDuckGoConfig = {
  enabled: boolean
  max_results: number
}

export type EditablePerplexityConfig = {
  enabled: boolean
  api_key: SecretValue
  max_results: number
}

export type EditableWebToolsConfig = {
  brave: EditableBraveConfig
  duckduckgo: EditableDuckDuckGoConfig
  perplexity: EditablePerplexityConfig
}

export type EditableCronToolsConfig = {
  exec_timeout_minutes: number
}

export type EditableExecConfig = {
  enable_deny_patterns: boolean
  custom_deny_patterns: string[]
}

export type EditableToolsConfig = {
  web: EditableWebToolsConfig
  cron: EditableCronToolsConfig
  exec: EditableExecConfig
}

export type GatewayConfig = {
  host: string
  port: number
}

export type HeartbeatConfig = {
  enabled: boolean
  interval: number
}

export type DevicesConfig = {
  enabled: boolean
  monitor_usb: boolean
}

export type EditableLogsConfig = {
  enabled: boolean
  path?: string
  max_days?: number
  rotation?: 'daily' | 'weekly'
}

export type EditableConfig = {
  agents: EditableAgentsConfig
  session?: EditableSessionConfig
  bindings?: AgentBinding[]
  channels: EditableChannelsConfig
  providers: EditableProvidersConfig
  gateway: GatewayConfig
  tools: EditableToolsConfig
  heartbeat: HeartbeatConfig
  devices: DevicesConfig
  logs: EditableLogsConfig
}

export type ConfigMetadata = {
  config_path: string
  source: string
  can_save: boolean
  restart_required_sections: string[]
  secrets_by_path: Record<string, string>
}

export type ConfigResponse = {
  config: EditableConfig
  meta: ConfigMetadata
}

export type ConfigError = {
  path: string
  message: string
  code: string
}

export type ConfigUpdateResponse = {
  config?: EditableConfig
  meta: ConfigMetadata
  errors?: ConfigError[]
}

export type ConfigUpdateRequest = {
  config: EditableConfig
}

export type ConfigValidateRequest = {
  config: EditableConfig
}

export type ConfigValidateResponse = {
  valid: boolean
  errors?: ConfigError[]
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

export type SessionAgentResponse = {
  session_key: string
  agent_id: string
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
        processing?: boolean
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
  | { event: 'subscribe.ack'; data: { session_key: string; processing?: boolean } }
  | { event: 'unsubscribe.ack'; data: { session_key: string } }
  | { event: 'cancel.ack'; data: { status: string } }
  | { event: 'pong'; data: { time: string } }
  | { event: 'attachments'; data: Attachment[] }
  | { event: 'error'; data: { code: string; message: string } }

export type ApprovalDecision = {
  request_id: string
  approved: boolean
}
