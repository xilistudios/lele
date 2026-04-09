package config

import "os"

// SecretMode indicates how a secret field is handled.
type SecretMode string

const (
	// SecretModeLiteral means the value is literal (stored directly).
	SecretModeLiteral SecretMode = "literal"
	// SecretModeEnv means the value comes from an environment variable.
	SecretModeEnv SecretMode = "env"
	// SecretModeEmpty means the value is empty.
	SecretModeEmpty SecretMode = "empty"
)

// SecretValue represents a secret field that can come from ENV.
type SecretValue struct {
	Mode       SecretMode `json:"mode"`
	Value      string     `json:"value,omitempty"`
	EnvName    string     `json:"env_name,omitempty"`
	EnvDefault *string    `json:"env_default,omitempty"`
	HasEnvVar  bool       `json:"has_env_var"`
}

// EditableDocument represents an editable config document
// with metadata to preserve secrets and placeholders.
type EditableDocument struct {
	Agents    EditableAgentsConfig    `json:"agents"`
	Session   EditableSessionConfig   `json:"session,omitempty"`
	Bindings  []AgentBinding          `json:"bindings,omitempty"`
	Channels  EditableChannelsConfig  `json:"channels"`
	Providers EditableProvidersConfig `json:"providers"`
	Gateway   GatewayConfig           `json:"gateway"`
	Tools     EditableToolsConfig     `json:"tools"`
	Heartbeat HeartbeatConfig         `json:"heartbeat"`
	Devices   DevicesConfig           `json:"devices"`
	Logs      EditableLogsConfig      `json:"logs"`
}

// EditableAgentsConfig represents agents in editable mode.
type EditableAgentsConfig struct {
	Defaults EditableAgentDefaults `json:"defaults"`
	List     []EditableAgentConfig `json:"list,omitempty"`
}

// EditableAgentDefaults represents agent defaults in editable mode.
type EditableAgentDefaults struct {
	Workspace           string   `json:"workspace"`
	RestrictToWorkspace bool     `json:"restrict_to_workspace"`
	Provider            string   `json:"provider"`
	Model               string   `json:"model"`
	ModelFallbacks      []string `json:"model_fallbacks,omitempty"`
	ImageModel          string   `json:"image_model,omitempty"`
	ImageModelFallbacks []string `json:"image_model_fallbacks,omitempty"`
	MaxTokens           int      `json:"max_tokens"`
	Temperature         *float64 `json:"temperature,omitempty"`
	MaxToolIterations   int      `json:"max_tool_iterations"`
}

// EditableAgentConfig represents an agent in editable mode.
type EditableAgentConfig struct {
	ID          string            `json:"id"`
	Default     bool              `json:"default,omitempty"`
	Name        string            `json:"name,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
	Skills      []string          `json:"skills,omitempty"`
	Subagents   *SubagentsConfig  `json:"subagents,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
}

// EditableSessionConfig represents session in editable mode.
type EditableSessionConfig struct {
	DMScope            string              `json:"dm_scope,omitempty"`
	IdentityLinks      map[string][]string `json:"identity_links,omitempty"`
	Ephemeral          bool                `json:"ephemeral"`
	EphemeralThreshold int                 `json:"ephemeral_threshold"`
}

// EditableChannelsConfig represents channels in editable mode.
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
}

// EditableWhatsAppConfig for WhatsApp.
type EditableWhatsAppConfig struct {
	Enabled   bool                `json:"enabled"`
	BridgeURL string              `json:"bridge_url"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

// EditableTelegramConfig for Telegram.
type EditableTelegramConfig struct {
	Enabled   bool                `json:"enabled"`
	Token     SecretValue         `json:"token"`
	Proxy     string              `json:"proxy,omitempty"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
	Verbose   VerboseLevel        `json:"verbose,omitempty"`
}

// EditableFeishuConfig for Feishu.
type EditableFeishuConfig struct {
	Enabled           bool                `json:"enabled"`
	AppID             SecretValue         `json:"app_id"`
	AppSecret         SecretValue         `json:"app_secret"`
	EncryptKey        SecretValue         `json:"encrypt_key"`
	VerificationToken SecretValue         `json:"verification_token"`
	AllowFrom         FlexibleStringSlice `json:"allow_from"`
}

