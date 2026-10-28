// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// T11: agent-level load_mcp_tools integration. Extends the T8 sync tests
// (which only check registration) with actual executions against a real
// manager: the loader registers the specs once, a second execution keeps the
// registry count stable, and the registered remote tool round-trips through
// the agent's own ToolRegistry. The manager gets an injected fake dialer
// (the exported mcp.Dialer seam) so no process is spawned.

package agent

import (
	"context"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

// e2eFakeConn is the ClientConn handed to the manager: it lists two remote
// tools and answers calls with a recognizable prefix.
type e2eFakeConn struct{}

func (e2eFakeConn) ListTools(context.Context) ([]mcpgo.Tool, error) {
	return []mcpgo.Tool{
		{
			Name:        "echo",
			Description: "Echo the message back",
			InputSchema: mcpgo.ToolInputSchema{
				Type:       "object",
				Properties: map[string]interface{}{"message": map[string]interface{}{"type": "string"}},
			},
		},
		{
			Name:        "summary",
			Description: "Summarize a label",
			InputSchema: mcpgo.ToolInputSchema{Type: "object"},
		},
	}, nil
}

func (e2eFakeConn) CallTool(_ context.Context, name string, _ map[string]interface{}) (*mcpgo.CallToolResult, error) {
	return &mcpgo.CallToolResult{
		Content: []mcpgo.Content{mcpgo.TextContent{Type: "text", Text: "fake:" + name}},
	}, nil
}

func (e2eFakeConn) Close() error { return nil }

// e2eFakeDialer implements mcp.Dialer with a single reusable connection.
type e2eFakeDialer struct{}

func (e2eFakeDialer) Dial(_ context.Context, _ mcp.ServerConfig) (mcp.ClientConn, error) {
	return e2eFakeConn{}, nil
}

// TestMCPSync_LoadMCPToolsIdempotentEndToEnd wires the agent loop's own
// sync to a real mcp.Manager (fake dialer), then executes load_mcp_tools
// twice through the agent's registry: first call registers the namespaced
// specs, second call keeps Count() stable, and the registered remote tool
// answers like a built-in.
func TestMCPSync_LoadMCPToolsIdempotentEndToEnd(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	tc.mcpManagers.newManager = func(paths mcp.Paths) mcpManager {
		return mcp.NewManager(paths, e2eFakeDialer{})
	}

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "demo-bin", "description": "Demo server"}}}`)
	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	layer := tc.sessionTools(agent, "chat-a")
	loader := mcpSessionLoader(t, tc, agent, "chat-a")

	ctx := context.Background()
	baseCount := agent.Tools.Count()
	before := layer.Count()

	// First execution: the remote specs land in THIS SESSION's layer.
	res := loader.Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("first load failed: %s", res.ForLLM)
	}
	if want := before + 2; layer.Count() != want {
		t.Errorf("session Count() = %d after first load, want %d", layer.Count(), want)
	}
	for _, name := range []string{"mcp_demo_echo", "mcp_demo_summary"} {
		if _, ok := layer.Get(name); !ok {
			t.Errorf("%s not in the session view, have %v", name, layer.List())
		}
	}
	// Isolation: the agent-wide registry is untouched, so no other chat of this
	// agent can see (or call) the remote tools.
	if got := agent.Tools.Count(); got != baseCount {
		t.Errorf("agent registry Count() = %d after a session load, want %d (unchanged)", got, baseCount)
	}
	for _, name := range []string{"mcp_demo_echo", "mcp_demo_summary"} {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s leaked into the agent-wide registry", name)
		}
	}

	// Second execution: no duplicate registration, listing still complete.
	res = loader.Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("second load failed: %s", res.ForLLM)
	}
	if layer.Count() != before+2 {
		t.Errorf("Count() = %d after second load, want %d (no duplicates)", layer.Count(), before+2)
	}
	if !strings.Contains(res.ForLLM, "0 newly registered, 2 already present") {
		t.Errorf("second load output lacks the idempotency counts:\n%s", res.ForLLM)
	}

	// The registered remote tool executes through the session's view.
	out := layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError {
		t.Fatalf("remote tool execution failed: %s", out.ForLLM)
	}
	if out.ForLLM != "fake:echo" {
		t.Errorf("remote tool = %q, want %q", out.ForLLM, "fake:echo")
	}

	// Unknown server stays a clean error at this level too.
	out = loader.Execute(ctx, map[string]interface{}{"server": "ghost"})
	if !out.IsError || !strings.Contains(out.ForLLM, "known servers: demo") {
		t.Errorf("unknown server output = %q (IsError=%v), want the known-servers list", out.ForLLM, out.IsError)
	}
}

// toolDefNames extracts the tool names from a GetDefinitions snapshot.
func toolDefNames(defs []map[string]interface{}) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		fn, _ := def["function"].(map[string]interface{})
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		names = append(names, name)
	}
	return names
}

// e2eMCPWiring builds the loop + coordinator with a real mcp.Manager over the
// e2e fake dialer and configures the demo server in the agent workspace.
func e2eMCPWiring(t *testing.T) (*AgentLoop, *toolCoordinatorImpl, *AgentInstance, string) {
	t.Helper()
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	tc.mcpManagers.newManager = func(paths mcp.Paths) mcpManager {
		return mcp.NewManager(paths, e2eFakeDialer{})
	}
	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "demo-bin", "description": "Demo server"}}}`)
	tc.syncMCPTools()
	return al, tc, mcpTestAgent(t, al), workspace
}

