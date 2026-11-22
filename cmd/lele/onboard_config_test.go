package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func saveMinimalAndParse(t *testing.T, cfg *config.Config) (string, map[string]interface{}) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.SaveMinimalConfig(path, cfg); err != nil {
		t.Fatalf("SaveMinimalConfig failed: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal of saved file failed: %v\n%s", err, data)
	}
	return path, parsed
}

func jsonSection(t *testing.T, parsed map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	raw, ok := parsed[key]
	if !ok {
		t.Fatalf("saved JSON has no %q section; keys: %v", key, jsonKeys(parsed))
	}
	section, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("%s is %T, want object", key, raw)
	}
	return section
}

func jsonKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestSetWebUIEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	if !cfg.Channels.Web.Enabled || !cfg.Channels.Native.Enabled {
		t.Fatal("expected web and native to default to enabled")
	}

	setWebUIEnabled(cfg, false)
	if cfg.Channels.Web.Enabled {
		t.Error("Web.Enabled = true after decline, want false")
	}
	if cfg.Channels.Native.Enabled {
		t.Error("Native.Enabled = true after decline, want false")
	}

	setWebUIEnabled(cfg, true)
	if !cfg.Channels.Web.Enabled || !cfg.Channels.Native.Enabled {
		t.Error("web/native must both be enabled after enabling")
	}
}

func TestOnboard_SaveMinimalConfig_DefaultsWriteNoChannelsOrGateway(t *testing.T) {
	_, parsed := saveMinimalAndParse(t, config.DefaultConfig())

	if _, ok := parsed["channels"]; ok {
		t.Errorf("saved JSON must not contain \"channels\" when everything equals the defaults, got: %v", parsed["channels"])
	}
	if _, ok := parsed["gateway"]; ok {
		t.Errorf("saved JSON must not contain \"gateway\" when it equals the default, got: %v", parsed["gateway"])
	}
	if _, ok := parsed["agents"]; !ok {
		t.Fatal("expected an \"agents\" section to still be written")
	}
}

