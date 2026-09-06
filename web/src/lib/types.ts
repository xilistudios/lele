export type GroupProfile = {
  id: string
  participants: string[]
  strategy: string
  moderator?: string
  rounds?: number
  max_turns?: number
  max_tokens_per_turn?: number
  total_token_budget?: number
  stop_keywords?: string[]
  parallel?: boolean
}

export type GroupsConfig = {
  list?: GroupProfile[]
}

export type Agent = {
  id: string
  name: string
  description?: string
  workspace: string
  model: string
  default?: boolean
  reasoning?: ReasoningConfig
}

export type Attachment = {
  name?: string
  path?: string
  mime_type?: string
  kind?: string
  caption?: string
}

export type ChatMode = 'chat' | 'agent' | 'group'

export type SessionKind = 'chat' | 'heartbeat' | 'cron' | 'cron-spawn' | 'subagent'

export type ChatSession = {
  key: string
  name?: string
  mode?: ChatMode
  kind?: SessionKind
  created: string
  updated: string
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

export type SystemVersionInfo = {
  version: string
  git_commit: string
  build_time: string
  go_version: string
  os: string
  arch: string
  binary: string
  supervisor: string
  dev_build: boolean
  has_backup: boolean
}

export type UpdateCheckInfo = {
  current: string
  latest: string
  update_available: boolean
  changelog?: string
  published_at?: string
  html_url?: string
}

export type UpdatePhase =
  | 'idle'
  | 'checking'
  | 'downloading'
  | 'verifying'
  | 'installing'
  | 'restarting'
  | 'done'
  | 'failed'

export type UpdateState = {
  phase: UpdatePhase
  progress: number
  from: string
  to: string
  error?: string
  started_at?: string
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

// AgentFiles represents the context files for an agent's workspace.
export type AgentFileInfo = {
  name: string
  size: number
  editable: boolean
}

export type AgentFilesResponse = {
  files: AgentFileInfo[]
  content?: string
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
  max_read_lines: number
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
  description?: string
  workspace?: string
  model?: AgentModelConfig
  skills?: string[]
  subagents?: SubagentsConfig
  temperature?: number
  reasoning?: ReasoningConfig
  max_iterations?: number
  max_tokens?: number
  context_window?: number
  supports_images?: boolean
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
  compaction_model?: string
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
  enable?: boolean
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

export type EditableSearXNGConfig = {
  enabled: boolean
  instance_url: string
  categories: string
  language: string
  safesearch: number
  max_results: number
}

export type EditableWebToolsConfig = {
  brave: EditableBraveConfig
  duckduckgo: EditableDuckDuckGoConfig
  perplexity: EditablePerplexityConfig
  searxng: EditableSearXNGConfig
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

export type DisplayConfig = {
  language?: string
}

export type EditableConfig = {
  agents: EditableAgentsConfig
  session?: EditableSessionConfig
  bindings?: AgentBinding[]
  groups?: GroupsConfig
  channels: EditableChannelsConfig
  providers: EditableProvidersConfig
  gateway: GatewayConfig
  tools: EditableToolsConfig
  heartbeat: HeartbeatConfig
  devices: DevicesConfig
  logs: EditableLogsConfig
  display?: DisplayConfig
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
  reasoning?: ReasoningConfig
}

export type ModelGroup = {
  provider: string
  models: ModelOption[]
}

export type ProviderModelInfo = {
  id: string
  object: string
  created: number
  owned_by: string
}

export type ProviderModelsResponse = {
  provider: string
  models: ProviderModelInfo[]
}

export type SessionNameResponse = {
  session_key: string
  name: string
}

export type SessionAgentResponse = {
  session_key: string
  agent_id: string
}

export type SessionThinkingResponse = {
  session_key: string
  level: string
}

export type SessionFolderResponse = {
  session_key: string
  folder: string
}

export type FsListEntry = {
  name: string
  path: string
  is_dir: boolean
}

export type FsListResponse = {
  path: string
  parent: string
  entries: FsListEntry[]
  home: string
  roots: string[]
  truncated: boolean
}

/**
 * A slash command the backend dispatches and the WebUI palette advertises.
 * Mirrors Go commands.CommandInfo (pkg/agent/commands). `name` already carries
 * the leading "/" (e.g. "/clear"); description/usage arrive in English from the
 * server and are intentionally not translated.
 */
export type SlashCommandInfo = {
  name: string
  description: string
  usage: string
  /**
   * Where the command was defined. Absent (undefined) for built-ins; set for
   * user-defined harness commands (pkg/harness.Source). Drives the palette
   * badge and the "[args]" usage hint — never a filter, so unknown future
   * sources still render.
   */
  source?: string
}

export type ChatCommandsResponse = {
  commands: SlashCommandInfo[]
}

export type SessionContextResponse = {
  session_key: string
  input_tokens: number
  output_tokens: number
  total_tokens: number
  cumulative_input_tokens: number
  cumulative_output_tokens: number
  cumulative_total_tokens: number
  context_window: number
  usage_percent: number
  compaction_count: number
}

export type SubagentTaskInfo = {
  task_id: string
  session_key: string
  label: string
  agent_id: string
  status: 'running' | 'completed' | 'not_done' | 'needs_context' | 'failed' | 'cancelled'
  summary: string
  created: number
  updated: number
  iterations: number
}

export type SessionSubagentsResponse = {
  session_key: string
  subagents: SubagentTaskInfo[]
}

// ── Group chat (MoA) types ──────────────────────────────────────────────────

export type GroupStatusEvent = {
  session_key?: string
  group_id: string
  status: 'started' | 'done' | 'stopped' | 'error'
  participants: string
}

export type GroupTurnEvent = {
  session_key?: string
  group_id: string
  speaker: string
  label: string
  role: 'proposer' | 'aggregator' | 'moderator' | 'critic'
  layer: number
  turn_index: number
  content: string
}

export type GroupCompleteEvent = {
  session_key?: string
  group_id: string
  strategy: string
  layers: number
  total_tokens: number
  content: string
}

/** A tool call within a group turn. */
export type GroupToolCall = {
  tool_call_id: string
  tool: string
  status: 'executing' | 'completed' | 'error'
  arguments?: string
  result?: string
}

/** A single turn in the group transcript (internal state). */
export type GroupTurn = {
  groupID: string
  speaker: string
  label: string
  role: 'proposer' | 'aggregator' | 'moderator' | 'critic'
  layer: number
  turnIndex: number
  content: string
  toolCalls?: GroupToolCall[]
}

/** Full state of a group conversation (internal state). */
export type GroupInfo = {
  groupID: string
  status: 'started' | 'done' | 'stopped' | 'error'
  strategy: string
  participants: string
  layers: number
  totalTokens: number
  createdAt: string
  turns: GroupTurn[]
  synthesis?: string
}

/** WS event payload for group.tool. */
export type GroupToolEvent = {
  session_key?: string
  group_id: string
  speaker: string
  label?: string
  layer: number
  turn_index: number
  tool_call_id: string
  tool: string
  status: 'executing' | 'completed' | 'error'
  arguments?: string
  result?: string
}

/** A single turn from the rehydration snapshot. */
export type GroupSnapshotTurn = {
  turn_index: number
  speaker: string
  label: string
  role: 'proposer' | 'aggregator' | 'moderator' | 'critic'
  layer: number
  content: string
  tool_calls?: GroupToolCall[]
}

/** Rehydration snapshot for a group, included in welcome/reconnected/history. */
export type GroupSnapshot = {
  group_id: string
  status: 'started' | 'done' | 'stopped' | 'error'
  strategy: string
  participants: string
  layers: number
  total_tokens: number
  created_at: string
  synthesis: string
  turns: GroupSnapshotTurn[]
}

export type ChannelsResponse = {
  channels: ChannelInfo[]
}

export type ChatSessionsResponse = {
  sessions: ChatSession[]
  total: number
  has_more: boolean
}

export type CreateSessionResponse = {
  session_key: string
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
  reasoningContent?: string
  streaming: boolean
  createdAt: string
  optimistic?: boolean
  failed?: boolean
  optimisticBaseCount?: number
  attachments?: Attachment[]
  sessionKey?: string
  toolName?: string
  toolArgs?: string
  toolResult?: string
  toolStatus?: ToolMessageStatus
  toolCallId?: string
  subagentSessionKey?: string
  error?: string
  excludeFromContext?: boolean
  /**
   * Stable logical identity that survives the WebSocket→HTTP transition.
   *
   * Streaming messages use ephemeral UUIDs; canonical history uses
   * content-hash ids. When mergeMessages swaps a message from its ephemeral
   * form to its confirmed form, it carries the ephemeral id over as
   * `stableId` so React can key the render tree on it. This prevents the
   * bubble from remounting (and replaying its enter animation) at the exact
   * moment a response or sent message is confirmed — the source of the
   * visible flicker during WebSocket updates.
   */
  stableId?: string
}

export type ToolStatus = {
  session_key?: string
  tool: string
  action: string
  arguments?: Record<string, unknown>
  subagent_session_key?: string
}

/**
 * Payload of the `command.applied` WS event, emitted when the backend expands a
 * user-defined slash command (pkg/harness) into its prompt. Mirrors Go
 * channels.WSCommandAppliedPayload. `command` has NO leading slash; the UI adds
 * it. `agent`/`model` are the per-turn overrides ("" when unset).
 */
export type WSCommandAppliedPayload = {
  session_key: string
  command: string
  description: string
  args: string
  agent: string
  model: string
  source: 'config' | 'global' | 'workspace' | 'directory' | string
}

export type ApprovalRequest = {
  id: string
  command: string
  reason: string
}

export type ApprovalResult = {
  requestId: string
  approved: boolean
  command: string
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

export type SafeClientInfo = {
  client_id: string
  device_name: string
  created: string
  expires: string
  last_seen: string
  session_keys?: string[]
}

export type AuthPINResponse = {
  pin: string
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

// StreamMessageState mirrors the backend's StreamMessageState struct.
// It represents the accumulated state of an in-progress streaming message.
export type StreamMessageState = {
  message_id: string
  session_key: string
  content: string
  reasoning_content: string
  done: boolean
  error?: string
  started_at: number
  last_chunk_at: number
}

export type StreamStatusResponse = {
  streams: StreamMessageState[]
}

export type HistoryResponse = {
  session_key: string
  processing: boolean
  messages: Array<{
    id: string
    role: 'user' | 'assistant' | 'tool'
    content: string
    reasoning_content?: string
    tool_calls?: HistoryToolCall[]
    tool_call_id?: string
    tool_name?: string
  }>
  has_more: boolean
  groups?: GroupSnapshot[]
}

export type ApiErrorResponse = {
  error?: string
  code?: string
  message?: string
  errors?: ConfigError[]
}

export type UploadedFile = {
  id: string
  path: string
  name: string
  mime_type: string
  size: number
}

export type SkillInfo = {
  id: string
  name: string
  description: string
  installed: boolean
  enabled: boolean
  source?: 'workspace' | 'global' | 'builtin'
}

export type AvailableSkill = {
  name: string
  repository: string
  description: string
  author: string
  tags: string[]
}

export type SkillsResponse = {
  skills: SkillInfo[]
}

export type AvailableSkillsResponse = {
  skills: AvailableSkill[]
}

export type SkillInstallResponse = {
  skill_id: string
  message: string
}

export type SkillRemoveResponse = {
  message: string
}

export type ScannedSkill = {
  name: string
  description: string
  path: string
  has_skill: boolean
}

export type ScanSkillsResponse = {
  skills: ScannedSkill[]
  repo: string
}

export type SkillToggleRequest = {
  enabled: boolean
}

export type SkillInstallBatchResponse = {
  installed: string[]
  count: number
  message: string
}

export type FileUploadResponse = {
  files: UploadedFile[]
}

export type ClientEvent =
  | {
      event: 'welcome' | 'reconnected'
      data: {
        client_id: string
        device_name: string
        session_key: string
        status: string
        agents: Agent[]
        server_time: string
        processing?: boolean
        groups_enabled?: boolean
      }
    }
  | { event: 'message.ack'; data: { message_id: string; session_key: string } }
  | {
      event: 'message.stream'
      data: { message_id: string; session_key?: string; chunk: string; done: boolean }
    }
  | {
      event: 'message.thinking'
      data: { message_id: string; session_key?: string; chunk: string }
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
        catchup_count: number
        is_initial: boolean
        messages: Array<{
          id?: string
          role: 'user' | 'assistant' | 'tool'
          content: string
          tool_call_id?: string
          tool_calls?: HistoryToolCall[]
        }>
      }
    }
  | { event: 'tool.executing'; data: ToolStatus & { tool_call_id?: string } }
  | { event: 'command.applied'; data: WSCommandAppliedPayload }
  | {
      event: 'tool.result' | 'subagent.result'
      data: {
        session_key?: string
        tool: string
        result: string
        subagent_session_key?: string
        tool_call_id?: string
      }
    }
  | { event: 'approval.request'; data: ApprovalRequest }
  | { event: 'approve.ack'; data: { request_id: string; approved: string } }
  | { event: 'approve.result'; data: { request_id: string; approved: boolean; command: string } }
  | { event: 'subscribe.ack'; data: { session_key: string; processing?: boolean } }
  | { event: 'unsubscribe.ack'; data: { session_key: string } }
  | { event: 'cancel.ack'; data: { status: string } }
  | { event: 'pong'; data: { time: string } }
  | { event: 'attachments'; data: Attachment[] }
  | { event: 'error'; data: { code: string; message: string } }
  | { event: 'stream.error'; data: { error: string } }
  | { event: 'history.updated'; data: { session_key: string; name?: string } }
  | { event: 'group.status'; data: GroupStatusEvent }
  | { event: 'group.turn'; data: GroupTurnEvent }
  | { event: 'group.complete'; data: GroupCompleteEvent }

export type ApprovalDecision = {
  request_id: string
  approved: boolean
}

export type ApproveResponse = {
  request_id: string
  approved: boolean
  message?: string
}

export type BackgroundExecInfo = {
  id: string
  agent_id: string
  command: string
  working_dir: string
  status: 'running' | 'completed' | 'stopped' | 'failed'
  start_time: string
  end_time: string | null
  exit_code: number
  elapsed_ms: number
}

export type BackgroundExecsResponse = {
  processes: BackgroundExecInfo[]
}

export type BackgroundExecOutputResponse = {
  id: string
  output: string
  status: string
  elapsed_ms: number
}

export type BackgroundExecStopResponse = {
  id: string
  stopped: boolean
}

export type CronSchedule = {
  kind: 'at' | 'every' | 'cron' | string
  atMs?: number | null
  everyMs?: number | null
  expr?: string
  tz?: string
}

export type CronSpawnConfig = {
  task: string
  label?: string
  agent_id?: string
  guidance?: string
  /** Optional model override (e.g. 'anthropic:claude-opus'). */
  model?: string
}

export type CronPayload = {
  kind: string
  message: string
  command?: string
  deliver: boolean
  channel?: string
  to?: string
  spawn?: CronSpawnConfig | null
}

export type CronJobState = {
  nextRunAtMs?: number | null
  lastRunAtMs?: number | null
  lastStatus?: string
  lastError?: string
}

export type CronJob = {
  id: string
  name: string
  enabled: boolean
  schedule: CronSchedule
  payload: CronPayload
  state: CronJobState
  createdAtMs: number
  updatedAtMs: number
  deleteAfterRun: boolean
}

export type CronStatus = {
  enabled: boolean
  jobs: number
  nextWakeAtMS?: number | null
}

export type CronJobsResponse = {
  jobs: CronJob[]
  status: CronStatus
}

export type CronJobResponse = {
  job: CronJob
}

export type CronJobInput = {
  name?: string
  enabled?: boolean
  schedule: CronSchedule
  /** Explicit null clears an existing message on update. */
  message?: string | null
  /** Explicit null clears an existing command on update. */
  command?: string | null
  deliver?: boolean
  channel?: string
  to?: string
  /** Spawn config. Explicit null clears an existing spawn config on update. */
  spawn?: CronSpawnConfig | null
}

export type SecretMeta = {
  name: string
  description: string
  tags: string[] | null
  scope: string[] | null
  created_at: string
  updated_at: string
  created_by: string
}

export type SecretStatus = {
  enabled: boolean
  backend: string
  count: number
}

export type SecretsListResponse = {
  secrets: SecretMeta[]
  status: SecretStatus
}

export type SecretDetailResponse = {
  secret: SecretMeta
  value: string
}

export type SecretInput = {
  name: string
  value: string
  description?: string
  tags?: string[]
  scope?: string[]
}

export type SecretAuditRecord = {
  secret_name: string
  agent_id: string
  session_key: string
  action: string
  timestamp: string
  granted: boolean
}

export type SecretsAuditResponse = {
  audit: SecretAuditRecord[]
}

export type LogEntry = {
  level: string
  timestamp: string
  component?: string
  message: string
  fields?: Record<string, unknown>
  caller?: string
}

export type LogsResponse = {
  entries: LogEntry[]
  total_lines: number
  returned_lines: number
  file: string
  date: string
  level: string
}

export type LogsDatesResponse = {
  dates: string[]
}
