package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/caarlos0/env/v11"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	var ss []string
	if err := json.Unmarshal(data, &ss); err == nil {
		*f = ss
		return nil
	}

	var raw []interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	result := make([]string, 0, len(raw))
	for _, v := range raw {
		switch val := v.(type) {
		case string:
			result = append(result, val)
		case float64:
			result = append(result, fmt.Sprintf("%.0f", val))
		default:
			result = append(result, fmt.Sprintf("%v", val))
		}
	}
	*f = result
	return nil
}

const (
	StrategyRoundRobin = "round_robin"
	StrategyMoA        = "moa"
	StrategyModerator  = "moderator"
	StrategyPipeline   = "pipeline"
)

var validStrategies = map[string]bool{
	StrategyRoundRobin: true,
	StrategyMoA:        true,
	StrategyModerator:  true,
	StrategyPipeline:   true,
}

func ValidStrategy(s string) bool {
	return validStrategies[s]
}

type GroupsConfig struct {
	Enabled bool           `json:"enabled"`
	List    []GroupProfile `json:"list,omitempty"`
}

type GroupProfile struct {
	ID               string   `json:"id"`
	Participants     []string `json:"participants"`
	Strategy         string   `json:"strategy"`
	Rounds           int      `json:"rounds,omitempty"`
	Moderator        string   `json:"moderator,omitempty"`
	MaxTurns         int      `json:"max_turns,omitempty"`
	MaxTokensPerTurn int      `json:"max_tokens_per_turn,omitempty"`
	TotalTokenBudget int      `json:"total_token_budget,omitempty"`
	StopKeywords     []string `json:"stop_keywords,omitempty"`
	Parallel         bool     `json:"parallel,omitempty"`
}

func (g *GroupProfile) Validate() error {
	if g.ID == "" {
		return fmt.Errorf("groups: group ID is required")
	}
	if len(g.Participants) == 0 {
		return fmt.Errorf("groups[%q]: participants list is empty", g.ID)
	}
	if !validStrategies[g.Strategy] {
		return fmt.Errorf("groups[%q]: invalid strategy %q (must be one of: %s, %s, %s, %s)",
			g.ID, g.Strategy, StrategyRoundRobin, StrategyMoA, StrategyModerator, StrategyPipeline)
	}
	if g.Moderator != "" {
		found := false
		for _, p := range g.Participants {
			if p == g.Moderator {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("groups[%q]: moderator %q is not in participants list", g.ID, g.Moderator)
		}
	}
	return nil
}

func (gc *GroupsConfig) ValidateGroups() error {
	for i := range gc.List {
		if err := gc.List[i].Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) GroupsFeatureEnabled() bool {
	return c != nil && c.Groups.Enabled
}

type Config struct {
	Agents    AgentsConfig     `json:"agents"`
	Bindings  []AgentBinding   `json:"bindings,omitempty"`
	Groups    GroupsConfig     `json:"groups,omitempty"`
	Session   SessionConfig    `json:"session,omitempty"`
	Channels  ChannelsConfig   `json:"channels"`
	Providers *ProvidersConfig `json:"providers,omitempty"`
	Gateway   GatewayConfig    `json:"gateway"`
	Server    ServerConfig     `json:"server,omitempty"`
	Tools     ToolsConfig      `json:"tools"`
	Heartbeat HeartbeatConfig  `json:"heartbeat"`
	Devices   DevicesConfig    `json:"devices"`
	Logs      LogsConfig       `json:"logs"`
	Keyring   KeyringConfig    `json:"keyring"`
	Updates   UpdatesConfig    `json:"updates"`
	Goal      GoalConfig       `json:"goal"`
	Language  string           `json:"language,omitempty" env:"LELE_LANG"`
	TUI       TUIConfig        `json:"tui,omitempty"`

	Commands map[string]CommandDefinition `json:"commands,omitempty"`
	Harness  HarnessConfig                `json:"harness,omitempty"`

	mu     sync.RWMutex
	rawDoc map[string]json.RawMessage
	base   *Config
}

type CommandDefinition struct {
	Description        string `json:"description,omitempty"`
	Agent              string `json:"agent,omitempty"`
	Model              string `json:"model,omitempty"`
	Template           string `json:"template"`
	AllowShell         bool   `json:"allow_shell,omitempty"`
	AllowAbsoluteFiles *bool  `json:"allow_absolute_files,omitempty"`
}

type HarnessConfig struct {
	AllowShell         bool `json:"allow_shell,omitempty"`
	AllowAbsoluteFiles bool `json:"allow_absolute_files,omitempty"`
}

type TUIConfig struct {
	MouseEnabled        bool `json:"mouse_enabled"`
	MaxRenderedMessages int  `json:"max_rendered_messages"`
	StreamThrottleMS    int  `json:"stream_throttle_ms"`
	KeepAwake           bool `json:"keep_awake,omitempty"`
	OnboardingCompleted bool `json:"onboarding_completed,omitempty"`
}

func (c *Config) clone() *Config {
	if c == nil {
		return nil
	}
	data, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var cloned Config
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil
	}
	cloned.rawDoc = c.rawDoc
	cloned.base = c.base
	return &cloned
}

type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
	List     []AgentConfig `json:"list,omitempty"`
}

type AgentModelConfig struct {
	Primary   string   `json:"primary,omitempty"`
	Fallbacks []string `json:"fallbacks,omitempty"`
}

func (m *AgentModelConfig) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Primary = s
		m.Fallbacks = nil
		return nil
	}
	type raw struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	m.Primary = r.Primary
	m.Fallbacks = r.Fallbacks
	return nil
}

func (m AgentModelConfig) MarshalJSON() ([]byte, error) {
	type raw struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
	}
	return json.Marshal(raw(m))
}

