package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func usageLoader(usage map[string]uint64) *fakeLoader {
	return &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs", Description: "file system"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
		usage:   map[string]map[string]uint64{"fs": usage},
	}
}

func manySpecs(n int) []LoadedMCPTool {
	specs := make([]LoadedMCPTool, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("tool%02d", i)
		specs = append(specs, LoadedMCPTool{
			Name:        "mcp_big_" + name,
			Server:      "big",
			RemoteName:  name,
			Description: "tool number " + name,
		})
	}
	return specs
}

func bigLoader(n int) *fakeLoader {
	return &fakeLoader{
		servers: []MCPServerInfo{{Name: "big"}},
		specs:   map[string][]LoadedMCPTool{"big": manySpecs(n)},
	}
}

func listedNames(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "- "); ok {
			name, _, _ := strings.Cut(rest, " ")
			names = append(names, name)
		}
	}
	return names
}

func newGetTool(loader MCPLoader) (*GetMCPToolTool, *ToolRegistry, *factoryRecorder) {
	reg := NewToolRegistry()
	rec := &factoryRecorder{}
	return NewGetMCPToolTool(loader, reg, rec.build), reg, rec
}

func assertOrder(t *testing.T, out string, want ...string) {
	t.Helper()
	prev := -1
	for _, w := range want {
		idx := strings.Index(out, w)
		if idx < 0 {
			t.Fatalf("output should contain %q:\n%s", w, out)
		}
		if idx < prev {
			t.Fatalf("%q appears out of order:\n%s", w, out)
		}
		prev = idx
	}
}

