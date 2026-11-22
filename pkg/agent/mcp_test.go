package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tools"
)

type mcpSpy struct {
	inner  mcpManager
	mu     sync.Mutex
	closes int
}

func (s *mcpSpy) Servers() []tools.MCPServerInfo {
	if s.inner == nil {
		return nil
	}
	return s.inner.Servers()
}

func (s *mcpSpy) LoadServer(ctx context.Context, server string) ([]tools.LoadedMCPTool, error) {
	return s.inner.LoadServer(ctx, server)
}

func (s *mcpSpy) ToolUsage(server string) map[string]uint64 {
	if s.inner == nil {
		return nil
	}
	return s.inner.ToolUsage(server)
}

func (s *mcpSpy) ToolFactory() func(tools.LoadedMCPTool) tools.Tool {
	return s.inner.ToolFactory()
}

func (s *mcpSpy) Close() error {
	s.mu.Lock()
	s.closes++
	s.mu.Unlock()
	return nil
}

func (s *mcpSpy) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closes
}

type mcpSpyRecorder struct {
	mu    sync.Mutex
	spies []*mcpSpy
}

func (r *mcpSpyRecorder) attach(set *mcpManagerSet) {
	set.newManager = func(paths mcp.Paths) mcpManager {
		s := &mcpSpy{inner: mcp.NewManager(paths, nil)}
		r.mu.Lock()
		r.spies = append(r.spies, s)
		r.mu.Unlock()
		return s
	}
}

func (r *mcpSpyRecorder) all() []*mcpSpy {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*mcpSpy, len(r.spies))
	copy(out, r.spies)
	return out
}

func newMCPTestLoop(t *testing.T) (*AgentLoop, string) {
	t.Helper()

	cfgDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", cfgDir)

	tmpDir := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	return NewAgentLoop(cfg, bus.NewMessageBus()), tmpDir
}

func writeAgentMCPJSON(t *testing.T, workspace, body string) {
	t.Helper()
	path := filepath.Join(workspace, "mcp.json")
	if body == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove %s: %v", path, err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mcpTestAgent(t *testing.T, al *AgentLoop) *AgentInstance {
	t.Helper()
	agent, ok := al.registry.GetAgent("main")
	if !ok {
		t.Fatalf("implicit agent %q missing from registry (have %v)", "main", al.registry.ListAgentIDs())
	}
	return agent
}

func mcpTestCoordinator(t *testing.T, al *AgentLoop) *toolCoordinatorImpl {
	t.Helper()
	tc, ok := al.toolCoordinator.(*toolCoordinatorImpl)
	if !ok || tc == nil {
		t.Fatalf("toolCoordinator is %T, want *toolCoordinatorImpl", al.toolCoordinator)
	}
	return tc
}

func mcpSessionTool(t *testing.T, tc *toolCoordinatorImpl, agent *AgentInstance, sessionKey, name string) tools.Tool {
	t.Helper()
	view := tc.sessionTools(agent, sessionKey)
	tool, ok := view.Get(name)
	if !ok {
		t.Fatalf("session %q is not offered %s (session tools: %v)", sessionKey, name, view.List())
	}
	return tool
}

func mcpLoadTools(t *testing.T, tc *toolCoordinatorImpl, agent *AgentInstance, sessionKey, server string, names ...string) *tools.ToolResult {
	t.Helper()
	getTool := mcpSessionTool(t, tc, agent, sessionKey, tools.GetMCPToolName)
	res := getTool.Execute(context.Background(), map[string]interface{}{
		"server": server,
		"tools":  names,
	})
	if res.IsError {
		t.Fatalf("get_mcp_tool(%s, %v) in session %q failed: %s", server, names, sessionKey, res.ForLLM)
	}
	return res
}

func mcpCallableCounts(res *tools.ToolResult) (newly, already int) {
	return strings.Count(res.ForLLM, "is now callable"),
		strings.Count(res.ForLLM, "is already registered and callable")
}

func mcpSessionOffers(tc *toolCoordinatorImpl, agent *AgentInstance, sessionKey, name string) bool {
	_, ok := tc.sessionTools(agent, sessionKey).Get(name)
	return ok
}

func mcpDiscoveryToolNames() []string { return []string{tools.ListMCPToolsName, tools.GetMCPToolName} }

