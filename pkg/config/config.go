package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/caarlos0/env/v11"
)

// FlexibleStringSlice is a []string that also accepts JSON numbers,
// so allow_from can contain both "123" and 123.
type FlexibleStringSlice []string

func (f *FlexibleStringSlice) UnmarshalJSON(data []byte) error {
	// Try []string first
	var ss []string
	if err := json.Unmarshal(data, &ss); err == nil {
		*f = ss
		return nil
	}

	// Try []interface{} to handle mixed types
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

type Config struct {
	Agents    AgentsConfig    `json:"agents"`
	Bindings  []AgentBinding  `json:"bindings,omitempty"`
	Session   SessionConfig   `json:"session,omitempty"`
	Channels  ChannelsConfig  `json:"channels"`
	Providers ProvidersConfig `json:"providers"`
	Gateway   GatewayConfig   `json:"gateway"`
	Tools     ToolsConfig     `json:"tools"`
	Heartbeat HeartbeatConfig `json:"heartbeat"`
	Devices   DevicesConfig   `json:"devices"`
	Logs      LogsConfig      `json:"logs"`
	mu        sync.RWMutex
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
	return &cloned
}

type AgentsConfig struct {
	Defaults AgentDefaults `json:"defaults"`
	List     []AgentConfig `json:"list,omitempty"`
}

// AgentModelConfig supports both string and structured model config.
// String format: "gpt-4" (just primary, no fallbacks)
// Object format: {"primary": "gpt-4", "fallbacks": ["claude-haiku"]}
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
	// Always serialize as object to maintain consistent structure in UI
	// This ensures the frontend always receives {primary, fallbacks} format
	type raw struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
	}
	return json.Marshal(raw{Primary: m.Primary, Fallbacks: m.Fallbacks})
}

