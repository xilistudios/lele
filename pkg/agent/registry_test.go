package agent

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

type mockRegistryProvider struct{}

func (m *mockRegistryProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "mock", FinishReason: "stop"}, nil
}

func (m *mockRegistryProvider) GetDefaultModel() string {
	return "mock-model"
}

func testCfg(t *testing.T, agents []config.AgentConfig) *config.Config {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         "/tmp/lele-test-registry",
				Model:             "testprovider:test-model",
				MaxTokens:         8192,
				MaxToolIterations: 10,
			},
			List: agents,
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

func TestNewAgentRegistry_ImplicitMain(t *testing.T) {
	cfg := testCfg(t, nil)
	registry := NewAgentRegistry(cfg)

	ids := registry.ListAgentIDs()
	if len(ids) != 1 || ids[0] != "main" {
		t.Errorf("expected implicit main agent, got %v", ids)
	}

	agent, ok := registry.GetAgent("main")
	if !ok || agent == nil {
		t.Fatal("expected to find 'main' agent")
	}
	if agent.ID != "main" {
		t.Errorf("agent.ID = %q, want 'main'", agent.ID)
	}
}

func TestNewAgentRegistry_ExplicitAgents(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "sales", Default: true, Name: "Sales Bot"},
		{ID: "support", Name: "Support Bot"},
	})
	registry := NewAgentRegistry(cfg)

	ids := registry.ListAgentIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 agents, got %d: %v", len(ids), ids)
	}

	sales, ok := registry.GetAgent("sales")
	if !ok || sales == nil {
		t.Fatal("expected to find 'sales' agent")
	}
	if sales.Name != "Sales Bot" {
		t.Errorf("sales.Name = %q, want 'Sales Bot'", sales.Name)
	}

	support, ok := registry.GetAgent("support")
	if !ok || support == nil {
		t.Fatal("expected to find 'support' agent")
	}
}

func TestAgentRegistry_GetAgent_Normalize(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "my-agent", Default: true},
	})
	registry := NewAgentRegistry(cfg)

	agent, ok := registry.GetAgent("My-Agent")
	if !ok || agent == nil {
		t.Fatal("expected to find agent with normalized ID")
	}
	if agent.ID != "my-agent" {
		t.Errorf("agent.ID = %q, want 'my-agent'", agent.ID)
	}
}

func TestAgentRegistry_GetDefaultAgent(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta", Default: true},
	})
	registry := NewAgentRegistry(cfg)

	// GetDefaultAgent first checks for "main", then returns any
	agent := registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected a default agent")
	}
}

func TestAgentRegistry_CanSpawnSubagent(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{
			ID:      "parent",
			Default: true,
			Subagents: &config.SubagentsConfig{
				AllowAgents: []string{"child1", "child2"},
			},
		},
		{ID: "child1"},
		{ID: "child2"},
		{ID: "restricted"},
	})
	registry := NewAgentRegistry(cfg)

	if !registry.CanSpawnSubagent("parent", "child1") {
		t.Error("expected parent to be allowed to spawn child1")
	}
	if !registry.CanSpawnSubagent("parent", "child2") {
		t.Error("expected parent to be allowed to spawn child2")
	}
	if registry.CanSpawnSubagent("parent", "restricted") {
		t.Error("expected parent to NOT be allowed to spawn restricted")
	}
	if registry.CanSpawnSubagent("child1", "child2") {
		t.Error("expected child1 to NOT be allowed to spawn (no subagents config)")
	}
}

func TestAgentRegistry_CanSpawnSubagent_Wildcard(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{
			ID:      "admin",
			Default: true,
			Subagents: &config.SubagentsConfig{
				AllowAgents: []string{"*"},
			},
		},
		{ID: "any-agent"},
	})
	registry := NewAgentRegistry(cfg)

	if !registry.CanSpawnSubagent("admin", "any-agent") {
		t.Error("expected wildcard to allow spawning any agent")
	}
	if !registry.CanSpawnSubagent("admin", "nonexistent") {
		t.Error("expected wildcard to allow spawning even nonexistent agents")
	}
}

func TestAgentRegistry_CanSpawnSubagent_DefaultAgentNoConfig(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "main", Default: true}, // No subagents config
		{ID: "coder"},
		{ID: "reviewer"},
	})
	registry := NewAgentRegistry(cfg)

	// Main agent without config should be able to spawn existing agents
	if !registry.CanSpawnSubagent("main", "coder") {
		t.Error("expected main to be allowed to spawn coder (no config, but coder exists)")
	}
	if !registry.CanSpawnSubagent("main", "reviewer") {
		t.Error("expected main to be allowed to spawn reviewer")
	}

	// Main should NOT be able to spawn nonexistent agent
	if registry.CanSpawnSubagent("main", "nonexistent") {
		t.Error("expected main to NOT be allowed to spawn nonexistent agent")
	}

	// Non-default agent without config cannot spawn
	if registry.CanSpawnSubagent("coder", "reviewer") {
		t.Error("expected coder (non-default, no config) to NOT be allowed to spawn")
	}
}

