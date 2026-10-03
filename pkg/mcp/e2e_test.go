// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// T11: end-to-end tests through real in-process MCP servers (mcptest).
//
// Everything exercised here is production code — the three mcp.json layers
// on disk, Discover, the Manager, the RemoteTool adapter, the load_mcp_tools
// loader and the ToolRegistry. The only substituted piece is the Dialer
// seam: it hands the manager the already-initialized mcptest client wrapped
// in the production clientConn, so no process is spawned (Start/Initialize
// against the real transports are covered by the T4 dialer tests).

package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
	"github.com/xilistudios/lele/pkg/tools"
)

// --- harness ----------------------------------------------------------------

// dialStep is one scripted outcome of e2eDialer: a ready connection or an
// error.
type dialStep struct {
	conn ClientConn
	err  error
}

// e2eDialer substitutes the production Dialer. Each ServerConfig is routed by
// its Command field (the fixtures give every fake server a distinct command)
// onto the steps scripted for that command, consumed in order. Every config
// passed to Dial is recorded so tests can assert which merged layer entry
// reached the dialer. An exhausted queue fails with a descriptive error —
// that doubles as the "server stays dead" reconnect case.
type e2eDialer struct {
	mu    sync.Mutex
	steps map[string][]dialStep
	seen  []ServerConfig
}

func newE2EDialer() *e2eDialer {
	return &e2eDialer{steps: make(map[string][]dialStep)}
}

// script queues dial outcomes for configs whose Command is command.
func (d *e2eDialer) script(command string, steps ...dialStep) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.steps[command] = append(d.steps[command], steps...)
}

// Dial implements Dialer.
func (d *e2eDialer) Dial(_ context.Context, srv ServerConfig) (ClientConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = append(d.seen, srv)
	queue := d.steps[srv.Command]
	if len(queue) == 0 {
		return nil, fmt.Errorf("e2e dialer: no dial scripted for command %q", srv.Command)
	}
	step := queue[0]
	d.steps[srv.Command] = queue[1:]
	if step.err != nil {
		return nil, step.err
	}
	return step.conn, nil
}

// dials counts how often a config with the given Command was dialed.
func (d *e2eDialer) dials(command string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, cfg := range d.seen {
		if cfg.Command == command {
			n++
		}
	}
	return n
}

// configs returns every ServerConfig passed to Dial, in order.
func (d *e2eDialer) configs() []ServerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ServerConfig, len(d.seen))
	copy(out, d.seen)
	return out
}

// connFor wraps an already-initialized mcptest client in the production
// clientConn — the manager sees a plain ClientConn, the same wrapper the
// production dialer builds (the pre-init path itself is T4 territory).
func connFor(t *testing.T, srv *mcptest.Server) ClientConn {
	t.Helper()
	cli := srv.Client()
	if cli == nil {
		t.Fatalf("mcptest server %q has no client (closed before dial?)", t.Name())
	}
	return &clientConn{cli: cli}
}

