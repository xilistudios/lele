package agent

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func createTestConfig(tmpDir string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "testprovider/test-model",
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"testprovider": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey:  "test-key",
						APIBase: "https://test.example.com/v1",
					},
				},
			},
		},
	}
}

func TestNewAgentInstance_UsesDefaultsTemperatureAndMaxTokens(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := createTestConfig(tmpDir)
	configuredTemp := 1.0
	cfg.Agents.Defaults.Temperature = &configuredTemp

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)

	if agent.MaxTokens != 1234 {
		t.Fatalf("MaxTokens = %d, want %d", agent.MaxTokens, 1234)
	}
	if agent.Temperature != 1.0 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 1.0)
	}
}

func TestNewAgentInstance_DefaultsTemperatureWhenZero(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := createTestConfig(tmpDir)
	configuredTemp := 0.0
	cfg.Agents.Defaults.Temperature = &configuredTemp

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)

	if agent.Temperature != 0.0 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 0.0)
	}
}

func TestNewAgentInstance_DefaultsTemperatureWhenUnset(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := createTestConfig(tmpDir)
	// Temperature is nil, should default to 0.7
	cfg.Agents.Defaults.Temperature = nil

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)

	if agent.Temperature != 0.7 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 0.7)
	}
}

func TestNewAgentInstance_AgentTemperatureOverridesDefaults(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := createTestConfig(tmpDir)
	defaultTemp := 0.5
	cfg.Agents.Defaults.Temperature = &defaultTemp

	agentCfg := &config.AgentConfig{
		ID:          "test-agent",
		Temperature: ptrFloat64(1.5),
	}

	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)

	if agent.Temperature != 1.5 {
		t.Fatalf("Temperature = %f, want %f (agent-level should override defaults)", agent.Temperature, 1.5)
	}
}

func ptrFloat64(v float64) *float64 {
	return &v
}

func TestNewAgentInstance_ResolvesNamedProviderModelAlias(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Provider = "chutes"
	cfg.Agents.Defaults.Model = "chutes:minimax"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"chutes": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"minimax": {Model: "minimax_m2.5"},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	if agent.Model != "chutes:minimax_m2.5" {
		t.Fatalf("Model = %q, want %q", agent.Model, "chutes:minimax_m2.5")
	}
}

func TestNewAgentInstance_ResolvesSlashModelAliasOnDefaultProvider(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Provider = "nanogpt"
	cfg.Agents.Defaults.Model = "nanogpt:qwen/qwen3.5-397b-a17b-thinking"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"nanogpt": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"qwen/qwen3.5-397b-a17b-thinking": {Model: "Qwen/Qwen3.5-397B-A17B-Thinking-2507"},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	if agent.Model != "nanogpt:Qwen/Qwen3.5-397B-A17B-Thinking-2507" {
		t.Fatalf("Model = %q, want %q", agent.Model, "nanogpt:Qwen/Qwen3.5-397B-A17B-Thinking-2507")
	}
}

func TestNewAgentInstance_RegistersReadImageToolWhenVisionEnabled(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "openai:gpt-4o"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Vision: true},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	if _, ok := agent.Tools.Get("read_image"); !ok {
		t.Fatal("expected read_image tool to be registered")
	}
	if !agent.SupportsImages {
		t.Fatal("expected SupportsImages to be true")
	}
}

func TestNewAgentInstance_ReadImageToolAlwaysRegistered(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "openai:gpt-4o-mini"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o-mini": {Vision: false},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	// read_image is always registered now, but filtered out dynamically
	// based on the current session model
	if _, ok := agent.Tools.Get("read_image"); !ok {
		t.Fatal("expected read_image tool to be registered regardless of vision support")
	}
	if agent.SupportsImages {
		t.Fatal("expected SupportsImages to be false for non-vision model")
	}
}