func TestAgentInstance_Model(t *testing.T) {
	model := &config.AgentModelConfig{Primary: "claude-opus"}
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "custom", Default: true, Model: model},
	})
	registry := NewAgentRegistry(cfg)

	agent, _ := registry.GetAgent("custom")
	if agent.Model != "claude-opus" {
		t.Errorf("agent.Model = %q, want 'claude-opus'", agent.Model)
	}
}

func TestAgentInstance_FallbackInheritance(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "inherit", Default: true},
	})
	cfg.Agents.Defaults.ModelFallbacks = []string{"openai/gpt-4o-mini", "anthropic/haiku"}
	registry := NewAgentRegistry(cfg)

	agent, _ := registry.GetAgent("inherit")
	if len(agent.Fallbacks) != 2 {
		t.Errorf("expected 2 fallbacks inherited from defaults, got %d", len(agent.Fallbacks))
	}
}

func TestAgentInstance_FallbackExplicitEmpty(t *testing.T) {
	model := &config.AgentModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{}, // explicitly empty = disable
	}
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "no-fallback", Default: true, Model: model},
	})
	cfg.Agents.Defaults.ModelFallbacks = []string{"should-not-inherit"}
	registry := NewAgentRegistry(cfg)

	agent, _ := registry.GetAgent("no-fallback")
	if len(agent.Fallbacks) != 0 {
		t.Errorf("expected 0 fallbacks (explicit empty), got %d: %v", len(agent.Fallbacks), agent.Fallbacks)
	}
}

func TestAgentRegistry_ReloadAgents_AddNewAgent(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)

	if len(registry.ListAgentIDs()) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(registry.ListAgentIDs()))
	}

	// Add a new agent via reload
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
		{ID: "beta"},
	})
	registry.ReloadAgents(newCfg)

	ids := registry.ListAgentIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 agents after reload, got %d: %v", len(ids), ids)
	}
	if _, ok := registry.GetAgent("beta"); !ok {
		t.Error("expected 'beta' agent after reload")
	}
}

func TestAgentRegistry_ReloadAgents_RemoveAgent(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
		{ID: "beta"},
	})
	registry := NewAgentRegistry(cfg)

	// Remove beta via reload
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry.ReloadAgents(newCfg)

	ids := registry.ListAgentIDs()
	if len(ids) != 1 || ids[0] != "alpha" {
		t.Errorf("expected only 'alpha' after reload, got %v", ids)
	}
}

func TestAgentRegistry_ReloadAgents_PreserveUnchanged(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)

	original, _ := registry.GetAgent("alpha")
	originalPtr := original // Save pointer to compare

	// Reload with same config — agent should be preserved
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded != originalPtr {
		t.Error("expected same agent instance when config unchanged, got new instance")
	}
}

func TestAgentRegistry_ReloadAgents_RecreateOnModelChange(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)

	original, _ := registry.GetAgent("alpha")

	// Change model
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Model: &config.AgentModelConfig{Primary: "testprovider:different-model"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Error("expected new agent instance when model changed, got same instance")
	}
}

func TestAgentConfigChanged_Temperature(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	// Change temperature via agent config
	temp := 0.3
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Temperature: &temp},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Error("expected new agent instance when temperature changed, got same instance")
	}
	if reloaded.Temperature != 0.3 {
		t.Errorf("expected temperature 0.3, got %v", reloaded.Temperature)
	}
}

func TestAgentConfigChanged_Fallbacks(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	// Change fallbacks via agent model config
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Model: &config.AgentModelConfig{
			Primary:   "testprovider:test-model",
			Fallbacks: []string{"testprovider:fallback-model"},
		}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Error("expected new agent instance when fallbacks changed, got same instance")
	}
	if len(reloaded.Fallbacks) != 1 || reloaded.Fallbacks[0] != "testprovider:fallback-model" {
		t.Errorf("expected fallbacks [testprovider:fallback-model], got %v", reloaded.Fallbacks)
	}
}

func TestAgentConfigChanged_ContextWindow(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	// Add a model config with a specific context window
	cfg.Providers.Named["testprovider"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "test-key",
			APIBase: "https://test.example.com/v1",
		},
		Models: map[string]config.ProviderModelConfig{
			"test-model": {
				ContextWindow: 64000,
			},
			"different-model": {
				ContextWindow: 256000,
			},
		},
	}
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	if original.ContextWindow != 64000 {
		t.Fatalf("expected initial context window 64000, got %d", original.ContextWindow)
	}

	// Change model to one with a different context window
	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Model: &config.AgentModelConfig{Primary: "testprovider:different-model"}},
	})
	newCfg.Providers.Named["testprovider"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "test-key",
			APIBase: "https://test.example.com/v1",
		},
		Models: map[string]config.ProviderModelConfig{
			"test-model": {
				ContextWindow: 64000,
			},
			"different-model": {
				ContextWindow: 256000,
			},
		},
	}
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Error("expected new agent instance when context window changed, got same instance")
	}
	if reloaded.ContextWindow != 256000 {
		t.Errorf("expected context window 256000, got %d", reloaded.ContextWindow)
	}
}