// startMCPServer starts an in-process MCP server exposing two tools:
//   - echo    ("message")  → text "<tag>:<message>"
//   - summary ("label")    → text plus structured content {label, server: tag}
//
// It returns the server and an idempotent close func: mcptest.Server.Close
// is NOT safe to call twice (its pipe fields are nilled), so tests that kill
// the server mid-flight must use the returned func instead of Close.
func startMCPServer(t *testing.T, tag string) (*mcptest.Server, func()) {
	t.Helper()
	srv, err := mcptest.NewServer(t,
		server.ServerTool{
			Tool: mcp.NewTool("echo",
				mcp.WithDescription("Echo the message back"),
				mcp.WithString("message", mcp.Required())),
			Handler: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				msg, _ := req.GetArguments()["message"].(string)
				return &mcp.CallToolResult{
					Content: []mcp.Content{mcp.TextContent{Type: "text", Text: tag + ":" + msg}},
				}, nil
			},
		},
		server.ServerTool{
			Tool: mcp.NewTool("summary",
				mcp.WithDescription("Summarize a label"),
				mcp.WithString("label", mcp.Required())),
			Handler: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				label, _ := req.GetArguments()["label"].(string)
				return &mcp.CallToolResult{
					Content: []mcp.Content{
						mcp.TextContent{Type: "text", Text: "summary for " + label},
					},
					StructuredContent: map[string]interface{}{"label": label, "server": tag},
				}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	closeOnce := sync.OnceFunc(srv.Close)
	t.Cleanup(closeOnce)
	return srv, closeOnce
}

// e2ePaths builds the three layer roots (global/agent/project) under one
// temp dir. The roots start empty; tests write the mcp.json files they need.
func e2ePaths(t *testing.T) Paths {
	t.Helper()
	base := t.TempDir()
	return Paths{
		LeleDir:        filepath.Join(base, "lele-home"),
		AgentWorkspace: filepath.Join(base, "workspace"),
		Cwd:            filepath.Join(base, "project"),
	}
}

// --- three layers → dial → round trip ---------------------------------------

// TestE2EThreeLayersToRoundTrip walks the whole happy path: three mcp.json
// layers on disk merge (project wins for the shared name), the merged entry
// is what reaches the Dialer, LoadServer namespaces the specs, and tools
// built by ToolFactory round-trip a call — text and structured content —
// through the production clientConn into the in-process server.
func TestE2EThreeLayersToRoundTrip(t *testing.T) {
	paths := e2ePaths(t)

	writeFileAt(t, filepath.Join(paths.LeleDir, "mcp.json"), `{
		"mcpServers": {
			"shared": {"command": "global-shared", "description": "Global shared"},
			"onlyglobal": {"command": "onlyglobal-bin", "description": "Only global"}
		}}`)
	writeFileAt(t, filepath.Join(paths.AgentWorkspace, "mcp.json"), `{
		"mcpServers": {
			"shared": {"command": "agent-shared", "description": "Agent shared"},
			"onlyagent": {"command": "onlyagent-bin", "description": "Only agent"}
		}}`)
	writeFileAt(t, filepath.Join(paths.Cwd, ".lele", "mcp.json"), `{
		"mcpServers": {
			"shared": {"command": "project-shared", "description": "Project shared"}
		}}`)

	// Discovery: the merge itself, before any manager exists.
	res, err := Discover(paths)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if want := []string{"onlyagent", "onlyglobal", "shared"}; !slices.Equal(res.Names, want) {
		t.Errorf("Names = %v, want %v", res.Names, want)
	}
	if got := res.Layers["shared"]; got != LayerProject {
		t.Errorf("shared layer = %q, want %q", got, LayerProject)
	}
	if cfg := res.Servers["shared"]; cfg.Command != "project-shared" || cfg.Description != "Project shared" {
		t.Errorf("shared config = %+v, want the whole project entry (not a field merge)", cfg)
	}
	if got := res.Layers["onlyglobal"]; got != LayerGlobal {
		t.Errorf("onlyglobal layer = %q, want %q", got, LayerGlobal)
	}
	if got := res.Layers["onlyagent"]; got != LayerAgent {
		t.Errorf("onlyagent layer = %q, want %q", got, LayerAgent)
	}

	// Manager over the scripted dialer; the server's tag mirrors the winning
	// layer so the answer itself proves which entry was dialed.
	srv, _ := startMCPServer(t, "project")
	dialer := newE2EDialer()
	dialer.script("project-shared", dialStep{conn: connFor(t, srv)})
	mgr := NewManager(paths, dialer)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	specs, err := mgr.LoadServer(ctx, "shared")
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	seenSpecs := map[string]bool{}
	for _, spec := range specs {
		seenSpecs[spec.Name] = true
	}
	if !seenSpecs["mcp_shared_echo"] || !seenSpecs["mcp_shared_summary"] || len(specs) != 2 {
		t.Errorf("specs = %v, want exactly mcp_shared_echo and mcp_shared_summary", specNames(specs))
	}

	// Idempotent load: a second LoadServer must not dial again.
	if _, err := mgr.LoadServer(ctx, "shared"); err != nil {
		t.Fatalf("second LoadServer: %v", err)
	}
	if n := dialer.dials("project-shared"); n != 1 {
		t.Errorf("dials = %d, want 1 (cached specs)", n)
	}
	if cfgs := dialer.configs(); len(cfgs) != 1 || cfgs[0].Description != "Project shared" {
		t.Errorf("dialed configs = %+v, want the merged project entry", cfgs)
	}

	// ToolFactory → RemoteTool → Execute round trip.
	factory := mgr.ToolFactory()
	var echo, summary tools.Tool
	for _, spec := range specs {
		tool := factory(spec)
		switch spec.RemoteName {
		case "echo":
			echo = tool
		case "summary":
			summary = tool
		}
	}
	if echo == nil || summary == nil {
		t.Fatalf("factory produced echo=%v summary=%v", echo, summary)
	}

	got := echo.Execute(ctx, map[string]interface{}{"message": "hola"})
	if got.IsError {
		t.Fatalf("echo failed: %s", got.ForLLM)
	}
	if got.ForLLM != "project:hola" {
		t.Errorf("echo = %q, want %q", got.ForLLM, "project:hola")
	}

	sum := summary.Execute(ctx, map[string]interface{}{"label": "t11"})
	if sum.IsError {
		t.Fatalf("summary failed: %s", sum.ForLLM)
	}
	for _, want := range []string{
		"summary for t11",
		"```json\n{",
		`"label": "t11"`,
		`"server": "project"`,
	} {
		if !strings.Contains(sum.ForLLM, want) {
			t.Errorf("structured result lacks %q:\n%s", want, sum.ForLLM)
		}
	}
	if n := dialer.dials("project-shared"); n != 1 {
		t.Errorf("dials after four operations = %d, want 1", n)
	}
}

// specNames renders the registered names of specs for failure messages.
func specNames(specs []tools.LoadedMCPTool) []string {
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Name)
	}
	return out
}