type AgentConfig struct {
	ID          string            `json:"id"`
	Default     bool              `json:"default,omitempty"`
	Name        string            `json:"name,omitempty"`
	Workspace   string            `json:"workspace,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
	Skills      []string          `json:"skills,omitempty"`
	Subagents   *SubagentsConfig  `json:"subagents,omitempty"`
	Temperature *float64          `json:"temperature,omitempty"`
}

type SubagentsConfig struct {
	AllowAgents []string          `json:"allow_agents,omitempty"`
	Model       *AgentModelConfig `json:"model,omitempty"`
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
	DMScope            string              `json:"dm_scope,omitempty"`
	IdentityLinks      map[string][]string `json:"identity_links,omitempty"`
	Ephemeral          bool                `json:"ephemeral"`
	EphemeralThreshold int                 `json:"ephemeral_threshold"`
}

const DefaultEphemeralThresholdSeconds = 560

type AgentDefaults struct {
	Workspace           string   `json:"workspace" env:"LELE_AGENTS_DEFAULTS_WORKSPACE"`
	RestrictToWorkspace bool     `json:"restrict_to_workspace" env:"LELE_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE"`
	Provider            string   `json:"provider" env:"LELE_AGENTS_DEFAULTS_PROVIDER"`
	Model               string   `json:"model" env:"LELE_AGENTS_DEFAULTS_MODEL"`
	ModelFallbacks      []string `json:"model_fallbacks,omitempty"`
	ImageModel          string   `json:"image_model,omitempty" env:"LELE_AGENTS_DEFAULTS_IMAGE_MODEL"`
	ImageModelFallbacks []string `json:"image_model_fallbacks,omitempty"`
	MaxTokens           int      `json:"max_tokens" env:"LELE_AGENTS_DEFAULTS_MAX_TOKENS"`
	Temperature         *float64 `json:"temperature,omitempty" env:"LELE_AGENTS_DEFAULTS_TEMPERATURE"`
	MaxToolIterations   int      `json:"max_tool_iterations" env:"LELE_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"`
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

type NativeConfig struct {
	Enabled           bool     `json:"enabled" env:"LELE_CHANNELS_NATIVE_ENABLED"`
	Host              string   `json:"host" env:"LELE_CHANNELS_NATIVE_HOST"`
	Port              int      `json:"port" env:"LELE_CHANNELS_NATIVE_PORT"`
	TokenExpiryDays   int      `json:"token_expiry_days" env:"LELE_CHANNELS_NATIVE_TOKEN_EXPIRY_DAYS"`
	PinExpiryMinutes  int      `json:"pin_expiry_minutes" env:"LELE_CHANNELS_NATIVE_PIN_EXPIRY_MINUTES"`
	MaxClients        int      `json:"max_clients" env:"LELE_CHANNELS_NATIVE_MAX_CLIENTS"`
	CORSOrigins       []string `json:"cors_origins" env:"LELE_CHANNELS_NATIVE_CORS_ORIGINS"`
	SessionExpiryDays int      `json:"session_expiry_days" env:"LELE_CHANNELS_NATIVE_SESSION_EXPIRY_DAYS"`
	MaxUploadSizeMB   int64    `json:"max_upload_size_mb" env:"LELE_CHANNELS_NATIVE_MAX_UPLOAD_SIZE_MB"`
	UploadTTLHours    int      `json:"upload_ttl_hours" env:"LELE_CHANNELS_NATIVE_UPLOAD_TTL_HOURS"`
	LeleDir           string   `json:"lele_dir,omitempty" env:"LELE_CHANNELS_NATIVE_LELE_DIR"`
}

type WebConfig struct {
	Enabled bool   `json:"enabled" env:"LELE_WEB_ENABLED"`
	Host    string `json:"host" env:"LELE_WEB_HOST"`
	Port    int    `json:"port" env:"LELE_WEB_PORT"`
}

type WhatsAppConfig struct {
	Enabled   bool                `json:"enabled" env:"LELE_CHANNELS_WHATSAPP_ENABLED"`
	BridgeURL string              `json:"bridge_url" env:"LELE_CHANNELS_WHATSAPP_BRIDGE_URL"`
	AllowFrom FlexibleStringSlice `json:"allow_from" env:"LELE_CHANNELS_WHATSAPP_ALLOW_FROM"`
}

// VerboseLevel represents the verbosity level for tool execution notifications
type VerboseLevel string

const (
	// VerboseOff disables all tool execution notifications
	VerboseOff VerboseLevel = "off"
	// VerboseBasic shows simplified action descriptions only
	VerboseBasic VerboseLevel = "basic"
	// VerboseFull shows detailed tool calls and results
	VerboseFull VerboseLevel = "full"
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
	Interval int  `json:"interval" env:"LELE_HEARTBEAT_INTERVAL"` // minutes, min 5
}

type DevicesConfig struct {
	Enabled    bool `json:"enabled" env:"LELE_DEVICES_ENABLED"`
	MonitorUSB bool `json:"monitor_usb" env:"LELE_DEVICES_MONITOR_USB"`
}

// LogsConfig holds logging-related configuration
type LogsConfig struct {
	Enabled  bool   `json:"enabled" env:"LELE_LOGS_ENABLED"`             // Enable/disable file logging
	Path     string `json:"path,omitempty" env:"LELE_LOGS_PATH"`         // Custom path (default: ~/.lele/logs)
	MaxDays  int    `json:"max_days,omitempty" env:"LELE_LOGS_MAX_DAYS"` // Max days to keep logs (default: 7)
	Rotation string `json:"rotation,omitempty" env:"LELE_LOGS_ROTATION"` // "daily" or "weekly" (default: daily)
}

type ProvidersConfig struct {
	Anthropic         ProviderConfig                 `json:"anthropic"`
	OpenAI            OpenAIProviderConfig           `json:"openai"`
	OpenRouter        ProviderConfig                 `json:"openrouter"`
	Groq              ProviderConfig                 `json:"groq"`
	Zhipu             ProviderConfig                 `json:"zhipu"`
	VLLM              ProviderConfig                 `json:"vllm"`
	Gemini            ProviderConfig                 `json:"gemini"`
	Nvidia            ProviderConfig                 `json:"nvidia"`
	Ollama            ProviderConfig                 `json:"ollama"`
	Moonshot          ProviderConfig                 `json:"moonshot"`
	ShengSuanYun      ProviderConfig                 `json:"shengsuanyun"`
	DeepSeek          ProviderConfig                 `json:"deepseek"`
	GitHubCopilot     ProviderConfig                 `json:"github_copilot"`
	NanogPT           ProviderConfig                 `json:"nanogpt"`
	AlibabaCodingPlan ProviderConfig                 `json:"alibaba_coding_plan"`
	Named             map[string]NamedProviderConfig `json:"-"`
}

type ProviderConfig struct {
	APIKey      string `json:"api_key" env:"LELE_PROVIDERS_{{.Name}}_API_KEY"`
	APIBase     string `json:"api_base" env:"LELE_PROVIDERS_{{.Name}}_API_BASE"`
	Proxy       string `json:"proxy,omitempty" env:"LELE_PROVIDERS_{{.Name}}_PROXY"`
	AuthMethod  string `json:"auth_method,omitempty" env:"LELE_PROVIDERS_{{.Name}}_AUTH_METHOD"`
	ConnectMode string `json:"connect_mode,omitempty" env:"LELE_PROVIDERS_{{.Name}}_CONNECT_MODE"` //only for Github Copilot, `stdio` or `grpc`
}

type OpenAIProviderConfig struct {
	ProviderConfig
	WebSearch bool `json:"web_search" env:"LELE_PROVIDERS_OPENAI_WEB_SEARCH"`
}

// ReasoningConfig holds reasoning-related configuration for models that support it.
// Based on OpenAI's reasoning API specification.
type ReasoningConfig struct {
	Effort  *string `json:"effort,omitempty"`  // "low", "medium", "high"
	Summary *string `json:"summary,omitempty"` // "auto", "detailed", "concise"
}

// Validate checks if the reasoning config has valid values.
func (r *ReasoningConfig) Validate() error {
	if r == nil {
		return nil
	}
	validEfforts := map[string]bool{"low": true, "medium": true, "high": true}
	validSummaries := map[string]bool{"auto": true, "detailed": true, "concise": true}

	if r.Effort != nil {
		if !validEfforts[strings.ToLower(*r.Effort)] {
			return fmt.Errorf("invalid reasoning effort: %q (must be low, medium, or high)", *r.Effort)
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
	return nil
}

type ProviderModelConfig struct {
	ContextWindow int              `json:"context_window,omitempty"`
	Model         string           `json:"model,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	Vision        bool             `json:"vision,omitempty"`
	Reasoning     *ReasoningConfig `json:"reasoning,omitempty"`
}

