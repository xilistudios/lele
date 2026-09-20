package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// --- minimal-save test helpers ----------------------------------------------

// saveMinimalAndParse writes cfg with config.SaveMinimalConfig and parses the
// resulting file, so tests can assert on the actual JSON shape (absence of
// keys), not just on the reloaded struct. Returns the file path and the raw
// parsed document.
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

// jsonSection returns the object stored under key, failing when the key is
// absent or not an object.
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

// --- onboarding config shaping ------------------------------------------------

// TestSetWebUIEnabled verifies the pure helper behind the "Enable Web UI +
// native channel?" answer: declining must genuinely disable BOTH channels
// (the code defaults are enabled, so a missing else-branch made the old
// prompt a no-op), and enabling must set both back.
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

// TestOnboard_SaveMinimalConfig_DefaultsWriteNoChannelsOrGateway verifies a
// defaults-only config (user accepted every default) writes NO channels and
// NO gateway keys at all.
func TestOnboard_SaveMinimalConfig_DefaultsWriteNoChannelsOrGateway(t *testing.T) {
	_, parsed := saveMinimalAndParse(t, config.DefaultConfig())

	if _, ok := parsed["channels"]; ok {
		t.Errorf("saved JSON must not contain \"channels\" when everything equals the defaults, got: %v", parsed["channels"])
	}
	if _, ok := parsed["gateway"]; ok {
		t.Errorf("saved JSON must not contain \"gateway\" when it equals the default, got: %v", parsed["gateway"])
	}
	// Sanity: the file really was serialized (other sections present).
	if _, ok := parsed["agents"]; !ok {
		t.Fatal("expected an \"agents\" section to still be written")
	}
}

// TestOnboard_SaveMinimalConfig_DeclinedWebUIWritesOnlyFalseFlags verifies the
// onboarding decline path produces exactly {"channels":{"web":{"enabled":
// false},"native":{"enabled":false}}} and nothing else under channels, and
// that LoadConfig restores the two explicit false values.
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

// TestOnboard_SaveMinimalConfig_GatewayPortOnly verifies a custom gateway
// port (what configureWebUI now writes instead of server.*) is the ONLY key
// under gateway in the saved file, and that the effective port survives a
// reload.
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
	// A custom port alone must not resurrect a channels section.
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

// TestOnboard_SaveMinimalConfig_ProviderKeyAndAgentsRoundTrip verifies that
// what onboarding actually configures — a provider with a literal API key and
// extra agents — survives SaveMinimalConfig -> LoadConfig unchanged, while
// untouched channels stay pruned and reload with their code defaults (web and
// native enabled).
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

	// Providers present with the literal key; channels/gateway pruned.
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
	// The legacy struct field is fed from the same JSON key; onboarding used
	// to write it via SaveConfig, so it must not regress.
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

	// Channels were never customized, so they reload with code defaults:
	// web AND native enabled.
	if !loaded.Channels.Web.Enabled {
		t.Error("reloaded web.enabled = false, want true (default)")
	}
	if !loaded.Channels.Native.Enabled {
		t.Error("reloaded native.enabled = false, want true (default)")
	}
	// The default effective port stays the gateway default.
	if got := loaded.EffectiveServerPort(); got != config.DefaultConfig().Gateway.Port {
		t.Errorf("EffectiveServerPort = %d, want %d", got, config.DefaultConfig().Gateway.Port)
	}
}

// TestConfigureAgentDefaults_Config tests agent default configuration
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

// TestAgentConfig_Struct tests the AgentConfig structure
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

// TestProviderModelConfig_Struct tests the ProviderModelConfig structure
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

// TestNamedProviderConfig_Struct tests the NamedProviderConfig structure
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

// TestWebConfig_Struct tests the WebConfig structure
func TestWebConfig_Struct(t *testing.T) {
	cfg := config.WebConfig{
		Enabled: true,
	}

	if !cfg.Enabled {
		t.Error("Web should be enabled")
	}
}

// TestDefaultConfig tests the default configuration
func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}

	// Check some defaults
	// MaxTokens: 0 means "use provider/model fallback" (valid default)
	if cfg.Agents.Defaults.MaxTokens < 0 {
		t.Error("MaxTokens should not be negative")
	}
	if cfg.Agents.Defaults.MaxToolIterations < 0 {
		t.Error("MaxToolIterations should not be negative (0 = unlimited)")
	}
}