// --- load_mcp_tools against a real registry ---------------------------------

// TestE2ELoadMCPToolsRegistersAndReloads runs the integration the LLM sees:
// a real ToolRegistry plus the production loader tool. The first execution
// registers every namespaced spec, the second is a no-op for the registry
// while still listing the specs, the registered remote tool round-trips, and
// an unknown server yields a clean error — all without a second dial.
func TestE2ELoadMCPToolsRegistersAndReloads(t *testing.T) {
	paths := e2ePaths(t)
	writeFileAt(t, filepath.Join(paths.AgentWorkspace, "mcp.json"),
		`{"mcpServers": {"files": {"command": "files-bin", "description": "Local file operations"}}}`)

	srv, _ := startMCPServer(t, "files")
	dialer := newE2EDialer()
	dialer.script("files-bin", dialStep{conn: connFor(t, srv)})
	mgr := NewManager(paths, dialer)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	registry := tools.NewToolRegistry()
	loader := tools.NewLoadMCPToolsTool(mgr, registry, mgr.ToolFactory())

	// First execution: both specs registered under namespaced names.
	res := loader.Execute(ctx, map[string]interface{}{"server": "files"})
	if res.IsError {
		t.Fatalf("first load failed: %s", res.ForLLM)
	}
	if registry.Count() != 2 {
		t.Errorf("Count() = %d after first load, want 2", registry.Count())
	}
	for _, name := range []string{"mcp_files_echo", "mcp_files_summary"} {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("%s not registered, have %v", name, registry.List())
		}
	}
	if want := `Loaded 2 tool(s) from MCP server "files": 2 newly registered, 0 already present.`; !strings.Contains(res.ForLLM, want) {
		t.Errorf("first load output lacks %q:\n%s", want, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "## mcp_files_echo") {
		t.Errorf("first load output lacks the spec listing:\n%s", res.ForLLM)
	}

	// Second execution: registry count stable, names still unique, listing
	// still complete (the LLM re-reads the specs after a reload).
	res = loader.Execute(ctx, map[string]interface{}{"server": "files"})
	if res.IsError {
		t.Fatalf("second load failed: %s", res.ForLLM)
	}
	if registry.Count() != 2 {
		t.Errorf("Count() = %d after second load, want 2 (no duplicate registration)", registry.Count())
	}
	if want := `0 newly registered, 2 already present`; !strings.Contains(res.ForLLM, want) {
		t.Errorf("second load output lacks %q:\n%s", want, res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "## mcp_files_summary") {
		t.Errorf("second load output lost the spec listing:\n%s", res.ForLLM)
	}
	defNames := map[string]bool{}
	for _, def := range registry.GetDefinitions() {
		fn, _ := def["function"].(map[string]interface{})
		name, _ := fn["name"].(string)
		if name == "" {
			t.Errorf("definition without name: %+v", def)
			continue
		}
		if defNames[name] {
			t.Errorf("duplicate definition name %q", name)
		}
		defNames[name] = true
	}
	if len(defNames) != registry.Count() {
		t.Errorf("GetDefinitions() has %d unique names, Count() = %d", len(defNames), registry.Count())
	}

	// The registered tool executes through the registry like any built-in.
	res = registry.Execute(ctx, "mcp_files_echo", map[string]interface{}{"message": "hola"})
	if res.IsError {
		t.Fatalf("registry execution failed: %s", res.ForLLM)
	}
	if res.ForLLM != "files:hola" {
		t.Errorf("echo via registry = %q, want %q", res.ForLLM, "files:hola")
	}

	// Unknown server: clean error naming the server; the authoritative
	// known-servers list comes from the loader layer EXACTLY ONCE (N4 — the
	// manager error itself no longer carries a list, so the wrapped error
	// cannot duplicate it).
	res = loader.Execute(ctx, map[string]interface{}{"server": "ghost"})
	if !res.IsError {
		t.Errorf("unknown server succeeded: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `failed to load MCP server "ghost"`) ||
		!strings.Contains(res.ForLLM, "known servers: files") {
		t.Errorf("unknown server error = %q, want it to name the server and the known list", res.ForLLM)
	}
	if n := strings.Count(res.ForLLM, "known servers:"); n != 1 {
		t.Errorf("known-servers list appears %d times in the final message, want exactly 1:\n%s", n, res.ForLLM)
	}

	// Everything above ran over exactly one connection.
	if n := dialer.dials("files-bin"); n != 1 {
		t.Errorf("dials = %d, want 1", n)
	}
}

// --- two servers, same remote tool name -------------------------------------

// TestE2ETwoServersSameRemoteToolName proves the namespacing keeps two
// servers that both expose a tool called "echo" apart in one registry: both
// get their own registered name and each answer comes from its own server.
func TestE2ETwoServersSameRemoteToolName(t *testing.T) {
	paths := e2ePaths(t)
	writeFileAt(t, filepath.Join(paths.AgentWorkspace, "mcp.json"), `{
		"mcpServers": {
			"alpha": {"command": "alpha-bin", "description": "Alpha server"},
			"beta": {"command": "beta-bin", "description": "Beta server"}
		}}`)

	srvAlpha, _ := startMCPServer(t, "alpha")
	srvBeta, _ := startMCPServer(t, "beta")
	dialer := newE2EDialer()
	dialer.script("alpha-bin", dialStep{conn: connFor(t, srvAlpha)})
	dialer.script("beta-bin", dialStep{conn: connFor(t, srvBeta)})
	mgr := NewManager(paths, dialer)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	registry := tools.NewToolRegistry()
	loader := tools.NewLoadMCPToolsTool(mgr, registry, mgr.ToolFactory())
	for _, name := range []string{"alpha", "beta"} {
		if res := loader.Execute(ctx, map[string]interface{}{"server": name}); res.IsError {
			t.Fatalf("load %s: %s", name, res.ForLLM)
		}
	}

	if registry.Count() != 4 {
		t.Errorf("Count() = %d, want 4 (2 tools × 2 servers)", registry.Count())
	}
	want := []string{"mcp_alpha_echo", "mcp_alpha_summary", "mcp_beta_echo", "mcp_beta_summary"}
	got := registry.List()
	// List() iterates a map: no order contract, so compare as a set (want is
	// written in lexicographic order).
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("List() = %v, want %v as a set", got, want)
	}

	a := registry.Execute(ctx, "mcp_alpha_echo", map[string]interface{}{"message": "hi"})
	b := registry.Execute(ctx, "mcp_beta_echo", map[string]interface{}{"message": "hi"})
	if a.IsError || b.IsError {
		t.Fatalf("execution failed: alpha=%q beta=%q", a.ForLLM, b.ForLLM)
	}
	if a.ForLLM != "alpha:hi" || b.ForLLM != "beta:hi" {
		t.Errorf("answers = alpha:%q beta:%q, want each from its own server", a.ForLLM, b.ForLLM)
	}

	servers := mgr.Servers()
	if names := infoNames(servers); !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Errorf("Servers() = %v, want [alpha beta]", names)
	}
}

// infoNames extracts the names of a server info list in order.
func infoNames(infos []tools.MCPServerInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		out = append(out, info.Name)
	}
	return out
}

