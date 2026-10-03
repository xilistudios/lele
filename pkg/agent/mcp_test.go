// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// T8/T9/T10 tests for the MCP wiring:
//   - sync: an agent whose workspace has an mcp.json gets load_mcp_tools and a
//     live manager; an agent without one does not.
//   - reload: deleting the mcp.json unregisters the loader and closes the
//     manager exactly once (spy).
//   - prompt: the "## MCP Servers" section renders name+description only and
//     the initialContext cache is dropped ONLY when the server set changed.
//   - shutdown: CloseMCPManagers is idempotent and Shutdown closes once.

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

// --- harness ----------------------------------------------------------------

// mcpSpy wraps a real mcp.Manager (so Servers() reflects the mcp.json on disk)
// and counts Close calls. The inner manager may be nil for shutdown tests that
// only need the close accounting.
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

// mcpSpyRecorder replaces a manager set's factory so every manager created by
// a sync pass is wrapped in a close spy. Must be attached before the sync
// under test (the startup sync inside NewAgentLoop runs before this).
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

// newMCPTestLoop builds a production-wired AgentLoop (registry + tool
// coordinator + startup MCP sync) isolated in a temp LELE_CONFIG_DIR, and
// returns it with the agent workspace path (the agent layer root of mcp.json).
func newMCPTestLoop(t *testing.T) (*AgentLoop, string) {
	t.Helper()

	// LELE_CONFIG_DIR must NOT be the workspace: the global layer lives at
	// <LeleDir>/mcp.json, so sharing one directory would make every
	// agent-layer mcp.json written by these tests also the global layer —
	// and a test that moves to an "empty" workspace would still see the
	// servers through global.
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

// writeAgentMCPJSON writes (or removes, with empty body) the agent-layer
// mcp.json inside the workspace.
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

// mcpTestAgent fetches the implicit "main" agent from the loop's registry.
func mcpTestAgent(t *testing.T, al *AgentLoop) *AgentInstance {
	t.Helper()
	agent, ok := al.registry.GetAgent("main")
	if !ok {
		t.Fatalf("implicit agent %q missing from registry (have %v)", "main", al.registry.ListAgentIDs())
	}
	return agent
}

// mcpTestCoordinator downcasts the loop's coordinator to the concrete type so
// tests can reach the mcpManagers set (the interface deliberately doesn't
// expose it).
func mcpTestCoordinator(t *testing.T, al *AgentLoop) *toolCoordinatorImpl {
	t.Helper()
	tc, ok := al.toolCoordinator.(*toolCoordinatorImpl)
	if !ok || tc == nil {
		t.Fatalf("toolCoordinator is %T, want *toolCoordinatorImpl", al.toolCoordinator)
	}
	return tc
}

// mcpSectionOf extracts the "## MCP Servers" section of a prompt (up to the
// next "---" separator). Returns "" when the section is absent. The heading
// is matched as its own line (leading \n) because the load_mcp_tools tool
// description quotes "'## MCP Servers'" inline and must not match.
func mcpSectionOf(prompt string) string {
	start := strings.Index(prompt, "\n## MCP Servers\n")
	if start < 0 {
		if strings.HasPrefix(prompt, "## MCP Servers\n") {
			start = 0
		} else {
			return ""
		}
	} else {
		start++ // keep the heading itself, skip the newline
	}
	rest := prompt[start:]
	if end := strings.Index(rest, "\n\n---\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// --- T8: sync ---------------------------------------------------------------

// TestMCPSync_RegistersLoaderWhenServersConfigured pins the happy path: an
// mcp.json in the agent workspace yields a live manager, a registered
// load_mcp_tools and a "## MCP Servers" prompt section.
func TestMCPSync_RegistersLoaderWhenServersConfigured(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	rec := &mcpSpyRecorder{}
	rec.attach(tc.mcpManagers)

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "true", "description": "Demo server"}}}`)

	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); !ok {
		t.Errorf("load_mcp_tools not registered, have %v", agent.Tools.List())
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

// TestMCPSync_NoServersLeavesLoaderUnregistered pins the negative path: with
// no mcp.json anywhere the loader stays absent, no manager is kept and the
// prompt has no MCP section.
func TestMCPSync_NoServersLeavesLoaderUnregistered(t *testing.T) {
	al, _ := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	rec := &mcpSpyRecorder{}
	rec.attach(tc.mcpManagers)

	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); ok {
		t.Errorf("load_mcp_tools must not be registered, have %v", agent.Tools.List())
	}
	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("a manager must not be stored when no server is configured")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("empty server list rendered a section: %q", sec)
	}
	// The bookkeeping manager created by the sync may have been created and
	// closed immediately; it must never stay open.
	for i, s := range rec.all() {
		if s.closeCount() == 0 {
			t.Errorf("spy %d was created but never closed (orphan)", i)
		}
	}
}

// TestMCPSync_RemovingServersClosesManagerAndUnregistersLoader is the reload
// acceptance criterion: deleting the mcp.json on disk and re-syncing must
// unregister load_mcp_tools and Close the manager exactly once, and the prompt
// section must disappear with it.
func TestMCPSync_RemovingServersClosesManagerAndUnregistersLoader(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)
	rec := &mcpSpyRecorder{}
	rec.attach(tc.mcpManagers)

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "true", "description": "Demo server"}}}`)
	tc.syncMCPTools()

	agent := mcpTestAgent(t, al)
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); !ok {
		t.Fatalf("load_mcp_tools not registered after first sync, have %v", agent.Tools.List())
	}
	// Build once so the cache holds the section; the reload must drop it.
	if mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()) == "" {
		t.Fatal("prompt lacks ## MCP Servers before reload")
	}
	spies := rec.all()
	if len(spies) != 1 {
		t.Fatalf("created %d managers, want 1", len(spies))
	}

	writeAgentMCPJSON(t, workspace, "")
	tc.syncMCPTools()

	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); ok {
		t.Errorf("load_mcp_tools must be unregistered after the servers vanished, have %v", agent.Tools.List())
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

	// A third sync must not close anything again (idempotent bookkeeping).
	tc.syncMCPTools()
	if got := spies[0].closeCount(); got != 1 {
		t.Errorf("manager closed %d times after extra sync, want 1", got)
	}
}