// TestMCPSync_RemovedServerUnregistersLoadedTools is the M3 regression: once
// load_mcp_tools has registered mcp_* names, deleting the server and syncing
// must remove them from the registry AND from GetDefinitions() — otherwise
// stale remote tools would be offered to the model with no manager behind
// them.
func TestMCPSync_RemovedServerUnregistersLoadedTools(t *testing.T) {
	_, tc, agent, workspace := e2eMCPWiring(t)

	layer := tc.sessionTools(agent, "chat-a")
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(context.Background(), map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	loaded := []string{"mcp_demo_echo", "mcp_demo_summary"}
	for _, name := range loaded {
		if _, ok := layer.Get(name); !ok {
			t.Fatalf("%s not in the session view after load, have %v", name, layer.List())
		}
	}
	found := false
	for _, name := range toolDefNames(layer.GetDefinitions()) {
		if name == "mcp_demo_echo" {
			found = true
		}
	}
	if !found {
		t.Fatal("mcp_demo_echo missing from the session's GetDefinitions() after load")
	}

	// Reload with the server deleted: the manager is retired, and its session
	// layers (loader + loaded remote tools) go with it.
	writeAgentMCPJSON(t, workspace, "")
	tc.syncMCPTools()

	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("the session layer must be dropped when its manager is retired")
	}
	for _, name := range loaded {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must never reach the agent-wide registry", name)
		}
	}
	for _, name := range toolDefNames(agent.Tools.GetDefinitions()) {
		if strings.HasPrefix(name, "mcp_") {
			t.Errorf("the base registry offers %s with no server behind it", name)
		}
	}
}

// TestMCPSync_AllowlistTightenedUnregistersLoadedTools is the second M3
// regression: excluding load_mcp_tools from the agent's allowlist on a
// reload must unregister the loader AND the mcp_* tools it already
// registered (while the prompt keeps listing the servers — they are
// configuration, not tools).
func TestMCPSync_AllowlistTightenedUnregistersLoadedTools(t *testing.T) {
	_, tc, agent, _ := e2eMCPWiring(t)

	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(context.Background(), map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load failed: %s", res.ForLLM)
	}
	if _, ok := tc.sessionTools(agent, "chat-a").Get("mcp_demo_echo"); !ok {
		t.Fatal("mcp_demo_echo not in the session view after load")
	}

	// Tighten the allowlist for "main": load_mcp_tools no longer survives.
	cfg := tc.al.cfg()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:    "main",
		Tools: []string{"web_fetch"},
	})
	tc.syncMCPTools()

	// The layer is dropped: the session falls back to the base registry, where
	// neither the loader nor the remote tools ever existed.
	sessionView := tc.sessionTools(agent, "chat-a")
	if sessionView != agent.Tools {
		t.Error("the session layer must be dropped when the allowlist excludes the loader")
	}
	if _, ok := sessionView.Get(tools.LoadMCPToolsName); ok {
		t.Error("load_mcp_tools must not be offered once the allowlist excludes it")
	}
	for _, name := range []string{"mcp_demo_echo", "mcp_demo_summary"} {
		if _, ok := sessionView.Get(name); ok {
			t.Errorf("%s must leave the session view with its loader (allowlist tightened)", name)
		}
	}
	for _, name := range toolDefNames(sessionView.GetDefinitions()) {
		if strings.HasPrefix(name, "mcp_") {
			t.Errorf("the session view still offers %s after the allowlist tightened", name)
		}
	}
	// Servers are configuration: the prompt section must survive the gate.
	if !strings.Contains(agent.ContextBuilder.BuildSystemPrompt(), "- **demo**") {
		t.Error("the ## MCP Servers prompt section must survive the allowlist gate")
	}
}