func TestOnboard_SaveMinimalConfig_DeclinedWebUIWritesOnlyFalseFlags(t *testing.T) {
	cfg := config.DefaultConfig()
	setWebUIEnabled(cfg, false)

	path, parsed := saveMinimalAndParse(t, cfg)

	channels := jsonSection(t, parsed, "channels")
	if len(channels) != 2 {
		t.Fatalf("channels keys = %v, want exactly [web native]", jsonKeys(channels))
	}
	web := jsonSection(t, channels, "web")
	if len(web) != 1 {
		t.Fatalf("web keys = %v, want exactly [enabled]", jsonKeys(web))
	}
	if web["enabled"] != false {
		t.Errorf("web.enabled = %v, want false", web["enabled"])
	}
	native := jsonSection(t, channels, "native")
	if len(native) != 1 {
		t.Fatalf("native keys = %v, want exactly [enabled]", jsonKeys(native))
	}
	if native["enabled"] != false {
		t.Errorf("native.enabled = %v, want false", native["enabled"])
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if loaded.Channels.Web.Enabled {
		t.Error("reloaded web.enabled = true, want false")
	}
	if loaded.Channels.Native.Enabled {
		t.Error("reloaded native.enabled = true, want false")
	}
}

func TestOnboard_SaveMinimalConfig_GatewayPortOnly(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Gateway.Port = 9999

	path, parsed := saveMinimalAndParse(t, cfg)

	gateway := jsonSection(t, parsed, "gateway")
	if len(gateway) != 1 {
		t.Fatalf("gateway keys = %v, want exactly [port]", jsonKeys(gateway))
	}
	if gateway["port"] != float64(9999) {
		t.Errorf("gateway.port = %v, want 9999", gateway["port"])
	}
	if _, ok := parsed["channels"]; ok {
		t.Errorf("saved JSON must not contain \"channels\", got: %v", parsed["channels"])
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if got := loaded.EffectiveServerPort(); got != 9999 {
		t.Errorf("EffectiveServerPort after reload = %d, want 9999", got)
	}
}

func TestOnboard_SaveMinimalConfig_ProviderKeyAndAgentsRoundTrip(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "anthropic"
	cfg.Agents.Defaults.Model = "anthropic:opus"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"anthropic": {
			Type: "anthropic",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-anthropic-literal-key-12345",
				APIBase: "https://api.anthropic.com/v1",
			},
			Models: map[string]config.ProviderModelConfig{
				"opus": {Model: "claude-opus-4-1", Vision: true},
			},
		},
	}
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:    "coder",
			Name:  "Coder",
			Model: &config.AgentModelConfig{Primary: "anthropic:opus"},
		},
	}

	path, parsed := saveMinimalAndParse(t, cfg)

	providers := jsonSection(t, parsed, "providers")
	anthropic := jsonSection(t, providers, "anthropic")
	if got := anthropic["api_key"]; got != "sk-anthropic-literal-key-12345" {
		t.Errorf("saved providers.anthropic.api_key = %v, want the literal key", got)
	}
	if _, ok := parsed["channels"]; ok {
		t.Errorf("saved JSON must not contain \"channels\", got: %v", parsed["channels"])
	}
	if _, ok := parsed["gateway"]; ok {
		t.Errorf("saved JSON must not contain \"gateway\", got: %v", parsed["gateway"])
	}

	loaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	prov, ok := loaded.Providers.GetNamed("anthropic")
	if !ok {
		t.Fatal("provider \"anthropic\" missing after reload")
	}
	if prov.APIKey != "sk-anthropic-literal-key-12345" {
		t.Errorf("reloaded APIKey = %q, want the literal key", prov.APIKey)
	}
	if prov.APIBase != "https://api.anthropic.com/v1" {
		t.Errorf("reloaded APIBase = %q", prov.APIBase)
	}
	if model, ok := prov.Models["opus"]; !ok || model.Model != "claude-opus-4-1" || !model.Vision {
		t.Errorf("reloaded models = %v, want opus -> claude-opus-4-1 [vision]", prov.Models)
	}
	if loaded.Providers.Anthropic.APIKey != "sk-anthropic-literal-key-12345" {
		t.Errorf("reloaded legacy Providers.Anthropic.APIKey = %q, want the literal key", loaded.Providers.Anthropic.APIKey)
	}

	if len(loaded.Agents.List) != 1 {
		t.Fatalf("reloaded agents list = %d entries, want 1", len(loaded.Agents.List))
	}
	agent := loaded.Agents.List[0]
	if agent.ID != "coder" || agent.Name != "Coder" {
		t.Errorf("reloaded agent = %q/%q, want coder/Coder", agent.ID, agent.Name)
	}
	if agent.Model == nil || agent.Model.Primary != "anthropic:opus" {
		t.Errorf("reloaded agent model = %v, want primary anthropic:opus", agent.Model)
	}
	if loaded.Agents.Defaults.Model != "anthropic:opus" || loaded.Agents.Defaults.Provider != "anthropic" {
		t.Errorf("reloaded agent defaults = %q/%q", loaded.Agents.Defaults.Provider, loaded.Agents.Defaults.Model)
	}

	if !loaded.Channels.Web.Enabled {
		t.Error("reloaded web.enabled = false, want true (default)")
	}
	if !loaded.Channels.Native.Enabled {
		t.Error("reloaded native.enabled = false, want true (default)")
	}
	if got := loaded.EffectiveServerPort(); got != config.DefaultConfig().Gateway.Port {
		t.Errorf("EffectiveServerPort = %d, want %d", got, config.DefaultConfig().Gateway.Port)
	}
}

func TestConfigureAgentDefaults_Config(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         "~/.lele/workspace",
				Provider:          "anthropic",
				Model:             "anthropic/claude-3-opus",
				MaxTokens:         8192,
				MaxToolIterations: 20,
			},
		},
	}

	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp

	if cfg.Agents.Defaults.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", cfg.Agents.Defaults.Provider, "anthropic")
	}
	if cfg.Agents.Defaults.Model != "anthropic/claude-3-opus" {
		t.Errorf("Model = %q, want %q", cfg.Agents.Defaults.Model, "anthropic/claude-3-opus")
	}
	if cfg.Agents.Defaults.MaxTokens != 8192 {
		t.Errorf("MaxTokens = %d, want %d", cfg.Agents.Defaults.MaxTokens, 8192)
	}
	if cfg.Agents.Defaults.MaxToolIterations != 20 {
		t.Errorf("MaxToolIterations = %d, want %d", cfg.Agents.Defaults.MaxToolIterations, 20)
	}
}

