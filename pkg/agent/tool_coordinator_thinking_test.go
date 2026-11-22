package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestSubagentWiring_SurfacesThinkingLevel(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	high := "high"
	off := "off"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				ThinkingLevel:     &high,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true},
				{ID: "worker", ThinkingLevel: &off},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	sm := al.GetSubagents()["main"]
	if sm == nil {
		t.Fatal("no subagent manager for parent agent 'main'")
	}

	parent, ok := al.registry.GetAgent("main")
	if !ok || parent.ThinkingLevel != "high" {
		t.Fatalf("precondition: parent resolved ThinkingLevel = %q, want %q", parent.ThinkingLevel, "high")
	}

	if got := sm.ThinkingLevelForTest(); got != "high" {
		t.Errorf("manager fallback ThinkingLevel = %q, want %q", got, "high")
	}

	found := sm.AgentContextForTest("worker")
	if found.ThinkingLevel != "off" {
		t.Errorf("callback(known target).ThinkingLevel = %q, want %q", found.ThinkingLevel, "off")
	}

	fallback := sm.AgentContextForTest("does-not-exist")
	if fallback.ThinkingLevel != "high" {
		t.Errorf("callback(unknown target).ThinkingLevel = %q, want %q", fallback.ThinkingLevel, "high")
	}
}