// TestMCPSync_WorkspaceRoundTripRestoresWorkingManager is the M4 regression:
// A (servers) → B (no servers, manager retired) → back to A. The retired
// manager must be DROPPED from the set, so returning to A builds a fresh,
// working manager instead of reusing the closed one ("manager is closed").
func TestMCPSync_WorkspaceRoundTripRestoresWorkingManager(t *testing.T) {
	_, tc, agent, workspaceA := e2eMCPWiring(t)

	// The manager works while we are in workspace A.
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(context.Background(), map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("load in A failed: %s", res.ForLLM)
	}

	// Move to a workspace with no MCP servers anywhere: manager retired.
	workspaceB := t.TempDir()
	agent.Workspace = workspaceB
	tc.syncMCPTools()
	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("retired manager must be dropped from the set " +
			"(a closed one would be reused when the workspace moves back)")
	}
	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("the session layer must be dropped while the workspace has no servers")
	}

	// Back to A: a FRESH manager must serve the loader end-to-end again.
	agent.Workspace = workspaceA
	tc.syncMCPTools()
	loader = mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(context.Background(), map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("loader after the workspace round-trip failed: %s", res.ForLLM)
	}
	if _, ok := tc.mcpManagers.get("main"); !ok {
		t.Error("workspace A must have a live manager again")
	}
}

// --- N2: registry-owned unregistration bookkeeping -------------------------

// foreignTool is a minimal Tool registered under an mcp_* name WITHOUT the
// loader (the stand-in for a tool that reached the registry through another
// channel — see TestMCPSync_RetirementUnregistersOnlyNamesThisEntryRegistered).
type foreignTool struct {
	name string
}

func (f *foreignTool) Name() string        { return f.name }
func (f *foreignTool) Description() string { return "foreign tool under an mcp_ name" }
func (f *foreignTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (f *foreignTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.NewToolResult("foreign")
}

// TestMCPSync_ConfigChangeThenRemovalUnregistersAllLoadedTools is the N2(a)
// regression: retirement must unregister the names THIS entry registered into
// the agent's registry, not whatever the manager's loaded cache happens to
// hold at that moment. The cache is cleared by a mid-flight config change
// (applyConfigChange), so the old LoadedNames()-based unregistration found
// nothing and left every mcp_* tool behind with no server behind it.
//
// The scenario also pins the reviewer's NOTE: after the fingerprint change
// the registry still holds the old mcp_* tool objects — a re-load does NOT
// re-register them (skip-if-present) — and they stay functional, because
// RemoteTool holds (server, remoteName, mgr) and CallRemote re-resolves the
// config on every call.
func TestMCPSync_ConfigChangeThenRemovalUnregistersAllLoadedTools(t *testing.T) {
	_, tc, agent, workspace := e2eMCPWiring(t)
	ctx := context.Background()

	layer := tc.sessionTools(agent, "chat-a")
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	if res := loader.Execute(ctx, map[string]interface{}{"server": "demo"}); res.IsError {
		t.Fatalf("first load failed: %s", res.ForLLM)
	}
	loaded := []string{"mcp_demo_echo", "mcp_demo_summary"}
	for _, name := range loaded {
		if _, ok := layer.Get(name); !ok {
			t.Fatalf("%s not in the session view after load, have %v", name, layer.List())
		}
	}

	// Config edit #1: fingerprint changes on the next resolve. The remote
	// tool call below triggers it — the stale tool object re-resolves the
	// config and works with the NEW one (and that resolve drops the loaded
	// cache the old unregistration code relied on).
	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "demo-bin-2", "description": "Demo v2"}}}`)
	out := layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError {
		t.Fatalf("remote tool after config edit #1 must keep working: %s", out.ForLLM)
	}
	if out.ForLLM != "fake:echo" {
		t.Errorf("remote tool = %q, want %q", out.ForLLM, "fake:echo")
	}

	// Re-loading after the change does NOT re-register (skip-if-present): the
	// stale tool object stays bound to the same manager and remote name.
	res := loader.Execute(ctx, map[string]interface{}{"server": "demo"})
	if res.IsError {
		t.Fatalf("re-load after config edit failed: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "0 newly registered, 2 already present") {
		t.Errorf("re-load should report everything as already present:\n%s", res.ForLLM)
	}
	out = layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola again"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("stale tool object must stay functional with the new config, got %+v", out)
	}

	// Config edit #2: clears the loaded cache again (the re-load above
	// re-cached it). Retirement must not depend on that cache.
	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "demo-bin-3", "description": "Demo v3"}}}`)
	out = layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "v3"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("remote tool after config edit #2 must keep working, got %+v", out)
	}

	// Remove the server: sync retires the manager AND every mcp_* name this
	// entry registered (pre-fix: LoadedNames() found an empty cache and
	// unregistered nothing — under-unregistration, RED).
	writeAgentMCPJSON(t, workspace, "")
	tc.syncMCPTools()

	sessionView := tc.sessionTools(agent, "chat-a")
	if sessionView != agent.Tools {
		t.Error("the session layer must be dropped when the server vanished")
	}
	for _, name := range loaded {
		if _, ok := sessionView.Get(name); ok {
			t.Errorf("%s still offered after retirement (under-unregistration)", name)
		}
	}
	for _, def := range toolDefNames(sessionView.GetDefinitions()) {
		if strings.HasPrefix(def, "mcp_") {
			t.Errorf("the session view still offers %s with no server behind it", def)
		}
	}
}