type AgentConfig struct {
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

type SubagentsConfig struct {
	AllowAgents   []string          `json:"allow_agents,omitempty"`
	Model         *AgentModelConfig `json:"model,omitempty"`
	TimeoutMin    int               `json:"timeout_minutes,omitempty"`
	MaxConcurrent int               `json:"max_concurrent,omitempty"`
	MaxIterations int               `json:"max_iterations,omitempty"`
}

type PeerMatch struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type BindingMatch struct {
	Channel   string     `json:"channel"`
	AccountID string     `json:"account_id,omitempty"`
	Peer      *PeerMatch `json:"peer,omitempty"`
	GuildID   string     `json:"guild_id,omitempty"`
	TeamID    string     `json:"team_id,omitempty"`
}

type AgentBinding struct {
	AgentID string       `json:"agent_id"`
	Match   BindingMatch `json:"match"`
}

type SessionConfig struct {
	DMScope                    string              `json:"dm_scope,omitempty"`
	IdentityLinks              map[string][]string `json:"identity_links,omitempty"`
	Ephemeral                  bool                `json:"ephemeral"`
	EphemeralThreshold         int                 `json:"ephemeral_threshold"`
	CompactionThresholdPercent int                 `json:"compaction_threshold_percent,omitempty"`
	CompactionModel            string              `json:"compaction_model,omitempty"`
	EvictExcludedFromMemory    bool                `json:"evict_excluded_from_memory"`
	DurableInbound             *bool               `json:"durable_inbound,omitempty"`
	DurableOutbound            *bool               `json:"durable_outbound,omitempty"`
	Resume                     *bool               `json:"resume_enabled,omitempty"`
}

func (s SessionConfig) DurableInboundEnabled() bool {
	return s.DurableInbound != nil && *s.DurableInbound
}

func (s SessionConfig) DurableOutboundEnabled() bool {
	return s.DurableOutbound != nil && *s.DurableOutbound
}

func (s SessionConfig) ResumeEnabled() bool {
	return s.Resume != nil && *s.Resume
}

const DefaultEphemeralThresholdSeconds = 560

const DefaultCompactionThresholdPercent = 75

const DefaultMaxRequestBytes = 8 << 20

const SessionEphemeralFileDefault = false

type AgentDefaults struct {
	Workspace                string            `json:"workspace" env:"LELE_AGENTS_DEFAULTS_WORKSPACE"`
	RestrictToWorkspace      bool              `json:"restrict_to_workspace" env:"LELE_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	Provider                 string            `json:"provider" env:"LELE_AGENTS_DEFAULTS_PROVIDER"`
	Model                    string            `json:"model" env:"LELE_AGENTS_DEFAULTS_MODEL"`
	ModelFallbacks           []string          `json:"model_fallbacks,omitempty"`
	ImageModel               string            `json:"image_model,omitempty" env:"LELE_AGENTS_DEFAULTS_IMAGE_MODEL"`
	ImageModelFallbacks      []string          `json:"image_model_fallbacks,omitempty"`
	MaxTokens                int               `json:"max_tokens" env:"LELE_AGENTS_DEFAULTS_MAX_TOKENS"`
	Temperature              *float64          `json:"temperature,omitempty" env:"LELE_AGENTS_DEFAULTS_TEMPERATURE"`
	ThinkingLevel            *string           `json:"thinking_level,omitempty" env:"LELE_AGENTS_DEFAULTS_THINKING_LEVEL"`
	MaxToolIterations        int               `json:"max_tool_iterations" env:"LELE_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
	MaxReadLines             int               `json:"max_read_lines" env:"LELE_AGENTS_DEFAULTS_MAX_READ_LINES"`
	MemoryMaxLines           int               `json:"memory_max_lines" env:"LELE_AGENTS_DEFAULTS_MEMORY_MAX_LINES"`
	SubagentTimeoutMinutes   int               `json:"subagent_timeout_minutes" env:"LELE_AGENTS_DEFAULTS_SUBAGENT_TIMEOUT_MINUTES"`
	SubagentMaxConcurrent    int               `json:"subagent_max_concurrent" env:"LELE_AGENTS_DEFAULTS_SUBAGENT_MAX_CONCURRENT"`
	SubagentMaxRetries       int               `json:"subagent_max_retries" env:"LELE_AGENTS_DEFAULTS_SUBAGENT_MAX_RETRIES"`
	SubagentMaxIterations    int               `json:"subagent_max_iterations" env:"LELE_AGENTS_DEFAULTS_SUBAGENT_MAX_ITERATIONS"`
	SubagentRetentionMinutes int               `json:"subagent_retention_minutes" env:"LELE_AGENTS_DEFAULTS_SUBAGENT_RETENTION_MINUTES"`
	LLMLoopTimeoutMinutes    int               `json:"llm_loop_timeout_minutes" env:"LELE_AGENTS_DEFAULTS_LLM_LOOP_TIMEOUT_MINUTES"`
	PromptCache              PromptCacheConfig `json:"prompt_cache,omitempty"`
}

type PromptCacheConfig struct {
	Enabled bool   `json:"enabled,omitempty" env:"LELE_AGENTS_DEFAULTS_PROMPT_CACHE_ENABLED"`
	TTL     string `json:"ttl,omitempty" env:"LELE_AGENTS_DEFAULTS_PROMPT_CACHE_TTL"`
}

func (p PromptCacheConfig) NormalizedTTL() string {
	if strings.EqualFold(strings.TrimSpace(p.TTL), "1h") {
		return "1h"
	}
	return "5m"
}

type ChannelsConfig struct {
	WhatsApp WhatsAppConfig `json:"whatsapp"`
	Telegram TelegramConfig `json:"telegram"`
	Feishu   FeishuConfig   `json:"feishu"`
	Discord  DiscordConfig  `json:"discord"`
	MaixCam  MaixCamConfig  `json:"maixcam"`
	QQ       QQConfig       `json:"qq"`
	DingTalk DingTalkConfig `json:"dingtalk"`
	Slack    SlackConfig    `json:"slack"`
	LINE     LINEConfig     `json:"line"`
	OneBot   OneBotConfig   `json:"onebot"`
	Native   NativeConfig   `json:"native"`
	Web      WebConfig      `json:"web"`
}

type NativeRateLimitConfig struct {
	Enabled             bool `json:"enabled" env:"LELE_CHANNELS_NATIVE_RATE_LIMIT_ENABLED"`
	PinPerMinute        int  `json:"pin_per_minute" env:"LELE_CHANNELS_NATIVE_RATE_LIMIT_PIN_PER_MINUTE"`
	PairPerMinute       int  `json:"pair_per_minute" env:"LELE_CHANNELS_NATIVE_RATE_LIMIT_PAIR_PER_MINUTE"`
	RefreshPerMinute    int  `json:"refresh_per_minute" env:"LELE_CHANNELS_NATIVE_RATE_LIMIT_REFRESH_PER_MINUTE"`
	APIPerMinute        int  `json:"api_per_minute" env:"LELE_CHANNELS_NATIVE_RATE_LIMIT_API_PER_MINUTE"`
	WSMessagesPerMinute int  `json:"ws_messages_per_minute" env:"LELE_CHANNELS_NATIVE_RATE_LIMIT_WS_MESSAGES_PER_MINUTE"`
}

const (
	DefaultNativeRateLimitPinPerMinute        = 10
	DefaultNativeRateLimitPairPerMinute       = 5
	DefaultNativeRateLimitRefreshPerMinute    = 20
	DefaultNativeRateLimitAPIPerMinute        = 120
	DefaultNativeRateLimitWSMessagesPerMinute = 120
)

type NativeConfig struct {
	Enabled           bool                  `json:"enabled" env:"LELE_CHANNELS_NATIVE_ENABLED"`
	Host              string                `json:"host" env:"LELE_CHANNELS_NATIVE_HOST"`
	Port              int                   `json:"port" env:"LELE_CHANNELS_NATIVE_PORT"`
	TokenExpiryDays   int                   `json:"token_expiry_days" env:"LELE_CHANNELS_NATIVE_TOKEN_EXPIRY_DAYS"`
	PinExpiryMinutes  int                   `json:"pin_expiry_minutes" env:"LELE_CHANNELS_NATIVE_PIN_EXPIRY_MINUTES"`
	MaxClients        int                   `json:"max_clients" env:"LELE_CHANNELS_NATIVE_MAX_CLIENTS"`
	CORSOrigins       []string              `json:"cors_origins" env:"LELE_CHANNELS_NATIVE_CORS_ORIGINS"`
	SessionExpiryDays int                   `json:"session_expiry_days" env:"LELE_CHANNELS_NATIVE_SESSION_EXPIRY_DAYS"`
	MaxUploadSizeMB   int64                 `json:"max_upload_size_mb" env:"LELE_CHANNELS_NATIVE_MAX_UPLOAD_SIZE_MB"`
	UploadTTLHours    int                   `json:"upload_ttl_hours" env:"LELE_CHANNELS_NATIVE_UPLOAD_TTL_HOURS"`
	LeleDir           string                `json:"lele_dir,omitempty" env:"LELE_CHANNELS_NATIVE_LELE_DIR"`
	RateLimit         NativeRateLimitConfig `json:"rate_limit"`
}

type WebConfig struct {
	Enabled bool `json:"enabled" env:"LELE_WEB_ENABLED"`
}

type WhatsAppConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_WHATSAPP_ENABLED"`
	BridgeURL string              `json:"bridge_url" env:"LELE_CHANNELS_WHATSAPP_BRIDGE_URL"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_WHATSAPP_ALLOW_FROM"`
}

type VerboseLevel string

const (
	VerboseOff   VerboseLevel = "off"
	VerboseBasic VerboseLevel = "basic"
	VerboseFull  VerboseLevel = "full"
)

type TelegramConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_TELEGRAM_ENABLED"`
	Token     string              `json:"token" env:"LELE_CHANNELS_TELEGRAM_TOKEN"`
	Proxy     string              `json:"proxy" env:"LELE_CHANNELS_TELEGRAM_PROXY"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_TELEGRAM_ALLOW_FROM"`
	Verbose   VerboseLevel        `json:"verbose,omitempty" env:"LELE_CHANNELS_TELEGRAM_VERBOSE"`
}

type FeishuConfig struct {
	Enabled           bool                `json:"enabled" env:"LELE_CHANNELS_FEISHU_ENABLED"`
	AppID             string              `json:"app_id" env:"LELE_CHANNELS_FEISHU_APP_ID"`
	AppSecret         string              `json:"app_secret" env:"LELE_CHANNELS_FEISHU_APP_SECRET"`
	EncryptKey        string              `json:"encrypt_key" env:"LELE_CHANNELS_FEISHU_ENCRYPT_KEY"`
	VerificationToken string              `json:"verification_token" env:"LELE_CHANNELS_FEISHU_VERIFICATION_TOKEN"`
	AllowFrom         FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_FEISHU_ALLOW_FROM"`
}

type DiscordConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_DISCORD_ENABLED"`
	Token     string              `json:"token" env:"LELE_CHANNELS_DISCORD_TOKEN"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_DISCORD_ALLOW_FROM"`
}

type MaixCamConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_MAIXCAM_ENABLED"`
	Host      string              `json:"host" env:"LELE_CHANNELS_MAIXCAM_HOST"`
	Port      int                 `json:"port" env:"LELE_CHANNELS_MAIXCAM_PORT"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_MAIXCAM_ALLOW_FROM"`
}

type QQConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_QQ_ENABLED"`
	AppID     string              `json:"app_id" env:"LELE_CHANNELS_QQ_APP_ID"`
	AppSecret string              `json:"app_secret" env:"LELE_CHANNELS_QQ_APP_SECRET"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_QQ_ALLOW_FROM"`
}

type DingTalkConfig struct {
	Enabled      bool                `json:"enabled" env:"LELE_CHANNELS_DINGTALK_ENABLED"`
	ClientID     string              `json:"client_id" env:"LELE_CHANNELS_DINGTALK_CLIENT_ID"`
	ClientSecret string              `json:"client_secret" env:"LELE_CHANNELS_DINGTALK_CLIENT_SECRET"`
	AllowFrom    FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_DINGTALK_ALLOW_FROM"`
}

type SlackConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_SLACK_ENABLED"`
	BotToken  string              `json:"bot_token" env:"LELE_CHANNELS_SLACK_BOT_TOKEN"`
	AppToken  string              `json:"app_token" env:"LELE_CHANNELS_SLACK_APP_TOKEN"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_SLACK_ALLOW_FROM"`
}

type LINEConfig struct {
	Enabled            bool                `json:"enabled" env:"LELE_CHANNELS_LINE_ENABLED"`
	ChannelSecret      string              `json:"channel_secret" env:"LELE_CHANNELS_LINE_CHANNEL_SECRET"`
	ChannelAccessToken string              `json:"channel_access_token" env:"LELE_CHANNELS_LINE_CHANNEL_ACCESS_TOKEN"`
	WebhookHost        string              `json:"webhook_host" env:"LELE_CHANNELS_LINE_WEBHOOK_HOST"`
	WebhookPort        int                 `json:"webhook_port" env:"LELE_CHANNELS_LINE_WEBHOOK_PORT"`
	WebhookPath        string              `json:"webhook_path" env:"LELE_CHANNELS_LINE_WEBHOOK_PATH"`
	AllowFrom          FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_LINE_ALLOW_FROM"`
}

type OneBotConfig struct {
	Enabled            bool                `json:"enabled" env:"LELE_CHANNELS_ONEBOT_ENABLED"`
	WSUrl              string              `json:"ws_url" env:"LELE_CHANNELS_ONEBOT_WS_URL"`
	AccessToken        string              `json:"access_token" env:"LELE_CHANNELS_ONEBOT_ACCESS_TOKEN"`
	ReconnectInterval  int                 `json:"reconnect_interval" env:"LELE_CHANNELS_ONEBOT_RECONNECT_INTERVAL"`
	GroupTriggerPrefix []string            `json:"group_trigger_prefix" env:"LELE_CHANNELS_ONEBOT_GROUP_TRIGGER_PREFIX"`
	AllowFrom          FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_ONEBOT_ALLOW_FROM"`
}

type HeartbeatConfig struct {
	Enabled  bool `json:"enabled" env:"LELE_HEARTBEAT_ENABLED"`
	Interval int  `json:"interval" env:"LELE_HEARTBEAT_INTERVAL"`
}

type DevicesConfig struct {
	Enabled    bool `json:"enabled" env:"LELE_DEVICES_ENABLED"`
	MonitorUSB bool `json:"monitor_usb" env:"LELE_DEVICES_MONITOR_USB"`
}

type UpdatesConfig struct {
	Enabled bool   `json:"enabled" env:"LELE_UPDATES_ENABLED"`
	Channel string `json:"channel,omitempty" env:"LELE_UPDATES_CHANNEL"`
	Repo    string `json:"repo,omitempty" env:"LELE_UPDATES_REPO"`
}

type LogsConfig struct {
	Enabled  bool   `json:"enabled" env:"LELE_LOGS_ENABLED"`
	Path     string `json:"path,omitempty" env:"LELE_LOGS_PATH"`
	MaxDays  int    `json:"max_days,omitempty" env:"LELE_LOGS_MAX_DAYS"`
	Rotation string `json:"rotation,omitempty" env:"LELE_LOGS_ROTATION"`
}

type KeyringConfig struct {
	Enabled          bool   `json:"enabled" env:"LELE_KEYRING_ENABLED"`
	Path             string `json:"path,omitempty" env:"LELE_KEYRING_PATH"`
	Backend          string `json:"backend,omitempty" env:"LELE_KEYRING_BACKEND"`
	AuditLogSize     int    `json:"audit_log_size,omitempty" env:"LELE_KEYRING_AUDIT_LOG_SIZE"`
	AllowAgentSet    bool   `json:"allow_agent_set" env:"LELE_KEYRING_ALLOW_AGENT_SET"`
	AllowAgentDelete bool   `json:"allow_agent_delete" env:"LELE_KEYRING_ALLOW_AGENT_DELETE"`
}

type GoalConfig struct {
	Judge GoalJudgeConfig `json:"judge"`
}

type GoalJudgeConfig struct {
	Mode  string `json:"mode" env:"LELE_GOAL_JUDGE_MODE"`
	Agent string `json:"agent,omitempty" env:"LELE_GOAL_JUDGE_AGENT"`
}

type ProvidersConfig struct {
	Anthropic          ProviderConfig                 `json:"anthropic"`
	OpenAI             OpenAIProviderConfig           `json:"openai"`
	OpenRouter         ProviderConfig                 `json:"openrouter"`
	Groq               ProviderConfig                 `json:"groq"`
	Zhipu              ProviderConfig                 `json:"zhipu"`
	VLLM               ProviderConfig                 `json:"vllm"`
	Gemini             ProviderConfig                 `json:"gemini"`
	Nvidia             ProviderConfig                 `json:"nvidia"`
	Ollama             ProviderConfig                 `json:"ollama"`
	Moonshot           ProviderConfig                 `json:"moonshot"`
	ShengSuanYun       ProviderConfig                 `json:"shengsuanyun"`
	DeepSeek           ProviderConfig                 `json:"deepseek"`
	GitHubCopilot      ProviderConfig                 `json:"github_copilot"`
	NanogPT            ProviderConfig                 `json:"nanogpt"`
	AlibabaCodingPlan  ProviderConfig                 `json:"alibaba_coding_plan"`
	AlibabaTokenPlan   ProviderConfig                 `json:"alibaba_token_plan"`
	AlibabaTokenPlanCN ProviderConfig                 `json:"alibaba_token_plan_cn"`
	ZAICodingPlan      ProviderConfig                 `json:"zai_coding_plan"`
	ModelArkCodingPlan ProviderConfig                 `json:"modelark_coding_plan"`
	Named              map[string]NamedProviderConfig `json:"-"`
}

