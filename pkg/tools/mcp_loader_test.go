package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// --- test doubles -----------------------------------------------------------

// fakeLoader implements MCPLoader with scripted behavior.
type fakeLoader struct {
	servers []MCPServerInfo
	specs   map[string][]LoadedMCPTool
	err     error
	loads   int
}

func (f *fakeLoader) Servers() []MCPServerInfo { return f.servers }

func (f *fakeLoader) LoadServer(_ context.Context, server string) ([]LoadedMCPTool, error) {
	f.loads++
	if f.err != nil {
		return nil, f.err
	}
	if specs, ok := f.specs[server]; ok {
		return specs, nil
	}
	// Mirrors the real manager's resolve() error (N4): the known-servers
	// list is deliberately absent — loadError appends the authoritative one.
	return nil, fmt.Errorf("mcp server %q is not configured", server)
}

// fakeTool is a minimal Tool implementation.
type fakeTool struct {
	name   string
	desc   string
	schema map[string]interface{}
}

func (f *fakeTool) Name() string               { return f.name }
func (f *fakeTool) Description() string        { return f.desc }
func (f *fakeTool) Parameters() map[string]any { return f.schema }
func (f *fakeTool) Execute(context.Context, map[string]any) *ToolResult {
	return NewToolResult("fake " + f.name)
}

// factoryRecorder builds fakeTools and remembers the specs it saw.
type factoryRecorder struct {
	built []string
}

func (r *factoryRecorder) build(spec LoadedMCPTool) Tool {
	r.built = append(r.built, spec.Name)
	return &fakeTool{name: spec.Name, desc: spec.Description, schema: spec.InputSchema}
}

// specSet returns three plausible specs of one server (Server stamped the way
// pkg/mcp's buildSpecs does it).
func specSet() []LoadedMCPTool {
	return []LoadedMCPTool{
		{
			Name:        "mcp_fs_read",
			Server:      "fs",
			RemoteName:  "read",
			Description: "Read a file",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
			},
		},
		{Name: "mcp_fs_write", Server: "fs", RemoteName: "write", Description: "Write a file",
			InputSchema: map[string]interface{}{"type": "object"}},
		{Name: "mcp_fs_stat", Server: "fs", RemoteName: "stat", Description: "File stats",
			InputSchema: map[string]interface{}{"type": "object"}},
	}
}

// newLoaderTool wires a loader tool with fakes over a fresh registry.
func newLoaderTool(loader MCPLoader) (*LoadMCPToolsTool, *ToolRegistry, *factoryRecorder) {
	reg := NewToolRegistry()
	rec := &factoryRecorder{}
	return NewLoadMCPToolsTool(loader, reg, rec.build), reg, rec
}

// --- T7 ---------------------------------------------------------------------

// TestLoadMCPToolsFirstExecutionRegisters: the happy path registers every
// spec and the ForLLM listing carries header, names, descriptions and schemas.
func TestLoadMCPToolsFirstExecutionRegisters(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs", Description: "file system"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
	}
	tool, reg, rec := newLoaderTool(loader)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if reg.Count() != 3 {
		t.Fatalf("registry count = %d, want 3 (%v)", reg.Count(), reg.List())
	}
	if len(rec.built) != 3 {
		t.Errorf("factory builds = %v, want 3", rec.built)
	}
	if _, ok := reg.Get("mcp_fs_read"); !ok {
		t.Error("mcp_fs_read must be registered under its namespaced name")
	}

	for _, want := range []string{
		`Loaded 3 tool(s) from MCP server "fs"`,
		"3 newly registered, 0 already present",
		"## mcp_fs_read",
		"Read a file",
		"## mcp_fs_write",
		"## mcp_fs_stat",
		`"type": "object"`,
		`"path"`,
	} {
		if !strings.Contains(out.ForLLM, want) {
			t.Errorf("ForLLM should contain %q, got:\n%s", want, out.ForLLM)
		}
	}
}