func TestListMCPToolsOrderedByUsage(t *testing.T) {
	loader := usageLoader(map[string]uint64{"read": 12, "write": 3})
	out := NewListMCPToolsTool(loader).Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	assertOrder(t, out.ForLLM, "mcp_fs_read", "mcp_fs_write", "mcp_fs_stat")
	wantHeader := `MCP server "fs": 3 tool(s), page 1/1 (page size 20). Ordered by usage frequency.`
	if !strings.Contains(out.ForLLM, wantHeader) {
		t.Errorf("header mismatch:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "mcp_fs_read — Read a file (used 12 times)") {
		t.Errorf("the used-12 marker is missing:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, "mcp_fs_stat — File stats (") {
		t.Errorf("a never-used tool must carry no usage marker:\n%s", out.ForLLM)
	}
}

func TestListMCPToolsSingularUsage(t *testing.T) {
	loader := usageLoader(map[string]uint64{"stat": 1})
	out := NewListMCPToolsTool(loader).Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "(used 1 time)") {
		t.Errorf("singular wording expected:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, "(used 1 times)") {
		t.Errorf("plural wording leaked into a count of 1:\n%s", out.ForLLM)
	}
}

func TestListMCPToolsTieBreakByNameAsc(t *testing.T) {
	loader := usageLoader(nil)
	out := NewListMCPToolsTool(loader).Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	got := listedNames(out.ForLLM)
	want := []string{"mcp_fs_read", "mcp_fs_stat", "mcp_fs_write"}
	if len(got) != len(want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listed %v, want %v", got, want)
		}
	}
}

func TestListMCPToolsPaginationDefaults(t *testing.T) {
	out := NewListMCPToolsTool(bigLoader(25)).Execute(context.Background(), map[string]any{"server": "big"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, `MCP server "big": 25 tool(s), page 1/2 (page size 20).`) {
		t.Errorf("header mismatch:\n%s", out.ForLLM)
	}
	names := listedNames(out.ForLLM)
	if len(names) != 20 {
		t.Fatalf("page 1 has %d tools, want 20", len(names))
	}
	if names[0] != "mcp_big_tool00" || names[19] != "mcp_big_tool19" {
		t.Errorf("page 1 = %v … %v", names[0], names[19])
	}
}

func TestListMCPToolsCustomPage(t *testing.T) {
	tool := NewListMCPToolsTool(bigLoader(25))
	first := tool.Execute(context.Background(), map[string]any{"server": "big", "page": 1, "page_size": 10})
	if first.IsError {
		t.Fatalf("unexpected error: %s", first.ForLLM)
	}
	if !strings.Contains(first.ForLLM, "page 1/3 (page size 10)") {
		t.Errorf("header mismatch:\n%s", first.ForLLM)
	}
	if got := listedNames(first.ForLLM); len(got) != 10 || got[0] != "mcp_big_tool00" {
		t.Errorf("page 1 = %v, want 10 tools starting at mcp_big_tool00", got)
	}

	last := tool.Execute(context.Background(), map[string]any{"server": "big", "page": 3, "page_size": 10})
	if last.IsError {
		t.Fatalf("unexpected error: %s", last.ForLLM)
	}
	if !strings.Contains(last.ForLLM, "page 3/3 (page size 10)") {
		t.Errorf("header mismatch:\n%s", last.ForLLM)
	}
	got := listedNames(last.ForLLM)
	if len(got) != 5 || got[0] != "mcp_big_tool20" || got[4] != "mcp_big_tool24" {
		t.Errorf("page 3 = %v, want the 5 remaining tools", got)
	}
}

func TestListMCPToolsPageSizeClamped(t *testing.T) {
	out := NewListMCPToolsTool(bigLoader(150)).Execute(context.Background(), map[string]any{"server": "big", "page_size": 500})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "page 1/2 (page size 100)") {
		t.Errorf("page_size should clamp to 100:\n%s", out.ForLLM)
	}
	if got := listedNames(out.ForLLM); len(got) != 100 {
		t.Errorf("listed %d tools, want 100", len(got))
	}
}

func TestListMCPToolsPageBeyondRange(t *testing.T) {
	out := NewListMCPToolsTool(bigLoader(25)).Execute(context.Background(), map[string]any{"server": "big", "page": 99})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "page 2/2 (page size 20)") {
		t.Errorf("out-of-range page should clamp to the last page:\n%s", out.ForLLM)
	}
	got := listedNames(out.ForLLM)
	if len(got) != 5 || got[0] != "mcp_big_tool20" {
		t.Errorf("clamped page = %v, want the 5 last tools", got)
	}
}

func TestListMCPToolsArgForms(t *testing.T) {
	tool := NewListMCPToolsTool(bigLoader(25))
	floatOut := tool.Execute(context.Background(), map[string]any{"server": "big", "page": float64(2), "page_size": float64(10)})
	intOut := tool.Execute(context.Background(), map[string]any{"server": "big", "page": 2, "page_size": 10})
	if floatOut.IsError || intOut.IsError {
		t.Fatalf("unexpected error: %s / %s", floatOut.ForLLM, intOut.ForLLM)
	}
	if floatOut.ForLLM != intOut.ForLLM {
		t.Errorf("float64 and int forms paginate differently:\n%s\n---\n%s", floatOut.ForLLM, intOut.ForLLM)
	}
	if !strings.Contains(floatOut.ForLLM, "page 2/3 (page size 10)") {
		t.Errorf("header mismatch:\n%s", floatOut.ForLLM)
	}
}

func TestListMCPToolsInvalidPagingFallsBack(t *testing.T) {
	tool := NewListMCPToolsTool(bigLoader(25))
	baseline := tool.Execute(context.Background(), map[string]any{"server": "big"})
	if baseline.IsError {
		t.Fatalf("unexpected error: %s", baseline.ForLLM)
	}

	argSets := []map[string]any{
		{"server": "big", "page": 0, "page_size": 0},
		{"server": "big", "page": -3, "page_size": -7},
		{"server": "big", "page": "two", "page_size": "ten"},
		{"server": "big", "page": nil, "page_size": nil},
		{"server": "big", "page": -0.5, "page_size": -1.5},
		{"server": "big", "page": "", "page_size": ""},
	}
	for _, args := range argSets {
		out := tool.Execute(context.Background(), args)
		if out.IsError {
			t.Errorf("args %v: unexpected error: %s", args, out.ForLLM)
			continue
		}
		if out.ForLLM != baseline.ForLLM {
			t.Errorf("args %v: expected the default page 1/size 20 listing:\n%s", args, out.ForLLM)
		}
	}
}

func TestListMCPToolsEmptyServer(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "empty"}},
		specs:   map[string][]LoadedMCPTool{"empty": {}},
	}
	out := NewListMCPToolsTool(loader).Execute(context.Background(), map[string]any{"server": "empty"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if out.ForLLM != `MCP server "empty" has no tools.` {
		t.Errorf("ForLLM = %q", out.ForLLM)
	}
}
func TestListMCPToolsErrors(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "ghe"}, {Name: "fs"}},
		specs:   map[string][]LoadedMCPTool{"fs": specSet()},
	}
	tool := NewListMCPToolsTool(loader)

	unknown := tool.Execute(context.Background(), map[string]any{"server": "nope"})
	if !unknown.IsError {
		t.Fatal("an unknown server must be an error")
	}
	if !strings.Contains(unknown.ForLLM, `failed to load MCP server "nope"`) ||
		!strings.Contains(unknown.ForLLM, "known servers: fs, ghe") {
		t.Errorf("shared load-error shape expected:\n%s", unknown.ForLLM)
	}
	if n := strings.Count(unknown.ForLLM, "known servers:"); n != 1 {
		t.Errorf("known-server list appears %d times, want exactly 1:\n%s", n, unknown.ForLLM)
	}

	for _, args := range []map[string]any{nil, {}, {"server": ""}, {"server": "   "}, {"server": 42}} {
		out := tool.Execute(context.Background(), args)
		if !out.IsError {
			t.Errorf("args %v: expected IsError", args)
			continue
		}
		if !strings.Contains(out.ForLLM, `"server"`) || !strings.Contains(out.ForLLM, "known servers: fs, ghe") {
			t.Errorf("args %v: error should mention the argument and the servers: %s", args, out.ForLLM)
		}
	}
}

func TestListMCPToolsNilLoader(t *testing.T) {
	out := NewListMCPToolsTool(nil).Execute(context.Background(), map[string]any{"server": "fs"})
	if !out.IsError || !strings.Contains(out.ForLLM, "not configured") {
		t.Fatalf("expected a not-configured error, got: %+v", out)
	}
}

func TestListMCPToolsNoSchemaInOutput(t *testing.T) {
	out := NewListMCPToolsTool(usageLoader(nil)).Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	for _, forbidden := range []string{"inputSchema", `"properties"`, `"path"`, `"type": "object"`, "```"} {
		if strings.Contains(out.ForLLM, forbidden) {
			t.Errorf("listing must not contain %q:\n%s", forbidden, out.ForLLM)
		}
	}
}

