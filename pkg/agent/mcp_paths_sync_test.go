// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// T3 tests for the exported MCP resync seam (plan Q1 / T3):
//   - MCPPathsFor returns EXACTLY what mcpManagerSet.pathsFor resolves for
//     the agent (never re-derived from config.json or pkg/channels — plan
//     EVIDENCE B2), is ok=false for unknown ids and nil-safe on a bare loop.
//   - SyncMCPServers is a no-op on a bare loop and after closeMCPManagers
//     (stopped guard), and — the real behaviour test — reflects an mcp.json
//     server gaining/losing "disabled": true between two calls, which is
//     exactly what the T4 write endpoint will rely on.

package agent

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

// --- MCPPathsFor ------------------------------------------------------------

// TestMCPPathsFor_MatchesPathsFor pins the resolver contract: MCPPathsFor
// returns EXACTLY what the agent's manager set resolves (pathsFor — the code
// path the manager itself was built with) for the default agent AND for an
// agent whose workspace moved; ok=false for an unknown id.
func TestMCPPathsFor_MatchesPathsFor(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	agent := mcpTestAgent(t, al)

	// Default agent: equals pathsFor, spelled out as the three roots.
	got, ok := al.MCPPathsFor("main")
	if !ok {
		t.Fatal(`MCPPathsFor("main") ok=false, want true`)
	}
	if want := tc.mcpManagers.pathsFor(agent); got != want {
		t.Errorf("MCPPathsFor = %+v, want pathsFor %+v", got, want)
	}
	if want := (mcp.Paths{
		LeleDir:        config.GetLeleDir(),
		AgentWorkspace: workspace,
		Cwd:            tc.mcpManagers.cwd,
	}); got != want {
		t.Errorf("MCPPathsFor = %+v, want %+v (config dir / real temp workspace / startup cwd)", got, want)
	}

	// Workspace moved: the answer follows agent.Workspace, still the same
	// resolver the next sync would use for this agent.
	moved := t.TempDir()
	agent.Workspace = moved
	got, ok = al.MCPPathsFor("main")
	if !ok {
		t.Fatal(`MCPPathsFor("main") ok=false after workspace move`)
	}
	if want := (mcp.Paths{
		LeleDir:        config.GetLeleDir(),
		AgentWorkspace: moved,
		Cwd:            tc.mcpManagers.cwd,
	}); got != want {
		t.Errorf("MCPPathsFor after move = %+v, want %+v", got, want)
	}
	if want := tc.mcpManagers.pathsFor(agent); got != want {
		t.Errorf("MCPPathsFor after move = %+v, want pathsFor %+v", got, want)
	}

	// Unknown id: nothing resolved, ok=false, zero value.
	if paths, ok := al.MCPPathsFor("ghost-agent"); ok || paths != (mcp.Paths{}) {
		t.Errorf("MCPPathsFor(unknown) = (%+v, %v), want (zero, false)", paths, ok)
	}
}

// TestMCPPathsFor_BareLoop is the belt for loops torn down (or built) before
// the coordinator/registry existed: never panics, ok=false.
func TestMCPPathsFor_BareLoop(t *testing.T) {
	al := &AgentLoop{}
	paths, ok := al.MCPPathsFor("main")
	if ok || paths != (mcp.Paths{}) {
		t.Errorf("MCPPathsFor on bare loop = (%+v, %v), want (zero, false)", paths, ok)
	}
}

// --- SyncMCPServers ---------------------------------------------------------

// TestSyncMCPServers_BareLoop: a bare &AgentLoop{} (nil coordinator) must
// make SyncMCPServers a no-op — no panic, mirroring CloseMCPManagers.
func TestSyncMCPServers_BareLoop(t *testing.T) {
	al := &AgentLoop{}
	al.SyncMCPServers() // must not panic
}

// TestSyncMCPServers_AfterCloseInstallsNothing: after closeMCPManagers() the
// set is stopped, so SyncMCPServers must install NOTHING — the manager set
// entry count stays 0 and load_mcp_tools is not registered (the stopped guard
// is reused inside syncMCPTools, never re-implemented by the seam).
func TestSyncMCPServers_AfterCloseInstallsNothing(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)

	// A server IS configured — the late sync must still install nothing.
	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"s": {"command": "true", "description": "Server s"}}}`)

	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("CloseMCPManagers(): %v", err)
	}
	al.SyncMCPServers()

	tc.mcpManagers.mu.Lock()
	count := len(tc.mcpManagers.entries)
	tc.mcpManagers.mu.Unlock()
	if count != 0 {
		t.Errorf("manager set entry count = %d after sync, want 0 (stopped)", count)
	}

	agent := mcpTestAgent(t, al)
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); ok {
		t.Error("load_mcp_tools must NOT be registered by a sync after closeMCPManagers")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("late sync fed a ## MCP Servers section: %q", sec)
	}
}

// TestSyncMCPServers_ReflectsMCPJSONToggle is THE behaviour test for the
// seam: a workspace whose mcp.json GAINS a server between two syncs → the
// first SyncMCPServers() registers load_mcp_tools and sets the "## MCP
// Servers" prompt section; then "disabled": true is written into that
// mcp.json by hand (raw os.WriteFile via writeAgentMCPJSON — no pkg/mcp
// helper) and a second SyncMCPServers() unregisters the tool and empties the
// section. This is the end-to-end proof that the T4 endpoint's write path
// can refresh the surface without re-reading config.json.
func TestSyncMCPServers_ReflectsMCPJSONToggle(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	agent := mcpTestAgent(t, al)

	// Sync #1 — baseline: no mcp.json anywhere → empty surface.
	al.SyncMCPServers()
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); ok {
		t.Fatal("load_mcp_tools registered before any server exists")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Fatalf("baseline prompt already has an MCP section: %q", sec)
	}

	// The workspace GAINS server "s" between two syncs (raw write).
	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"s": {"command": "true", "description": "Server s"}}}`)
	al.SyncMCPServers()
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); !ok {
		t.Fatalf("sync after mcp.json gained a server did not register load_mcp_tools, have %v",
			agent.Tools.List())
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); !strings.Contains(sec, "- **s**") {
		t.Fatalf("prompt section must list the new server, got %q", sec)
	}
	if _, ok := tc.mcpManagers.get("main"); !ok {
		t.Fatal("sync after mcp.json gained a server must install a manager")
	}

	// Toggle the same server off by hand: "disabled": true (raw write again).
	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"s": {"command": "true", "description": "Server s", "disabled": true}}}`)
	al.SyncMCPServers()
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); ok {
		t.Error("load_mcp_tools must be unregistered after the server was disabled")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("prompt section must be empty after the disable, got %q", sec)
	}
	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("manager must be dropped when the last server was disabled")
	}
}
