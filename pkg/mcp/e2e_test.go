package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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

type dialStep struct {
	conn ClientConn
	err  error
}

type e2eDialer struct {
	mu    sync.Mutex
	steps map[string][]dialStep
	seen  []ServerConfig
}

func newE2EDialer() *e2eDialer {
	return &e2eDialer{steps: make(map[string][]dialStep)}
}

func (d *e2eDialer) script(command string, steps ...dialStep) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.steps[command] = append(d.steps[command], steps...)
}

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

func (d *e2eDialer) configs() []ServerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]ServerConfig, len(d.seen))
	copy(out, d.seen)
	return out
}

func connFor(t *testing.T, srv *mcptest.Server) ClientConn {
	t.Helper()
	cli := srv.Client()
	if cli == nil {
		t.Fatalf("mcptest server %q has no client (closed before dial?)", t.Name())
	}
	return &clientConn{cli: cli}
}

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

func e2ePaths(t *testing.T) Paths {
	t.Helper()
	base := t.TempDir()
	return Paths{
		LeleDir:        filepath.Join(base, "lele-home"),
		AgentWorkspace: filepath.Join(base, "workspace"),
		Cwd:            filepath.Join(base, "project"),
	}
}

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

	if _, err := mgr.LoadServer(ctx, "shared"); err != nil {
		t.Fatalf("second LoadServer: %v", err)
	}
	if n := dialer.dials("project-shared"); n != 1 {
		t.Errorf("dials = %d, want 1 (cached specs)", n)
	}
	if cfgs := dialer.configs(); len(cfgs) != 1 || cfgs[0].Description != "Project shared" {
		t.Errorf("dialed configs = %+v, want the merged project entry", cfgs)
	}

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

func specNames(specs []tools.LoadedMCPTool) []string {
	out := make([]string, 0, len(specs))
	for _, spec := range specs {
		out = append(out, spec.Name)
	}
	return out
}