// TestNewAgentInstance_RegistersReadImageToolWithModelAlias tests that read_image
// is registered when using a model alias that maps to a different resolved model name.
// This tests the bug fix where getProviderModelConfig was using the resolved model
// name instead of the alias to look up the config.
func TestNewAgentInstance_RegistersReadImageToolWithModelAlias(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Provider = "myprovider"
	cfg.Agents.Defaults.Model = "myprovider:vision-model"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"myprovider": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				// Alias "vision-model" maps to resolved model "gpt-4o-vision"
				"vision-model": {Model: "gpt-4o-vision", Vision: true},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	if _, ok := agent.Tools.Get("read_image"); !ok {
		t.Fatal("expected read_image tool to be registered when using model alias with vision: true")
	}
	if !agent.SupportsImages {
		t.Fatal("expected SupportsImages to be true")
	}
}

// ============================================================================
// resolveAgentThinkingLevel
// ============================================================================

func TestResolveAgentThinkingLevel(t *testing.T) {
	tests := []struct {
		name     string
		agentCfg *config.AgentConfig
		defaults *config.AgentDefaults
		want     string
	}{
		{
			name:     "both unset",
			agentCfg: nil,
			defaults: nil,
			want:     "",
		},
		{
			name:     "nil agent config uses defaults",
			agentCfg: nil,
			defaults: &config.AgentDefaults{ThinkingLevel: strPtr("low")},
			want:     "low",
		},
		{
			name:     "agent wins over defaults",
			agentCfg: &config.AgentConfig{ThinkingLevel: strPtr("high")},
			defaults: &config.AgentDefaults{ThinkingLevel: strPtr("low")},
			want:     "high",
		},
		{
			name:     "agent unset inherits defaults",
			agentCfg: &config.AgentConfig{},
			defaults: &config.AgentDefaults{ThinkingLevel: strPtr("medium")},
			want:     "medium",
		},
		{
			name:     "agent off overrides defaults",
			agentCfg: &config.AgentConfig{ThinkingLevel: strPtr("off")},
			defaults: &config.AgentDefaults{ThinkingLevel: strPtr("high")},
			want:     "off",
		},
		{
			name:     "normalizes case and whitespace",
			agentCfg: &config.AgentConfig{ThinkingLevel: strPtr(" HIGH ")},
			defaults: nil,
			want:     "high",
		},
		{
			name:     "default sentinel resolves to unset",
			agentCfg: &config.AgentConfig{ThinkingLevel: strPtr("default")},
			defaults: &config.AgentDefaults{ThinkingLevel: strPtr("high")},
			want:     "",
		},
		{
			name:     "invalid value resolves to unset",
			agentCfg: &config.AgentConfig{ThinkingLevel: strPtr("ultra")},
			defaults: nil,
			want:     "",
		},
		{
			name:     "invalid defaults value resolves to unset",
			agentCfg: nil,
			defaults: &config.AgentDefaults{ThinkingLevel: strPtr("nope")},
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAgentThinkingLevel(tc.agentCfg, tc.defaults); got != tc.want {
				t.Errorf("resolveAgentThinkingLevel() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNewAgentInstance_SetsThinkingLevel pins the wiring: the value stored on
// the instance must be the resolved one (agent over defaults), because
// buildLLMOptions relies on it being pre-resolved.
func TestNewAgentInstance_SetsThinkingLevel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-think-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := createTestConfig(tmpDir)
	cfg.Agents.Defaults.ThinkingLevel = strPtr("low")

	// Defaults path (nil agent config = default agent).
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg)
	if agent.ThinkingLevel != "low" {
		t.Fatalf("ThinkingLevel = %q, want %q (defaults)", agent.ThinkingLevel, "low")
	}

	// Agent-specific path wins over defaults.
	agentCfg := &config.AgentConfig{ID: "main", Default: true, ThinkingLevel: strPtr(" HIGH ")}
	agent = NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)
	if agent.ThinkingLevel != "high" {
		t.Fatalf("ThinkingLevel = %q, want %q (agent wins, normalized)", agent.ThinkingLevel, "high")
	}
}