// --- server dies mid-session --------------------------------------------------

// TestE2EServerDiesReconnectOnce pins the reconnect-once semantics against a
// server that really dies (the mcptest transport is closed, exactly like a
// crashed stdio child): the first post-mortem call drops the dead
// connection, redials once and either succeeds on the new connection or
// fails with a clean wrapped "reconnect" error — never a panic or a hang.
func TestE2EServerDiesReconnectOnce(t *testing.T) {
	t.Run("redial fails cleanly", func(t *testing.T) {
		paths := e2ePaths(t)
		writeFileAt(t, filepath.Join(paths.AgentWorkspace, "mcp.json"),
			`{"mcpServers": {"dying": {"command": "dying-bin", "description": "Dying server"}}}`)

		srv, kill := startMCPServer(t, "old")
		dialer := newE2EDialer()
		dialer.script("dying-bin",
			dialStep{conn: connFor(t, srv)},
			dialStep{err: fmt.Errorf("redial refused: server is gone")},
		)
		mgr := NewManager(paths, dialer)
		t.Cleanup(func() { _ = mgr.Close() })

		ctx := context.Background()
		specs, err := mgr.LoadServer(ctx, "dying")
		if err != nil {
			t.Fatalf("LoadServer: %v", err)
		}
		factory := mgr.ToolFactory()
		var echo tools.Tool
		for _, spec := range specs {
			if spec.RemoteName == "echo" {
				echo = factory(spec)
			}
		}
		if echo == nil {
			t.Fatal("no echo spec")
		}

		alive := echo.Execute(ctx, map[string]interface{}{"message": "hola"})
		if alive.IsError || alive.ForLLM != "old:hola" {
			t.Fatalf("call before death = %q (err=%v), want old:hola", alive.ForLLM, alive.IsError)
		}

		kill() // the server dies

		dead := echo.Execute(ctx, map[string]interface{}{"message": "hola"})
		if !dead.IsError {
			t.Fatalf("call after death succeeded: %s", dead.ForLLM)
		}
		if !strings.Contains(dead.ForLLM, "reconnect") {
			t.Errorf("error lacks the reconnect context: %s", dead.ForLLM)
		}
		if !strings.Contains(dead.ForLLM, "redial refused") {
			t.Errorf("error lacks the dialer failure: %s", dead.ForLLM)
		}
		if n := dialer.dials("dying-bin"); n != 2 {
			t.Errorf("dials = %d, want 2 (initial + one redial)", n)
		}

		// A later direct call fails the same way, without wedging or panicking.
		if _, err := mgr.CallRemote(ctx, "dying", "echo", map[string]interface{}{"message": "x"}); err == nil {
			t.Error("CallRemote after death succeeded")
		} else if !strings.Contains(err.Error(), "no dial scripted") {
			t.Errorf("CallRemote error = %v, want the dialer failure", err)
		}
	})

	t.Run("redial succeeds on a fresh server", func(t *testing.T) {
		paths := e2ePaths(t)
		writeFileAt(t, filepath.Join(paths.AgentWorkspace, "mcp.json"),
			`{"mcpServers": {"flip": {"command": "flip-bin", "description": "Flipping server"}}}`)

		oldSrv, killOld := startMCPServer(t, "old")
		newSrv, _ := startMCPServer(t, "new")
		dialer := newE2EDialer()
		dialer.script("flip-bin",
			dialStep{conn: connFor(t, oldSrv)},
			dialStep{conn: connFor(t, newSrv)},
		)
		mgr := NewManager(paths, dialer)
		t.Cleanup(func() { _ = mgr.Close() })

		ctx := context.Background()
		specs, err := mgr.LoadServer(ctx, "flip")
		if err != nil {
			t.Fatalf("LoadServer: %v", err)
		}
		factory := mgr.ToolFactory()
		var echo tools.Tool
		for _, spec := range specs {
			if spec.RemoteName == "echo" {
				echo = factory(spec)
			}
		}
		if echo == nil {
			t.Fatal("no echo spec")
		}

		first := echo.Execute(ctx, map[string]interface{}{"message": "hola"})
		if first.IsError || first.ForLLM != "old:hola" {
			t.Fatalf("first call = %q (err=%v), want old:hola", first.ForLLM, first.IsError)
		}

		killOld()

		second := echo.Execute(ctx, map[string]interface{}{"message": "hola"})
		if second.IsError {
			t.Fatalf("call after reconnect failed: %s", second.ForLLM)
		}
		if second.ForLLM != "new:hola" {
			t.Errorf("call after reconnect = %q, want new:hola (retried on the redialed connection)", second.ForLLM)
		}
		if n := dialer.dials("flip-bin"); n != 2 {
			t.Errorf("dials = %d, want 2", n)
		}
	})
}

