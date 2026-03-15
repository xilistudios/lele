package agent

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestNewAgentInstance_UsesDefaultsTemperatureAndMaxTokens(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
		},
	}

	configuredTemp := 1.0
	cfg.Agents.Defaults.Temperature = &configuredTemp

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

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

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
		},
	}

	configuredTemp := 0.0
	cfg.Agents.Defaults.Temperature = &configuredTemp

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

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

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
		},
	}

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

	if agent.Temperature != 0.7 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 0.7)
	}
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
	cfg.Agents.Defaults.Model = "chutes/minimax"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"chutes": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"minimax": {Model: "minimax_m2.5"},
			},
		},
	}

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)
	if agent.Model != "chutes/minimax_m2.5" {
		t.Fatalf("Model = %q, want %q", agent.Model, "chutes/minimax_m2.5")
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
	cfg.Agents.Defaults.Model = "qwen/qwen3.5-397b-a17b-thinking"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"nanogpt": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"qwen/qwen3.5-397b-a17b-thinking": {Model: "Qwen/Qwen3.5-397B-A17B-Thinking-2507"},
			},
		},
	}

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)
	if agent.Model != "nanogpt/Qwen/Qwen3.5-397B-A17B-Thinking-2507" {
		t.Fatalf("Model = %q, want %q", agent.Model, "nanogpt/Qwen/Qwen3.5-397B-A17B-Thinking-2507")
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
	cfg.Agents.Defaults.Model = "openai/gpt-4o"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Vision: true},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if _, ok := agent.Tools.Get("read_image"); !ok {
		t.Fatal("expected read_image tool to be registered")
	}
	if !agent.SupportsImages {
		t.Fatal("expected SupportsImages to be true")
	}
}

func TestNewAgentInstance_DoesNotRegisterReadImageToolWithoutVision(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = tmpDir
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "openai/gpt-4o-mini"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o-mini": {Vision: false},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if _, ok := agent.Tools.Get("read_image"); ok {
		t.Fatal("did not expect read_image tool to be registered")
	}
	if agent.SupportsImages {
		t.Fatal("expected SupportsImages to be false")
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
	cfg.Agents.Defaults.Model = "myprovider/vision-model"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"myprovider": {
			Type: "openai",
			Models: map[string]config.ProviderModelConfig{
				// Alias "vision-model" maps to resolved model "gpt-4o-vision"
				"vision-model": {Model: "gpt-4o-vision", Vision: true},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if _, ok := agent.Tools.Get("read_image"); !ok {
		t.Fatal("expected read_image tool to be registered when using model alias with vision: true")
	}
	if !agent.SupportsImages {
		t.Fatal("expected SupportsImages to be true")
	}
}