type ProviderConfig struct {
	APIKey      string            `json:"api_key" env:"LELE_PROVIDERS_{{.Name}}_API_KEY"`
	APIBase     string            `json:"api_base" env:"LELE_PROVIDERS_{{.Name}}_API_BASE"`
	Proxy       string            `json:"proxy,omitempty" env:"LELE_PROVIDERS_{{.Name}}_PROXY"`
	AuthMethod  string            `json:"auth_method,omitempty" env:"LELE_PROVIDERS_{{.Name}}_AUTH_METHOD"`
	ConnectMode string            `json:"connect_mode,omitempty" env:"LELE_PROVIDERS_{{.Name}}_CONNECT_MODE"`
	Headers     map[string]string `json:"headers,omitempty"`
}

type OpenAIProviderConfig struct {
	ProviderConfig
	WebSearch bool `json:"web_search" env:"LELE_PROVIDERS_OPENAI_WEB_SEARCH"`
}

type ReasoningConfig struct {
	Effort    *string `json:"effort,omitempty"`
	MaxTokens *int    `json:"max_tokens,omitempty"`
	Exclude   *bool   `json:"exclude,omitempty"`
	Summary   *string `json:"summary,omitempty"`
	Enable    bool    `json:"enable,omitempty"`
}

// ThinkingLevels is the ordered set of thinking levels accepted by
// NormalizeThinkingLevel and cycled by /think:
// off → low → medium → high → xhigh → max → off.
// "xhigh" is the extra-high effort tier (e.g. GPT-5.1-Codex-Max) and "max" the
// maximum tier (e.g. Claude Opus 4.6 adaptive thinking); both are passed
// through to providers that support them.
var ThinkingLevels = []string{"off", "low", "medium", "high", "xhigh", "max"}

func NormalizeThinkingLevel(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", "default", "none":
		return "", true
	}
	for _, level := range ThinkingLevels {
		if v == level {
			return v, true
		}
	}
	return "", false
}

func (r *ReasoningConfig) Validate() error {
	if r == nil {
		return nil
	}
	// Per-model efforts are the thinking levels minus "off", which is specific
	// to thinking_level / the /think override (it sends an explicit disable).
	validEfforts := make(map[string]bool, len(ThinkingLevels))
	for _, level := range ThinkingLevels {
		if level != "off" {
			validEfforts[level] = true
		}
	}
	validSummaries := map[string]bool{"auto": true, "detailed": true, "concise": true}

	if r.Effort != nil {
		if !validEfforts[strings.ToLower(*r.Effort)] {
			return fmt.Errorf("invalid reasoning effort: %q (must be low, medium, high, xhigh, or max)", *r.Effort)
		}
		low := strings.ToLower(*r.Effort)
		r.Effort = &low
	}
	if r.Summary != nil {
		if !validSummaries[strings.ToLower(*r.Summary)] {
			return fmt.Errorf("invalid reasoning summary: %q (must be auto, detailed, or concise)", *r.Summary)
		}
		low := strings.ToLower(*r.Summary)
		r.Summary = &low
	}
	if r.MaxTokens != nil && *r.MaxTokens < 1 {
		return fmt.Errorf("invalid reasoning max_tokens: %d (must be >= 1)", *r.MaxTokens)
	}
	return nil
}