// TestMCPSync_RetirementLeavesForeignMCPNamedToolAlone is the N2(b) sub-case
// (cross-server name collision) under the session-scoped model: two servers
// expose the SAME remote tool name ("echo") under distinct namespaces, and
// mcp_beta_echo already exists in the AGENT registry as a foreign tool (it was
// not created by the loader). The loader must report it as "already present" —
// the session layer resolves through to the base registry, so the foreign
// instance wins — and retirement must leave it untouched: only the layer's own
// names can ever disappear, and they disappear with the layer.
func TestMCPSync_RetirementLeavesForeignMCPNamedToolAlone(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	tc.mcpManagers.newManager = func(paths mcp.Paths) mcpManager {
		return mcp.NewManager(paths, e2eFakeDialer{})
	}
	agent := mcpTestAgent(t, al)
	ctx := context.Background()

	// The foreign same-named tool: present BEFORE any MCP load happened.
	foreign := &foreignTool{name: "mcp_beta_echo"}
	agent.Tools.Register(foreign)

	writeAgentMCPJSON(t, workspace, `{
	  "mcpServers": {
	    "alpha": {"command": "alpha-bin", "description": "Alpha server"},
	    "beta":  {"command": "beta-bin",  "description": "Beta server"}
	  }}`)
	tc.syncMCPTools()

	layer := tc.sessionTools(agent, "chat-a")
	loader := mcpSessionLoader(t, tc, agent, "chat-a")
	// Load alpha: both of its tools are new and get registered in the layer.
	res := loader.Execute(ctx, map[string]interface{}{"server": "alpha"})
	if res.IsError {
		t.Fatalf("load alpha: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "2 newly registered, 0 already present") {
		t.Errorf("alpha load output = %s, want 2 newly registered", res.ForLLM)
	}
	// Load beta: its echo resolves to the foreign base tool (already present,
	// NOT registered by us), only summary is new.
	res = loader.Execute(ctx, map[string]interface{}{"server": "beta"})
	if res.IsError {
		t.Fatalf("load beta: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "1 newly registered, 1 already present") {
		t.Errorf("beta load output = %s, want 1 newly registered + 1 already present", res.ForLLM)
	}
	if _, ok := layer.Get("mcp_alpha_echo"); !ok {
		t.Fatalf("mcp_alpha_echo not in the session view, have %v", layer.List())
	}
	if got, _ := layer.Get("mcp_beta_echo"); got != tools.Tool(foreign) {
		t.Fatal("mcp_beta_echo must still be the foreign instance after the load")
	}

	// Retire (every server vanished): the layer goes as a whole, and the
	// foreign tool — which lives in the base registry — survives untouched.
	writeAgentMCPJSON(t, workspace, "")
	tc.syncMCPTools()

	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("the session layer must be dropped on retirement")
	}
	for _, name := range []string{"mcp_alpha_echo", "mcp_alpha_summary", "mcp_beta_summary"} {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s was registered into the agent-wide registry (leak)", name)
		}
	}
	got, ok := agent.Tools.Get("mcp_beta_echo")
	if !ok {
		t.Error("the foreign mcp_beta_echo must survive retirement")
	} else if got != tools.Tool(foreign) {
		t.Error("the foreign mcp_beta_echo was replaced instead of preserved")
	}
}