func TestAgentConfig_Struct(t *testing.T) {
	model := &config.AgentModelConfig{Primary: "test-model"}
	cfg := config.AgentConfig{
		ID:     "test-agent",
		Name:   "Test Agent",
		Model:  model,
		Skills: []string{"skill-1", "skill-2"},
	}

	if cfg.ID != "test-agent" {
		t.Errorf("ID = %q, want %q", cfg.ID, "test-agent")
	}
	if cfg.Name != "Test Agent" {
		t.Errorf("Name = %q, want %q", cfg.Name, "Test Agent")
	}
	if len(cfg.Skills) != 2 {
		t.Errorf("Skills length = %d, want 2", len(cfg.Skills))
	}
}

func TestProviderModelConfig_Struct(t *testing.T) {
	mcfg := config.ProviderModelConfig{
		Model:         "claude-3-opus",
		Vision:        true,
		ContextWindow: 100000,
	}

	if mcfg.Model != "claude-3-opus" {
		t.Errorf("Model = %q, want %q", mcfg.Model, "claude-3-opus")
	}
	if !mcfg.Vision {
		t.Error("Vision should be true")
	}
	if mcfg.ContextWindow != 100000 {
		t.Errorf("ContextWindow = %d, want %d", mcfg.ContextWindow, 100000)
	}
}

func TestNamedProviderConfig_Struct(t *testing.T) {
	cfg := config.NamedProviderConfig{
		Type: "anthropic",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-test-key",
			APIBase: "https://api.anthropic.com/v1",
		},
		Models: map[string]config.ProviderModelConfig{
			"opus": {Model: "claude-3-opus", Vision: true},
		},
	}

	if cfg.Type != "anthropic" {
		t.Errorf("Type = %q, want %q", cfg.Type, "anthropic")
	}
	if cfg.APIKey != "sk-test-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-test-key")
	}
}

func TestWebConfig_Struct(t *testing.T) {
	cfg := config.WebConfig{
		Enabled: true,
	}

	if !cfg.Enabled {
		t.Error("Web should be enabled")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}

	if cfg.Agents.Defaults.MaxTokens < 0 {
		t.Error("MaxTokens should not be negative")
	}
	if cfg.Agents.Defaults.MaxToolIterations < 0 {
		t.Error("MaxToolIterations should not be negative (0 = unlimited)")
	}
}

func TestConfig_WorkspacePath(t *testing.T) {
	tmpDir := "/tmp/test-workspace"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
			},
		},
	}

	workspace := cfg.WorkspacePath()
	if workspace == "" {
		t.Error("WorkspacePath() returned empty string")
	}
	if !filepath.IsAbs(workspace) && !strings.HasPrefix(workspace, "~/") {
		t.Errorf("WorkspacePath() = %q, expected absolute or home-relative path", workspace)
	}
}

func TestConfig_LogsPath(t *testing.T) {
	cfg := config.DefaultConfig()

	logsPath := cfg.LogsPath()
	if logsPath == "" {
		t.Error("LogsPath() returned empty string")
	}
}

func TestConfig_Defaults(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Agents.Defaults.MaxTokens < 0 {
		t.Error("MaxTokens should not be negative")
	}
	if cfg.Agents.Defaults.MaxToolIterations < 0 {
		t.Error("MaxToolIterations should not be negative (0 = unlimited)")
	}
	if cfg.Agents.Defaults.Temperature != nil {
		if *cfg.Agents.Defaults.Temperature < 0 || *cfg.Agents.Defaults.Temperature > 2 {
			t.Error("Temperature should be between 0 and 2")
		}
	}
}

func TestConfig_Providers(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Providers.Anthropic.APIKey == "" {
		t.Log("Anthropic API key is empty (expected for default config)")
	}

	if cfg.Providers.Named == nil {
		t.Log("Named providers map is nil (expected for default config)")
	}
}

func TestConfig_Agents(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg.Agents.Defaults.Model == "" {
		t.Error("Default model should be set")
	}

	if cfg.Agents.List == nil {
		t.Log("Agent list is nil (expected for default config)")
	}
}

func TestConfig_Channels(t *testing.T) {
	cfg := config.DefaultConfig()

	if !cfg.Channels.Web.Enabled {
		t.Log("Web channel disabled by default")
	}

	if !cfg.Channels.Native.Enabled {
		t.Log("Native channel disabled by default")
	}
	if cfg.Channels.Native.Host != "127.0.0.1" {
		t.Errorf("Native Host = %q, want %q", cfg.Channels.Native.Host, "127.0.0.1")
	}
	if cfg.Channels.Native.Port != 18790 {
		t.Errorf("Native Port = %d, want %d", cfg.Channels.Native.Port, 18790)
	}
}