func TestListMCPToolsDescriptions(t *testing.T) {
	long := strings.Repeat("a", 119) + "b" + strings.Repeat("c", 80)
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "d"}},
		specs: map[string][]LoadedMCPTool{"d": {
			{Name: "mcp_d_multi", Server: "d", RemoteName: "multi", Description: "First line\nsecond line\nthird"},
			{Name: "mcp_d_long", Server: "d", RemoteName: "long", Description: long},
			{Name: "mcp_d_empty", Server: "d", RemoteName: "empty", Description: "  \n  "},
		}},
	}
	out := NewListMCPToolsTool(loader).Execute(context.Background(), map[string]any{"server": "d"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "- mcp_d_multi — First line") {
		t.Errorf("only the first line should be shown:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, "second line") {
		t.Errorf("later lines must be dropped:\n%s", out.ForLLM)
	}
	wantTruncated := "— " + strings.Repeat("a", 119) + "b…"
	if !strings.Contains(out.ForLLM, wantTruncated) {
		t.Errorf("a >120-rune description must be truncated with an ellipsis:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, "ccc") {
		t.Errorf("text beyond rune 120 leaked into the listing:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "\n- mcp_d_empty\n") {
		t.Errorf("an empty description must drop the description part entirely:\n%s", out.ForLLM)
	}
}

func TestListMCPToolsRegistersNothing(t *testing.T) {
	base := NewToolRegistry()
	base.Register(&fakeTool{name: "read_file", desc: "built-in"})
	layer := NewScopedRegistry(base)

	out := NewListMCPToolsTool(usageLoader(map[string]uint64{"read": 4})).Execute(context.Background(), map[string]any{"server": "fs"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if got := layer.Count(); got != 1 {
		t.Errorf("layer Count() = %d, want 1 (listing must not register)", got)
	}
	for _, name := range layer.List() {
		if strings.HasPrefix(name, "mcp_") {
			t.Errorf("listing registered %q", name)
		}
	}
}

func TestListMCPToolsDoesNotMutateCache(t *testing.T) {
	loader := usageLoader(map[string]uint64{"write": 99})
	tool := NewListMCPToolsTool(loader)

	first := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if first.IsError {
		t.Fatalf("unexpected error: %s", first.ForLLM)
	}
	if got := listedNames(first.ForLLM); got[0] != "mcp_fs_write" {
		t.Fatalf("listing order = %v, want mcp_fs_write first", got)
	}
	cached := loader.specs["fs"]
	gotNames := make([]string, len(cached))
	for i, spec := range cached {
		gotNames[i] = spec.Name
	}
	want := "mcp_fs_read,mcp_fs_write,mcp_fs_stat"
	if strings.Join(gotNames, ",") != want {
		t.Errorf("the cached slice was reordered: %v, want %s", gotNames, want)
	}

	second := tool.Execute(context.Background(), map[string]any{"server": "fs"})
	if second.IsError {
		t.Fatalf("unexpected error: %s", second.ForLLM)
	}
	if second.ForLLM != first.ForLLM {
		t.Errorf("repeated listing differs:\n%s\n---\n%s", first.ForLLM, second.ForLLM)
	}
	if cachedAgain := loader.specs["fs"]; len(cachedAgain) != 3 || cachedAgain[0].Name != "mcp_fs_read" {
		t.Errorf("the cached slice changed between calls: %v", cachedAgain)
	}
}

func TestListMCPToolsShape(t *testing.T) {
	tool := NewListMCPToolsTool(&fakeLoader{})
	if tool.Name() != ListMCPToolsName || ListMCPToolsName != "list_mcp_tools" {
		t.Errorf("Name = %q, want list_mcp_tools", tool.Name())
	}
	for _, want := range []string{"names and short descriptions only", "no input schemas", "get_mcp_tool"} {
		if !strings.Contains(tool.Description(), want) {
			t.Errorf("Description should contain %q, got: %s", want, tool.Description())
		}
	}

	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("parameters type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %v", params)
	}
	server, _ := props["server"].(map[string]any)
	if server["type"] != "string" {
		t.Errorf("server property = %v, want a string", props["server"])
	}
	page, _ := props["page"].(map[string]any)
	if page["type"] != "integer" || page["minimum"] != 1 {
		t.Errorf("page property = %v, want an integer with minimum 1", props["page"])
	}
	size, _ := props["page_size"].(map[string]any)
	if size["type"] != "integer" || size["minimum"] != 1 || size["maximum"] != maxMCPToolsPageSize {
		t.Errorf("page_size property = %v, want integer 1..%d", props["page_size"], maxMCPToolsPageSize)
	}
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "server" {
		t.Errorf("required = %v, want [server]", params["required"])
	}
}

func TestGetMCPToolByNamespacedName(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))

	out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "mcp_fs_read"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, `Tool mcp_fs_read from MCP server "fs" is now callable.`) {
		t.Errorf("header mismatch:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "Read a file") {
		t.Errorf("the full description is missing:\n%s", out.ForLLM)
	}
	for _, want := range []string{"inputSchema:", "```json", `"path": {`, `"type": "string"`, "```"} {
		if !strings.Contains(out.ForLLM, want) {
			t.Errorf("output should contain %q:\n%s", want, out.ForLLM)
		}
	}
	if ids := rec.built; len(ids) != 1 || ids[0] != "mcp_fs_read" {
		t.Errorf("factory built %v, want exactly [mcp_fs_read]", ids)
	}
	if reg.Count() != 1 {
		t.Errorf("registry count = %d, want 1 (only the requested tool)", reg.Count())
	}
	if _, ok := reg.Get("mcp_fs_write"); ok {
		t.Error("mcp_fs_write must not be registered by a single-tool load")
	}
	if _, ok := reg.Get("mcp_fs_stat"); ok {
		t.Error("mcp_fs_stat must not be registered by a single-tool load")
	}
}

func TestGetMCPToolByRemoteName(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))

	out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "write"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "Tool mcp_fs_write") {
		t.Errorf("the namespaced name should be reported:\n%s", out.ForLLM)
	}
	if ids := rec.built; len(ids) != 1 || ids[0] != "mcp_fs_write" {
		t.Errorf("factory built %v, want exactly [mcp_fs_write]", ids)
	}
	if _, ok := reg.Get("mcp_fs_write"); !ok {
		t.Error("mcp_fs_write must be registered")
	}
	if reg.Count() != 1 {
		t.Errorf("registry count = %d, want 1", reg.Count())
	}
}

