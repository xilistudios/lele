package providers

import (
	"strings"
	"testing"
)

func TestBuildCLIToolsPrompt(t *testing.T) {
	t.Run("headers present with no tools", func(t *testing.T) {
		got := buildCLIToolsPrompt(nil)
		if !strings.Contains(got, "## Available Tools") {
			t.Errorf("expected header, got: %q", got)
		}
		if !strings.Contains(got, "### Tool Definitions:") {
			t.Errorf("expected tool definitions header, got: %q", got)
		}
	})

	t.Run("formats function tools and skips non-function", func(t *testing.T) {
		tools := []ToolDefinition{
			{
				Type: "function",
				Function: ToolFunctionDefinition{
					Name:        "get_weather",
					Description: "Get weather",
					Parameters:  map[string]any{"type": "object"},
				},
			},
			{Type: "not-function", Function: ToolFunctionDefinition{Name: "ignored"}},
			{
				Type: "function",
				Function: ToolFunctionDefinition{
					Name: "no_params",
				},
			},
		}
		got := buildCLIToolsPrompt(tools)
		if !strings.Contains(got, "#### get_weather") {
			t.Errorf("expected get_weather section, got: %q", got)
		}
		if !strings.Contains(got, "Description: Get weather") {
			t.Errorf("expected description, got: %q", got)
		}
		if strings.Contains(got, "ignored") {
			t.Errorf("non-function tool should be skipped, got: %q", got)
		}
		if !strings.Contains(got, "#### no_params") {
			t.Errorf("expected no_params section, got: %q", got)
		}
		// Parameters JSON should appear for get_weather.
		if !strings.Contains(got, `"type":"object"`) {
			t.Errorf("expected parameters JSON, got: %q", got)
		}
	})
}

func TestNormalizeToolCall(t *testing.T) {
	t.Run("name from Function when Name empty", func(t *testing.T) {
		tc := ToolCall{ID: "c1", Function: &FunctionCall{Name: "func_a", Arguments: `{"x":1}`}}
		got := NormalizeToolCall(tc)
		if got.Name != "func_a" {
			t.Errorf("Name = %q, want func_a", got.Name)
		}
		if got.Function.Name != "func_a" {
			t.Errorf("Function.Name = %q, want func_a", got.Function.Name)
		}
		if got.Arguments["x"] != 1.0 {
			t.Errorf("Arguments[x] = %v, want 1.0", got.Arguments["x"])
		}
	})

	t.Run("nil Arguments defaults to empty map", func(t *testing.T) {
		tc := ToolCall{ID: "c2", Name: "func_b"}
		got := NormalizeToolCall(tc)
		if got.Arguments == nil {
			t.Error("Arguments should not be nil")
		}
		if len(got.Arguments) != 0 {
			t.Errorf("Arguments = %v, want empty", got.Arguments)
		}
		if got.Function == nil {
			t.Fatal("Function should be populated")
		}
	})

	t.Run("Function populated when nil with args JSON", func(t *testing.T) {
		tc := ToolCall{ID: "c3", Name: "func_c", Arguments: map[string]any{"y": 2}}
		got := NormalizeToolCall(tc)
		if got.Function == nil {
			t.Fatal("Function should be created")
		}
		if got.Function.Arguments == "" {
			t.Errorf("Function.Arguments should be set, got empty")
		}
		if got.Function.Name != "func_c" {
			t.Errorf("Function.Name = %q, want func_c", got.Function.Name)
		}
	})

	t.Run("Function name populated from top-level name", func(t *testing.T) {
		tc := ToolCall{ID: "c4", Name: "func_d", Function: &FunctionCall{Arguments: `{"z":3}`}}
		got := NormalizeToolCall(tc)
		if got.Function.Name != "func_d" {
			t.Errorf("Function.Name = %q, want func_d", got.Function.Name)
		}
		// Arguments already parsed from Function.Arguments
		if got.Arguments["z"] != 3.0 {
			t.Errorf("Arguments[z] = %v, want 3.0", got.Arguments["z"])
		}
	})

	t.Run("unparseable function args leaves empty arguments", func(t *testing.T) {
		tc := ToolCall{ID: "c5", Name: "func_e", Function: &FunctionCall{Arguments: `not-json`}}
		got := NormalizeToolCall(tc)
		if len(got.Arguments) != 0 {
			t.Errorf("Arguments = %v, want empty", got.Arguments)
		}
	})

	t.Run("fully populated tool call unchanged", func(t *testing.T) {
		tc := ToolCall{
			ID:        "c6",
			Name:      "func_f",
			Arguments: map[string]any{"a": 1},
			Function:  &FunctionCall{Name: "func_f", Arguments: `{"a":1}`},
		}
		got := NormalizeToolCall(tc)
		if got.ID != "c6" || got.Name != "func_f" {
			t.Errorf("got %#v, want unchanged", got)
		}
		if got.Arguments["a"] != 1 {
			t.Errorf("Arguments[a] = %v, want 1", got.Arguments["a"])
		}
	})
}