type ProviderModelConfig struct {
	ContextWindow int              `json:"context_window,omitempty"`
	Model         string           `json:"model,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	Vision        bool             `json:"vision,omitempty"`
	Video         bool             `json:"video,omitempty"`
	Reasoning     *ReasoningConfig `json:"reasoning,omitempty"`
	ThinkingType  string           `json:"thinking_type,omitempty"`
}

const (
	ThinkingTypeAuto       = "auto"
	ThinkingTypeDeepSeek   = "deepseek"
	ThinkingTypeOpenAI     = "openai"
	ThinkingTypeOpenRouter = "openrouter"
	ThinkingTypeQwen       = "qwen"
	ThinkingTypeNone       = "none"
)

func NormalizeThinkingType(v string) (string, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "", ThinkingTypeAuto:
		return ThinkingTypeAuto, true
	case ThinkingTypeDeepSeek, ThinkingTypeOpenAI, ThinkingTypeOpenRouter, ThinkingTypeQwen, ThinkingTypeNone:
		return v, true
	default:
		return "", false
	}
}

func (p *ProviderModelConfig) Validate() error {
	if p.Reasoning != nil {
		if err := p.Reasoning.Validate(); err != nil {
			return fmt.Errorf("model %q: %w", p.Model, err)
		}
	}
	if _, ok := NormalizeThinkingType(p.ThinkingType); !ok {
		return fmt.Errorf("model %q: invalid thinking_type %q (valid: auto, deepseek, openai, openrouter, qwen, none)", p.Model, p.ThinkingType)
	}
	return nil
}

type NamedProviderConfig struct {
	Type string `json:"type,omitempty"`
	ProviderConfig
	WebSearch *bool                          `json:"web_search,omitempty"`
	Models    map[string]ProviderModelConfig `json:"models,omitempty"`
}

func (p *ProvidersConfig) UnmarshalJSON(data []byte) error {
	if p == nil {
		return nil
	}
	type alias ProvidersConfig
	aux := (*alias)(p)
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if p.Named == nil {
		p.Named = map[string]NamedProviderConfig{}
	}
	for key, val := range raw {
		name := strings.ToLower(strings.TrimSpace(key))
		named := NamedProviderConfig{}
		if err := json.Unmarshal(val, &named); err != nil {
			return fmt.Errorf("invalid provider config %q: %w", key, err)
		}
		if named.Type == "" {
			named.Type = name
		}
		for modelName, modelCfg := range named.Models {
			if err := modelCfg.Validate(); err != nil {
				return fmt.Errorf("provider %q, model %q: %w", name, modelName, err)
			}
		}
		if err := protocoltypes.ValidateCustomHeaders(named.Headers); err != nil {
			return fmt.Errorf("provider %q: %w", name, err)
		}
		p.Named[name] = named
	}

	p.ensureNamedDefaults()
	return nil
}

func (p *ProvidersConfig) MarshalJSON() ([]byte, error) {
	if p == nil {
		return []byte("null"), nil
	}
	out := map[string]NamedProviderConfig{}
	hasData := false
	for k, v := range p.Named {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		if v.Type == "" {
			v.Type = key
		}
		if !v.IsEmpty() {
			out[key] = v
			hasData = true
		}
	}

	put := func(name string, cfg NamedProviderConfig) {
		key := strings.ToLower(name)
		if _, exists := out[key]; exists {
			return
		}
		if cfg.IsEmpty() {
			return
		}
		if cfg.Type == "" {
			cfg.Type = key
		}
		out[key] = cfg
		hasData = true
	}

	ws := p.OpenAI.WebSearch
	put("anthropic", NamedProviderConfig{Type: "anthropic", ProviderConfig: p.Anthropic})
	put("openai", NamedProviderConfig{Type: "openai", ProviderConfig: p.OpenAI.ProviderConfig, WebSearch: &ws})
	put("openrouter", NamedProviderConfig{Type: "openrouter", ProviderConfig: p.OpenRouter})
	put("groq", NamedProviderConfig{Type: "groq", ProviderConfig: p.Groq})
	put("zhipu", NamedProviderConfig{Type: "zhipu", ProviderConfig: p.Zhipu})
	put("vllm", NamedProviderConfig{Type: "vllm", ProviderConfig: p.VLLM})
	put("gemini", NamedProviderConfig{Type: "gemini", ProviderConfig: p.Gemini})
	put("nvidia", NamedProviderConfig{Type: "nvidia", ProviderConfig: p.Nvidia})
	put("ollama", NamedProviderConfig{Type: "ollama", ProviderConfig: p.Ollama})
	put("moonshot", NamedProviderConfig{Type: "moonshot", ProviderConfig: p.Moonshot})
	put("shengsuanyun", NamedProviderConfig{Type: "shengsuanyun", ProviderConfig: p.ShengSuanYun})
	put("deepseek", NamedProviderConfig{Type: "deepseek", ProviderConfig: p.DeepSeek})
	put("github_copilot", NamedProviderConfig{Type: "github_copilot", ProviderConfig: p.GitHubCopilot})
	put("nanogpt", NamedProviderConfig{Type: "nanogpt", ProviderConfig: p.NanogPT})
	put("alibaba_coding_plan", NamedProviderConfig{Type: "alibaba_coding_plan", ProviderConfig: p.AlibabaCodingPlan})
	put("alibaba_token_plan", NamedProviderConfig{Type: "alibaba_token_plan", ProviderConfig: p.AlibabaTokenPlan})
	put("alibaba_token_plan_cn", NamedProviderConfig{Type: "alibaba_token_plan_cn", ProviderConfig: p.AlibabaTokenPlanCN})
	put("zai_coding_plan", NamedProviderConfig{Type: "zai_coding_plan", ProviderConfig: p.ZAICodingPlan})
	put("modelark_coding_plan", NamedProviderConfig{Type: "modelark_coding_plan", ProviderConfig: p.ModelArkCodingPlan})

	if !hasData {
		return []byte("null"), nil
	}

	return json.Marshal(out)
}

func (p NamedProviderConfig) IsEmpty() bool {
	return p.APIKey == "" &&
		p.APIBase == "" &&
		p.Proxy == "" &&
		p.AuthMethod == "" &&
		p.ConnectMode == "" &&
		len(p.Headers) == 0 &&
		len(p.Models) == 0
}

func (p *ProvidersConfig) ensureNamedDefaults() {
	if p == nil {
		return
	}
	if p.Named == nil {
		p.Named = map[string]NamedProviderConfig{}
	}
	put := func(name string, cfg NamedProviderConfig) {
		key := strings.ToLower(name)
		if existing, ok := p.Named[key]; ok {
			if existing.Type == "" {
				existing.Type = key
				p.Named[key] = existing
			}
			return
		}
		if cfg.Type == "" {
			cfg.Type = key
		}
		p.Named[key] = cfg
	}

	ws := p.OpenAI.WebSearch
	put("anthropic", NamedProviderConfig{Type: "anthropic", ProviderConfig: p.Anthropic})
	put("openai", NamedProviderConfig{Type: "openai", ProviderConfig: p.OpenAI.ProviderConfig, WebSearch: &ws})
	put("openrouter", NamedProviderConfig{Type: "openrouter", ProviderConfig: p.OpenRouter})
	put("groq", NamedProviderConfig{Type: "groq", ProviderConfig: p.Groq})
	put("zhipu", NamedProviderConfig{Type: "zhipu", ProviderConfig: p.Zhipu})
	put("vllm", NamedProviderConfig{Type: "vllm", ProviderConfig: p.VLLM})
	put("gemini", NamedProviderConfig{Type: "gemini", ProviderConfig: p.Gemini})
	put("nvidia", NamedProviderConfig{Type: "nvidia", ProviderConfig: p.Nvidia})
	put("ollama", NamedProviderConfig{Type: "ollama", ProviderConfig: p.Ollama})
	put("moonshot", NamedProviderConfig{Type: "moonshot", ProviderConfig: p.Moonshot})
	put("shengsuanyun", NamedProviderConfig{Type: "shengsuanyun", ProviderConfig: p.ShengSuanYun})
	put("deepseek", NamedProviderConfig{Type: "deepseek", ProviderConfig: p.DeepSeek})
	put("github_copilot", NamedProviderConfig{Type: "github_copilot", ProviderConfig: p.GitHubCopilot})
	put("nanogpt", NamedProviderConfig{Type: "nanogpt", ProviderConfig: p.NanogPT})
	put("alibaba_coding_plan", NamedProviderConfig{Type: "alibaba_coding_plan", ProviderConfig: p.AlibabaCodingPlan})
	put("alibaba_token_plan", NamedProviderConfig{Type: "alibaba_token_plan", ProviderConfig: p.AlibabaTokenPlan})
	put("alibaba_token_plan_cn", NamedProviderConfig{Type: "alibaba_token_plan_cn", ProviderConfig: p.AlibabaTokenPlanCN})
	put("zai_coding_plan", NamedProviderConfig{Type: "zai_coding_plan", ProviderConfig: p.ZAICodingPlan})
	put("modelark_coding_plan", NamedProviderConfig{Type: "modelark_coding_plan", ProviderConfig: p.ModelArkCodingPlan})
}

func (p *ProvidersConfig) GetNamed(name string) (NamedProviderConfig, bool) {
	if p == nil {
		return NamedProviderConfig{}, false
	}
	p.ensureNamedDefaults()
	cfg, ok := p.Named[strings.ToLower(strings.TrimSpace(name))]
	return cfg, ok
}

func (p *ProvidersConfig) ListNamed() map[string]NamedProviderConfig {
	if p == nil {
		return make(map[string]NamedProviderConfig)
	}
	p.ensureNamedDefaults()
	result := make(map[string]NamedProviderConfig, len(p.Named))
	for name, cfg := range p.Named {
		result[name] = cfg
	}
	return result
}

func (c *Config) HasUsableProvider() bool {
	if c.Providers == nil || len(c.Providers.Named) == 0 {
		return false
	}
	for _, p := range c.Providers.Named {
		hasKey := p.APIKey != ""
		isLocal := p.Type == "ollama"
		if (hasKey || isLocal) && len(p.Models) > 0 {
			return true
		}
	}
	return false
}

func (p *ProvidersConfig) resolveModelAliasInProvider(provider, model string, preferExact bool) (string, bool) {
	if p == nil {
		return "", false
	}
	if provider == "" {
		return "", false
	}

	named, ok := p.GetNamed(provider)
	if !ok || named.Models == nil {
		return "", false
	}

	if aliasCfg, found := named.Models[model]; found {
		resolved := strings.TrimSpace(aliasCfg.Model)
		if resolved != "" {
			return resolved, true
		}
		return model, true
	}

	normalizedModel := strings.ToLower(strings.ReplaceAll(model, ".", "-"))
	aliasCfg, found := named.Models[normalizedModel]
	if !found || strings.TrimSpace(aliasCfg.Model) == "" {
		for _, aliasVal := range named.Models {
			resolvedVal := strings.ToLower(strings.TrimSpace(aliasVal.Model))
			if resolvedVal == normalizedModel || strings.HasSuffix(resolvedVal, "/"+normalizedModel) {
				aliasCfg = aliasVal
				found = true
				break
			}
		}
	}

	if !found {
		return "", false
	}

	resolved := strings.TrimSpace(aliasCfg.Model)
	if resolved == "" {
		return model, true
	}
	return resolved, true
}

func (p *ProvidersConfig) ResolveModelAlias(rawModel, defaultProvider string) string {
	if p == nil {
		return rawModel
	}
	rawModel = strings.TrimSpace(rawModel)
	if rawModel == "" {
		return rawModel
	}

	provider := normalizeProviderKey(defaultProvider)
	pattern := provider + ":"
	if provider != "" && strings.HasPrefix(rawModel, pattern) {
		modelName := strings.TrimSpace(rawModel[len(pattern):])
		if modelName != "" {
			if resolved, found := p.resolveModelAliasInProvider(provider, modelName, true); found {
				return pattern + resolved
			}
		}
	}

	var model string
	idx := strings.Index(rawModel, ":")
	if idx > 0 {
		provider = normalizeProviderKey(rawModel[:idx])
		model = strings.TrimSpace(rawModel[idx+1:])
		if model == "" {
			return rawModel
		}
	} else {
		model = rawModel
	}

	if provider == "" {
		return rawModel
	}

	if resolved, found := p.resolveModelAliasInProvider(provider, model, false); found {
		return provider + ":" + resolved
	}

	p.ensureNamedDefaults()
	for provName := range p.Named {
		if provName == provider {
			continue
		}
		if resolved, found := p.resolveModelAliasInProvider(provName, model, false); found {
			return provName + ":" + resolved
		}
	}

	return rawModel
}

func normalizeProviderKey(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen", "zen":
		return "opencode"
	case "opencode-go":
		return "opencode_go"
	case "qwen":
		return "qwen-portal"
	case "alibaba-token-plan", "qwen-token-plan", "qwen-cloud-token-plan", "qwen_cloud_token_plan", "alibaba-token":
		return "alibaba_token_plan"
	case "alibaba-token-plan-cn", "qwen-token-plan-cn", "alibaba-token-cn":
		return "alibaba_token_plan_cn"
	case "kimi-code", "kimi-coding", "kimi-for-coding":
		return "kimi_for_coding"
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "glm":
		return "zhipu"
	case "google":
		return "gemini"
	case "grok", "x.ai", "x-ai":
		return "xai"
	case "nous-portal", "nousresearch":
		return "nous"
	case "lm-studio":
		return "lmstudio"
	case "step":
		return "stepfun"
	case "ai-gateway", "aigateway":
		return "vercel"
	case "hf":
		return "huggingface"
	case "novita-ai":
		return "novita"
	case "mimo":
		return "xiaomi"
	case "tokenhub", "tencent":
		return "tencent_tokenhub"
	case "gmi-cloud":
		return "gmi"
	case "togetherai":
		return "together"
	case "fireworks-ai":
		return "fireworks"
	case "ollama-cloud":
		return "ollama_cloud"
	case "amazon-bedrock", "aws":
		return "bedrock"
	case "azure-foundry":
		return "azure_foundry"
	}
	return p
}

type GatewayConfig struct {
	Host string `json:"host" env:"LELE_GATEWAY_HOST"`
	Port int    `json:"port" env:"LELE_GATEWAY_PORT"`
}

type ServerConfig struct {
	Host string `json:"host" env:"LELE_SERVER_HOST"`
	Port int    `json:"port" env:"LELE_SERVER_PORT"`
}

func (c *Config) EffectiveServerHost() string {
	if c.Server.Host != "" {
		return c.Server.Host
	}
	if c.Gateway.Host != "" {
		return c.Gateway.Host
	}
	if c.Channels.Native.Host != "" {
		return c.Channels.Native.Host
	}
	return "0.0.0.0"
}

func (c *Config) EffectiveServerPort() int {
	if c.Server.Port != 0 {
		return c.Server.Port
	}
	if c.Gateway.Port != 0 {
		return c.Gateway.Port
	}
	if c.Channels.Native.Port != 0 {
		return c.Channels.Native.Port
	}
	return 8080
}

type BraveConfig struct {
	Enabled    bool   `json:"enabled" env:"LELE_TOOLS_WEB_BRAVE_ENABLED"`
	APIKey     string `json:"api_key" env:"LELE_TOOLS_WEB_BRAVE_API_KEY"`
	MaxResults int    `json:"max_results" env:"LELE_TOOLS_WEB_BRAVE_MAX_RESULTS"`
}

type DuckDuckGoConfig struct {
	Enabled    bool `json:"enabled" env:"LELE_TOOLS_WEB_DUCKDUCKGO_ENABLED"`
	MaxResults int  `json:"max_results" env:"LELE_TOOLS_WEB_DUCKDUCKGO_MAX_RESULTS"`
}

type PerplexityConfig struct {
	Enabled    bool   `json:"enabled" env:"LELE_TOOLS_WEB_PERPLEXITY_ENABLED"`
	APIKey     string `json:"api_key" env:"LELE_TOOLS_WEB_PERPLEXITY_API_KEY"`
	MaxResults int    `json:"max_results" env:"LELE_TOOLS_WEB_PERPLEXITY_MAX_RESULTS"`
}

type SearXNGConfig struct {
	Enabled     bool   `json:"enabled" env:"LELE_TOOLS_WEB_SEARXNG_ENABLED"`
	InstanceURL string `json:"instance_url" env:"LELE_TOOLS_WEB_SEARXNG_INSTANCE_URL"`
	Categories  string `json:"categories" env:"LELE_TOOLS_WEB_SEARXNG_CATEGORIES"`
	Language    string `json:"language" env:"LELE_TOOLS_WEB_SEARXNG_LANGUAGE"`
	SafeSearch  int    `json:"safesearch" env:"LELE_TOOLS_WEB_SEARXNG_SAFESEARCH"`
	MaxResults  int    `json:"max_results" env:"LELE_TOOLS_WEB_SEARXNG_MAX_RESULTS"`
}

type WebToolsConfig struct {
	Brave      BraveConfig      `json:"brave"`
	DuckDuckGo DuckDuckGoConfig `json:"duckduckgo"`
	Perplexity PerplexityConfig `json:"perplexity"`
	SearXNG    SearXNGConfig    `json:"searxng"`
}

type CronToolsConfig struct {
	ExecTimeoutMinutes int `json:"exec_timeout_minutes" env:"LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES"`
}

type ExecConfig struct {
	EnableDenyPatterns bool     `json:"enable_deny_patterns" env:"LELE_TOOLS_EXEC_ENABLE_DENY_PATTERNS"`
	CustomDenyPatterns []string `json:"custom_deny_patterns" env:"LELE_TOOLS_EXEC_CUSTOM_DENY_PATTERNS"`
	TimeoutSeconds     int      `json:"timeout_seconds" env:"LELE_TOOLS_EXEC_TIMEOUT_SECONDS"`
	WhitelistCommands  []string `json:"whitelist_commands" env:"LELE_TOOLS_EXEC_WHITELIST_COMMANDS"`
}

type ToolsConfig struct {
	Web  WebToolsConfig  `json:"web"`
	Cron CronToolsConfig `json:"cron"`
	Exec ExecConfig      `json:"exec"`
}

func DefaultConfig() *Config {
	return &Config{
		Agents: AgentsConfig{
			Defaults: AgentDefaults{
				Workspace:                "",
				RestrictToWorkspace:      true,
				Provider:                 "openrouter",
				Model:                    "deepseek-v4-pro",
				MaxTokens:                0,
				MaxToolIterations:        0,
				MaxReadLines:             500,
				MemoryMaxLines:           500,
				SubagentTimeoutMinutes:   30,
				SubagentMaxIterations:    0,
				SubagentMaxRetries:       2,
				SubagentRetentionMinutes: 5,
				LLMLoopTimeoutMinutes:    0,
			},
		},
		Session: SessionConfig{
			Ephemeral:                  true,
			EphemeralThreshold:         DefaultEphemeralThresholdSeconds,
			CompactionThresholdPercent: DefaultCompactionThresholdPercent,
			EvictExcludedFromMemory:    true,
		},
		Channels: ChannelsConfig{
			WhatsApp: WhatsAppConfig{
				Enabled:   false,
				BridgeURL: "ws://localhost:3001",
				AllowFrom: FlexibleStringSlice{},
			},
			Telegram: TelegramConfig{
				Enabled:   false,
				Token:     "",
				AllowFrom: FlexibleStringSlice{},
				Verbose:   VerboseOff,
			},
			Feishu: FeishuConfig{
				Enabled:           false,
				AppID:             "",
				AppSecret:         "",
				EncryptKey:        "",
				VerificationToken: "",
				AllowFrom:         FlexibleStringSlice{},
			},
			Discord: DiscordConfig{
				Enabled:   false,
				Token:     "",
				AllowFrom: FlexibleStringSlice{},
			},
			MaixCam: MaixCamConfig{
				Enabled:   false,
				Host:      "0.0.0.0",
				Port:      18790,
				AllowFrom: FlexibleStringSlice{},
			},
			QQ: QQConfig{
				Enabled:   false,
				AppID:     "",
				AppSecret: "",
				AllowFrom: FlexibleStringSlice{},
			},
			DingTalk: DingTalkConfig{
				Enabled:      false,
				ClientID:     "",
				ClientSecret: "",
				AllowFrom:    FlexibleStringSlice{},
			},
			Slack: SlackConfig{
				Enabled:   false,
				BotToken:  "",
				AppToken:  "",
				AllowFrom: FlexibleStringSlice{},
			},
			LINE: LINEConfig{
				Enabled:            false,
				ChannelSecret:      "",
				ChannelAccessToken: "",
				WebhookHost:        "0.0.0.0",
				WebhookPort:        18791,
				WebhookPath:        "/webhook/line",
				AllowFrom:          FlexibleStringSlice{},
			},
			OneBot: OneBotConfig{
				Enabled:            false,
				WSUrl:              "ws://127.0.0.1:3001",
				AccessToken:        "",
				ReconnectInterval:  5,
				GroupTriggerPrefix: []string{},
				AllowFrom:          FlexibleStringSlice{},
			},
			Native: NativeConfig{
				Enabled:           true,
				Host:              "127.0.0.1",
				Port:              18790,
				TokenExpiryDays:   30,
				PinExpiryMinutes:  5,
				MaxClients:        5,
				CORSOrigins:       []string{"http://localhost", "http://localhost:18790", "http://127.0.0.1:18790", "http://0.0.0.0:18790", "tauri://localhost", "https://tauri.localhost"},
				SessionExpiryDays: 30,
				MaxUploadSizeMB:   50,
				UploadTTLHours:    24,
				LeleDir:           "",
				RateLimit: NativeRateLimitConfig{
					Enabled:             false,
					PinPerMinute:        DefaultNativeRateLimitPinPerMinute,
					PairPerMinute:       DefaultNativeRateLimitPairPerMinute,
					RefreshPerMinute:    DefaultNativeRateLimitRefreshPerMinute,
					APIPerMinute:        DefaultNativeRateLimitAPIPerMinute,
					WSMessagesPerMinute: DefaultNativeRateLimitWSMessagesPerMinute,
				},
			},
			Web: WebConfig{
				Enabled: true,
			},
		},
		Providers: &ProvidersConfig{
			Anthropic:         ProviderConfig{},
			OpenAI:            OpenAIProviderConfig{WebSearch: true},
			OpenRouter:        ProviderConfig{},
			Groq:              ProviderConfig{},
			Zhipu:             ProviderConfig{},
			VLLM:              ProviderConfig{},
			Gemini:            ProviderConfig{},
			Nvidia:            ProviderConfig{},
			Ollama:            ProviderConfig{},
			Moonshot:          ProviderConfig{},
			ShengSuanYun:      ProviderConfig{},
			NanogPT:           ProviderConfig{},
			DeepSeek:          ProviderConfig{},
			GitHubCopilot:     ProviderConfig{},
			AlibabaCodingPlan: ProviderConfig{},
		},
		Gateway: GatewayConfig{
			Host: "0.0.0.0",
			Port: 18790,
		},
		Server: ServerConfig{
			Host: "",
			Port: 0,
		},
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Brave: BraveConfig{
					Enabled:    false,
					APIKey:     "",
					MaxResults: 5,
				},
				DuckDuckGo: DuckDuckGoConfig{
					Enabled:    true,
					MaxResults: 5,
				},
				Perplexity: PerplexityConfig{
					Enabled:    false,
					APIKey:     "",
					MaxResults: 5,
				},
				SearXNG: SearXNGConfig{
					Enabled:     false,
					InstanceURL: "",
					Categories:  "general",
					Language:    "auto",
					SafeSearch:  0,
					MaxResults:  5,
				},
			},
			Cron: CronToolsConfig{
				ExecTimeoutMinutes: 5,
			},
			Exec: ExecConfig{
				EnableDenyPatterns: true,
				TimeoutSeconds:     60,
			},
		},
		Heartbeat: HeartbeatConfig{
			Enabled:  true,
			Interval: 30,
		},
		Devices: DevicesConfig{
			Enabled:    false,
			MonitorUSB: true,
		},
		Logs: LogsConfig{
			Enabled:  true,
			Path:     "",
			MaxDays:  7,
			Rotation: "daily",
		},
		Keyring: KeyringConfig{
			Enabled:          true,
			Path:             "",
			Backend:          "auto",
			AuditLogSize:     1000,
			AllowAgentSet:    false,
			AllowAgentDelete: false,
		},
		Updates: UpdatesConfig{
			Enabled: true,
			Channel: "stable",
			Repo:    "",
		},
		Goal: GoalConfig{
			Judge: GoalJudgeConfig{
				Mode: "inline",
			},
		},
	}
}

var envVarRegex = regexp.MustCompile(`\{\{ENV_([A-Za-z_][A-Za-z0-9_]*)(?::([^}]*))?\}\}`)

func expandEnvString(value string) string {
	if !strings.Contains(value, "{{ENV_") {
		return value
	}
	return envVarRegex.ReplaceAllStringFunc(value, func(match string) string {
		submatches := envVarRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		envValue := os.Getenv(submatches[1])
		if envValue != "" {
			return envValue
		}

		if len(submatches) > 2 && submatches[2] != "" {
			return submatches[2]
		}

		return ""
	})
}

func expandEnvTree(node interface{}) interface{} {
	switch value := node.(type) {
	case string:
		return expandEnvString(value)
	case map[string]interface{}:
		for key, item := range value {
			value[key] = expandEnvTree(item)
		}
		return value
	case []interface{}:
		for i, item := range value {
			value[i] = expandEnvTree(item)
		}
		return value
	}
	return node
}

func misplacedEnvPlaceholder(data []byte) (string, int, bool) {
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		if escaped {
			escaped = false
			continue
		}
		if inString && data[i] == '\\' {
			escaped = true
			continue
		}
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if inString || data[i] != '{' {
			continue
		}
		loc := envVarRegex.FindIndex(data[i:])
		if loc != nil && loc[0] == 0 {
			return string(data[i : i+loc[1]]), i, true
		}
	}
	return "", 0, false
}

func configParseError(path string, data []byte, err error) error {
	placeholder, offset, ok := misplacedEnvPlaceholder(data)
	if !ok {
		return fmt.Errorf("failed to parse config %s: %w", path, err)
	}
	return fmt.Errorf("failed to parse config %s: env placeholder %s at byte offset %d is not inside a JSON string (env placeholders are only expanded in string values): %w", path, placeholder, offset, err)
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}

	var tree interface{}
	if err := decodeJSONValue(data, &tree); err != nil {
		return nil, configParseError(path, data, err)
	}

	var rawDoc map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawDoc); err != nil {
		return nil, configParseError(path, data, err)
	}

	expanded, err := json.Marshal(expandEnvTree(tree))
	if err != nil {
		return nil, err
	}

	sessionEphemeralConfigured := false
	providersConfigured := false
	if sessionRaw, ok := rawDoc["session"]; ok {
		var rawSession map[string]json.RawMessage
		if err := json.Unmarshal(sessionRaw, &rawSession); err == nil {
			_, sessionEphemeralConfigured = rawSession["ephemeral"]
		}
	}
	if providersRaw, ok := rawDoc["providers"]; ok && len(providersRaw) > 2 {
		providersConfigured = true
	}

	if err := json.Unmarshal(expanded, cfg); err != nil {
		return nil, err
	}
	if !sessionEphemeralConfigured {
		cfg.Session.Ephemeral = SessionEphemeralFileDefault
	}
	if cfg.Session.EphemeralThreshold <= 0 {
		cfg.Session.EphemeralThreshold = DefaultEphemeralThresholdSeconds
	}
	if cfg.Session.CompactionThresholdPercent <= 0 || cfg.Session.CompactionThresholdPercent > 100 {
		cfg.Session.CompactionThresholdPercent = DefaultCompactionThresholdPercent
	}

	if !providersConfigured {
		cfg.Providers = nil
	}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

	cfg.base = cfg.clone()
	cfg.rawDoc = rawDoc
	return cfg, nil
}

func (c *Config) Reload(path string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.Agents = loaded.Agents
	c.Bindings = loaded.Bindings
	c.Groups = loaded.Groups
	c.Session = loaded.Session
	c.Channels = loaded.Channels
	c.Providers = loaded.Providers
	c.Gateway = loaded.Gateway
	c.Server = loaded.Server
	c.Tools = loaded.Tools
	c.Heartbeat = loaded.Heartbeat
	c.Devices = loaded.Devices
	c.Logs = loaded.Logs
	c.Keyring = loaded.Keyring
	c.Updates = loaded.Updates
	c.Goal = loaded.Goal
	c.Language = loaded.Language
	c.TUI = loaded.TUI
	c.Commands = loaded.Commands
	c.Harness = loaded.Harness
	c.rawDoc = loaded.rawDoc
	c.base = loaded.base
	return nil
}

func (c *Config) Snapshot() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clone()
}

func DefaultConfigPath() string {
	return filepath.Join(GetLeleDir(), "config.json")
}

var errConfigIsNil = fmt.Errorf("config is nil")

func SaveConfig(path string, cfg *Config) error {
	return saveConfig(path, cfg, nil)
}

func saveConfig(path string, cfg *Config, beforeWrite func()) error {
	if cfg == nil {
		return errConfigIsNil
	}

	cfg.mu.RLock()
	next, err := jsonTreeOf(cfg)
	snapshot := cfg.clone()
	var base map[string]interface{}
	if err == nil {
		baseConfig := cfg.base
		if baseConfig == nil {
			baseConfig = DefaultConfig()
		}
		base, err = jsonTreeOf(baseConfig)
	}
	fallbackRaw := cfg.rawDoc
	cfg.mu.RUnlock()
	if err != nil {
		return err
	}
	if snapshot == nil {
		return fmt.Errorf("failed to snapshot config for saving %s", path)
	}

	raw, state, readErr := rawDocumentFromFile(path)
	if state == documentCorrupt {
		return fmt.Errorf("refusing to save over an unparsable config %s: %w", path, readErr)
	}
	if state == documentMissing {
		raw = fallbackRaw
		if raw == nil {
			raw = map[string]json.RawMessage{}
		}
	}

	merged, err := mergeConfigDocument(raw, base, next)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}
	if beforeWrite != nil {
		beforeWrite()
	}
	if err := writeConfigFile(path, data); err != nil {
		return err
	}
	cfg.markSaved(merged, snapshot)
	return nil
}

func (c *Config) markSaved(raw map[string]json.RawMessage, base *Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	base.base = nil
	base.rawDoc = nil
	c.rawDoc = raw
	c.base = base
}

func (c *Config) TelegramVerbose() VerboseLevel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Channels.Telegram.Verbose
}

func (c *Config) SetTelegramVerbose(level VerboseLevel) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Channels.Telegram.Verbose = level
}

func (c *Config) PersistTelegramVerbose(path string, level VerboseLevel) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	c.SetTelegramVerbose(level)
	return SaveConfig(path, c)
}

func (c *Config) SessionEphemeralEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Session.Ephemeral
}

func (c *Config) SessionEphemeralThresholdSeconds() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Session.EphemeralThreshold <= 0 {
		return DefaultEphemeralThresholdSeconds
	}
	return c.Session.EphemeralThreshold
}

func (c *Config) SessionCompactionThresholdPercent() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Session.CompactionThresholdPercent <= 0 || c.Session.CompactionThresholdPercent > 100 {
		return DefaultCompactionThresholdPercent
	}
	return c.Session.CompactionThresholdPercent
}

func (c *Config) CompactionModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Session.CompactionModel
}

func (c *Config) EvictExcludedFromMemory() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	enabled := c.Session.EvictExcludedFromMemory
	if v := os.Getenv("LELE_EVICT_EXCLUDED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			enabled = b
		}
	}
	return enabled
}

func (c *Config) SetSessionEphemeral(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Session.Ephemeral = enabled
	if c.Session.EphemeralThreshold <= 0 {
		c.Session.EphemeralThreshold = DefaultEphemeralThresholdSeconds
	}
}

func (c *Config) PersistSessionEphemeral(path string, enabled bool) error {
	if path == "" {
		path = DefaultConfigPath()
	}
	c.SetSessionEphemeral(enabled)
	return SaveConfig(path, c)
}

func (c *Config) WorkspacePath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Agents.Defaults.Workspace == "" {
		return filepath.Join(GetLeleDir(), "workspace")
	}
	return expandHome(c.Agents.Defaults.Workspace)
}

func (c *Config) LogsPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Logs.Path == "" {
		return filepath.Join(GetLeleDir(), "logs")
	}
	return expandHome(c.Logs.Path)
}

func (c *Config) KeyringVaultPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Keyring.Path == "" {
		return filepath.Join(GetLeleDir(), "keyring.enc")
	}
	return expandHome(c.Keyring.Path)
}

func (c *Config) GetAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Providers.OpenRouter.APIKey != "" {
		return c.Providers.OpenRouter.APIKey
	}
	if c.Providers.Anthropic.APIKey != "" {
		return c.Providers.Anthropic.APIKey
	}
	if c.Providers.OpenAI.APIKey != "" {
		return c.Providers.OpenAI.APIKey
	}
	if c.Providers.Gemini.APIKey != "" {
		return c.Providers.Gemini.APIKey
	}
	if c.Providers.Zhipu.APIKey != "" {
		return c.Providers.Zhipu.APIKey
	}
	if c.Providers.Groq.APIKey != "" {
		return c.Providers.Groq.APIKey
	}
	if c.Providers.VLLM.APIKey != "" {
		return c.Providers.VLLM.APIKey
	}
	if c.Providers.ShengSuanYun.APIKey != "" {
		return c.Providers.ShengSuanYun.APIKey
	}
	return ""
}

func (c *Config) GetAPIBase() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Providers.OpenRouter.APIKey != "" {
		if c.Providers.OpenRouter.APIBase != "" {
			return c.Providers.OpenRouter.APIBase
		}
		return "https://openrouter.ai/api/v1"
	}
	if c.Providers.Zhipu.APIKey != "" {
		return c.Providers.Zhipu.APIBase
	}
	if c.Providers.VLLM.APIKey != "" && c.Providers.VLLM.APIBase != "" {
		return c.Providers.VLLM.APIBase
	}
	return ""
}

type ModelConfig struct {
	Primary   string
	Fallbacks []string
}

func (c *Config) GetModelConfig() ModelConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ModelConfig{
		Primary:   c.Agents.Defaults.Model,
		Fallbacks: c.Agents.Defaults.ModelFallbacks,
	}
}

func (c *Config) GetImageModelConfig() ModelConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ModelConfig{
		Primary:   c.Agents.Defaults.ImageModel,
		Fallbacks: c.Agents.Defaults.ImageModelFallbacks,
	}
}

func (c *Config) GetLanguage() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Language == "" {
		return "es"
	}
	return c.Language
}

func (c *Config) SetLanguage(lang string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Language = lang
}

func (c *Config) GetAvailableLanguages() []string {
	return i18n.AvailableLanguages()
}

func expandHome(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

func GetLeleDir() string {
	if envDir := os.Getenv("LELE_CONFIG_DIR"); envDir != "" {
		return expandHome(envDir)
	}
	return getDefaultLeleDir()
}

func getDefaultLeleDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".lele"
	}
	return filepath.Join(homeDir, ".lele")
}