func TestGetMCPToolSecondCallIdempotent(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))
	args := map[string]any{"server": "fs", "tool": "mcp_fs_read"}

	first := tool.Execute(context.Background(), args)
	if first.IsError {
		t.Fatalf("first execute: %s", first.ForLLM)
	}
	second := tool.Execute(context.Background(), args)
	if second.IsError {
		t.Fatalf("second execute: %s", second.ForLLM)
	}
	if !strings.Contains(second.ForLLM, "already registered and callable") {
		t.Errorf("second call should say it is already registered:\n%s", second.ForLLM)
	}
	if strings.Contains(second.ForLLM, "is now callable") {
		t.Errorf("second call must not claim a fresh registration:\n%s", second.ForLLM)
	}
	if len(rec.built) != 1 {
		t.Errorf("factory builds = %v, want 1 (no rebuild)", rec.built)
	}
	if reg.Count() != 1 {
		t.Errorf("registry count = %d, want 1", reg.Count())
	}
	if !strings.Contains(second.ForLLM, `"path"`) {
		t.Errorf("second call must still render the schema:\n%s", second.ForLLM)
	}
}

func TestGetMCPToolBaseRegistryWins(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))
	sentinel := &fakeTool{name: "mcp_fs_read", desc: "built-in wins"}
	reg.Register(sentinel)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "mcp_fs_read"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	got, ok := reg.Get("mcp_fs_read")
	if !ok {
		t.Fatal("mcp_fs_read disappeared from the registry")
	}
	if got != Tool(sentinel) {
		t.Errorf("the existing tool was overwritten: got %T", got)
	}
	if len(rec.built) != 0 {
		t.Errorf("factory builds = %v, want none", rec.built)
	}
	if !strings.Contains(out.ForLLM, "already registered and callable") {
		t.Errorf("a pre-existing tool should be reported as already registered:\n%s", out.ForLLM)
	}
	if reg.Count() != 1 {
		t.Errorf("registry count = %d, want 1", reg.Count())
	}
}

func TestGetMCPToolRetainedEntrySpec(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))
	retained := &fakeTool{
		name: "mcp_fs_read",
		desc: "built-in reader",
		schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"handle": map[string]interface{}{"type": "integer"},
			},
		},
	}
	reg.Register(retained)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "mcp_fs_read"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if len(rec.built) != 0 {
		t.Errorf("factory builds = %v, want none (the retained tool wins)", rec.built)
	}
	if !strings.Contains(out.ForLLM, "built-in reader") {
		t.Errorf("the retained description must be rendered:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, `"handle"`) {
		t.Errorf("the retained schema must be rendered:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, `"path"`) {
		t.Errorf("the remote schema describes a tool that will not run:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, `from MCP server "fs"`) {
		t.Errorf("the retained entry must not be attributed to the requested server:\n%s", out.ForLLM)
	}
	if got, _ := reg.Get("mcp_fs_read"); got != Tool(retained) {
		t.Error("the retained tool was replaced")
	}
}

func TestGetMCPToolRegistryNameWinsOverRemoteName(t *testing.T) {
	specs := []LoadedMCPTool{
		{Name: "mcp_fs_mcp_fs_read", Server: "fs", RemoteName: "mcp_fs_read", Description: "Nested lookalike"},
		{Name: "mcp_fs_read", Server: "fs", RemoteName: "read", Description: "Read a file"},
		{Name: "mcp_fs_write", Server: "fs", RemoteName: "write", Description: "Write a file"},
	}
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}},
		specs:   map[string][]LoadedMCPTool{"fs": specs},
	}

	cases := []struct {
		name    string
		request []string
		want    []string
	}{
		{name: "listed name resolves to its own spec", request: []string{"mcp_fs_read"}, want: []string{"mcp_fs_read"}},
		{
			name:    "both listed names load both tools",
			request: []string{"mcp_fs_read", "mcp_fs_mcp_fs_read"},
			want:    []string{"mcp_fs_read", "mcp_fs_mcp_fs_read"},
		},
		{name: "ordinary remote name", request: []string{"read"}, want: []string{"mcp_fs_read"}},
		{name: "remote-only name still resolves", request: []string{"write"}, want: []string{"mcp_fs_write"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, _, rec := newGetTool(loader)
			out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tools": tc.request})
			if out.IsError {
				t.Fatalf("unexpected error: %s", out.ForLLM)
			}
			if len(rec.built) != len(tc.want) {
				t.Fatalf("factory built %v, want %v", rec.built, tc.want)
			}
			for i, name := range tc.want {
				if rec.built[i] != name {
					t.Errorf("factory built %v, want %v", rec.built, tc.want)
					break
				}
			}
			if len(tc.request) == 1 {
				desc := map[string]string{
					"mcp_fs_read":  "Read a file",
					"read":         "Read a file",
					"write":        "Write a file",
					"mcp_fs_write": "Write a file",
				}[tc.request[0]]
				if !strings.Contains(out.ForLLM, desc) {
					t.Errorf("%q must render %q:\n%s", tc.request[0], desc, out.ForLLM)
				}
				if strings.Contains(out.ForLLM, "Nested lookalike") {
					t.Errorf("%q resolved to the nested lookalike:\n%s", tc.request[0], out.ForLLM)
				}
			}
		})
	}
}