// TestLoadMCPToolsSecondExecutionIdempotent: specs are listed again but
// nothing is re-registered or re-fetched.
func TestLoadMCPToolsSecondExecutionIdempotent(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
	}
	tool, reg, rec := newLoaderTool(loader)

	if out := tool.Execute(context.Background(), map[string]any{"server": "fs"}); out.IsError {
		t.Fatalf("first execute: %s", out.ForLLM)
	}
	out := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("second execute: %s", out.ForLLM)
	}
	if reg.Count() != 3 {
		t.Errorf("registry count = %d, want 3", reg.Count())
	}
	if len(rec.built) != 3 {
		t.Errorf("factory builds = %d, want 3 (no rebuilds)", len(rec.built))
	}
	if !strings.Contains(out.ForLLM, "0 newly registered, 3 already present") {
		t.Errorf("second listing should report everything as present:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "## mcp_fs_read") {
		t.Error("second listing must still show the specs so the LLM can use them")
	}
}

// TestLoadMCPToolsPreexistingNotOverwritten: an already-registered name
// (built-in or from another server) keeps its original tool instance.
func TestLoadMCPToolsPreexistingNotOverwritten(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
	}
	tool, reg, rec := newLoaderTool(loader)
	sentinel := &fakeTool{name: "mcp_fs_read", desc: "built-in wins"}
	reg.Register(sentinel)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	got, ok := reg.Get("mcp_fs_read")
	if !ok {
		t.Fatal("mcp_fs_read disappeared from the registry")
	}
	if got != Tool(sentinel) {
		t.Errorf("existing tool was overwritten: got %T", got)
	}
	if reg.Count() != 3 {
		t.Errorf("registry count = %d, want 3", reg.Count())
	}
	if len(rec.built) != 2 {
		t.Errorf("factory builds = %v, want only the 2 missing specs", rec.built)
	}
	if !strings.Contains(out.ForLLM, "2 newly registered, 1 already present") {
		t.Errorf("listing should report the mix:\n%s", out.ForLLM)
	}
}

// TestLoadMCPToolsUnknownServer: ErrorResult, no registration, known list.
func TestLoadMCPToolsUnknownServer(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}, {Name: "ghe"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
	}
	tool, reg, rec := newLoaderTool(loader)

	out := tool.Execute(context.Background(), map[string]any{"server": "nope"})
	if !out.IsError {
		t.Fatal("expected IsError for an unknown server")
	}
	if reg.Count() != 0 || len(rec.built) != 0 {
		t.Errorf("nothing must be registered (count=%d, built=%v)", reg.Count(), rec.built)
	}
	if !strings.Contains(out.ForLLM, `failed to load MCP server "nope"`) {
		t.Errorf("error should name the server: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "known servers: fs, ghe") {
		t.Errorf("error should list known servers: %s", out.ForLLM)
	}
	// N4: the manager-side error no longer carries a list, so the final
	// message contains the authoritative list from loadError EXACTLY once —
	// a duplicated "known servers:" line is the regression this pins.
	if n := strings.Count(out.ForLLM, "known servers:"); n != 1 {
		t.Errorf("known-server list appears %d times, want exactly 1:\n%s", n, out.ForLLM)
	}
}

// TestLoadMCPToolsLoadErrorAlwaysListsKnownServers pins the decoupling: even
// when the wrapped error mentions "known servers:" with a DIFFERENT (stale)
// list, loadError appends the authoritative set from loader.Servers(). No
// strings.Contains coupling may decide whether the list appears.
func TestLoadMCPToolsLoadErrorAlwaysListsKnownServers(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}, {Name: "ghe"}},
		err:     errors.New(`stale listing from elsewhere: known servers: ghost`),
	}
	tool, reg, _ := newLoaderTool(loader)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if !out.IsError {
		t.Fatal("expected IsError")
	}
	if reg.Count() != 0 {
		t.Error("nothing must be registered on failure")
	}
	if !strings.Contains(out.ForLLM, "known servers: fs, ghe") {
		t.Errorf("authoritative known-servers list missing:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "known servers: ghost") {
		t.Errorf("wrapped error text must survive:\n%s", out.ForLLM)
	}
}