// EditableDiscordConfig for Discord.
type EditableDiscordConfig struct {
	Enabled   bool                `json:"enabled"`
	Token     SecretValue         `json:"token"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

// EditableMaixCamConfig for MaixCam.
type EditableMaixCamConfig struct {
	Enabled   bool                `json:"enabled"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

// EditableQQConfig for QQ.
type EditableQQConfig struct {
	Enabled   bool                `json:"enabled"`
	AppID     SecretValue         `json:"app_id"`
	AppSecret SecretValue         `json:"app_secret"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

// EditableDingTalkConfig for DingTalk.
type EditableDingTalkConfig struct {
	Enabled      bool                `json:"enabled"`
	ClientID     SecretValue         `json:"client_id"`
	ClientSecret SecretValue         `json:"client_secret"`
	AllowFrom    FlexibleStringSlice `json:"allow_from"`
}

// EditableSlackConfig for Slack.
type EditableSlackConfig struct {
	Enabled   bool                `json:"enabled"`
	BotToken  SecretValue         `json:"bot_token"`
	AppToken  SecretValue         `json:"app_token"`
	AllowFrom FlexibleStringSlice `json:"allow_from"`
}

// EditableLINEConfig for LINE.
type EditableLINEConfig struct {
	Enabled            bool                `json:"enabled"`
	ChannelSecret      SecretValue         `json:"channel_secret"`
	ChannelAccessToken SecretValue         `json:"channel_access_token"`
	WebhookHost        string              `json:"webhook_host"`
	WebhookPort        int                 `json:"webhook_port"`
	WebhookPath        string              `json:"webhook_path"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"`
}

// EditableOneBotConfig for OneBot.
type EditableOneBotConfig struct {
	Enabled            bool                `json:"enabled"`
	WSUrl              string              `json:"ws_url"`
	AccessToken        SecretValue         `json:"access_token"`
	ReconnectInterval  int                 `json:"reconnect_interval"`
	GroupTriggerPrefix []string            `json:"group_trigger_prefix"`
	AllowFrom          FlexibleStringSlice `json:"allow_from"`
}

// EditableNativeConfig for Native channel.
type EditableNativeConfig struct {
	Enabled           bool     `json:"enabled"`
	Host              string   `json:"host"`
	Port              int      `json:"port"`
	TokenExpiryDays   int      `json:"token_expiry_days"`
	PinExpiryMinutes  int      `json:"pin_expiry_minutes"`
	MaxClients        int      `json:"max_clients"`
	CORSOrigins       []string `json:"cors_origins"`
	SessionExpiryDays int      `json:"session_expiry_days"`
	MaxUploadSizeMB   int64    `json:"max_upload_size_mb"`
	UploadTTLHours    int      `json:"upload_ttl_hours"`
}

// EditableProvidersConfig for providers.
// It serializes as a flat object to preserve config.json compatibility.
type EditableProvidersConfig map[string]EditableNamedProviderConfig

// EditableNamedProviderConfig for named providers.
type EditableNamedProviderConfig struct {
	Type        string                         `json:"type,omitempty"`
	APIKey      SecretValue                    `json:"api_key"`
	APIBase     string                         `json:"api_base"`
	Proxy       string                         `json:"proxy,omitempty"`
	AuthMethod  string                         `json:"auth_method,omitempty"`
	ConnectMode string                         `json:"connect_mode,omitempty"`
	WebSearch   *bool                          `json:"web_search,omitempty"`
	Models      map[string]ProviderModelConfig `json:"models,omitempty"`
}

// EditableToolsConfig for tools.
type EditableToolsConfig struct {
	Web  EditableWebToolsConfig `json:"web"`
	Cron CronToolsConfig        `json:"cron"`
	Exec EditableExecConfig     `json:"exec"`
}

// EditableWebToolsConfig for web tools.
type EditableWebToolsConfig struct {
	Brave      EditableBraveConfig      `json:"brave"`
	DuckDuckGo DuckDuckGoConfig         `json:"duckduckgo"`
	Perplexity EditablePerplexityConfig `json:"perplexity"`
}

// EditableBraveConfig for Brave.
type EditableBraveConfig struct {
	Enabled    bool        `json:"enabled"`
	APIKey     SecretValue `json:"api_key"`
	MaxResults int         `json:"max_results"`
}

// EditablePerplexityConfig for Perplexity.
type EditablePerplexityConfig struct {
	Enabled    bool        `json:"enabled"`
	APIKey     SecretValue `json:"api_key"`
	MaxResults int         `json:"max_results"`
}

// EditableExecConfig for exec.
type EditableExecConfig struct {
	EnableDenyPatterns bool     `json:"enable_deny_patterns"`
	CustomDenyPatterns []string `json:"custom_deny_patterns"`
}

// EditableLogsConfig for logs.
type EditableLogsConfig struct {
	Enabled  bool   `json:"enabled"`
	Path     string `json:"path,omitempty"`
	MaxDays  int    `json:"max_days,omitempty"`
	Rotation string `json:"rotation,omitempty"`
}

// DocumentMetadata contains document metadata.
type DocumentMetadata struct {
	ConfigPath              string            `json:"config_path"`
	Source                  string            `json:"source"` // "file"
	CanSave                 bool              `json:"can_save"`
	RestartRequiredSections []string          `json:"restart_required_sections"`
	SecretsByPath           map[string]string `json:"secrets_by_path"` // path -> mode
}

// ValidationError represents a validation error.
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

// resolve returns the real value of a SecretValue.
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
	default:
		return ""
	}
}