// --- disabled / absent configuration -----------------------------------------

// TestE2EDisabledServerExcludedAndNeverDials: the project layer can disable
// a server the global layer enables; the name leaves Servers(), LoadServer
// reports the layer that disabled it, and no dial is ever attempted.
func TestE2EDisabledServerExcludedAndNeverDials(t *testing.T) {
	paths := e2ePaths(t)
	writeFileAt(t, filepath.Join(paths.LeleDir, "mcp.json"),
		`{"mcpServers": {"off": {"command": "off-bin", "description": "Enabled globally"}}}`)
	writeFileAt(t, filepath.Join(paths.Cwd, ".lele", "mcp.json"),
		`{"mcpServers": {"off": {"command": "off-bin", "disabled": true, "description": "Disabled in project"}}}`)

	res, err := Discover(paths)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Names) != 0 {
		t.Errorf("Names = %v, want none (disabled wins)", res.Names)
	}
	if layer := res.Disabled["off"]; layer != LayerProject {
		t.Errorf("Disabled[off] = %q, want %q", layer, LayerProject)
	}

	dialer := newE2EDialer() // nothing scripted: any dial would fail loudly
	mgr := NewManager(paths, dialer)
	t.Cleanup(func() { _ = mgr.Close() })

	if servers := mgr.Servers(); len(servers) != 0 {
		t.Errorf("Servers() = %+v, want none", servers)
	}
	if _, err := mgr.LoadServer(context.Background(), "off"); err == nil {
		t.Error("LoadServer of a disabled server succeeded")
	} else if !strings.Contains(err.Error(), "disabled") || !strings.Contains(err.Error(), "project") {
		t.Errorf("error = %v, want it to name the disabled flag and the project layer", err)
	}
	if n := dialer.dials("off-bin"); n != 0 {
		t.Errorf("dials = %d, want 0 (disabled servers never dial)", n)
	}
}