// TestLoadMCPToolsBareLoaderErrorGetsKnownServers: a loader that returns a
// bare error still produces a helpful message.
func TestLoadMCPToolsBareLoaderErrorGetsKnownServers(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}, {Name: "ghe"}},
		err:     fmt.Errorf("connection exploded"),
	}
	tool, reg, _ := newLoaderTool(loader)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if !out.IsError {
		t.Fatal("expected IsError")
	}
	if reg.Count() != 0 {
		t.Error("nothing must be registered on failure")
	}
	if !strings.Contains(out.ForLLM, "connection exploded") {
		t.Errorf("cause must survive: %s", out.ForLLM)
	}
	if n := strings.Count(out.ForLLM, "known servers:"); n != 1 {
		t.Errorf("known-server list appears %d times, want exactly 1:\n%s", n, out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "fs, ghe") {
		t.Errorf("appended list should name the servers: %s", out.ForLLM)
	}
}

// TestLoadMCPToolsMissingServerArgument: args validation with a known list.
func TestLoadMCPToolsMissingServerArgument(t *testing.T) {
	loader := &fakeLoader{servers: []MCPServerInfo{{Name: "fs"}}}
	tool, reg, _ := newLoaderTool(loader)

	for _, args := range []map[string]any{nil, {}, {"server": ""}, {"server": 42}} {
		out := tool.Execute(context.Background(), args)
		if !out.IsError {
			t.Errorf("args %v: expected IsError", args)
		}
		if !strings.Contains(out.ForLLM, `"server"`) {
			t.Errorf("args %v: error should mention the server argument: %s", args, out.ForLLM)
		}
		if !strings.Contains(out.ForLLM, "known servers: fs") {
			t.Errorf("args %v: error should list known servers: %s", args, out.ForLLM)
		}
	}
	if reg.Count() != 0 {
		t.Errorf("registry count = %d, want 0", reg.Count())
	}
	if loader.loads != 0 {
		t.Errorf("LoadServer calls = %d, want 0 (rejected locally)", loader.loads)
	}
}

// TestLoadMCPToolsUnconfigured: nil seams fail cleanly, never panic.
func TestLoadMCPToolsUnconfigured(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
	}
	reg := NewToolRegistry()
	rec := &factoryRecorder{}

	cases := []struct {
		name string
		tool *LoadMCPToolsTool
	}{
		{"nil loader", NewLoadMCPToolsTool(nil, reg, rec.build)},
		{"nil registry", NewLoadMCPToolsTool(loader, nil, rec.build)},
		{"nil factory", NewLoadMCPToolsTool(loader, reg, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.tool.Execute(context.Background(), map[string]any{"server": "fs"})
			if !out.IsError {
				t.Fatalf("expected IsError, got: %s", out.ForLLM)
			}
			if !strings.Contains(out.ForLLM, "not configured") {
				t.Errorf("error should say it is not configured: %s", out.ForLLM)
			}
		})
	}
	if reg.Count() != 0 {
		t.Errorf("registry count = %d, want 0", reg.Count())
	}
}

// TestLoadMCPToolsShape: name, description and parameters contract.
func TestLoadMCPToolsShape(t *testing.T) {
	tool, _, _ := newLoaderTool(&fakeLoader{})

	if tool.Name() != "load_mcp_tools" {
		t.Errorf("Name = %q, want load_mcp_tools", tool.Name())
	}

	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("parameters type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %v", params)
	}
	server, ok := props["server"].(map[string]any)
	if !ok || server["type"] != "string" {
		t.Errorf("server property = %v, want a string", props["server"])
	}
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "server" {
		t.Errorf("required = %v, want [server]", params["required"])
	}

	desc := tool.Description()
	for _, want := range []string{"## MCP Servers", `{"server": "<name>"}`, "regular tools"} {
		if !strings.Contains(desc, want) {
			t.Errorf("Description should contain %q, got: %s", want, desc)
		}
	}
}