func TestGetMCPToolUnknownTool(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))

	out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "remove"})
	if !out.IsError {
		t.Fatal("an unknown tool must be an error")
	}
	if !strings.Contains(out.ForLLM, `tool(s) "remove" not found on MCP server "fs"`) {
		t.Errorf("error should name the tool and the server:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "list_mcp_tools") {
		t.Errorf("error should point at list_mcp_tools:\n%s", out.ForLLM)
	}
	for _, leaked := range []string{"mcp_fs_read", "mcp_fs_write", "mcp_fs_stat"} {
		if strings.Contains(out.ForLLM, leaked) {
			t.Errorf("error must not dump the tool list (%q leaked):\n%s", leaked, out.ForLLM)
		}
	}
	if reg.Count() != 0 || len(rec.built) != 0 {
		t.Errorf("nothing must be registered (count=%d, built=%v)", reg.Count(), rec.built)
	}
}

func TestGetMCPToolMissingArguments(t *testing.T) {
	loader := &fakeLoader{servers: []MCPServerInfo{{Name: "fs"}}}
	tool, reg, _ := newGetTool(loader)

	for _, args := range []map[string]any{nil, {}, {"tool": "read"}, {"server": ""}, {"server": 42}} {
		out := tool.Execute(context.Background(), args)
		if !out.IsError {
			t.Errorf("args %v: expected IsError", args)
			continue
		}
		if !strings.Contains(out.ForLLM, `"server"`) || !strings.Contains(out.ForLLM, "known servers: fs") {
			t.Errorf("args %v: error should mention the argument and the known servers: %s", args, out.ForLLM)
		}
	}

	for _, args := range []map[string]any{{"server": "fs"}, {"server": "fs", "tool": ""}, {"server": "fs", "tool": "  "}} {
		out := tool.Execute(context.Background(), args)
		if !out.IsError {
			t.Errorf("args %v: expected IsError", args)
			continue
		}
		if !strings.Contains(out.ForLLM, `"tool"`) || !strings.Contains(out.ForLLM, "list_mcp_tools") {
			t.Errorf("args %v: error should mention the tool argument and list_mcp_tools: %s", args, out.ForLLM)
		}
	}
	if reg.Count() != 0 {
		t.Errorf("registry count = %d, want 0", reg.Count())
	}
	if loader.loads != 0 {
		t.Errorf("LoadServer calls = %d, want 0 (rejected locally)", loader.loads)
	}
}
func TestGetMCPToolUnconfigured(t *testing.T) {
	loader := usageLoader(nil)
	reg := NewToolRegistry()
	rec := &factoryRecorder{}

	cases := []struct {
		name string
		tool *GetMCPToolTool
	}{
		{"nil loader", NewGetMCPToolTool(nil, reg, rec.build)},
		{"nil registry", NewGetMCPToolTool(loader, nil, rec.build)},
		{"nil factory", NewGetMCPToolTool(loader, reg, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "read"})
			if !out.IsError {
				t.Fatalf("expected IsError, got: %s", out.ForLLM)
			}
			if !strings.Contains(out.ForLLM, "not configured") {
				t.Errorf("error should say it is not configured: %s", out.ForLLM)
			}
		})
	}
	if reg.Count() != 0 || len(rec.built) != 0 {
		t.Errorf("nothing must be registered (count=%d, built=%v)", reg.Count(), rec.built)
	}
}

func TestGetMCPToolLoadError(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "fs"}},
		err:     fmt.Errorf("connection exploded"),
	}
	tool, reg, rec := newGetTool(loader)

	out := tool.Execute(context.Background(), map[string]any{"server": "fs", "tool": "read"})
	if !out.IsError {
		t.Fatal("expected IsError")
	}
	if !strings.Contains(out.ForLLM, "connection exploded") || !strings.Contains(out.ForLLM, "known servers: fs") {
		t.Errorf("shared load-error shape expected:\n%s", out.ForLLM)
	}
	if reg.Count() != 0 || len(rec.built) != 0 {
		t.Errorf("nothing must be registered (count=%d, built=%v)", reg.Count(), rec.built)
	}
}

func TestGetMCPToolEmptySchema(t *testing.T) {
	loader := &fakeLoader{
		servers: []MCPServerInfo{{Name: "s"}},
		specs: map[string][]LoadedMCPTool{"s": {
			{Name: "mcp_s_ping", Server: "s", RemoteName: "ping", Description: "Ping the server"},
		}},
	}
	tool, _, _ := newGetTool(loader)

	out := tool.Execute(context.Background(), map[string]any{"server": "s", "tool": "ping"})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "inputSchema:\n```json\n{}\n```") {
		t.Errorf("an empty schema should render as {}:\n%s", out.ForLLM)
	}
}

func TestGetMCPToolShape(t *testing.T) {
	tool, _, _ := newGetTool(&fakeLoader{})
	if tool.Name() != GetMCPToolName || GetMCPToolName != "get_mcp_tool" {
		t.Errorf("Name = %q, want get_mcp_tool", tool.Name())
	}
	for _, want := range []string{"full specification", "input schema", "list_mcp_tools"} {
		if !strings.Contains(tool.Description(), want) {
			t.Errorf("Description should contain %q, got: %s", want, tool.Description())
		}
	}

	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("parameters type = %v, want object", params["type"])
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing: %v", params)
	}
	for _, key := range []string{"server", "tool"} {
		prop, _ := props[key].(map[string]any)
		if prop["type"] != "string" {
			t.Errorf("%s property = %v, want a string", key, props[key])
		}
	}
	tools, _ := props["tools"].(map[string]any)
	if tools["type"] != "array" {
		t.Errorf("tools property = %v, want an array", props["tools"])
	}
	if items, _ := tools["items"].(map[string]any); items["type"] != "string" {
		t.Errorf("tools items = %v, want strings", tools["items"])
	}
	required, ok := params["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "server" {
		t.Errorf("required = %v, want [server]", params["required"])
	}
}