func TestE2EGetMCPToolRegistersAndReloads(t *testing.T) {
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
	list := tools.NewListMCPToolsTool(mgr)
	get := tools.NewGetMCPToolTool(mgr, registry, mgr.ToolFactory())

	res := list.Execute(ctx, map[string]interface{}{"server": "files"})
	if res.IsError {
		t.Fatalf("list failed: %s", res.ForLLM)
	}
	for _, name := range []string{"mcp_files_echo", "mcp_files_summary"} {
		if !strings.Contains(res.ForLLM, name) {
			t.Errorf("listing lacks %s:\n%s", name, res.ForLLM)
		}
	}
	if strings.Contains(res.ForLLM, "inputSchema") {
		t.Errorf("listing must stay schema-free:\n%s", res.ForLLM)
	}
	if registry.Count() != 0 {
		t.Errorf("Count() = %d after listing, want 0 (listing registers nothing)", registry.Count())
	}

	batch := map[string]interface{}{"server": "files", "tools": []interface{}{"echo", "summary"}}
	res = get.Execute(ctx, batch)
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
	if want := `Loaded 2 tool(s) from MCP server "files".`; !strings.Contains(res.ForLLM, want) {
		t.Errorf("first load output lacks %q:\n%s", want, res.ForLLM)
	}
	if want := `Tool mcp_files_echo from MCP server "files" is now callable.`; !strings.Contains(res.ForLLM, want) {
		t.Errorf("first load output lacks %q:\n%s", want, res.ForLLM)
	}
	if got := strings.Count(res.ForLLM, "inputSchema:"); got != 2 {
		t.Errorf("first load rendered %d schemas, want 2:\n%s", got, res.ForLLM)
	}

	res = get.Execute(ctx, batch)
	if res.IsError {
		t.Fatalf("second load failed: %s", res.ForLLM)
	}
	if registry.Count() != 2 {
		t.Errorf("Count() = %d after second load, want 2 (no duplicate registration)", registry.Count())
	}
	if got := strings.Count(res.ForLLM, "already registered and callable"); got != 2 {
		t.Errorf("%d blocks report an existing tool, want 2:\n%s", got, res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "is now callable") {
		t.Errorf("second load must not claim a fresh registration:\n%s", res.ForLLM)
	}
	if got := strings.Count(res.ForLLM, "inputSchema:"); got != 2 {
		t.Errorf("second load rendered %d schemas, want 2:\n%s", got, res.ForLLM)
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

	res = registry.Execute(ctx, "mcp_files_echo", map[string]interface{}{"message": "hola"})
	if res.IsError {
		t.Fatalf("registry execution failed: %s", res.ForLLM)
	}
	if res.ForLLM != "files:hola" {
		t.Errorf("echo via registry = %q, want %q", res.ForLLM, "files:hola")
	}

	res = get.Execute(ctx, map[string]interface{}{"server": "ghost", "tool": "echo"})
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

	if n := dialer.dials("files-bin"); n != 1 {
		t.Errorf("dials = %d, want 1", n)
	}
}

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
	get := tools.NewGetMCPToolTool(mgr, registry, mgr.ToolFactory())
	for _, name := range []string{"alpha", "beta"} {
		res := get.Execute(ctx, map[string]interface{}{
			"server": name,
			"tools":  []interface{}{"echo", "summary"},
		})
		if res.IsError {
			t.Fatalf("load %s: %s", name, res.ForLLM)
		}
	}

	if registry.Count() != 4 {
		t.Errorf("Count() = %d, want 4 (2 tools × 2 servers)", registry.Count())
	}
	want := []string{"mcp_alpha_echo", "mcp_alpha_summary", "mcp_beta_echo", "mcp_beta_summary"}
	got := registry.List()
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

func infoNames(infos []tools.MCPServerInfo) []string {
	out := make([]string, 0, len(infos))
	for _, info := range infos {
		out = append(out, info.Name)
	}
	return out
}

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

		kill()

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

	dialer := newE2EDialer()
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
	listOut := tools.NewListMCPToolsTool(mgr).Execute(context.Background(), map[string]interface{}{"server": "x"})
	if !listOut.IsError || !strings.Contains(listOut.ForLLM, "known servers: (none)") {
		t.Errorf("list output = %q (IsError=%v), want a clean unknown-server error", listOut.ForLLM, listOut.IsError)
	}
	getOut := tools.NewGetMCPToolTool(mgr, registry, mgr.ToolFactory()).
		Execute(context.Background(), map[string]interface{}{"server": "x", "tool": "echo"})
	if !getOut.IsError || !strings.Contains(getOut.ForLLM, "known servers: (none)") {
		t.Errorf("get output = %q (IsError=%v), want a clean unknown-server error", getOut.ForLLM, getOut.IsError)
	}
	if registry.Count() != 0 {
		t.Errorf("Count() = %d, want 0 (nothing was registered)", registry.Count())
	}
	if n := len(dialer.configs()); n != 0 {
		t.Errorf("dials = %d, want 0", n)
	}
}

func TestE2EUsageOrderingListGetCallList(t *testing.T) {
	paths := e2ePaths(t)
	if err := os.MkdirAll(paths.LeleDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", paths.LeleDir, err)
	}
	writeFileAt(t, filepath.Join(paths.AgentWorkspace, "mcp.json"),
		`{"mcpServers": {"files": {"command": "files-bin", "description": "Local file operations"}}}`)

	srv, _ := startMCPServer(t, "files")
	dialer := newE2EDialer()
	dialer.script("files-bin", dialStep{conn: connFor(t, srv)})
	mgr := NewManager(paths, dialer)
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := context.Background()
	registry := tools.NewToolRegistry()
	list := tools.NewListMCPToolsTool(mgr)
	get := tools.NewGetMCPToolTool(mgr, registry, mgr.ToolFactory())

	before := list.Execute(ctx, map[string]interface{}{"server": "files"})
	if before.IsError {
		t.Fatalf("first list failed: %s", before.ForLLM)
	}
	if got := e2eListedNames(before.ForLLM); !slices.Equal(got, []string{"mcp_files_echo", "mcp_files_summary"}) {
		t.Fatalf("first listing = %v, want alphabetical [mcp_files_echo mcp_files_summary]", got)
	}

	if res := get.Execute(ctx, map[string]interface{}{"server": "files", "tool": "summary"}); res.IsError {
		t.Fatalf("get failed: %s", res.ForLLM)
	}
	call := registry.Execute(ctx, "mcp_files_summary", map[string]interface{}{"label": "usage"})
	if call.IsError {
		t.Fatalf("summary call failed: %s", call.ForLLM)
	}
	if !strings.Contains(call.ForLLM, "summary for usage") {
		t.Errorf("call result = %q, want the server's answer", call.ForLLM)
	}

	usagePath := filepath.Join(paths.LeleDir, "mcp_usage.json")
	data, err := os.ReadFile(usagePath)
	if err != nil {
		t.Fatalf("read %s: %v", usagePath, err)
	}
	var counts map[string]map[string]uint64
	if err := json.Unmarshal(data, &counts); err != nil {
		t.Fatalf("parse %s: %v", usagePath, err)
	}
	if got := counts["files"]["summary"]; got != 1 {
		t.Errorf(`usage["files"]["summary"] = %d, want 1 (file: %s)`, got, data)
	}

	after := list.Execute(ctx, map[string]interface{}{"server": "files"})
	if after.IsError {
		t.Fatalf("second list failed: %s", after.ForLLM)
	}
	if got := e2eListedNames(after.ForLLM); !slices.Equal(got, []string{"mcp_files_summary", "mcp_files_echo"}) {
		t.Errorf("second listing = %v, want the used tool first", got)
	}
	if !strings.Contains(after.ForLLM, "(used 1 time)") {
		t.Errorf("the used tool should carry its count:\n%s", after.ForLLM)
	}

	srv2, _ := startMCPServer(t, "files")
	dialer2 := newE2EDialer()
	dialer2.script("files-bin", dialStep{conn: connFor(t, srv2)})
	mgr2 := NewManager(paths, dialer2)
	t.Cleanup(func() { _ = mgr2.Close() })
	if got := mgr2.ToolUsage("files")["summary"]; got != 1 {
		t.Errorf("reloaded manager ToolUsage(summary) = %d, want 1", got)
	}
	reloaded := tools.NewListMCPToolsTool(mgr2).Execute(ctx, map[string]interface{}{"server": "files"})
	if reloaded.IsError {
		t.Fatalf("reloaded list failed: %s", reloaded.ForLLM)
	}
	if got := e2eListedNames(reloaded.ForLLM); !slices.Equal(got, []string{"mcp_files_summary", "mcp_files_echo"}) {
		t.Errorf("reloaded listing = %v, want the persisted ranking", got)
	}

	if n := dialer.dials("files-bin"); n != 1 {
		t.Errorf("dials = %d, want 1", n)
	}
}

func e2eListedNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "- "); ok {
			name, _, _ := strings.Cut(rest, " ")
			names = append(names, name)
		}
	}
	return names
}