// Validate checks if the provider model config is valid.
func (p *ProviderModelConfig) Validate() error {
	if p.Reasoning != nil {
		if err := p.Reasoning.Validate(); err != nil {
			return fmt.Errorf("model %q: %w", p.Model, err)
		}
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
		// Validate model configs
		for modelName, modelCfg := range named.Models {
			if err := modelCfg.Validate(); err != nil {
				return fmt.Errorf("provider %q, model %q: %w", name, modelName, err)
			}
		}
		p.Named[name] = named
	}

	p.ensureNamedDefaults()
	return nil
}

func (p *ProvidersConfig) MarshalJSON() ([]byte, error) {
	out := map[string]NamedProviderConfig{}
	for k, v := range p.Named {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		if v.Type == "" {
			v.Type = key
		}
		out[key] = v
	}

	put := func(name string, cfg NamedProviderConfig) {
		key := strings.ToLower(name)
		if _, exists := out[key]; exists {
			return
		}
		if cfg.Type == "" {
			cfg.Type = key
		}
		out[key] = cfg
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

	return json.Marshal(out)
}

func (p *ProvidersConfig) ensureNamedDefaults() {
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
}

func (p *ProvidersConfig) GetNamed(name string) (NamedProviderConfig, bool) {
	p.ensureNamedDefaults()
	cfg, ok := p.Named[strings.ToLower(strings.TrimSpace(name))]
	return cfg, ok
}

func (p *ProvidersConfig) ListNamed() map[string]NamedProviderConfig {
	p.ensureNamedDefaults()
	result := make(map[string]NamedProviderConfig, len(p.Named))
	for name, cfg := range p.Named {
		result[name] = cfg
	}
	return result
}

func (p *ProvidersConfig) resolveModelAliasInProvider(provider, model string, preferExact bool) (string, bool) {
	if provider == "" {
		return "", false
	}

	named, ok := p.GetNamed(provider)
	if !ok || named.Models == nil {
		return "", false
	}

	// Always try exact match first, regardless of preferExact
	if aliasCfg, found := named.Models[model]; found {
		resolved := strings.TrimSpace(aliasCfg.Model)
		if resolved != "" {
			return resolved, true
		}
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
		return "", false
	}
	return resolved, true
}

func (p *ProvidersConfig) ResolveModelAlias(rawModel, defaultProvider string) string {
	rawModel = strings.TrimSpace(rawModel)
	if rawModel == "" {
		return rawModel
	}

	provider := normalizeProviderKey(defaultProvider)
	if provider != "" && strings.Contains(rawModel, "/") {
		if resolved, found := p.resolveModelAliasInProvider(provider, rawModel, true); found {
			log.Printf("[DEBUG] ResolveModelAlias: %s -> %s/%s (found in %s)\n", rawModel, provider, resolved, provider)
			return provider + "/" + resolved
		}
	}

	model := rawModel
	if idx := strings.Index(rawModel, "/"); idx > 0 {
		provider = normalizeProviderKey(rawModel[:idx])
		model = strings.TrimSpace(rawModel[idx+1:])
		if model == "" {
			return rawModel
		}
	}

	if provider == "" {
		return rawModel
	}

	// Normalize model name for comparison (lowercase, replace . with -)
	normalizedModel := strings.ToLower(strings.ReplaceAll(model, ".", "-"))

	// Try to find model in the specified provider first.
	if resolved, found := p.resolveModelAliasInProvider(provider, model, strings.Contains(model, "/")); found {
		// Always return with the provider from config
		// Format: provider/resolved_model (e.g., chutes/Qwen/Qwen3-Coder-Next)
		log.Printf("[DEBUG] ResolveModelAlias: %s -> %s/%s (found in %s)\n", rawModel, provider, resolved, provider)
		return provider + "/" + resolved
	}

	// If not found in specified provider (or provider doesn't exist),
	// search across all providers for the model alias
	p.ensureNamedDefaults()
	for provName, provCfg := range p.Named {
		if provCfg.Models == nil {
			continue
		}
		// Try exact match first
		aliasCfg, found := provCfg.Models[normalizedModel]
		if !found || strings.TrimSpace(aliasCfg.Model) == "" {
			// Try matching against the resolved model values
			for _, aliasVal := range provCfg.Models {
				resolvedVal := strings.ToLower(strings.TrimSpace(aliasVal.Model))
				if resolvedVal == normalizedModel || strings.HasSuffix(resolvedVal, "/"+normalizedModel) {
					aliasCfg = aliasVal
					found = true
					break
				}
			}
		}
		if found && strings.TrimSpace(aliasCfg.Model) != "" {
			resolved := strings.TrimSpace(aliasCfg.Model)
			// Return with the provider from config where we found it
			// Format: provider/resolved_model (e.g., chutes/Qwen/Qwen3.5-397B-A17B-TEE)
			log.Printf("[DEBUG] ResolveModelAlias: %s -> %s/%s (found in %s)\n", rawModel, provName, resolved, provName)
			return provName + "/" + resolved
		}
	}

	// Model not found anywhere, return original
	log.Printf("[DEBUG] ResolveModelAlias: %s -> %s (NOT FOUND)\n", rawModel, rawModel)
	return rawModel
}

func normalizeProviderKey(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "glm":
		return "zhipu"
	case "google":
		return "gemini"
	}
	return p
}

