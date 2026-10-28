// Lele - Ultra-lightweight personal AI agent
// Tests for the scoped (layered) ToolRegistry used to keep MCP tools
// session-local.
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"context"
	"testing"
)

// scopedTestTool is a minimal Tool for registry tests.
type scopedTestTool struct {
	name string
	desc string
}

func (s *scopedTestTool) Name() string        { return s.name }
func (s *scopedTestTool) Description() string { return s.desc }
func (s *scopedTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (s *scopedTestTool) Execute(context.Context, map[string]interface{}) *ToolResult {
	return NewToolResult("ran " + s.name)
}

func scopedTestNames(t *testing.T, defs []map[string]interface{}) []string {
	t.Helper()
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		fn, _ := def["function"].(map[string]interface{})
		if fn == nil {
			t.Fatalf("definition without function block: %v", def)
		}
		name, _ := fn["name"].(string)
		names = append(names, name)
	}
	return names
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestScopedRegistry_RegistrationsStayInTheLayer pins the core isolation
// invariant: what is registered on a scoped registry never reaches its base.
func TestScopedRegistry_RegistrationsStayInTheLayer(t *testing.T) {
	base := NewToolRegistry()
	base.Register(&scopedTestTool{name: "read_file", desc: "read"})

	layer := NewScopedRegistry(base)
	if !layer.IsScoped() {
		t.Fatal("NewScopedRegistry must return a scoped registry")
	}
	if base.IsScoped() {
		t.Fatal("the base registry must stay unscoped")
	}

	layer.Register(&scopedTestTool{name: "mcp_fs_read", desc: "remote read"})

	if _, ok := layer.Get("mcp_fs_read"); !ok {
		t.Error("the layer must see its own tool")
	}
	if _, ok := base.Get("mcp_fs_read"); ok {
		t.Error("a layer registration leaked into the base registry")
	}
	if got := base.Count(); got != 1 {
		t.Errorf("base Count() = %d, want 1", got)
	}
	if got := layer.Count(); got != 2 {
		t.Errorf("layer Count() = %d, want 2 (own + inherited)", got)
	}
	// The base tool is still reachable through the layer.
	if _, ok := layer.Get("read_file"); !ok {
		t.Error("the layer must fall back to the base registry")
	}
}

// TestScopedRegistry_LayerShadowsBase verifies the resolution order (own layer
// first) and that unregistering from the layer unmasks — never deletes — the
// base entry.
func TestScopedRegistry_LayerShadowsBase(t *testing.T) {
	base := NewToolRegistry()
	baseTool := &scopedTestTool{name: "exec", desc: "base exec"}
	base.Register(baseTool)

	layer := NewScopedRegistry(base)
	layerTool := &scopedTestTool{name: "exec", desc: "layer exec"}
	layer.Register(layerTool)

	if got, _ := layer.Get("exec"); got != Tool(layerTool) {
		t.Error("the layer's own registration must win over the base one")
	}
	if got := layer.Count(); got != 1 {
		t.Errorf("Count() = %d, want 1 (shadowed base name counted once)", got)
	}

	layer.Unregister("exec")
	if got, ok := layer.Get("exec"); !ok || got != Tool(baseTool) {
		t.Error("unregistering the layer entry must unmask the base entry")
	}
	if _, ok := base.Get("exec"); !ok {
		t.Error("Unregister on a layer must never delete from the base registry")
	}
}

// TestScopedRegistry_DefinitionsMerge checks that the provider-facing views
// (ToProviderDefs / GetDefinitions / GetSummaries) expose the merged set with
// shadowing resolved, in a deterministic (sorted) order.
func TestScopedRegistry_DefinitionsMerge(t *testing.T) {
	base := NewToolRegistry()
	base.Register(&scopedTestTool{name: "web_fetch", desc: "fetch"})
	base.Register(&scopedTestTool{name: "read_file", desc: "read base"})

	layer := NewScopedRegistry(base)
	layer.Register(&scopedTestTool{name: "read_file", desc: "read layer"})
	layer.Register(&scopedTestTool{name: "mcp_fs_read", desc: "remote read"})

	defs := scopedTestNames(t, layer.GetDefinitions())
	if len(defs) != 3 {
		t.Fatalf("GetDefinitions() = %v, want 3 entries", defs)
	}
	// Sorted, and read_file appears exactly once (layer version wins).
	if defs[0] != "mcp_fs_read" || defs[1] != "read_file" || defs[2] != "web_fetch" {
		t.Errorf("GetDefinitions() order = %v, want [mcp_fs_read read_file web_fetch]", defs)
	}

	providerDefs := layer.ToProviderDefs()
	if len(providerDefs) != 3 {
		t.Fatalf("ToProviderDefs() = %d entries, want 3", len(providerDefs))
	}
	descs := make(map[string]string, len(providerDefs))
	for _, def := range providerDefs {
		descs[def.Function.Name] = def.Function.Description
	}
	if descs["read_file"] != "read layer" {
		t.Errorf("read_file description = %q, want the layer's %q", descs["read_file"], "read layer")
	}

	summaries := layer.GetSummaries()
	if len(summaries) != 3 {
		t.Errorf("GetSummaries() = %v, want 3 entries", summaries)
	}

	// The base keeps its own (unpolluted) view.
	if got := scopedTestNames(t, base.GetDefinitions()); containsName(got, "mcp_fs_read") {
		t.Errorf("base GetDefinitions() = %v, must not contain the layer tool", got)
	}
}

// TestScopedRegistry_BaseChangesAreVisibleLive pins that the base is read live,
// not snapshotted: a reload that adds or removes a base tool is reflected in
// every existing layer.
func TestScopedRegistry_BaseChangesAreVisibleLive(t *testing.T) {
	base := NewToolRegistry()
	layer := NewScopedRegistry(base)

	base.Register(&scopedTestTool{name: "cron", desc: "cron"})
	if _, ok := layer.Get("cron"); !ok {
		t.Fatal("a tool added to the base must be visible through the layer")
	}

	base.Unregister("cron")
	if _, ok := layer.Get("cron"); ok {
		t.Error("a tool removed from the base must disappear from the layer")
	}
}

// TestScopedRegistry_ExecuteResolvesThroughLayer makes sure execution follows
// the same resolution as Get (this is the path toolExecutor uses).
func TestScopedRegistry_ExecuteResolvesThroughLayer(t *testing.T) {
	base := NewToolRegistry()
	base.Register(&scopedTestTool{name: "base_tool", desc: "base"})
	layer := NewScopedRegistry(base)
	layer.Register(&scopedTestTool{name: "mcp_x", desc: "remote"})

	res := layer.Execute(context.Background(), "mcp_x", nil)
	if res.IsError || res.ForLLM != "ran mcp_x" {
		t.Errorf("layer tool execution = %+v, want ran mcp_x", res)
	}
	res = layer.Execute(context.Background(), "base_tool", nil)
	if res.IsError || res.ForLLM != "ran base_tool" {
		t.Errorf("base tool execution through the layer = %+v, want ran base_tool", res)
	}
	res = layer.Execute(context.Background(), "ghost", nil)
	if !res.IsError {
		t.Error("an unknown tool must report an error")
	}
}

// TestCloneWithout_FlattensLayers pins that a subagent clone taken from a layer
// is a plain registry with the merged view: it keeps working after the layer is
// gone, and it never shares state back with the layer.
func TestCloneWithout_FlattensLayers(t *testing.T) {
	base := NewToolRegistry()
	base.Register(&scopedTestTool{name: "read_file", desc: "read"})
	base.Register(&scopedTestTool{name: "exec", desc: "exec"})

	layer := NewScopedRegistry(base)
	layer.Register(&scopedTestTool{name: "mcp_fs_read", desc: "remote"})

	clone := layer.CloneWithout("exec")
	if clone.IsScoped() {
		t.Error("a clone must be a plain (flattened) registry")
	}
	if _, ok := clone.Get("read_file"); !ok {
		t.Error("the clone must keep the inherited base tools")
	}
	if _, ok := clone.Get("mcp_fs_read"); !ok {
		t.Error("the clone must keep the layer's own tools")
	}
	if _, ok := clone.Get("exec"); ok {
		t.Error("CloneWithout must honour its exclusion list")
	}

	// Dropping the layer (session reset) must not affect the clone.
	layer.Unregister("mcp_fs_read")
	if _, ok := clone.Get("mcp_fs_read"); !ok {
		t.Error("the flattened clone must not follow later layer changes")
	}
}
