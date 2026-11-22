package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

func TestMCPPathsFor_MatchesPathsFor(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	agent := mcpTestAgent(t, al)

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

	if paths, ok := al.MCPPathsFor("ghost-agent"); ok || paths != (mcp.Paths{}) {
		t.Errorf("MCPPathsFor(unknown) = (%+v, %v), want (zero, false)", paths, ok)
	}
}

func TestMCPPathsFor_BareLoop(t *testing.T) {
	al := &AgentLoop{}
	paths, ok := al.MCPPathsFor("main")
	if ok || paths != (mcp.Paths{}) {
		t.Errorf("MCPPathsFor on bare loop = (%+v, %v), want (zero, false)", paths, ok)
	}
}

func TestSyncMCPServers_BareLoop(t *testing.T) {
	al := &AgentLoop{}
	al.SyncMCPServers()
}

func TestSyncMCPServers_AfterCloseInstallsNothing(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)

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
	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("a sync after closeMCPManagers must NOT build a session layer")
	}
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must NOT be registered by a sync after closeMCPManagers", name)
		}
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("late sync fed a ## MCP Servers section: %q", sec)
	}
}

func TestSyncMCPServers_ReflectsMCPJSONToggle(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	agent := mcpTestAgent(t, al)

	sessionOffers := func(name string) bool {
		_, ok := tc.sessionTools(agent, "chat-a").Get(name)
		return ok
	}
	sessionOffersDiscovery := func() bool {
		for _, name := range mcpDiscoveryToolNames() {
			if !sessionOffers(name) {
				return false
			}
		}
		return true
	}
	sessionOffersNoDiscovery := func() error {
		for _, name := range mcpDiscoveryToolNames() {
			if sessionOffers(name) {
				return fmt.Errorf("%s is still offered by the session", name)
			}
		}
		return nil
	}

	al.SyncMCPServers()
	if err := sessionOffersNoDiscovery(); err != nil {
		t.Fatalf("discovery tools offered before any server exists: %v", err)
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Fatalf("baseline prompt already has an MCP section: %q", sec)
	}

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"s": {"command": "true", "description": "Server s"}}}`)
	al.SyncMCPServers()
	if !sessionOffersDiscovery() {
		t.Fatalf("sync after mcp.json gained a server did not offer the discovery tools, have %v",
			tc.sessionTools(agent, "chat-a").List())
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); !strings.Contains(sec, "- **s**") {
		t.Fatalf("prompt section must list the new server, got %q", sec)
	}
	if _, ok := tc.mcpManagers.get("main"); !ok {
		t.Fatal("sync after mcp.json gained a server must install a manager")
	}

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"s": {"command": "true", "description": "Server s", "disabled": true}}}`)
	al.SyncMCPServers()
	if err := sessionOffersNoDiscovery(); err != nil {
		t.Errorf("the discovery tools must stop being offered after the server was disabled: %v", err)
	}
	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("disabling the last server must drop the session layer")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("prompt section must be empty after the disable, got %q", sec)
	}
	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("manager must be dropped when the last server was disabled")
	}
}
