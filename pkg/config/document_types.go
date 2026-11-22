package config

import "os"

type SecretMode string

const (
	SecretModeLiteral SecretMode = "literal"
	SecretModeEnv     SecretMode = "env"
	SecretModeKeyring SecretMode = "keyring"
	SecretModeEmpty   SecretMode = "empty"
)

type SecretValue struct {
	Mode       SecretMode `json:"mode"`
	Value      string     `json:"value,omitempty"`
	EnvName    string     `json:"env_name,omitempty"`
	EnvDefault *string    `json:"env_default,omitempty"`
	SecretName string     `json:"secret_name,omitempty"`
	HasEnvVar  bool       `json:"has_env_var"`
}

type EditableDocument struct {
	Agents    EditableAgentsConfig    `json:"agents"`
	Session   EditableSessionConfig   `json:"session,omitempty"`
	Bindings  []AgentBinding          `json:"bindings,omitempty"`
	Groups    GroupsConfig            `json:"groups,omitempty"`
	Channels  EditableChannelsConfig  `json:"channels"`
	Providers EditableProvidersConfig `json:"providers"`
	Gateway   GatewayConfig           `json:"gateway"`
	Tools     EditableToolsConfig     `json:"tools"`
	Heartbeat HeartbeatConfig         `json:"heartbeat"`
	Devices   DevicesConfig           `json:"devices"`
	Logs      EditableLogsConfig      `json:"logs"`

	Commands map[string]CommandDefinition `json:"commands,omitempty"`
	Harness  HarnessConfig                `json:"harness,omitempty"`
}

type EditableAgentsConfig struct {
	Defaults EditableAgentDefaults `json:"defaults"`
	List     []EditableAgentConfig `json:"list,omitempty"`
}

type EditableAgentDefaults struct {
	Workspace                string            `json:"workspace"`
	RestrictToWorkspace      bool              `json:"restrict_to_workspace"`
	Provider                 string            `json:"provider"`
	Model                    string            `json:"model"`
	ModelFallbacks           []string          `json:"model_fallbacks,omitempty"`
	ImageModel               string            `json:"image_model,omitempty"`
	ImageModelFallbacks      []string          `json:"image_model_fallbacks,omitempty"`
	MaxTokens                int               `json:"max_tokens"`
	Temperature              *float64          `json:"temperature,omitempty"`
	ThinkingLevel            *string           `json:"thinking_level,omitempty"`
	MaxToolIterations        int               `json:"max_tool_iterations"`
	MaxReadLines             int               `json:"max_read_lines"`
	MemoryMaxLines           int               `json:"memory_max_lines"`
	SubagentTimeoutMinutes   int               `json:"subagent_timeout_minutes,omitempty"`
	SubagentMaxConcurrent    int               `json:"subagent_max_concurrent,omitempty"`
	SubagentMaxRetries       int               `json:"subagent_max_retries,omitempty"`
	SubagentMaxIterations    int               `json:"subagent_max_iterations,omitempty"`
	SubagentRetentionMinutes int               `json:"subagent_retention_minutes,omitempty"`
	LLMLoopTimeoutMinutes    int               `json:"llm_loop_timeout_minutes,omitempty"`
	PromptCache              PromptCacheConfig `json:"prompt_cache,omitempty"`
}