func specBlocks(out string) int { return strings.Count(out, "inputSchema:") }

func batchArgs(server string, tools ...string) map[string]any {
	list := make([]any, len(tools))
	for i, name := range tools {
		list[i] = name
	}
	return map[string]any{"server": server, "tools": list}
}

func TestGetMCPToolBatchLoadsEveryTool(t *testing.T) {
	loader := usageLoader(nil)
	tool, reg, rec := newGetTool(loader)

	out := tool.Execute(context.Background(), batchArgs("fs", "mcp_fs_stat", "read", "write"))
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, `Loaded 3 tool(s) from MCP server "fs".`) {
		t.Errorf("summary header missing:\n%s", out.ForLLM)
	}
	if got := specBlocks(out.ForLLM); got != 3 {
		t.Errorf("rendered %d schema blocks, want 3:\n%s", got, out.ForLLM)
	}
	assertOrder(t, out.ForLLM, "Tool mcp_fs_stat", "Tool mcp_fs_read", "Tool mcp_fs_write")
	if ids := rec.built; strings.Join(ids, ",") != "mcp_fs_stat,mcp_fs_read,mcp_fs_write" {
		t.Errorf("factory built %v, want the three in caller order", ids)
	}
	if reg.Count() != 3 {
		t.Errorf("registry count = %d, want 3", reg.Count())
	}
	if loader.loads != 1 {
		t.Errorf("LoadServer calls = %d, want 1 (one load per Execute)", loader.loads)
	}
}

func TestGetMCPToolBatchArrayForms(t *testing.T) {
	anyTool, anyReg, anyRec := newGetTool(usageLoader(nil))
	strTool, strReg, strRec := newGetTool(usageLoader(nil))

	fromJSON := anyTool.Execute(context.Background(), batchArgs("fs", "read", "write"))
	fromGo := strTool.Execute(context.Background(),
		map[string]any{"server": "fs", "tools": []string{"read", "write"}})
	if fromJSON.IsError || fromGo.IsError {
		t.Fatalf("unexpected error: %s / %s", fromJSON.ForLLM, fromGo.ForLLM)
	}
	if fromJSON.ForLLM != fromGo.ForLLM {
		t.Errorf("[]interface{} and []string render differently:\n%s\n---\n%s", fromJSON.ForLLM, fromGo.ForLLM)
	}
	if strings.Join(anyRec.built, ",") != strings.Join(strRec.built, ",") ||
		anyReg.Count() != strReg.Count() {
		t.Errorf("registration differs: %v/%d vs %v/%d",
			anyRec.built, anyReg.Count(), strRec.built, strReg.Count())
	}
}

func TestGetMCPToolBatchUnionOfToolAndTools(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))

	out := tool.Execute(context.Background(), map[string]any{
		"server": "fs",
		"tool":   "stat",
		"tools":  []any{"read", "write", "stat"},
	})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if got := specBlocks(out.ForLLM); got != 3 {
		t.Errorf("rendered %d schema blocks, want 3 (stat appears once):\n%s", got, out.ForLLM)
	}
	assertOrder(t, out.ForLLM, "Tool mcp_fs_stat", "Tool mcp_fs_read", "Tool mcp_fs_write")
	if ids := rec.built; strings.Join(ids, ",") != "mcp_fs_stat,mcp_fs_read,mcp_fs_write" {
		t.Errorf("factory built %v, want stat,read,write", ids)
	}
	if reg.Count() != 3 {
		t.Errorf("registry count = %d, want 3", reg.Count())
	}
}

func TestGetMCPToolBatchRemoteAndNamespacedNameCollapse(t *testing.T) {
	batch, batchReg, batchRec := newGetTool(usageLoader(nil))
	single, _, _ := newGetTool(usageLoader(nil))

	got := batch.Execute(context.Background(), batchArgs("fs", "read", "mcp_fs_read"))
	if got.IsError {
		t.Fatalf("unexpected error: %s", got.ForLLM)
	}
	want := single.Execute(context.Background(), map[string]any{"server": "fs", "tool": "read"})
	if want.IsError {
		t.Fatalf("single-tool baseline failed: %s", want.ForLLM)
	}
	if got.ForLLM != want.ForLLM {
		t.Errorf("a collapsed batch must render like a single load:\n%s\n---\n%s", got.ForLLM, want.ForLLM)
	}
	if strings.Contains(got.ForLLM, "Loaded ") {
		t.Errorf("a single resolved spec must not get a summary header:\n%s", got.ForLLM)
	}
	if ids := batchRec.built; len(ids) != 1 || ids[0] != "mcp_fs_read" {
		t.Errorf("factory built %v, want exactly [mcp_fs_read]", ids)
	}
	if batchReg.Count() != 1 {
		t.Errorf("registry count = %d, want 1", batchReg.Count())
	}
}

func TestGetMCPToolBatchIdenticalDuplicatesCollapse(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))

	out := tool.Execute(context.Background(), batchArgs("fs", "read", "read", "  read  ", "read"))
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if got := specBlocks(out.ForLLM); got != 1 {
		t.Errorf("rendered %d schema blocks, want 1:\n%s", got, out.ForLLM)
	}
	if ids := rec.built; len(ids) != 1 {
		t.Errorf("factory built %v, want one tool", ids)
	}
	if reg.Count() != 1 {
		t.Errorf("registry count = %d, want 1", reg.Count())
	}
}