type GatewayConfig struct {
	Host string `json:"host" env:"LELE_GATEWAY_HOST"`
	Port int    `json:"port" env:"LELE_GATEWAY_PORT"`
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

type WebToolsConfig struct {
	Brave      BraveConfig      `json:"brave"`
	DuckDuckGo DuckDuckGoConfig `json:"duckduckgo"`
	Perplexity PerplexityConfig `json:"perplexity"`
}

type CronToolsConfig struct {
	ExecTimeoutMinutes int `json:"exec_timeout_minutes" env:"LELE_TOOLS_CRON_EXEC_TIMEOUT_MINUTES"` // 0 means no timeout
}

type ExecConfig struct {
	EnableDenyPatterns bool     `json:"enable_deny_patterns" env:"LELE_TOOLS_EXEC_ENABLE_DENY_PATTERNS"`
	CustomDenyPatterns []string `json:"custom_deny_patterns" env:"LELE_TOOLS_EXEC_CUSTOM_DENY_PATTERNS"`
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
				Workspace:           "~/.lele/workspace",
				RestrictToWorkspace: true,
				Provider:            "nanogpt",
				Model:               "nanogpt/qwen3-5-397b-a17b-thinking",
				MaxTokens:           8192,
				MaxToolIterations:   20,
			},
		},
		Session: SessionConfig{
			Ephemeral:          true,
			EphemeralThreshold: DefaultEphemeralThresholdSeconds,
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
				Enabled:           false,
				Host:              "127.0.0.1",
				Port:              18793,
				TokenExpiryDays:   30,
				PinExpiryMinutes:  5,
				MaxClients:        5,
				CORSOrigins:       []string{"http://localhost", "http://localhost:3005", "http://127.0.0.1:3005", "http://0.0.0.0:3005", "tauri://localhost", "https://tauri.localhost"},
				SessionExpiryDays: 30,
				MaxUploadSizeMB:   50,
				UploadTTLHours:    24,
				LeleDir:           getDefaultLeleDir(),
			},
			Web: WebConfig{
				Enabled: false,
				Host:    "0.0.0.0",
				Port:    3005,
			},
		},
		Providers: ProvidersConfig{
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
			AlibabaCodingPlan: ProviderConfig{APIBase: "https://coding-intl.dashscope.aliyuncs.com/v1"},
		},
		Gateway: GatewayConfig{
			Host: "0.0.0.0",
			Port: 18790,
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
			},
			Cron: CronToolsConfig{
				ExecTimeoutMinutes: 5, // default 5 minutes for LLM operations
			},
			Exec: ExecConfig{
				EnableDenyPatterns: true,
			},
		},
		Heartbeat: HeartbeatConfig{
			Enabled:  true,
			Interval: 30, // default 30 minutes
		},
		Devices: DevicesConfig{
			Enabled:    false,
			MonitorUSB: true,
		},
		Logs: LogsConfig{
			Enabled:  true,
			Path:     "~/.lele/logs",
			MaxDays:  7,
			Rotation: "daily",
		},
	}
}

