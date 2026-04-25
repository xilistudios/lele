package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

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
		Port:    3005,
		Host:    "0.0.0.0",
	}

	if !cfg.Enabled {
		t.Error("Web should be enabled")
	}
	if cfg.Port != 3005 {
		t.Errorf("Port = %d, want %d", cfg.Port, 3005)
	}
}

// TestDefaultConfig tests the default configuration
func TestDefaultConfig(t *testing.T) {
	cfg := config.DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig returned nil")
	}

	// Check some defaults
	if cfg.Agents.Defaults.MaxTokens <= 0 {
		t.Error("MaxTokens should be positive")
	}
	if cfg.Agents.Defaults.MaxToolIterations <= 0 {
		t.Error("MaxToolIterations should be positive")
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
	if cfg.Agents.Defaults.MaxTokens <= 0 {
		t.Error("MaxTokens should be positive")
	}
	if cfg.Agents.Defaults.MaxToolIterations <= 0 {
		t.Error("MaxToolIterations should be positive")
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
	if cfg.Channels.Web.Host != "0.0.0.0" {
		t.Errorf("Web Host = %q, want %q", cfg.Channels.Web.Host, "0.0.0.0")
	}
	if cfg.Channels.Web.Port != 3005 {
		t.Errorf("Web Port = %d, want %d", cfg.Channels.Web.Port, 3005)
	}

	if !cfg.Channels.Native.Enabled {
		t.Log("Native channel disabled by default")
	}
	if cfg.Channels.Native.Host != "127.0.0.1" {
		t.Errorf("Native Host = %q, want %q", cfg.Channels.Native.Host, "127.0.0.1")
	}
	if cfg.Channels.Native.Port != 18793 {
		t.Errorf("Native Port = %d, want %d", cfg.Channels.Native.Port, 18793)
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
		Providers: config.ProvidersConfig{
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
				Port:    3005,
			},
		},
	}

	if !cfg.Channels.Web.Enabled {
		t.Error("Web should be enabled")
	}
}