// --- T9: prompt section + cache invalidation --------------------------------

// TestMCPServersSection covers both render rules: no section for an empty
// list, and exactly one sorted name+description line per server (with the
// load_mcp_tools hint) when non-empty.
func TestMCPServersSection(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())

	t.Run("empty list renders nothing", func(t *testing.T) {
		cb.SetMCPServers(nil)
		if sec := mcpSectionOf(cb.BuildSystemPrompt()); sec != "" {
			t.Errorf("empty list rendered a section: %q", sec)
		}
	})

	t.Run("non-empty list renders one line per server", func(t *testing.T) {
		cb.SetMCPServers([]tools.MCPServerInfo{
			{Name: "git", Description: "Git tools", Layer: "agent"},
			{Name: "db", Description: "Database access", Layer: "global"},
		})
		prompt := cb.BuildSystemPrompt()
		if !strings.Contains(prompt, "## MCP Servers") {
			t.Fatal("non-empty list did not render the section")
		}
		if !strings.Contains(prompt, "Use `load_mcp_tools` with a server name to load its tools.") {
			t.Error("section lacks the load_mcp_tools hint")
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
		// Names/descriptions only — never schemas (scoped to the section: the
		// rest of the prompt legitimately talks about tool "parameters").
		if sec := mcpSectionOf(prompt); sec == "" {
			t.Fatal("MCP section not found for schema check")
		} else if strings.Contains(sec, "inputSchema") || strings.Contains(sec, "parameters") {
			t.Error("section leaked schema text")
		}
	})
}