func mcpSectionOf(prompt string) string {
	start := strings.Index(prompt, "\n## MCP Servers\n")
	if start < 0 {
		if strings.HasPrefix(prompt, "## MCP Servers\n") {
			start = 0
		} else {
			return ""
		}
	} else {
		start++
	}
	rest := prompt[start:]
	if end := strings.Index(rest, "\n\n---\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestMCPSync_RegistersDiscoveryToolsWhenServersConfigured(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	rec := &mcpSpyRecorder{}
	rec.attach(tc.mcpManagers)

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "true", "description": "Demo server"}}}`)

	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	baseCount := agent.Tools.Count()
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must not be registered in the agent-wide registry, have %v", name, agent.Tools.List())
		}
	}
	layer := tc.sessionTools(agent, "chat-a")
	if !layer.IsScoped() {
		t.Fatal("the session view must be a scoped layer while MCP servers are configured")
	}
	for _, name := range mcpDiscoveryToolNames() {
		if _, ok := layer.Get(name); !ok {
			t.Errorf("%s not offered to the session, have %v", name, layer.List())
		}
	}
	if _, ok := layer.Get(tools.LoadMCPToolsName); ok {
		t.Errorf("the bulk loader must not be registered anywhere (layer: %v)", layer.List())
	}
	if want := baseCount + len(mcpDiscoveryToolNames()); layer.Count() != want {
		t.Errorf("session layer Count() = %d, want %d (base + the discovery pair)", layer.Count(), want)
	}
	if _, ok := layer.Get("read_file"); !ok {
		t.Errorf("the session view must fall back to the agent registry, have %v", layer.List())
	}

	entry, ok := tc.mcpManagers.get("main")
	if !ok {
		t.Fatal("no MCP manager stored for agent main")
	}
	if servers := entry.mgr.Servers(); len(servers) != 1 || servers[0].Name != "demo" {
		t.Errorf("manager.Servers() = %+v, want one server named demo", servers)
	}
	if spies := rec.all(); len(spies) != 1 {
		t.Errorf("created %d managers, want 1", len(spies))
	}

	prompt := agent.ContextBuilder.BuildSystemPrompt()
	sec := mcpSectionOf(prompt)
	if sec == "" {
		t.Error("system prompt lacks the ## MCP Servers section")
	}
	if !strings.Contains(prompt, "- **demo** — Demo server") {
		t.Error("system prompt lacks the demo server line")
	}
	if sec != "" {
		if strings.Contains(sec, "inputSchema") || strings.Contains(sec, "parameters") {
			t.Error("prompt section leaked tool schema text")
		}
	}
}

func TestMCPSync_NoServersLeavesDiscoveryToolsUnregistered(t *testing.T) {
	al, _ := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	rec := &mcpSpyRecorder{}
	rec.attach(tc.mcpManagers)

	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must not be registered, have %v", name, agent.Tools.List())
		}
	}
	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("with no MCP servers configured the session view must be the base registry")
	}
	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("a manager must not be stored when no server is configured")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("empty server list rendered a section: %q", sec)
	}
	for i, s := range rec.all() {
		if s.closeCount() == 0 {
			t.Errorf("spy %d was created but never closed (orphan)", i)
		}
	}
}

func TestMCPSync_RemovingServersClosesManagerAndUnregistersDiscoveryTools(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	rec := &mcpSpyRecorder{}
	rec.attach(tc.mcpManagers)

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "true", "description": "Demo server"}}}`)
	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	layer := tc.sessionTools(agent, "chat-a")
	for _, name := range mcpDiscoveryToolNames() {
		if _, ok := layer.Get(name); !ok {
			t.Fatalf("%s not offered after first sync, have %v", name, layer.List())
		}
	}
	if mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()) == "" {
		t.Fatal("prompt lacks ## MCP Servers before reload")
	}
	spies := rec.all()
	if len(spies) != 1 {
		t.Fatalf("created %d managers, want 1", len(spies))
	}

	writeAgentMCPJSON(t, workspace, "")
	tc.syncMCPTools()

	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("the session layer must be dropped when its manager is retired")
	}
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must not be registered in the base registry, have %v", name, agent.Tools.List())
		}
	}
	if got := spies[0].closeCount(); got != 1 {
		t.Errorf("manager closed %d times, want exactly 1", got)
	}
	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("manager must be dropped from the set when its servers are gone")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("stale ## MCP Servers section survived the reload: %q", sec)
	}

	tc.syncMCPTools()
	if got := spies[0].closeCount(); got != 1 {
		t.Errorf("manager closed %d times after extra sync, want 1", got)
	}
}

