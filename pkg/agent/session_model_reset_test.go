package agent

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestHint_UnpinnedResendKeepsModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Model: &config.AgentModelConfig{Primary: "default-model"}},
				{ID: "agent1", Model: &config.AgentModelConfig{Primary: "agent1-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model"},
						"agent1-model":  {Model: "agent1-model"},
						"custom-model":  {Model: "custom-model"},
					},
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sessionKey := "test-session:unpinned-model"

	if _, pinned := al.sessionAgentOverride(sessionKey); pinned {
		t.Fatal("session starts pinned — test setup broken")
	}

	al.providable.SetSessionModel(sessionKey, "custom-model")
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("after dropdown selection: model = %q, want %q", got, "test:custom-model")
	}

	al.providable.HintSessionAgent(sessionKey, al.providable.GetDefaultAgentID())

	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("model after redundant agent_id resend = %q, want %q "+
			"(a routing hint must not clear the dropdown selection)",
			got, "test:custom-model")
	}

	for i := 0; i < 5; i++ {
		al.providable.HintSessionAgent(sessionKey, al.providable.GetDefaultAgentID())
	}
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("model after repeated hints = %q, want %q", got, "test:custom-model")
	}

	if _, pinned := al.sessionAgentOverride(sessionKey); pinned {
		t.Error("a routing hint pinned the session — routes can no longer win")
	}
}

func TestHint_AgentSwitchStillBindsAndKeepsModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Model: &config.AgentModelConfig{Primary: "default-model"}},
				{ID: "agent1", Model: &config.AgentModelConfig{Primary: "agent1-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model"},
						"agent1-model":  {Model: "agent1-model"},
						"custom-model":  {Model: "custom-model"},
					},
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sessionKey := "test-session:hint-switch"

	al.providable.SetSessionModel(sessionKey, "custom-model")
	al.providable.HintSessionAgent(sessionKey, "agent1")

	if got := al.providable.GetSessionAgent(sessionKey); got != "agent1" {
		t.Fatalf("agent after hint = %q, want %q", got, "agent1")
	}
	if id, pinned := al.sessionAgentOverride(sessionKey); !pinned || id != "agent1" {
		t.Fatalf("hint bind did not produce a pin: (%q,%v)", id, pinned)
	}
	if got := al.providable.GetSessionModel(sessionKey); got != "test:custom-model" {
		t.Fatalf("model after hint bind = %q, want %q", got, "test:custom-model")
	}
}

func TestSetSessionAgent_ExplicitSwitchStillResetsModel(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "agent-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "default-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "agent1", Model: &config.AgentModelConfig{Primary: "agent1-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"default-model": {Model: "default-model"},
						"agent1-model":  {Model: "agent1-model"},
						"custom-model":  {Model: "custom-model"},
					},
				},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sessionKey := "test-session:explicit-switch"

	al.providable.SetSessionModel(sessionKey, "custom-model")
	al.providable.SetSessionAgent(sessionKey, "agent1")

	if got := al.providable.GetSessionModel(sessionKey); got != "test:agent1-model" {
		t.Fatalf("model after explicit switch = %q, want the new agent's default %q",
			got, "test:agent1-model")
	}
}
