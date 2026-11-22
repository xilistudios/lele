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

func mcpDemoTools() []string { return []string{"echo", "summary"} }

func mcpDemoNamespaced() []string { return []string{"mcp_demo_echo", "mcp_demo_summary"} }

type e2eFakeDialer struct{}

func (e2eFakeDialer) Dial(_ context.Context, _ mcp.ServerConfig) (mcp.ClientConn, error) {
	return e2eFakeConn{}, nil
}

func TestMCPSync_GetMCPToolIdempotentEndToEnd(t *testing.T) {
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

	ctx := context.Background()
	baseCount := agent.Tools.Count()
	before := layer.Count()

	res := mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)
	if newly, already := mcpCallableCounts(res); newly != 2 || already != 0 {
		t.Errorf("first load outcome = %d new / %d already, want 2/0:\n%s", newly, already, res.ForLLM)
	}
	if want := before + 2; layer.Count() != want {
		t.Errorf("session Count() = %d after first load, want %d", layer.Count(), want)
	}
	for _, name := range mcpDemoNamespaced() {
		if _, ok := layer.Get(name); !ok {
			t.Errorf("%s not in the session view, have %v", name, layer.List())
		}
	}
	if got := agent.Tools.Count(); got != baseCount {
		t.Errorf("agent registry Count() = %d after a session load, want %d (unchanged)", got, baseCount)
	}
	for _, name := range mcpDemoNamespaced() {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s leaked into the agent-wide registry", name)
		}
	}

	getTool := mcpSessionTool(t, tc, agent, "chat-a", tools.GetMCPToolName)
	res = getTool.Execute(ctx, map[string]interface{}{"server": "demo", "tools": mcpDemoTools()})
	if res.IsError {
		t.Fatalf("second load failed: %s", res.ForLLM)
	}
	if layer.Count() != before+2 {
		t.Errorf("Count() = %d after second load, want %d (no duplicates)", layer.Count(), before+2)
	}
	if newly, already := mcpCallableCounts(res); newly != 0 || already != 2 {
		t.Errorf("second load outcome = %d new / %d already, want 0/2 (idempotent):\n%s",
			newly, already, res.ForLLM)
	}

	out := layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError {
		t.Fatalf("remote tool execution failed: %s", out.ForLLM)
	}
	if out.ForLLM != "fake:echo" {
		t.Errorf("remote tool = %q, want %q", out.ForLLM, "fake:echo")
	}

	out = getTool.Execute(ctx, map[string]interface{}{"server": "ghost", "tool": "echo"})
	if !out.IsError || !strings.Contains(out.ForLLM, "known servers: demo") {
		t.Errorf("unknown server output = %q (IsError=%v), want the known-servers list", out.ForLLM, out.IsError)
	}
}

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

func TestMCPSync_RemovedServerUnregistersLoadedTools(t *testing.T) {
	_, tc, agent, workspace := e2eMCPWiring(t)

	layer := tc.sessionTools(agent, "chat-a")
	mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)
	loaded := mcpDemoNamespaced()
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

func TestMCPSync_AllowlistTightenedUnregistersLoadedTools(t *testing.T) {
	_, tc, agent, _ := e2eMCPWiring(t)

	mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)
	if _, ok := tc.sessionTools(agent, "chat-a").Get("mcp_demo_echo"); !ok {
		t.Fatal("mcp_demo_echo not in the session view after load")
	}

	cfg := tc.al.cfg()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:    "main",
		Tools: []string{"web_fetch"},
	})
	tc.syncMCPTools()

	sessionView := tc.sessionTools(agent, "chat-a")
	if sessionView != agent.Tools {
		t.Error("the session layer must be dropped when the allowlist keeps neither discovery tool")
	}
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
		if _, ok := sessionView.Get(name); ok {
			t.Errorf("%s must not be offered once the allowlist excludes the discovery tools", name)
		}
	}
	for _, name := range mcpDemoNamespaced() {
		if _, ok := sessionView.Get(name); ok {
			t.Errorf("%s must leave the session view with its loader (allowlist tightened)", name)
		}
	}
	for _, name := range toolDefNames(sessionView.GetDefinitions()) {
		if strings.HasPrefix(name, "mcp_") {
			t.Errorf("the session view still offers %s after the allowlist tightened", name)
		}
	}
	if !strings.Contains(agent.ContextBuilder.BuildSystemPrompt(), "- **demo**") {
		t.Error("the ## MCP Servers prompt section must survive the allowlist gate")
	}
}