func TestMCPServersSection(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())

	t.Run("empty list renders nothing", func(t *testing.T) {
		cb.SetMCPServers(nil)
		if sec := mcpSectionOf(cb.BuildSystemPrompt()); sec != "" {
			t.Errorf("empty list rendered a section: %q", sec)
		}
	})

	t.Run("non-empty list renders one line per server", func(t *testing.T) {
		cb.SetMCPDiscovery(true, true)
		cb.SetMCPServers([]tools.MCPServerInfo{
			{Name: "git", Description: "Git tools", Layer: "agent"},
			{Name: "db", Description: "Database access", Layer: "global"},
		})
		prompt := cb.BuildSystemPrompt()
		if !strings.Contains(prompt, "## MCP Servers") {
			t.Fatal("non-empty list did not render the section")
		}
		if !strings.Contains(prompt, "Use `list_mcp_tools` with a server name to browse its tools") {
			t.Error("section lacks the list_mcp_tools hint")
		}
		if !strings.Contains(prompt, "then `get_mcp_tool` with") ||
			!strings.Contains(prompt, `"tool": "<tool>"`) ||
			!strings.Contains(prompt, `"tools": ["<t1>", "<t2>"]`) {
			t.Error("section lacks the get_mcp_tool hint (single tool + batch form)")
		}
		if got := strings.Count(prompt, "## MCP Servers"); got != 1 {
			t.Errorf("heading appears %d times, want 1", got)
		}
		git := strings.Index(prompt, "- **git** — Git tools")
		db := strings.Index(prompt, "- **db** — Database access")
		if git < 0 || db < 0 {
			t.Fatalf("server lines missing (git=%d db=%d)", git, db)
		}
		if db > git {
			t.Error("servers are not rendered sorted by name (db must precede git)")
		}
		if sec := mcpSectionOf(prompt); sec == "" {
			t.Fatal("MCP section not found for schema check")
		} else if strings.Contains(sec, "inputSchema") || strings.Contains(sec, "parameters") {
			t.Error("section leaked schema text")
		}
	})
}

func TestMCPServersSection_DiscoveryHintsMatchPermissions(t *testing.T) {
	servers := []tools.MCPServerInfo{{Name: "git", Description: "Git tools", Layer: "agent"}}

	cases := []struct {
		name string
		list bool
		get  bool
	}{
		{name: "both allowed", list: true, get: true},
		{name: "list only", list: true, get: false},
		{name: "get only", list: false, get: true},
		{name: "neither allowed", list: false, get: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cb := NewContextBuilder(t.TempDir())
			cb.SetMCPDiscovery(tc.list, tc.get)
			cb.SetMCPServers(servers)

			sec := mcpSectionOf(cb.BuildSystemPrompt())
			if !strings.Contains(sec, "- **git** — Git tools") {
				t.Fatalf("the server list must stay in every case:\n%s", sec)
			}
			if got := strings.Contains(sec, "`list_mcp_tools`"); got != tc.list {
				t.Errorf("section mentions list_mcp_tools = %v, want %v:\n%s", got, tc.list, sec)
			}
			if got := strings.Contains(sec, "`get_mcp_tool`"); got != tc.get {
				t.Errorf("section mentions get_mcp_tool = %v, want %v:\n%s", got, tc.get, sec)
			}
		})
	}
}

func TestPromptCacheInvalidation_MCPDiscovery(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "git", Description: "Git tools"}})
	cb.SetMCPDiscovery(true, true)

	both := cb.BuildSystemPrompt()
	cb.SetMCPDiscovery(true, true)
	if again := cb.BuildSystemPrompt(); again != both {
		t.Error("repeating the same permissions must not change the prompt")
	}

	cb.SetMCPDiscovery(true, false)
	tightened := cb.BuildSystemPrompt()
	if strings.Contains(tightened, "`get_mcp_tool`") {
		t.Error("the cached prompt was not rebuilt after the tightening")
	}
	if !strings.Contains(tightened, "`list_mcp_tools`") {
		t.Error("list_mcp_tools must still be documented")
	}
}

