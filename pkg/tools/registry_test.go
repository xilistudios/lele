package tools

import (
	"context"
	"reflect"
	"testing"
)

type registryTestTool struct {
	name        string
	description string
}

func (t *registryTestTool) Name() string {
	return t.name
}

func (t *registryTestTool) Description() string {
	return t.description
}

func (t *registryTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
	}
}

func (t *registryTestTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "ok"}
}

func TestToolRegistry_ToProviderDefs_SortsByToolName(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "zeta", description: "z tool"})
	r.Register(&registryTestTool{name: "alpha", description: "a tool"})
	r.Register(&registryTestTool{name: "beta", description: "b tool"})

	defs := r.ToProviderDefs()
	if len(defs) != 3 {
		t.Fatalf("len(defs) = %d, want 3", len(defs))
	}

	got := []string{defs[0].Function.Name, defs[1].Function.Name, defs[2].Function.Name}
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool order = %v, want %v", got, want)
	}
}

func TestToolRegistry_GetSummaries_SortsByToolName(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "zeta", description: "z tool"})
	r.Register(&registryTestTool{name: "alpha", description: "a tool"})
	r.Register(&registryTestTool{name: "beta", description: "b tool"})

	got := r.GetSummaries()
	want := []string{
		"- `alpha` - a tool",
		"- `beta` - b tool",
		"- `zeta` - z tool",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summaries = %v, want %v", got, want)
	}
}

func TestToolRegistry_GetDefinitions_SortsByToolName(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&registryTestTool{name: "zeta", description: "z tool"})
	r.Register(&registryTestTool{name: "alpha", description: "a tool"})
	r.Register(&registryTestTool{name: "beta", description: "b tool"})

	defs := r.GetDefinitions()
	if len(defs) != 3 {
		t.Fatalf("len(defs) = %d, want 3", len(defs))
	}

	got := make([]string, 0, len(defs))
	for _, def := range defs {
		fn, ok := def["function"].(map[string]interface{})
		if !ok {
			t.Fatalf("function schema type = %T", def["function"])
		}
		name, ok := fn["name"].(string)
		if !ok {
			t.Fatalf("function name type = %T", fn["name"])
		}
		got = append(got, name)
	}

	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("definition order = %v, want %v", got, want)
	}
}