type EditableAgentConfig struct {
	ID            string            `json:"id"`
	Default       bool              `json:"default,omitempty"`
	Name          string            `json:"name,omitempty"`
	Description   string            `json:"description,omitempty"`
	Workspace     string            `json:"workspace,omitempty"`
	Model         *AgentModelConfig `json:"model,omitempty"`
	Skills        []string          `json:"skills,omitempty"`
	Subagents     *SubagentsConfig  `json:"subagents,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	ThinkingLevel *string           `json:"thinking_level,omitempty"`
	Tools         []string          `json:"tools,omitempty"`
}

type EditableSessionConfig struct {
	DMScope                    string              `json:"dm_scope,omitempty"`
	IdentityLinks              map[string][]string `json:"identity_links,omitempty"`
	Ephemeral                  bool                `json:"ephemeral"`
	EphemeralThreshold         int                 `json:"ephemeral_threshold"`
	CompactionThresholdPercent int                 `json:"compaction_threshold_percent,omitempty"`
	CompactionModel            string              `json:"compaction_model,omitempty"`
	EvictExcludedFromMemory    bool                `json:"evict_excluded_from_memory,omitempty"`

	DurableInbound *bool `json:"durable_inbound,omitempty"`

	DurableOutbound *bool `json:"durable_outbound,omitempty"`

	Resume *bool `json:"resume_enabled,omitempty"`
}

type EditableChannelsConfig struct {
	WhatsApp EditableWhatsAppConfig `json:"whatsapp"`
	Telegram EditableTelegramConfig `json:"telegram"`
	Feishu   EditableFeishuConfig   `json:"feishu"`
	Discord  EditableDiscordConfig  `json:"discord"`
	MaixCam  EditableMaixCamConfig  `json:"maixcam"`
	QQ       EditableQQConfig       `json:"qq"`
	DingTalk EditableDingTalkConfig `json:"dingtalk"`
	Slack    EditableSlackConfig    `json:"slack"`
	LINE     EditableLINEConfig     `json:"line"`
	OneBot   EditableOneBotConfig   `json:"onebot"`
	Native   EditableNativeConfig   `json:"native"`
	Web      EditableWebConfig      `json:"web"`
}

type EditableWebConfig struct {
	Enabled bool `json:"enabled"`
}

type EditableWhatsAppConfig struct {
	Enabled   bool                `json:"enabled"`
	BridgeURL string              `json:"bridge_url"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

type EditableTelegramConfig struct {
	Enabled   bool                `json:"enabled"`
	Token     SecretValue         `json:"token"`
	Proxy     string              `json:"proxy,omitempty"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
	Verbose   VerboseLevel        `json:"verbose,omitempty"`
}

type EditableFeishuConfig struct {
	Enabled           bool                `json:"enabled"`
	AppID             SecretValue         `json:"app_id"`
	AppSecret         SecretValue         `json:"app_secret"`
	EncryptKey        SecretValue         `json:"encrypt_key"`
	VerificationToken SecretValue         `json:"verification_token"`
	AllowFrom         FlexibleStringSlice `json:"allow_from"`
}

type EditableDiscordConfig struct {
	Enabled   bool                `json:"enabled"`
	Token     SecretValue         `json:"token"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

type EditableMaixCamConfig struct {
	Enabled   bool                `json:"enabled"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

type EditableQQConfig struct {
	Enabled   bool                `json:"enabled"`
	AppID     SecretValue         `json:"app_id"`
	AppSecret SecretValue         `json:"app_secret"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

type EditableDingTalkConfig struct {
	Enabled      bool                `json:"enabled"`
	ClientID     SecretValue         `json:"client_id"`
	ClientSecret SecretValue         `json:"client_secret"`
	AllowFrom    FlexibleStringSlice `json:"allow_from"`
}

type EditableSlackConfig struct {
	Enabled   bool                `json:"enabled"`
	BotToken  SecretValue         `json:"bot_token"`
	AppToken  SecretValue         `json:"app_token"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

type EditableLINEConfig struct {
	Enabled            bool                `json:"enabled"`
	ChannelSecret      SecretValue         `json:"channel_secret"`
	ChannelAccessToken SecretValue         `json:"channel_access_token"`
	WebhookHost        string              `json:"webhook_host"`
	WebhookPort        int                 `json:"webhook_port"`
	WebhookPath        string              `json:"webhook_path"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"`
}

type EditableOneBotConfig struct {
	Enabled            bool                `json:"enabled"`
	WSUrl              string              `json:"ws_url"`
	AccessToken        SecretValue         `json:"access_token"`
	ReconnectInterval  int                 `json:"reconnect_interval"`
	GroupTriggerPrefix []string            `json:"group_trigger_prefix"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"`
}

type EditableNativeConfig struct {
	Enabled           bool                  `json:"enabled"`
	Host              string                `json:"host"`
	Port              int                   `json:"port"`
	TokenExpiryDays   int                   `json:"token_expiry_days"`
	PinExpiryMinutes  int                   `json:"pin_expiry_minutes"`
	MaxClients        int                   `json:"max_clients"`
	CORSOrigins       []string              `json:"cors_origins"`
	SessionExpiryDays int                   `json:"session_expiry_days"`
	MaxUploadSizeMB   int64                 `json:"max_upload_size_mb"`
	UploadTTLHours    int                   `json:"upload_ttl_hours"`
	RateLimit         NativeRateLimitConfig `json:"rate_limit"`
}

type EditableProvidersConfig map[string]EditableNamedProviderConfig

type EditableNamedProviderConfig struct {
	Type        string                         `json:"type,omitempty"`
	APIKey      SecretValue                    `json:"api_key"`
	APIBase     string                         `json:"api_base"`
	Proxy       string                         `json:"proxy,omitempty"`
	AuthMethod  string                         `json:"auth_method,omitempty"`
	ConnectMode string                         `json:"connect_mode,omitempty"`
	Headers     map[string]string              `json:"headers,omitempty"`
	WebSearch   *bool                          `json:"web_search,omitempty"`
	Models      map[string]ProviderModelConfig `json:"models,omitempty"`
}

type EditableToolsConfig struct {
	Web  EditableWebToolsConfig `json:"web"`
	Cron CronToolsConfig        `json:"cron"`
	Exec EditableExecConfig     `json:"exec"`
}

type EditableWebToolsConfig struct {
	Brave      EditableBraveConfig      `json:"brave"`
	DuckDuckGo DuckDuckGoConfig         `json:"duckduckgo"`
	Perplexity EditablePerplexityConfig `json:"perplexity"`
	SearXNG    EditableSearXNGConfig    `json:"searxng"`
}

type EditableBraveConfig struct {
	Enabled    bool        `json:"enabled"`
	APIKey     SecretValue `json:"api_key"`
	MaxResults int         `json:"max_results"`
}

type EditablePerplexityConfig struct {
	Enabled    bool        `json:"enabled"`
	APIKey     SecretValue `json:"api_key"`
	MaxResults int         `json:"max_results"`
}

type EditableSearXNGConfig struct {
	Enabled     bool   `json:"enabled"`
	InstanceURL string `json:"instance_url"`
	Categories  string `json:"categories"`
	Language    string `json:"language"`
	SafeSearch  int    `json:"safesearch"`
	MaxResults  int    `json:"max_results"`
}

type EditableExecConfig struct {
	EnableDenyPatterns bool     `json:"enable_deny_patterns"`
	CustomDenyPatterns []string `json:"custom_deny_patterns"`
	TimeoutSeconds     int      `json:"timeout_seconds"`
	WhitelistCommands  []string `json:"whitelist_commands"`
}

type EditableLogsConfig struct {
	Enabled  bool   `json:"enabled"`
	Path     string `json:"path,omitempty"`
	MaxDays  int    `json:"max_days,omitempty"`
	Rotation string `json:"rotation,omitempty"`
}

type DocumentMetadata struct {
	ConfigPath              string            `json:"config_path"`
	Source                  string            `json:"source"`
	CanSave                 bool              `json:"can_save"`
	RestartRequiredSections []string          `json:"restart_required_sections"`
	SecretsByPath           map[string]string `json:"secrets_by_path"`
}

type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (sv SecretValue) resolve() string {
	switch sv.Mode {
	case SecretModeLiteral:
		return sv.Value
	case SecretModeEnv:
		if val := os.Getenv(sv.EnvName); val != "" {
			return val
		}
		if sv.EnvDefault != nil {
			return *sv.EnvDefault
		}
		return ""
	case SecretModeKeyring:
		if keyringResolver != nil {
			if val, err := keyringResolver(sv.SecretName); err == nil {
				return val
			}
		}
		return ""
	default:
		return ""
	}
}

var keyringResolver func(name string) (string, error)

func RegisterKeyringResolver(fn func(name string) (string, error)) {
	keyringResolver = fn
}