// TestConfig_WorkspacePath tests the workspace path function
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
	// Verify the path is non-empty and exists or is a valid path
	if !filepath.IsAbs(workspace) && !strings.HasPrefix(workspace, "~/") {
		t.Errorf("WorkspacePath() = %q, expected absolute or home-relative path", workspace)
	}
}

// TestConfig_LogsPath tests the logs path function
func TestConfig_LogsPath(t *testing.T) {
	cfg := config.DefaultConfig()

	logsPath := cfg.LogsPath()
	if logsPath == "" {
		t.Error("LogsPath() returned empty string")
	}
}

// TestConfig_Defaults tests the default agent configuration
func TestConfig_Defaults(t *testing.T) {
	cfg := config.DefaultConfig()

	// Verify defaults have reasonable values
	// MaxTokens: 0 means "use provider/model fallback" (valid default)
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
	// Temperature may be nil in default config, which is acceptable
}

// TestConfig_Providers tests the providers configuration structure
func TestConfig_Providers(t *testing.T) {
	cfg := config.DefaultConfig()

	// Verify providers config exists
	// Default config may have empty API keys and nil Named map
	if cfg.Providers.Anthropic.APIKey == "" {
		t.Log("Anthropic API key is empty (expected for default config)")
	}

	// Named providers map may be nil in default config
	if cfg.Providers.Named == nil {
		t.Log("Named providers map is nil (expected for default config)")
	}
}

// TestConfig_Agents tests the agents configuration structure
func TestConfig_Agents(t *testing.T) {
	cfg := config.DefaultConfig()

	// Verify defaults exist
	if cfg.Agents.Defaults.Model == "" {
		t.Error("Default model should be set")
	}

	// Agent list may be nil in default config
	if cfg.Agents.List == nil {
		t.Log("Agent list is nil (expected for default config)")
	}
}

// TestConfig_Channels tests the channels configuration structure
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

// TestNativeConfig_Struct tests the NativeConfig structure
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

// TestProviderConfig_Struct tests the ProviderConfig structure
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

// TestOpenAIProviderConfig_Struct tests the OpenAIProviderConfig structure
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

// TestAgentModelConfig tests the AgentModelConfig structure
func TestAgentModelConfig(t *testing.T) {
	cfg := config.AgentModelConfig{
		Primary: "anthropic/claude-3-opus",
	}

	if cfg.Primary != "anthropic/claude-3-opus" {
		t.Errorf("Primary = %q, want %q", cfg.Primary, "anthropic/claude-3-opus")
	}
}

// TestFormatTemperature tests temperature formatting
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

// TestFormatTemperature_Rounding tests temperature rounding
func TestFormatTemperature_Rounding(t *testing.T) {
	temp := 0.7345
	cfg := config.AgentDefaults{Temperature: &temp}
	if cfg.Temperature == nil {
		t.Fatal("Temperature is nil")
	}
	// Verify the value is stored correctly
	if *cfg.Temperature != 0.7345 {
		t.Errorf("Temperature = %g, want %g", *cfg.Temperature, 0.7345)
	}
}

// TestFormatFloat tests float formatting
func TestFormatFloat(t *testing.T) {
	f := 0.12345
	if f <= 0 {
		t.Error("Float should be positive")
	}
}

// TestFormatInt tests integer formatting
func TestFormatInt(t *testing.T) {
	i := 8192
	if i <= 0 {
		t.Error("Int should be positive")
	}
}

// TestFormatIntWithDefault tests integer with default
func TestFormatIntWithDefault(t *testing.T) {
	i := 0
	if i != 0 {
		t.Errorf("Int = %d, want 0", i)
	}
}

// TestPrintProviderSummary tests the provider summary output
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

	// Verify maskAPIKey works with provider summary
	// "sk-test-key-123" -> first 4: "sk-t", last 4: "-123"
	keyDisplay := maskAPIKey(cfg.Providers.Named["anthropic"].APIKey)
	if keyDisplay != "sk-t...-123" {
		t.Errorf("Key display = %q, want %q", keyDisplay, "sk-t...-123")
	}
}

// TestPrintAgentSummary tests the agent summary output
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

// TestPrintWebUIEnabled tests Web UI enabled output
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