// TestPromptCacheInvalidation_MCPServers pins decision (b) from the plan: a
// changed server set drops the cached initialContext (next build reflects it),
// while a no-op set leaves the cache untouched (no needless prompt rebuild).
func TestPromptCacheInvalidation_MCPServers(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())

	cachedPrompt := cb.GetInitialContext()
	if cachedPrompt == "" || cb.initialContext == "" {
		t.Fatal("expected a populated initialContext cache after first build")
	}

	// Changed list => cache dropped, next build reflects the new server.
	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "demo", Description: "Demo", Layer: "agent"}})
	if cb.initialContext != "" {
		t.Fatal("SetMCPServers with a changed list did not drop the cache")
	}
	rebuilt := cb.GetInitialContext()
	if !strings.Contains(rebuilt, "## MCP Servers") || !strings.Contains(rebuilt, "- **demo**") {
		t.Error("rebuilt prompt does not reflect the new server list")
	}

	// Identical list => cache preserved (identity of the built prompt holds).
	before := cb.initialContext
	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "demo", Description: "Demo", Layer: "agent"}})
	if cb.initialContext == "" {
		t.Error("no-op SetMCPServers dropped the cache (needless rebuild)")
	}
	if cb.initialContext != before {
		t.Error("no-op SetMCPServers changed the cached prompt")
	}

	// Description-only change counts as a change too.
	cb.SetMCPServers([]tools.MCPServerInfo{{Name: "demo", Description: "Demo v2", Layer: "agent"}})
	if cb.initialContext != "" {
		t.Error("description change did not drop the cache")
	}
	if got := cb.GetInitialContext(); !strings.Contains(got, "Demo v2") {
		t.Error("rebuilt prompt shows the stale description")
	}
}

// TestMCPServersEqual covers the set-comparison helper: order-insensitive but
// sensitive to name, description and layer.
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

// --- T10: shutdown ----------------------------------------------------------

// TestMCPShutdown pins the teardown contract: Shutdown closes every live
// manager exactly once, a second Shutdown (or a direct CloseMCPManagers call,
// as the gateway's mcp-stop hook does) is a no-op, and errors never surface
// from the drain.
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

	// Second Shutdown + the gateway hook path: both must find nothing left.
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

// TestMCPShutdown_NilCoordinator is the belt for loops torn down before the
// coordinator existed: CloseMCPManagers must be nil-safe, not panic.
func TestMCPShutdown_NilCoordinator(t *testing.T) {
	al := &AgentLoop{}
	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("CloseMCPManagers() on a bare loop = %v, want nil", err)
	}
}

// --- N3: sync after teardown installs nothing -------------------------------

// TestMCPSync_SyncAfterCloseMCPManagersInstallsNothing is the N3 regression:
// the config watcher's Stop does NOT join its goroutine (pkg/config/
// watcher.go), so a reload already in flight can legitimately reach
// syncMCPTools AFTER services-stop ran CloseMCPManagers. The set's stopped
// flag must turn such a late sync into a no-op: no manager may be installed
// (nobody would ever close it) and load_mcp_tools must not be (re)registered.
// Pre-fix (stopped check removed) the sync created a manager and registered
// the loader — RED. The put-refusal belt is asserted in the same test.
func TestMCPSync_SyncAfterCloseMCPManagersInstallsNothing(t *testing.T) {
	al, workspace := newMCPTestLoop(t)
	tc := mcpTestCoordinator(t, al)

	writeAgentMCPJSON(t, workspace,
		`{"mcpServers": {"demo": {"command": "true", "description": "Demo server"}}}`)

	// Teardown: gateway mcp-stop / AgentLoop.Shutdown → closeAll.
	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("CloseMCPManagers(): %v", err)
	}

	// The late reload reaches the sync AFTER teardown.
	tc.syncMCPTools()

	if _, ok := tc.mcpManagers.get("main"); ok {
		t.Error("sync after closeAll must not install a manager (stopped set)")
	}
	agent := mcpTestAgent(t, al)
	if _, ok := agent.Tools.Get(tools.LoadMCPToolsName); ok {
		t.Error("load_mcp_tools must not be (re)registered after teardown")
	}
	if sec := mcpSectionOf(agent.ContextBuilder.BuildSystemPrompt()); sec != "" {
		t.Errorf("late sync must not feed a ## MCP Servers section: %q", sec)
	}

	// Belt: put() also refuses after teardown and closes the incoming manager.
	spy := &mcpSpy{}
	tc.mcpManagers.put("late", mcpManagerEntry{mgr: spy, registered: newNameSet()})
	if _, ok := tc.mcpManagers.get("late"); ok {
		t.Error("put after closeAll must refuse the entry")
	}
	if spy.closeCount() != 1 {
		t.Errorf("refused manager closed %d times, want exactly 1", spy.closeCount())
	}

	// Teardown stays idempotent.
	if err := al.CloseMCPManagers(); err != nil {
		t.Fatalf("second CloseMCPManagers(): %v", err)
	}
}