func TestNativeConfig_Struct(t *testing.T) {
	cfg := config.NativeConfig{
		Enabled:          true,
		Host:             "127.0.0.1",
		Port:             18793,
		MaxClients:       5,
		TokenExpiryDays:  30,
		PinExpiryMinutes: 5,
	}

	if !cfg.Enabled {
		t.Error("Native should be enabled")
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want %q", cfg.Host, "127.0.0.1")
	}
	if cfg.MaxClients != 5 {
		t.Errorf("MaxClients = %d, want %d", cfg.MaxClients, 5)
	}
}

func TestProviderConfig_Struct(t *testing.T) {
	cfg := config.ProviderConfig{
		APIKey:  "sk-test-key",
		APIBase: "https://api.example.com/v1",
	}

	if cfg.APIKey != "sk-test-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-test-key")
	}
	if cfg.APIBase != "https://api.example.com/v1" {
		t.Errorf("APIBase = %q, want %q", cfg.APIBase, "https://api.example.com/v1")
	}
}

func TestOpenAIProviderConfig_Struct(t *testing.T) {
	cfg := config.OpenAIProviderConfig{
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-openai-key",
			APIBase: "https://api.openai.com/v1",
		},
	}

	if cfg.APIKey != "sk-openai-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "sk-openai-key")
	}
}

func TestAgentModelConfig(t *testing.T) {
	cfg := config.AgentModelConfig{
		Primary: "anthropic/claude-3-opus",
	}

	if cfg.Primary != "anthropic/claude-3-opus" {
		t.Errorf("Primary = %q, want %q", cfg.Primary, "anthropic/claude-3-opus")
	}
}

func TestFormatTemperature(t *testing.T) {
	temp := 0.7
	cfg := config.AgentDefaults{Temperature: &temp}
	if cfg.Temperature == nil {
		t.Fatal("Temperature is nil")
	}
	if *cfg.Temperature != 0.7 {
		t.Errorf("Temperature = %g, want %g", *cfg.Temperature, 0.7)
	}
}

func TestFormatTemperature_Rounding(t *testing.T) {
	temp := 0.7345
	cfg := config.AgentDefaults{Temperature: &temp}
	if cfg.Temperature == nil {
		t.Fatal("Temperature is nil")
	}
	if *cfg.Temperature != 0.7345 {
		t.Errorf("Temperature = %g, want %g", *cfg.Temperature, 0.7345)
	}
}

func TestFormatFloat(t *testing.T) {
	f := 0.12345
	if f <= 0 {
		t.Error("Float should be positive")
	}
}

func TestFormatInt(t *testing.T) {
	i := 8192
	if i <= 0 {
		t.Error("Int should be positive")
	}
}

func TestFormatIntWithDefault(t *testing.T) {
	i := 0
	if i != 0 {
		t.Errorf("Int = %d, want 0", i)
	}
}

func TestPrintProviderSummary(t *testing.T) {
	cfg := &config.Config{
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type: "anthropic",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "sk-test-key-123",
						APIBase: "https://api.anthropic.com/v1",
					},
					Models: map[string]config.ProviderModelConfig{
						"opus": {Model: "claude-3-opus", Vision: true},
					},
				},
			},
		},
	}

	keyDisplay := maskAPIKey(cfg.Providers.Named["anthropic"].APIKey)
	if keyDisplay != "sk-t...-123" {
		t.Errorf("Key display = %q, want %q", keyDisplay, "sk-t...-123")
	}
}

func TestPrintAgentSummary(t *testing.T) {
	temp := 0.7
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Model:             "anthropic/claude-3-opus",
				MaxTokens:         8192,
				Temperature:       &temp,
				MaxToolIterations: 20,
			},
		},
	}

	if cfg.Agents.Defaults.Model != "anthropic/claude-3-opus" {
		t.Errorf("Model = %q, want %q", cfg.Agents.Defaults.Model, "anthropic/claude-3-opus")
	}
}

func TestPrintWebUIEnabled(t *testing.T) {
	cfg := &config.Config{
		Channels: config.ChannelsConfig{
			Web: config.WebConfig{
				Enabled: true,
			},
		},
	}

	if !cfg.Channels.Web.Enabled {
		t.Error("Web should be enabled")
	}
}