var envVarRegex = regexp.MustCompile(`\{\{ENV_([A-Za-z_][A-Za-z0-9_]*)(?::([^}]*))?\}\}`)

func expandEnvVars(data []byte) []byte {
	return envVarRegex.ReplaceAllFunc(data, func(match []byte) []byte {
		submatches := envVarRegex.FindSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		envName := string(submatches[1])
		envValue := os.Getenv(envName)

		if envValue != "" {
			return []byte(envValue)
		}

		if len(submatches) > 2 && string(submatches[2]) != "" {
			return submatches[2]
		}

		return []byte{}
	})
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

	data = expandEnvVars(data)

	sessionEphemeralConfigured := false
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err == nil {
		if sessionRaw, ok := raw["session"]; ok {
			var rawSession map[string]json.RawMessage
			if err := json.Unmarshal(sessionRaw, &rawSession); err == nil {
				_, sessionEphemeralConfigured = rawSession["ephemeral"]
			}
		}
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if !sessionEphemeralConfigured {
		cfg.Session.Ephemeral = false
	}
	if cfg.Session.EphemeralThreshold <= 0 {
		cfg.Session.EphemeralThreshold = DefaultEphemeralThresholdSeconds
	}

	if err := env.Parse(cfg); err != nil {
		return nil, err
	}

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
	c.Session = loaded.Session
	c.Channels = loaded.Channels
	c.Providers = loaded.Providers
	c.Gateway = loaded.Gateway
	c.Tools = loaded.Tools
	c.Heartbeat = loaded.Heartbeat
	c.Devices = loaded.Devices
	c.Logs = loaded.Logs
	return nil
}

func (c *Config) Snapshot() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.clone()
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".lele", "config.json")
}

func SaveConfig(path string, cfg *Config) error {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
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
	return expandHome(c.Agents.Defaults.Workspace)
}

func (c *Config) LogsPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.Logs.Path == "" {
		return expandHome("~/.lele/logs")
	}
	return expandHome(c.Logs.Path)
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

// ModelConfig holds primary model and fallback list.
type ModelConfig struct {
	Primary   string
	Fallbacks []string
}

// GetModelConfig returns the text model configuration with fallbacks.
func (c *Config) GetModelConfig() ModelConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ModelConfig{
		Primary:   c.Agents.Defaults.Model,
		Fallbacks: c.Agents.Defaults.ModelFallbacks,
	}
}

// GetImageModelConfig returns the image model configuration with fallbacks.
func (c *Config) GetImageModelConfig() ModelConfig {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return ModelConfig{
		Primary:   c.Agents.Defaults.ImageModel,
		Fallbacks: c.Agents.Defaults.ImageModelFallbacks,
	}
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

func getDefaultLeleDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return ".lele"
	}
	return filepath.Join(homeDir, ".lele")
}