func TestMCPSync_WorkspaceRoundTripRestoresWorkingManager(t *testing.T) {
	_, tc, agent, workspaceA := e2eMCPWiring(t)

	mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)

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

	agent.Workspace = workspaceA
	tc.syncMCPTools()
	mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)
	if _, ok := tc.mcpManagers.get("main"); !ok {
		t.Error("workspace A must have a live manager again")
	}
}

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

func TestMCPSync_ConfigChangeThenRemovalUnregistersAllLoadedTools(t *testing.T) {
	_, tc, agent, workspace := e2eMCPWiring(t)
	ctx := context.Background()

	layer := tc.sessionTools(agent, "chat-a")
	mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)
	loaded := mcpDemoNamespaced()
	for _, name := range loaded {
		if _, ok := layer.Get(name); !ok {
			t.Fatalf("%s not in the session view after load, have %v", name, layer.List())
		}
	}

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "demo-bin-2", "description": "Demo v2"}}}`)
	out := layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola"})
	if out.IsError {
		t.Fatalf("remote tool after config edit #1 must keep working: %s", out.ForLLM)
	}
	if out.ForLLM != "fake:echo" {
		t.Errorf("remote tool = %q, want %q", out.ForLLM, "fake:echo")
	}

	res := mcpLoadTools(t, tc, agent, "chat-a", "demo", mcpDemoTools()...)
	if newly, already := mcpCallableCounts(res); newly != 0 || already != 2 {
		t.Errorf("re-load outcome = %d new / %d already, want 0/2:\n%s", newly, already, res.ForLLM)
	}
	out = layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "hola again"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("stale tool object must stay functional with the new config, got %+v", out)
	}

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "demo-bin-3", "description": "Demo v3"}}}`)
	out = layer.Execute(ctx, "mcp_demo_echo", map[string]interface{}{"message": "v3"})
	if out.IsError || out.ForLLM != "fake:echo" {
		t.Errorf("remote tool after config edit #2 must keep working, got %+v", out)
	}

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

func TestMCPSync_RetirementLeavesForeignMCPNamedToolAlone(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	tc.mcpManagers.newManager = func(paths mcp.Paths) mcpManager {
		return mcp.NewManager(paths, e2eFakeDialer{})
	}
	agent := mcpTestAgent(t, al)

	foreign := &foreignTool{name: "mcp_beta_echo"}
	agent.Tools.Register(foreign)

	writeAgentMCPJSON(t, workspace, `{
	  "mcpServers": {
	    "alpha": {"command": "alpha-bin", "description": "Alpha server"},
	    "beta":  {"command": "beta-bin",  "description": "Beta server"}
	  }}`)
	tc.syncMCPTools()

	layer := tc.sessionTools(agent, "chat-a")
	res := mcpLoadTools(t, tc, agent, "chat-a", "alpha", mcpDemoTools()...)
	if newly, already := mcpCallableCounts(res); newly != 2 || already != 0 {
		t.Errorf("alpha load outcome = %d new / %d already, want 2/0:\n%s", newly, already, res.ForLLM)
	}
	res = mcpLoadTools(t, tc, agent, "chat-a", "beta", mcpDemoTools()...)
	if newly, already := mcpCallableCounts(res); newly != 1 || already != 1 {
		t.Errorf("beta load outcome = %d new / %d already, want 1/1:\n%s", newly, already, res.ForLLM)
	}
	if _, ok := layer.Get("mcp_alpha_echo"); !ok {
		t.Fatalf("mcp_alpha_echo not in the session view, have %v", layer.List())
	}
	if got, _ := layer.Get("mcp_beta_echo"); got != tools.Tool(foreign) {
		t.Fatal("mcp_beta_echo must still be the foreign instance after the load")
	}

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