func TestGetMCPToolBatchPartialSuccess(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))

	out := tool.Execute(context.Background(), batchArgs("fs", "read", "remove", "write"))
	if out.IsError {
		t.Fatalf("a partial success must not be an error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, `Loaded 2 tool(s) from MCP server "fs".`) {
		t.Errorf("summary header should count only the found tools:\n%s", out.ForLLM)
	}
	if got := specBlocks(out.ForLLM); got != 2 {
		t.Errorf("rendered %d schema blocks, want 2:\n%s", got, out.ForLLM)
	}
	wantNote := `Not found on MCP server "fs": "remove". Call list_mcp_tools with {"server": "fs"} to see the available tools.`
	if !strings.Contains(out.ForLLM, wantNote) {
		t.Errorf("trailing note missing:\n%s", out.ForLLM)
	}
	if strings.Count(out.ForLLM, "Not found on MCP server") != 1 {
		t.Errorf("the note must appear once:\n%s", out.ForLLM)
	}
	if strings.Contains(out.ForLLM, `"mcp_fs_stat"`) || strings.Contains(out.ForLLM, `"stat"`) {
		t.Errorf("a found tool must not be reported as missing:\n%s", out.ForLLM)
	}
	for _, name := range []string{"mcp_fs_read", "mcp_fs_write"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("%s must be registered despite the missing sibling", name)
		}
	}
	if _, ok := reg.Get("mcp_fs_remove"); ok {
		t.Error("the missing tool must not be registered")
	}
	if strings.Join(rec.built, ",") != "mcp_fs_read,mcp_fs_write" {
		t.Errorf("factory built %v, want the two found tools", rec.built)
	}
	if reg.Count() != 2 {
		t.Errorf("registry count = %d, want 2", reg.Count())
	}
}

func TestGetMCPToolBatchAllMissing(t *testing.T) {
	loader := usageLoader(nil)
	tool, reg, rec := newGetTool(loader)

	out := tool.Execute(context.Background(), batchArgs("fs", "remove", "rename"))
	if !out.IsError {
		t.Fatal("a batch with no resolvable name must be an error")
	}
	if !strings.Contains(out.ForLLM, `tool(s) "remove", "rename" not found on MCP server "fs"`) {
		t.Errorf("error should enumerate the requested names:\n%s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "list_mcp_tools") {
		t.Errorf("error should point at list_mcp_tools:\n%s", out.ForLLM)
	}
	for _, leaked := range []string{"mcp_fs_read", "mcp_fs_write", "mcp_fs_stat"} {
		if strings.Contains(out.ForLLM, leaked) {
			t.Errorf("error must not dump the tool list (%q leaked):\n%s", leaked, out.ForLLM)
		}
	}
	if reg.Count() != 0 || len(rec.built) != 0 {
		t.Errorf("nothing must be registered (count=%d, built=%v)", reg.Count(), rec.built)
	}
	if loader.loads != 1 {
		t.Errorf("LoadServer calls = %d, want 1", loader.loads)
	}
}

func TestGetMCPToolBatchNoUsableNames(t *testing.T) {
	loader := &fakeLoader{servers: []MCPServerInfo{{Name: "fs"}}, specs: map[string][]LoadedMCPTool{"fs": specSet()}}
	tool, reg, rec := newGetTool(loader)

	argSets := []map[string]any{
		{"server": "fs"},
		{"server": "fs", "tool": ""},
		{"server": "fs", "tool": "   "},
		{"server": "fs", "tools": []any{}},
		{"server": "fs", "tools": []string{}},
		{"server": "fs", "tools": []any{"", "  "}},
		{"server": "fs", "tools": []string{"", "\t"}},
		{"server": "fs", "tools": []any{42, nil, true}},
		{"server": "fs", "tools": []any{"", 42, ""}},
		{"server": "fs", "tools": "read"},
		{"server": "fs", "tools": 7},
	}
	for _, args := range argSets {
		out := tool.Execute(context.Background(), args)
		if !out.IsError {
			t.Errorf("args %v: expected IsError", args)
			continue
		}
		if !strings.Contains(out.ForLLM, `"tool" (or "tools")`) ||
			!strings.Contains(out.ForLLM, "list_mcp_tools") {
			t.Errorf("args %v: error should mention the arguments and list_mcp_tools: %s", args, out.ForLLM)
		}
	}
	if loader.loads != 0 {
		t.Errorf("LoadServer calls = %d, want 0 (rejected locally)", loader.loads)
	}
	if reg.Count() != 0 || len(rec.built) != 0 {
		t.Errorf("nothing must be registered (count=%d, built=%v)", reg.Count(), rec.built)
	}
}

func TestGetMCPToolBatchIdempotent(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))
	args := batchArgs("fs", "read", "write", "stat")

	first := tool.Execute(context.Background(), args)
	if first.IsError {
		t.Fatalf("first execute: %s", first.ForLLM)
	}
	second := tool.Execute(context.Background(), args)
	if second.IsError {
		t.Fatalf("second execute: %s", second.ForLLM)
	}
	if got := strings.Count(second.ForLLM, "already registered and callable"); got != 3 {
		t.Errorf("%d blocks report an existing tool, want 3:\n%s", got, second.ForLLM)
	}
	if strings.Contains(second.ForLLM, "is now callable") {
		t.Errorf("second call must not claim a fresh registration:\n%s", second.ForLLM)
	}
	if got := specBlocks(second.ForLLM); got != 3 {
		t.Errorf("the schemas must be rendered again (%d blocks):\n%s", got, second.ForLLM)
	}
	if len(rec.built) != 3 {
		t.Errorf("factory builds = %v, want 3 (no rebuild on the second call)", rec.built)
	}
	if reg.Count() != 3 {
		t.Errorf("registry count = %d, want 3", reg.Count())
	}
}

