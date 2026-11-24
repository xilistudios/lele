package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAgentModelConfig_UnmarshalString(t *testing.T) {
	var m AgentModelConfig
	if err := json.Unmarshal([]byte(`"gpt-4"`), &m); err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if m.Primary != "gpt-4" {
		t.Errorf("Primary = %q, want 'gpt-4'", m.Primary)
	}
	if m.Fallbacks != nil {
		t.Errorf("Fallbacks = %v, want nil", m.Fallbacks)
	}
}

func TestAgentModelConfig_UnmarshalObject(t *testing.T) {
	var m AgentModelConfig
	data := `{"primary": "claude-opus", "fallbacks": ["gpt-4o-mini", "haiku"]}`
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if m.Primary != "claude-opus" {
		t.Errorf("Primary = %q, want 'claude-opus'", m.Primary)
	}
	if len(m.Fallbacks) != 2 {
		t.Fatalf("Fallbacks len = %d, want 2", len(m.Fallbacks))
	}
	if m.Fallbacks[0] != "gpt-4o-mini" || m.Fallbacks[1] != "haiku" {
		t.Errorf("Fallbacks = %v", m.Fallbacks)
	}
}

func TestAgentModelConfig_MarshalObjectNoFallbacks(t *testing.T) {
	m := AgentModelConfig{Primary: "gpt-4"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var result map[string]interface{}
	json.Unmarshal(data, &result)
	if result["primary"] != "gpt-4" {
		t.Errorf("primary = %v, want 'gpt-4'", result["primary"])
	}
	if _, ok := result["fallbacks"]; ok {
		t.Errorf("fallbacks key present, want omitted")
	}
}

func TestAgentModelConfig_MarshalObject(t *testing.T) {
	m := AgentModelConfig{Primary: "claude-opus", Fallbacks: []string{"haiku"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var result map[string]interface{}
	json.Unmarshal(data, &result)
	if result["primary"] != "claude-opus" {
		t.Errorf("primary = %v", result["primary"])
	}
}

func TestProviderModelConfig_UnmarshalVision(t *testing.T) {
	var modelCfg ProviderModelConfig
	if err := json.Unmarshal([]byte(`{"vision":true}`), &modelCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !modelCfg.Vision {
		t.Fatal("expected Vision to be true")
	}
}

func TestProviderModelConfig_UnmarshalVideo(t *testing.T) {
	var modelCfg ProviderModelConfig
	if err := json.Unmarshal([]byte(`{"video":true}`), &modelCfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !modelCfg.Video {
		t.Fatal("expected Video to be true")
	}

	var omitted ProviderModelConfig
	if err := json.Unmarshal([]byte(`{"vision":true}`), &omitted); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if omitted.Video {
		t.Fatal("expected Video to be false when the key is omitted")
	}

	data, err := json.Marshal(ProviderModelConfig{Model: "gpt-4o", Vision: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"video"`) {
		t.Errorf("zero Video must be omitted from marshaled JSON: %s", data)
	}
}

func TestAgentConfig_FullParse(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.lele/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			},
			"list": [
				{
					"id": "sales",
					"default": true,
					"name": "Sales Bot",
					"model": "gpt-4"
				},
				{
					"id": "support",
					"name": "Support Bot",
					"model": {
						"primary": "claude-opus",
						"fallbacks": ["haiku"]
					},
					"subagents": {
						"allow_agents": ["sales"]
					}
				}
			]
		},
		"bindings": [
			{
				"agent_id": "support",
				"match": {
					"channel": "telegram",
					"account_id": "*",
					"peer": {"kind": "direct", "id": "user123"}
				}
			}
		],
		"session": {
			"dm_scope": "per-peer",
			"ephemeral": true,
			"ephemeral_threshold": 560,
			"identity_links": {
				"john": ["telegram:123", "discord:john#1234"]
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Agents.List) != 2 {
		t.Fatalf("agents.list len = %d, want 2", len(cfg.Agents.List))
	}

	sales := cfg.Agents.List[0]
	if sales.ID != "sales" || !sales.Default || sales.Name != "Sales Bot" {
		t.Errorf("sales = %+v", sales)
	}
	if sales.Model == nil || sales.Model.Primary != "gpt-4" {
		t.Errorf("sales.Model = %+v", sales.Model)
	}

	support := cfg.Agents.List[1]
	if support.ID != "support" || support.Name != "Support Bot" {
		t.Errorf("support = %+v", support)
	}
	if support.Model == nil || support.Model.Primary != "claude-opus" {
		t.Errorf("support.Model = %+v", support.Model)
	}
	if len(support.Model.Fallbacks) != 1 || support.Model.Fallbacks[0] != "haiku" {
		t.Errorf("support.Model.Fallbacks = %v", support.Model.Fallbacks)
	}
	if support.Subagents == nil || len(support.Subagents.AllowAgents) != 1 {
		t.Errorf("support.Subagents = %+v", support.Subagents)
	}

	if len(cfg.Bindings) != 1 {
		t.Fatalf("bindings len = %d, want 1", len(cfg.Bindings))
	}
	binding := cfg.Bindings[0]
	if binding.AgentID != "support" || binding.Match.Channel != "telegram" {
		t.Errorf("binding = %+v", binding)
	}
	if binding.Match.Peer == nil || binding.Match.Peer.Kind != "direct" || binding.Match.Peer.ID != "user123" {
		t.Errorf("binding.Match.Peer = %+v", binding.Match.Peer)
	}

	if cfg.Session.DMScope != "per-peer" {
		t.Errorf("Session.DMScope = %q", cfg.Session.DMScope)
	}
	if len(cfg.Session.IdentityLinks) != 1 {
		t.Errorf("Session.IdentityLinks = %v", cfg.Session.IdentityLinks)
	}
	if !cfg.Session.Ephemeral {
		t.Error("Session.Ephemeral should be true")
	}
	if cfg.Session.EphemeralThreshold != 560 {
		t.Errorf("Session.EphemeralThreshold = %d, want 560", cfg.Session.EphemeralThreshold)
	}
	links := cfg.Session.IdentityLinks["john"]
	if len(links) != 2 {
		t.Errorf("john links = %v", links)
	}
}

func TestConfig_BackwardCompat_NoAgentsList(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.lele/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Agents.List) != 0 {
		t.Errorf("agents.list should be empty for backward compat, got %d", len(cfg.Agents.List))
	}
	if len(cfg.Bindings) != 0 {
		t.Errorf("bindings should be empty, got %d", len(cfg.Bindings))
	}
}

func TestDefaultConfig_HeartbeatEnabled(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

func TestDefaultConfig_WorkspacePath(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.WorkspacePath() == "" {
		t.Error("WorkspacePath should not be empty")
	}
}

func TestDefaultConfig_Model(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Model == "" {
		t.Error("Model should not be empty")
	}
}

func TestDefaultConfig_MaxTokens(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxTokens < 0 {
		t.Error("MaxTokens should not be negative")
	}
}

func TestDefaultConfig_MaxToolIterations(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxToolIterations < 0 {
		t.Error("MaxToolIterations should not be negative")
	}
}

func TestDefaultConfig_Temperature(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
}

func TestDefaultConfig_Gateway(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Gateway.Host != "0.0.0.0" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
}

func TestDefaultConfig_Providers(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Providers.Anthropic.APIKey != "" {
		t.Error("Anthropic API key should be empty by default")
	}
	if cfg.Providers.OpenAI.APIKey != "" {
		t.Error("OpenAI API key should be empty by default")
	}
	if cfg.Providers.OpenRouter.APIKey != "" {
		t.Error("OpenRouter API key should be empty by default")
	}
}

func TestDefaultConfig_Channels(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Channels.Telegram.Enabled {
		t.Error("Telegram should be disabled by default")
	}
	if cfg.Channels.Discord.Enabled {
		t.Error("Discord should be disabled by default")
	}
	if cfg.Channels.Slack.Enabled {
		t.Error("Slack should be disabled by default")
	}
}

func TestDefaultConfig_WebTools(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Tools.Web.Brave.MaxResults != 5 {
		t.Error("Expected Brave MaxResults 5, got ", cfg.Tools.Web.Brave.MaxResults)
	}
	if cfg.Tools.Web.Brave.APIKey != "" {
		t.Error("Brave API key should be empty by default")
	}
	if cfg.Tools.Web.DuckDuckGo.MaxResults != 5 {
		t.Error("Expected DuckDuckGo MaxResults 5, got ", cfg.Tools.Web.DuckDuckGo.MaxResults)
	}
	if cfg.Tools.Web.SearXNG.Enabled {
		t.Error("SearXNG should be disabled by default")
	}
	if cfg.Tools.Web.SearXNG.InstanceURL != "" {
		t.Error("SearXNG InstanceURL should be empty by default, got ", cfg.Tools.Web.SearXNG.InstanceURL)
	}
	if cfg.Tools.Web.SearXNG.Categories != "general" {
		t.Error("Expected SearXNG Categories 'general', got ", cfg.Tools.Web.SearXNG.Categories)
	}
	if cfg.Tools.Web.SearXNG.Language != "auto" {
		t.Error("Expected SearXNG Language 'auto', got ", cfg.Tools.Web.SearXNG.Language)
	}
	if cfg.Tools.Web.SearXNG.MaxResults != 5 {
		t.Error("Expected SearXNG MaxResults 5, got ", cfg.Tools.Web.SearXNG.MaxResults)
	}
}

func TestSaveConfig_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("config file has permission %04o, want 0600", perm)
	}
}

func TestConfig_PersistTelegramVerbose(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := cfg.PersistTelegramVerbose(path, VerboseBasic); err != nil {
		t.Fatalf("PersistTelegramVerbose failed: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Channels.Telegram.Verbose != VerboseBasic {
		t.Fatalf("telegram verbose = %q, want %q", loaded.Channels.Telegram.Verbose, VerboseBasic)
	}
}

func TestConfig_PersistSessionEphemeral(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := cfg.PersistSessionEphemeral(path, true); err != nil {
		t.Fatalf("PersistSessionEphemeral failed: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !loaded.Session.Ephemeral {
		t.Fatal("session ephemeral should be true after persistence")
	}
	if loaded.Session.EphemeralThreshold != DefaultEphemeralThresholdSeconds {
		t.Fatalf("session ephemeral threshold = %d, want %d", loaded.Session.EphemeralThreshold, DefaultEphemeralThresholdSeconds)
	}
}

func TestConfig_Complete(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.WorkspacePath() == "" {
		t.Error("WorkspacePath should not be empty")
	}
	if cfg.Agents.Defaults.Model == "" {
		t.Error("Model should not be empty")
	}
	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
	if cfg.Agents.Defaults.MaxTokens < 0 {
		t.Error("MaxTokens should not be negative")
	}
	if cfg.Agents.Defaults.MaxToolIterations < 0 {
		t.Error("MaxToolIterations should not be negative")
	}
	if cfg.Gateway.Host != "0.0.0.0" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

func TestDefaultConfig_OpenAIWebSearchEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Providers.OpenAI.WebSearch {
		t.Fatal("DefaultConfig().Providers.OpenAI.WebSearch should be true")
	}
}

func TestDefaultConfig_EphemeralEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Session.Ephemeral {
		t.Fatal("DefaultConfig().Session.Ephemeral should be true")
	}
	if cfg.Session.EphemeralThreshold != DefaultEphemeralThresholdSeconds {
		t.Fatalf("DefaultConfig().Session.EphemeralThreshold = %d, want %d", cfg.Session.EphemeralThreshold, DefaultEphemeralThresholdSeconds)
	}
}

func TestLoadConfig_OpenAIWebSearchDefaultsTrueWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"api_base":""}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Providers.OpenAI.WebSearch {
		t.Fatal("OpenAI codex web search should remain true when unset in config file")
	}
}

func TestLoadConfig_EphemeralDefaultsFalseWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"session":{"dm_scope":"per-peer"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Session.Ephemeral {
		t.Fatal("ephemeral should remain disabled for legacy config files that omit the field")
	}
	if cfg.Session.EphemeralThreshold != DefaultEphemeralThresholdSeconds {
		t.Fatalf("ephemeral threshold = %d, want %d", cfg.Session.EphemeralThreshold, DefaultEphemeralThresholdSeconds)
	}
}

func TestLoadConfig_OpenAIWebSearchCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"providers":{"openai":{"web_search":false}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Providers.OpenAI.WebSearch {
		t.Fatal("OpenAI codex web search should be false when disabled in config file")
	}
}

func TestProvidersConfig_SupportsNamedProvidersAndModels(t *testing.T) {
	cfg := DefaultConfig()
	data := []byte(`{
		"providers": {
			"my-openai-compatible": {
				"type": "openai",
				"api_key": "sk-test",
				"api_base": "https://example.test/v1",
				"models": {
					"fast": {"model": "gpt-4o-mini"}
				}
			}
		}
	}`)
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	named, ok := cfg.Providers.GetNamed("my-openai-compatible")
	if !ok {
		t.Fatal("expected named provider")
	}
	if named.Type != "openai" {
		t.Fatalf("type = %q, want openai", named.Type)
	}
	if named.Models["fast"].Model != "gpt-4o-mini" {
		t.Fatalf("model alias = %q, want gpt-4o-mini", named.Models["fast"].Model)
	}
}

func TestProvidersConfig_ResolveModelAlias(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers.Named = map[string]NamedProviderConfig{
		"chutes": {
			Type: "openai",
			Models: map[string]ProviderModelConfig{
				"minimax": {Model: "minimax_m2.5"},
			},
		},
		"nanogpt": {
			Type: "openai",
			Models: map[string]ProviderModelConfig{
				"kimi":                            {Model: "moonshotai/kimi-k2.5:thinking"},
				"qwen/qwen3.5-397b-a17b-thinking": {Model: "Qwen/Qwen3.5-397B-A17B-Thinking-2507"},
			},
		},
		"qwen-portal": {
			Type: "openai",
			Models: map[string]ProviderModelConfig{
				"qwen3.5-397b-a17b-thinking": {Model: "Qwen/Qwen3.5-397B-A17B-Thinking-2507"},
			},
		},
	}

	if got := cfg.Providers.ResolveModelAlias("chutes:minimax", ""); got != "chutes:minimax_m2.5" {
		t.Fatalf("ResolveModelAlias(chutes:minimax) = %q, want %q", got, "chutes:minimax_m2.5")
	}
	if got := cfg.Providers.ResolveModelAlias("minimax", "chutes"); got != "chutes:minimax_m2.5" {
		t.Fatalf("ResolveModelAlias(minimax, default chutes) = %q, want %q", got, "chutes:minimax_m2.5")
	}
	if got := cfg.Providers.ResolveModelAlias("nanogpt:kimi", "nanogpt"); got != "nanogpt:moonshotai/kimi-k2.5:thinking" {
		t.Fatalf("ResolveModelAlias(nanogpt:kimi, default nanogpt) = %q, want %q", got, "nanogpt:moonshotai/kimi-k2.5:thinking")
	}
	if got := cfg.Providers.ResolveModelAlias("qwen:qwen3.5-397b-a17b-thinking", "nanogpt"); got != "qwen-portal:Qwen/Qwen3.5-397B-A17B-Thinking-2507" {
		t.Fatalf("ResolveModelAlias(qwen:qwen3.5-397b-a17b-thinking, default nanogpt) = %q, want %q", got, "qwen-portal:Qwen/Qwen3.5-397B-A17B-Thinking-2507")
	}
	if got := cfg.Providers.ResolveModelAlias("nanogpt:qwen/qwen3.5-397b-a17b-thinking", ""); got != "nanogpt:Qwen/Qwen3.5-397B-A17B-Thinking-2507" {
		t.Fatalf("ResolveModelAlias(nanogpt:qwen/qwen3.5-397b-a17b-thinking) = %q, want %q", got, "nanogpt:Qwen/Qwen3.5-397B-A17B-Thinking-2507")
	}
	if got := cfg.Providers.ResolveModelAlias("qwen:qwen3.5-397b-a17b-thinking", ""); got != "qwen-portal:Qwen/Qwen3.5-397B-A17B-Thinking-2507" {
		t.Fatalf("ResolveModelAlias(qwen:qwen3.5-397b-a17b-thinking) = %q, want %q", got, "qwen-portal:Qwen/Qwen3.5-397B-A17B-Thinking-2507")
	}
}

func TestProvidersConfig_ResolveModelAlias_KeyOnlyNoModelField(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers.Named = map[string]NamedProviderConfig{
		"deepseek": {
			Type: "deepseek",
			Models: map[string]ProviderModelConfig{
				"deepseek-v4-flash": {ContextWindow: 1000000, MaxTokens: 32000},
				"deepseek-v4-pro":   {ContextWindow: 1000000, MaxTokens: 32000},
			},
		},
		"openrouter": {
			Type: "openrouter",
			Models: map[string]ProviderModelConfig{
				"openrouter-deepseek-v4-flash": {Model: "deepseek/deepseek-v4-flash"},
				"openrouter-deepseek-v4-pro":   {Model: "deepseek/deepseek-v4-pro"},
			},
		},
	}

	if got := cfg.Providers.ResolveModelAlias("deepseek:deepseek-v4-flash", "alibaba"); got != "deepseek:deepseek-v4-flash" {
		t.Fatalf("ResolveModelAlias(deepseek:deepseek-v4-flash, alibaba) = %q, want %q", got, "deepseek:deepseek-v4-flash")
	}
	if got := cfg.Providers.ResolveModelAlias("deepseek:deepseek-v4-pro", ""); got != "deepseek:deepseek-v4-pro" {
		t.Fatalf("ResolveModelAlias(deepseek:deepseek-v4-pro) = %q, want %q", got, "deepseek:deepseek-v4-pro")
	}
	if got := cfg.Providers.ResolveModelAlias("deepseek-v4-flash", "deepseek"); got != "deepseek:deepseek-v4-flash" {
		t.Fatalf("ResolveModelAlias(deepseek-v4-flash, deepseek) = %q, want %q", got, "deepseek:deepseek-v4-flash")
	}
}

func expandEnvFixture(t *testing.T, input string) map[string]string {
	t.Helper()
	output := expandEnvString(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return result
}

func TestExpandEnvVars_Basic(t *testing.T) {
	os.Setenv("TEST_API_KEY", "my-secret-key")
	defer os.Unsetenv("TEST_API_KEY")

	result := expandEnvFixture(t, `{"api_key": "{{ENV_TEST_API_KEY}}"}`)
	if result["api_key"] != "my-secret-key" {
		t.Errorf("api_key = %q, want 'my-secret-key'", result["api_key"])
	}
}

func TestExpandEnvVars_WithDefault(t *testing.T) {
	result := expandEnvFixture(t, `{"api_key": "{{ENV_MISSING_KEY:default-value}}"}`)
	if result["api_key"] != "default-value" {
		t.Errorf("api_key = %q, want 'default-value'", result["api_key"])
	}
}

func TestExpandEnvVars_EnvExistsOverridesDefault(t *testing.T) {
	os.Setenv("TEST_OVERRIDE_KEY", "actual-value")
	defer os.Unsetenv("TEST_OVERRIDE_KEY")

	result := expandEnvFixture(t, `{"api_key": "{{ENV_TEST_OVERRIDE_KEY:default-value}}"}`)
	if result["api_key"] != "actual-value" {
		t.Errorf("api_key = %q, want 'actual-value'", result["api_key"])
	}
}

func TestExpandEnvVars_EmptyWhenNoDefault(t *testing.T) {
	result := expandEnvFixture(t, `{"api_key": "{{ENV_NONEXISTENT_KEY}}"}`)
	if result["api_key"] != "" {
		t.Errorf("api_key = %q, want ''", result["api_key"])
	}
}

func TestExpandEnvVars_MultipleVars(t *testing.T) {
	os.Setenv("TEST_KEY1", "value1")
	os.Setenv("TEST_KEY2", "value2")
	defer os.Unsetenv("TEST_KEY1")
	defer os.Unsetenv("TEST_KEY2")

	result := expandEnvFixture(t, `{"key1": "{{ENV_TEST_KEY1}}", "key2": "{{ENV_TEST_KEY2:default2}}"}`)
	if result["key1"] != "value1" {
		t.Errorf("key1 = %q, want 'value1'", result["key1"])
	}
	if result["key2"] != "value2" {
		t.Errorf("key2 = %q, want 'value2'", result["key2"])
	}
}

func TestLoadConfig_WithEnvVars(t *testing.T) {
	os.Setenv("LELE_TEST_API_KEY", "test-api-key-123")
	defer os.Unsetenv("LELE_TEST_API_KEY")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	configContent := `{
		"providers": {
			"anthropic": {
				"api_key": "{{ENV_LELE_TEST_API_KEY}}"
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Providers.Anthropic.APIKey != "test-api-key-123" {
		t.Errorf("Anthropic API key = %q, want 'test-api-key-123'", cfg.Providers.Anthropic.APIKey)
	}
}

func TestReasoningConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ReasoningConfig
		wantErr bool
	}{
		{
			name:    "nil config",
			cfg:     nil,
			wantErr: false,
		},
		{
			name:    "valid effort low",
			cfg:     &ReasoningConfig{Effort: strPtr("low")},
			wantErr: false,
		},
		{
			name:    "valid effort high",
			cfg:     &ReasoningConfig{Effort: strPtr("high")},
			wantErr: false,
		},
		{
			name:    "valid effort xhigh",
			cfg:     &ReasoningConfig{Effort: strPtr("xhigh")},
			wantErr: false,
		},
		{
			name:    "valid effort max",
			cfg:     &ReasoningConfig{Effort: strPtr("max")},
			wantErr: false,
		},
		{
			name:    "invalid effort",
			cfg:     &ReasoningConfig{Effort: strPtr("extreme")},
			wantErr: true,
		},
		{
			name:    "valid summary",
			cfg:     &ReasoningConfig{Summary: strPtr("auto")},
			wantErr: false,
		},
		{
			name:    "invalid summary",
			cfg:     &ReasoningConfig{Summary: strPtr("bad")},
			wantErr: true,
		},
		{
			name:    "valid max_tokens",
			cfg:     &ReasoningConfig{MaxTokens: intPtr(4096)},
			wantErr: false,
		},
		{
			name:    "invalid max_tokens zero",
			cfg:     &ReasoningConfig{MaxTokens: intPtr(0)},
			wantErr: true,
		},
		{
			name:    "invalid max_tokens negative",
			cfg:     &ReasoningConfig{MaxTokens: intPtr(-1)},
			wantErr: true,
		},
		{
			name: "all fields valid",
			cfg: &ReasoningConfig{
				Effort:    strPtr("high"),
				MaxTokens: intPtr(4096),
				Exclude:   boolPtr(false),
				Summary:   strPtr("auto"),
				Enable:    true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestReasoningConfig_EffortLowercased(t *testing.T) {
	cfg := &ReasoningConfig{Effort: strPtr("HIGH")}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if *cfg.Effort != "high" {
		t.Errorf("effort = %q, want 'high'", *cfg.Effort)
	}
}

func TestReasoningConfig_SummaryLowercased(t *testing.T) {
	cfg := &ReasoningConfig{Summary: strPtr("DETAILED")}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if *cfg.Summary != "detailed" {
		t.Errorf("summary = %q, want 'detailed'", *cfg.Summary)
	}
}

func TestProviderModelConfig_ValidateWithReasoning(t *testing.T) {
	pmc := &ProviderModelConfig{
		Model: "gpt-5",
		Reasoning: &ReasoningConfig{
			Effort:    strPtr("high"),
			MaxTokens: intPtr(8192),
			Exclude:   boolPtr(false),
		},
	}
	if err := pmc.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestLoadConfig_SearXNGEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	configJSON := `{
		"tools": {
			"web": {
				"searxng": {
					"enabled": true,
					"instance_url": "https://search.example.com",
					"categories": "news",
					"language": "es",
					"safesearch": 2,
					"max_results": 10
				}
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if !cfg.Tools.Web.SearXNG.Enabled {
		t.Error("SearXNG should be enabled")
	}
	if cfg.Tools.Web.SearXNG.InstanceURL != "https://search.example.com" {
		t.Errorf("Expected instance_url 'https://search.example.com', got '%s'", cfg.Tools.Web.SearXNG.InstanceURL)
	}
	if cfg.Tools.Web.SearXNG.Categories != "news" {
		t.Errorf("Expected categories 'news', got '%s'", cfg.Tools.Web.SearXNG.Categories)
	}
	if cfg.Tools.Web.SearXNG.Language != "es" {
		t.Errorf("Expected language 'es', got '%s'", cfg.Tools.Web.SearXNG.Language)
	}
	if cfg.Tools.Web.SearXNG.SafeSearch != 2 {
		t.Errorf("Expected safesearch 2, got %d", cfg.Tools.Web.SearXNG.SafeSearch)
	}
	if cfg.Tools.Web.SearXNG.MaxResults != 10 {
		t.Errorf("Expected max_results 10, got %d", cfg.Tools.Web.SearXNG.MaxResults)
	}
}

func TestAgentConfig_Description(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": { "workspace": "~/.lele/workspace", "model": "gpt-4" },
			"list": [
				{
					"id": "sales",
					"name": "Sales Bot",
					"description": "Handles sales inquiries and product recommendations"
				},
				{
					"id": "support",
					"name": "Support Bot"
				}
			]
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Agents.List) != 2 {
		t.Fatalf("agents.list len = %d, want 2", len(cfg.Agents.List))
	}

	sales := cfg.Agents.List[0]
	if sales.Description != "Handles sales inquiries and product recommendations" {
		t.Errorf("sales.Description = %q, want %q", sales.Description, "Handles sales inquiries and product recommendations")
	}

	support := cfg.Agents.List[1]
	if support.Description != "" {
		t.Errorf("support.Description = %q, want empty", support.Description)
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var roundTrip Config
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("round-trip unmarshal: %v", err)
	}

	if roundTrip.Agents.List[0].Description != "Handles sales inquiries and product recommendations" {
		t.Errorf("round-trip sales.Description = %q", roundTrip.Agents.List[0].Description)
	}
	if roundTrip.Agents.List[1].Description != "" {
		t.Errorf("round-trip support.Description = %q, want empty", roundTrip.Agents.List[1].Description)
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func boolPtr(b bool) *bool    { return &b }
func TestConfig_EvictExcluded_Default(t *testing.T) {
	t.Setenv("LELE_EVICT_EXCLUDED", "")
	cfg := DefaultConfig()

	if !cfg.EvictExcludedFromMemory() {
		t.Error("EvictExcludedFromMemory should be true by default")
	}
}

func TestConfig_EvictExcluded_False(t *testing.T) {
	t.Setenv("LELE_EVICT_EXCLUDED", "")
	cfg := DefaultConfig()
	cfg.Session.EvictExcludedFromMemory = false

	if cfg.EvictExcludedFromMemory() {
		t.Error("EvictExcludedFromMemory should be false when Session.EvictExcludedFromMemory is false")
	}
}

func TestConfig_EvictExcluded_EnvOverride(t *testing.T) {
	cfg := DefaultConfig()
	t.Setenv("LELE_EVICT_EXCLUDED", "false")
	if cfg.EvictExcludedFromMemory() {
		t.Error("EvictExcludedFromMemory should be false when LELE_EVICT_EXCLUDED=false")
	}

	cfg.Session.EvictExcludedFromMemory = false
	t.Setenv("LELE_EVICT_EXCLUDED", "true")
	if !cfg.EvictExcludedFromMemory() {
		t.Error("EvictExcludedFromMemory should be true when LELE_EVICT_EXCLUDED=true")
	}
}

func TestHasUsableProvider(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *Config
		expected bool
	}{
		{"nil providers", &Config{Providers: nil}, false},
		{"empty named", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{}}}, false},
		{"provider with key but no models", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{
			"openai": {ProviderConfig: ProviderConfig{APIKey: "sk-xxx"}, Models: map[string]ProviderModelConfig{}},
		}}}, false},
		{"provider with models but no key", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{
			"openai": {ProviderConfig: ProviderConfig{}, Models: map[string]ProviderModelConfig{"gpt": {}}},
		}}}, false},
		{"provider with key and models", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{
			"openai": {ProviderConfig: ProviderConfig{APIKey: "sk-xxx"}, Models: map[string]ProviderModelConfig{"gpt": {}}},
		}}}, true},
		{"ollama without key but with models", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{
			"ollama": {Type: "ollama", ProviderConfig: ProviderConfig{}, Models: map[string]ProviderModelConfig{"llama": {}}},
		}}}, true},
		{"ollama without models", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{
			"ollama": {Type: "ollama", ProviderConfig: ProviderConfig{}, Models: map[string]ProviderModelConfig{}},
		}}}, false},
		{"multiple providers, one valid", &Config{Providers: &ProvidersConfig{Named: map[string]NamedProviderConfig{
			"empty":  {ProviderConfig: ProviderConfig{}, Models: map[string]ProviderModelConfig{}},
			"openai": {ProviderConfig: ProviderConfig{APIKey: "sk-xxx"}, Models: map[string]ProviderModelConfig{"gpt": {}}},
		}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.HasUsableProvider()
			if got != tt.expected {
				t.Errorf("HasUsableProvider() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultConfig_SubagentMaxRetries(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.SubagentMaxRetries != 2 {
		t.Errorf("SubagentMaxRetries = %d, want 2 (0 disables subagent retries entirely)",
			cfg.Agents.Defaults.SubagentMaxRetries)
	}
}

func TestApplyDefaults_SubagentMaxRetries(t *testing.T) {
	t.Run("unset inherits default", func(t *testing.T) {
		doc := defaultEditableDocument()
		doc.Agents.Defaults.SubagentMaxRetries = 0
		applyDefaults(doc)
		if got := doc.Agents.Defaults.SubagentMaxRetries; got != 2 {
			t.Errorf("SubagentMaxRetries = %d, want 2", got)
		}
	})

	t.Run("explicit value preserved", func(t *testing.T) {
		doc := defaultEditableDocument()
		doc.Agents.Defaults.SubagentMaxRetries = 5
		applyDefaults(doc)
		if got := doc.Agents.Defaults.SubagentMaxRetries; got != 5 {
			t.Errorf("SubagentMaxRetries = %d, want 5", got)
		}
	})
}

func TestLoadConfig_SubagentMaxRetriesZeroPinned(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	if err := os.WriteFile(path, []byte(`{"agents":{"defaults":{"subagent_max_retries":0}}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if got := loaded.Agents.Defaults.SubagentMaxRetries; got != 0 {
		t.Errorf("SubagentMaxRetries = %d, want 0 (explicit 0 must disable retries)", got)
	}
}

func TestNormalizeThinkingType(t *testing.T) {
	tests := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"", ThinkingTypeAuto, true},
		{"auto", ThinkingTypeAuto, true},
		{"AUTO", ThinkingTypeAuto, true},
		{" deepseek ", ThinkingTypeDeepSeek, true},
		{"DeepSeek", ThinkingTypeDeepSeek, true},
		{"openai", ThinkingTypeOpenAI, true},
		{"openrouter", ThinkingTypeOpenRouter, true},
		{"qwen", ThinkingTypeQwen, true},
		{"none", ThinkingTypeNone, true},
		{"banana", "", false},
		{"deep-seek", "", false},
		{"off", "", false},
	}

	for _, tc := range tests {
		got, ok := NormalizeThinkingType(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("NormalizeThinkingType(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

func TestProviderModelConfig_ValidateThinkingType(t *testing.T) {
	for _, valid := range []string{"", "auto", "deepseek", "openai", "openrouter", "qwen", "none"} {
		cfg := ProviderModelConfig{Model: "m", ThinkingType: valid}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with thinking_type %q: %v, want nil", valid, err)
		}
	}

	cfg := ProviderModelConfig{Model: "m", ThinkingType: "banana"}
	err := cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() with thinking_type %q: nil, want error", "banana")
	}
	if !strings.Contains(err.Error(), "thinking_type") {
		t.Errorf("error %q should mention thinking_type", err)
	}
}

func TestProvidersConfig_UnmarshalRejectsInvalidThinkingType(t *testing.T) {
	var p ProvidersConfig
	err := json.Unmarshal([]byte(`{
		"myprov": {
			"api_base": "https://example.com/v1",
			"models": {"m1": {"model": "m1", "thinking_type": "banana"}}
		}
	}`), &p)
	if err == nil {
		t.Fatal("unmarshal with invalid thinking_type: nil, want error")
	}
	if !strings.Contains(err.Error(), "thinking_type") {
		t.Errorf("error %q should mention thinking_type", err)
	}
}

func TestProvidersConfig_UnmarshalAcceptsThinkingType(t *testing.T) {
	var p ProvidersConfig
	if err := json.Unmarshal([]byte(`{
		"myprov": {
			"api_base": "https://example.com/v1",
			"models": {"m1": {"model": "m1", "thinking_type": "deepseek"}}
		}
	}`), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := p.Named["myprov"].Models["m1"].ThinkingType
	if got != ThinkingTypeDeepSeek {
		t.Errorf("ThinkingType = %q, want %q", got, ThinkingTypeDeepSeek)
	}
}

func TestProvidersConfigMarshalJSONPersistsAuthMethodOnlyProvider(t *testing.T) {
	p := &ProvidersConfig{Anthropic: ProviderConfig{AuthMethod: "oauth"}}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) == "null" {
		t.Fatal("marshal = null, want an object containing the auth_method-only provider")
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal marshalled data %q: %v", data, err)
	}
	anthropic, ok := out["anthropic"]
	if !ok {
		t.Fatalf("marshalled JSON %q has no %q entry", data, "anthropic")
	}
	if got := anthropic["auth_method"]; got != "oauth" {
		t.Errorf("anthropic.auth_method = %v, want %q", got, "oauth")
	}

	empty, err := json.Marshal(&ProvidersConfig{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(empty) != "null" {
		t.Errorf("marshal empty = %q, want %q", empty, "null")
	}
}

func TestProvidersConfigAuthMethodRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	cfg := DefaultConfig()
	cfg.Providers.Anthropic.AuthMethod = "oauth"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Providers == nil {
		t.Fatal("loaded.Providers = nil, want the saved providers section")
	}
	if got := loaded.Providers.Anthropic.AuthMethod; got != "oauth" {
		t.Errorf("loaded.Providers.Anthropic.AuthMethod = %q, want %q", got, "oauth")
	}
}

func TestNormalizeProviderKey_OpenCodeGo(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"opencode-go", "opencode_go"},
		{"OpenCode-Go", "opencode_go"},
		{"opencode_go", "opencode_go"},
		{"opencode-zen", "opencode"},
		{"zen", "opencode"},
	}

	for _, tt := range tests {
		if got := normalizeProviderKey(tt.input); got != tt.want {
			t.Errorf("normalizeProviderKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestProvidersConfigMarshalJSONPersistsKeylessConfiguredProvider(t *testing.T) {
	p := &ProvidersConfig{
		Named: map[string]NamedProviderConfig{
			"ollama": {
				Type:           "ollama",
				ProviderConfig: ProviderConfig{APIBase: "http://localhost:11434/v1"},
			},
			"openai": {
				Type:           "openai",
				ProviderConfig: ProviderConfig{ConnectMode: "oauth"},
				WebSearch:      boolPtr(false),
			},
		},
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal marshalled data %q: %v", data, err)
	}

	ollama, ok := out["ollama"]
	if !ok {
		t.Fatalf("marshalled JSON %q has no keyless APIBase provider", data)
	}
	if got := ollama["api_base"]; got != "http://localhost:11434/v1" {
		t.Errorf("ollama.api_base = %v, want local endpoint", got)
	}
	if got := ollama["type"]; got != "ollama" {
		t.Errorf("ollama.type = %v, want %q", got, "ollama")
	}

	openai, ok := out["openai"]
	if !ok {
		t.Fatalf("marshalled JSON %q has no OAuth-completed provider", data)
	}
	if got := openai["connect_mode"]; got != "oauth" {
		t.Errorf("openai.connect_mode = %v, want %q", got, "oauth")
	}
}

func TestProvidersConfigRoundTripsKeylessConfiguredProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := DefaultConfig()
	cfg.Providers.Ollama.APIBase = "http://192.168.1.50:11434/v1"
	cfg.Providers.Ollama.Proxy = "http://proxy.lan:3128"

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Providers == nil {
		t.Fatal("loaded.Providers = nil")
	}
	if loaded.Providers.Ollama.APIBase != cfg.Providers.Ollama.APIBase {
		t.Errorf("ollama.api_base = %q, want %q", loaded.Providers.Ollama.APIBase, cfg.Providers.Ollama.APIBase)
	}
	if loaded.Providers.Ollama.Proxy != cfg.Providers.Ollama.Proxy {
		t.Errorf("ollama.proxy = %q, want %q", loaded.Providers.Ollama.Proxy, cfg.Providers.Ollama.Proxy)
	}
}

func TestDefaultConfigMarshalsNoProviders(t *testing.T) {
	data, err := json.Marshal(DefaultConfig().Providers)
	if err != nil {
		t.Fatalf("marshal DefaultConfig().Providers: %v", err)
	}
	if string(data) != "null" {
		t.Fatalf("a pristine config must persist no provider, got %s", data)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if got := string(file["providers"]); got != "null" {
		t.Fatalf("saved config persists providers %s, want null", got)
	}
}