func TestPromptCacheInvalidation_MCPServers(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())

	cachedPrompt := cb.GetInitialContext()
	if cachedPrompt == "" || cb.initialContext == "" {
		t.Fatal("expected a populated initialContext cache after first build")
	}

	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "demo", Description: "Demo", Layer: "agent"}})
	if cb.initialContext != "" {
		t.Fatal("SetMCPServers with a changed list did not drop the cache")
	}
	rebuilt := cb.GetInitialContext()
	if !strings.Contains(rebuilt, "## MCP Servers") || !strings.Contains(rebuilt, "- **demo**") {
		t.Error("rebuilt prompt does not reflect the new server list")
	}

	before := cb.initialContext
	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "demo", Description: "Demo", Layer: "agent"}})
	if cb.initialContext == "" {
		t.Error("no-op SetMCPServers dropped the cache (needless rebuild)")
	}
	if cb.initialContext != before {
		t.Error("no-op SetMCPServers changed the cached prompt")
	}

	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "demo", Description: "Demo v2", Layer: "agent"}})
	if cb.initialContext != "" {
		t.Error("description change did not drop the cache")
	}
	if got := cb.GetInitialContext(); !strings.Contains(got, "Demo v2") {
		t.Error("rebuilt prompt shows the stale description")
	}
}

func TestMCPServersEqual(t *testing.T) {
	a := []tools.MCPServerInfo{
		{Name: "git", Description: "Git", Layer: "agent"},
		{Name: "db", Description: "DB", Layer: "global"},
	}
	b := []tools.MCPServerInfo{
		{Name: "db", Description: "DB", Layer: "global"},
		{Name: "git", Description: "Git", Layer: "agent"},
	}
	if !mcpServersEqual(a, b) {
		t.Error("same set in different order must be equal")
	}
	if mcpServersEqual(a, nil) {
		t.Error("non-empty vs nil must differ")
	}
	c := []tools.MCPServerInfo{{Name: "git", Description: "Git v2", Layer: "agent"}}
	if mcpServersEqual(a, c) {
		t.Error("description change must differ")
	}
	d := []tools.MCPServerInfo{{Name: "git", Description: "Git", Layer: "project"}}
	if mcpServersEqual(a, d) {
		t.Error("layer change must differ")
	}
}

func TestMCPShutdown(t *testing.T) {
	al, _ := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	spy := &mcpSpy{}
	tc.mcpManagers.put("main", mcpManagerEntry{mgr: spy})

	al.running.Store(true)
	if err := al.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}
	if got := spy.closeCount(); got != 1 {
		t.Errorf("manager closed %d times after Shutdown, want 1", got)
	}
	if al.running.Load() {
		t.Error("running must be false after Shutdown")
	}

	if err := al.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() = %v, want nil", err)
	}
	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("CloseMCPManagers() after Shutdown = %v, want nil", err)
	}
	if got := spy.closeCount(); got != 1 {
		t.Errorf("manager closed %d times after repeat teardown, want 1", got)
	}
}

func TestMCPShutdown_NilCoordinator(t *testing.T) {
	al := &AgentLoop{}
	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("CloseMCPManagers() on a bare loop = %v, want nil", err)
	}
}

func TestMCPSync_SyncAfterCloseMCPManagersInstallsNothing(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "true", "description": "Demo server"}}}`)

	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("CloseMCPManagers(): %v", err)
	}

	tc.syncMCPTools()

	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("sync after closeAll must not install a manager (stopped set)")
	}
	agent := mcpTestAgent(t, al)
	if got := tc.sessionTools(agent, "chat-a"); got != agent.Tools {
		t.Error("a late sync must not build a session layer after teardown")
	}
	for _, name := range append([]string{tools.LoadMCPToolsName}, mcpDiscoveryToolNames()...) {
		if _, ok := agent.Tools.Get(name); ok {
			t.Errorf("%s must not be (re)registered after teardown", name)
		}
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("late sync must not feed a ## MCP Servers section: %q", sec)
	}

	spy := &mcpSpy{}
	tc.mcpManagers.put("late", mcpManagerEntry{mgr: spy})
	if _, ok := tc.mcpManagers.get("late"); ok {
		t.Error("put after closeAll must refuse the entry")
	}
	if spy.closeCount() != 1 {
		t.Errorf("refused manager closed %d times, want exactly 1", spy.closeCount())
	}

	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("second CloseMCPManagers(): %v", err)
	}
}