func TestGetMCPToolBatchBaseRegistryCollision(t *testing.T) {
	tool, reg, rec := newGetTool(usageLoader(nil))
	sentinel := &fakeTool{name: "mcp_fs_read", desc: "built-in wins"}
	reg.Register(sentinel)

	out := tool.Execute(context.Background(), batchArgs("fs", "read", "write"))
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if got, _ := reg.Get("mcp_fs_read"); got != Tool(sentinel) {
		t.Errorf("the built-in tool was overwritten: got %T", got)
	}
	if _, ok := reg.Get("mcp_fs_write"); !ok {
		t.Error("the rest of the batch must still register")
	}
	if ids := rec.built; len(ids) != 1 || ids[0] != "mcp_fs_write" {
		t.Errorf("factory built %v, want exactly [mcp_fs_write]", ids)
	}
	if strings.Count(out.ForLLM, "already registered and callable") != 1 {
		t.Errorf("exactly one block should report an existing tool:\n%s", out.ForLLM)
	}
	if strings.Count(out.ForLLM, "is now callable") != 1 {
		t.Errorf("exactly one block should report a fresh registration:\n%s", out.ForLLM)
	}
	if reg.Count() != 2 {
		t.Errorf("registry count = %d, want 2", reg.Count())
	}
}

func TestGetMCPToolBatchLoadsServerOnceEvenWithDuplicates(t *testing.T) {
	loader := usageLoader(nil)
	tool, _, _ := newGetTool(loader)

	out := tool.Execute(context.Background(), batchArgs("fs", "read", "read", "nope", "mcp_fs_read", "write"))
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if loader.loads != 1 {
		t.Errorf("LoadServer calls = %d, want 1", loader.loads)
	}
}

func TestDiscoveryToolsListKnownServers(t *testing.T) {
	cases := []struct {
		name   string
		loader *fakeLoader
		want   string
	}{
		{
			name:   "sorted names",
			loader: &fakeLoader{servers: []MCPServerInfo{{Name: "zeta"}, {Name: "alpha"}}},
			want:   "known servers: alpha, zeta",
		},
		{name: "no server configured", loader: &fakeLoader{}, want: "known servers: (none)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			getTool, _, _ := newGetTool(tc.loader)
			for name, tool := range map[string]Tool{
				ListMCPToolsName: NewListMCPToolsTool(tc.loader),
				GetMCPToolName:   getTool,
			} {
				out := tool.Execute(context.Background(), map[string]any{})
				if !out.IsError {
					t.Fatalf("%s: a call without the server argument must fail:\n%s", name, out.ForLLM)
				}
				if !strings.Contains(out.ForLLM, tc.want) {
					t.Errorf("%s: output lacks %q:\n%s", name, tc.want, out.ForLLM)
				}
			}
		})
	}
}

func TestLoadErrorAlwaysAppendsAuthoritativeServers(t *testing.T) {
	cases := []struct {
		name             string
		err              error
		wantCause        string
		wantKnownMarkers int
	}{
		{"stale list in the cause", fmt.Errorf("stale listing from elsewhere: known servers: ghost"), "known servers: ghost", 2},
		{"bare cause", fmt.Errorf("connection exploded"), "connection exploded", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loader := &fakeLoader{
				servers: []MCPServerInfo{{Name: "ghe"}, {Name: "fs"}},
				err:     tc.err,
			}
			out := NewListMCPToolsTool(loader).Execute(context.Background(), map[string]any{"server": "fs"})
			if !out.IsError {
				t.Fatalf("expected IsError, got: %s", out.ForLLM)
			}
			if n := strings.Count(out.ForLLM, "known servers:"); n != tc.wantKnownMarkers {
				t.Errorf("known-server markers = %d, want %d:\n%s", n, tc.wantKnownMarkers, out.ForLLM)
			}
			if n := strings.Count(out.ForLLM, "known servers: fs, ghe"); n != 1 {
				t.Errorf("authoritative list appears %d times, want exactly 1:\n%s", n, out.ForLLM)
			}
			if !strings.Contains(out.ForLLM, tc.wantCause) {
				t.Errorf("wrapped error text must survive (%q):\n%s", tc.wantCause, out.ForLLM)
			}
		})
	}
}

func TestListMCPToolsUsageMarkers(t *testing.T) {
	cases := []struct {
		name  string
		count uint64
		want  string
	}{
		{name: "never used", count: 0, want: "- mcp_fs_read — Read a file\n"},
		{name: "used once", count: 1, want: "- mcp_fs_read — Read a file (used 1 time)\n"},
		{name: "used twice", count: 2, want: "- mcp_fs_read — Read a file (used 2 times)\n"},
		{name: "used many times", count: 17, want: "- mcp_fs_read — Read a file (used 17 times)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := NewListMCPToolsTool(usageLoader(map[string]uint64{"read": tc.count})).
				Execute(context.Background(), map[string]any{"server": "fs"})
			if out.IsError {
				t.Fatalf("unexpected error: %s", out.ForLLM)
			}
			if !strings.Contains(out.ForLLM, tc.want) {
				t.Errorf("listing lacks %q:\n%s", tc.want, out.ForLLM)
			}
		})
	}
}

func TestListMCPToolsFractionalPagingTruncates(t *testing.T) {
	out := NewListMCPToolsTool(bigLoader(25)).Execute(context.Background(),
		map[string]any{"server": "big", "page": 2.9, "page_size": 10.7})
	if out.IsError {
		t.Fatalf("unexpected error: %s", out.ForLLM)
	}
	if !strings.Contains(out.ForLLM, "page 2/3 (page size 10)") {
		t.Errorf("fractional paging should truncate:\n%s", out.ForLLM)
	}
}