// TestE2ENoMCPFilesAnywhere: with the three roots empty the manager reports
// no servers, never dials and the loader tool says so — no error, no panic.
// (The agent-level counterpart — load_mcp_tools staying unregistered — is
// TestMCPSync_NoServersLeavesLoaderUnregistered in pkg/agent.)
func TestE2ENoMCPFilesAnywhere(t *testing.T) {
	paths := e2ePaths(t)

	res, err := Discover(paths)
	if err != nil {
		t.Errorf("Discover with no files: %v, want no error (absent layers are not warnings)", err)
	}
	if len(res.Names) != 0 {
		t.Errorf("Names = %v, want none", res.Names)
	}

	dialer := newE2EDialer()
	mgr := NewManager(paths, dialer)
	t.Cleanup(func() { _ = mgr.Close() })

	if servers := mgr.Servers(); len(servers) != 0 {
		t.Errorf("Servers() = %+v, want none", servers)
	}
	if _, err := mgr.LoadServer(context.Background(), "anything"); err == nil {
		t.Error("LoadServer without configuration succeeded")
	} else if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error = %v, want a not-configured message", err)
	} else if strings.Contains(err.Error(), "known servers:") {
		t.Errorf("manager error = %v, must not carry the known-servers list (the loader appends it once)", err)
	}

	registry := tools.NewToolRegistry()
	loader := tools.NewLoadMCPToolsTool(mgr, registry, mgr.ToolFactory())
	out := loader.Execute(context.Background(), map[string]interface{}{"server": "x"})
	if !out.IsError || !strings.Contains(out.ForLLM, "known servers: (none)") {
		t.Errorf("loader output = %q (IsError=%v), want a clean unknown-server error", out.ForLLM, out.IsError)
	}
	if n := len(dialer.configs()); n != 0 {
		t.Errorf("dials = %d, want 0", n)
	}
}
