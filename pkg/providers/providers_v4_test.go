package providers

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestNormalizeToolCall_FunctionNameAndArgumentsBranches covers additional
// branches in NormalizeToolCall:
//   - Function non-nil with empty top-level Name -> Name copied from Function (line 81-83)
//   - Function non-nil with empty Function.Arguments -> serialized from Arguments (line 84-86)
func TestNormalizeToolCall_FunctionNameAndArgumentsBranches(t *testing.T) {
	t.Run("top-level name sync from function when empty", func(t *testing.T) {
		tc := ToolCall{
			ID: "a1",
			Function: &FunctionCall{
				Name:      "sync_me",
				Arguments: `{"k":1}`,
			},
		}
		got := NormalizeToolCall(tc)
		if got.Name != "sync_me" {
			t.Errorf("Name = %q, want sync_me", got.Name)
		}
	})

	t.Run("function arguments filled from serialized args", func(t *testing.T) {
		tc := ToolCall{
			ID:        "a2",
			Name:      "filled_args",
			Arguments: map[string]any{"z": 9},
			Function: &FunctionCall{
				Name: "filled_args",
			},
		}
		got := NormalizeToolCall(tc)
		if got.Function.Arguments == "" {
			t.Error("Function.Arguments should be populated from serialized args")
		}
	})
}

// TestExtractToolCallsFromText_InvalidArgJSON covers the branch in
// extractToolCallsFromText where a tool call's arguments string is not valid
// JSON. The parsed arguments are dropped (nil) while the call is kept.
func TestExtractToolCallsFromText_InvalidArgJSON(t *testing.T) {
	text := `Here is a tool call: {"tool_calls":[{"id":"c1","type":"function","function":{"name":"inspect","arguments":"not-json"}}]} tail`
	calls := extractToolCallsFromText(text)
	if len(calls) != 1 {
		t.Fatalf("extractToolCallsFromText returned %d calls, want 1", len(calls))
	}
	if calls[0].Name != "inspect" {
		t.Errorf("Name = %q, want inspect", calls[0].Name)
	}
	if calls[0].Arguments != nil {
		t.Errorf("Arguments = %#v, want nil for unparseable JSON", calls[0].Arguments)
	}
	if calls[0].Function == nil || calls[0].Function.Arguments != "not-json" {
		t.Errorf("Function.Arguments should keep original, got %#v", calls[0].Function)
	}
}

// TestExtractToolCallsFromText_NoToolCalls covers the early return when no
// tool_calls JSON marker is present.
func TestExtractToolCallsFromText_NoToolCalls(t *testing.T) {
	if calls := extractToolCallsFromText("just a plain reply"); calls != nil {
		t.Errorf("extractToolCallsFromText = %#v, want nil", calls)
	}
}

// TestStripToolCallsFromTextCovers the strip function's branches:
//   - no tool_calls marker -> unchanged text
//   - tool_calls present -> text collapsed around the JSON
func TestStripToolCallsFromText(t *testing.T) {
	if got := stripToolCallsFromText("plain reply"); got != "plain reply" {
		t.Errorf("no-marker strip = %q, want unchanged", got)
	}

	text := `pre {"tool_calls":[{"id":"c1"}]} post`
	got := stripToolCallsFromText(text)
	// The JSON is removed; pre and post text (trimmed) surround the gap.
	if got != "pre  post" {
		t.Errorf("strip = %q, want %q", got, "pre  post")
	}
}

// TestNormalizeToolCall_BothNamesEmpty covers the branch in the else block
// where both Function.Name and top-level Name are empty and Function.Arguments
// is empty, so all three fill-in branches run.
func TestNormalizeToolCall_EmptyEverything(t *testing.T) {
	tc := ToolCall{
		ID: "a3",
		Function: &FunctionCall{
			Arguments: `{"q":5}`,
		},
	}
	// Name is empty, Function.Name is empty -> no copy.
	got := NormalizeToolCall(tc)
	if got.Name != "" {
		t.Errorf("Name = %q, want empty", got.Name)
	}
	if got.Function.Name != "" {
		t.Errorf("Function.Name = %q, want empty", got.Function.Name)
	}
	if got.Function.Arguments == "" {
		t.Error("Function.Arguments should be serialized from parsed Arguments")
	}
}// TestSelectionFromNamedProvider_UncoveredBranches covers additional branches
// in selectionFromNamedProvider that were not yet exercised:
//   - anthropic named with oauth/token AuthMethod and empty API base -> ClaudeAuth + default base
//   - claude-cli named type with empty workspace path -> workspace "."
//   - codex-cli named type with empty workspace path -> workspace "."
func TestSelectionFromNamedProvider_UncoveredBranches(t *testing.T) {
	t.Run("anthropic named oauth with defaulted base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		named := config.NamedProviderConfig{
			Type:           "claude",
			ProviderConfig: config.ProviderConfig{AuthMethod: "oauth"},
		}
		sel, err := selectionFromNamedProvider(cfg, "claude", "claude-opus", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeAuth {
			t.Errorf("providerType = %v, want ClaudeAuth", sel.providerType)
		}
		if sel.apiBase != defaultAnthropicAPIBase {
			t.Errorf("apiBase = %q, want %q", sel.apiBase, defaultAnthropicAPIBase)
		}
	})

	t.Run("claude-cli named type with empty workspace", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = ""
		named := config.NamedProviderConfig{Type: "claude-cli"}
		sel, err := selectionFromNamedProvider(cfg, "claude-cli", "", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeCLI {
			t.Errorf("providerType = %v, want ClaudeCLI", sel.providerType)
		}
		// When Workspace is empty, WorkspacePath() returns the default lele workspace dir
		if sel.workspace == "" {
			t.Error("workspace should not be empty")
		}
	})

	t.Run("codex-cli named type with empty workspace", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = ""
		named := config.NamedProviderConfig{Type: "codex-cli"}
		sel, err := selectionFromNamedProvider(cfg, "codex-cli", "", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLI {
			t.Errorf("providerType = %v, want CodexCLI", sel.providerType)
		}
		// When Workspace is empty, WorkspacePath() returns the default lele workspace dir
		if sel.workspace == "" {
			t.Error("workspace should not be empty")
		}
	})
}