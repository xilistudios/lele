// Lele - Ultra-lightweight personal AI agent
// T4 (per-agent thinking level): the wiring in registerSharedToolsForAgent
// must (a) seed the SubagentManager fallback with the PARENT's resolved
// ThinkingLevel and (b) surface the TARGET agent's resolved ThinkingLevel
// through the agent-context callback (parent's config when the target is
// unknown). The parent's per-session /think override must never reach either
// path — only the resolved config level is wired here.
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestSubagentWiring_SurfacesThinkingLevel(t *testing.T) {
	// Parent "main" resolves thinking_level "high"; the target "worker"
	// resolves "off" (per-agent value beats the global default).
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

	// (a) Manager fallback carries the parent's resolved level.
	if got := sm.ThinkingLevelForTest(); got != "high" {
		t.Errorf("manager fallback ThinkingLevel = %q, want %q", got, "high")
	}

	// (b) Found target: the callback surfaces the TARGET's level, not the parent's.
	found := sm.AgentContextForTest("worker")
	if found.ThinkingLevel != "off" {
		t.Errorf("callback(known target).ThinkingLevel = %q, want %q", found.ThinkingLevel, "off")
	}

	// (c) Unknown target: the fallback branch surfaces the PARENT's level.
	fallback := sm.AgentContextForTest("does-not-exist")
	if fallback.ThinkingLevel != "high" {
		t.Errorf("callback(unknown target).ThinkingLevel = %q, want %q", fallback.ThinkingLevel, "high")
	}
}
