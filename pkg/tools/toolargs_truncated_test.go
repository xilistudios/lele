// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tools

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestTruncatedArgumentsError_ListsSurvivingKeys(t *testing.T) {
	res := TruncatedArgumentsError(providers.ToolCall{
		Name:      "write_file",
		Arguments: map[string]any{"path": "/tmp/a", "content": "partial"},
	})
	if res == nil || !res.IsError {
		t.Fatalf("expected an error result, got %#v", res)
	}
	// Sorted so the message is stable and comparable across runs.
	if !strings.Contains(res.ForLLM, "content, path") {
		t.Fatalf("surviving keys missing or unordered: %q", res.ForLLM)
	}
	// Only the key names belong in the message: the partial value is what got
	// cut, echoing it would invite the model to treat it as complete.
	if strings.Contains(res.ForLLM, "partial") {
		t.Fatalf("the truncated value was echoed back: %q", res.ForLLM)
	}
}

func TestTruncatedArgumentsError_NoSurvivingKeys(t *testing.T) {
	res := TruncatedArgumentsError(providers.ToolCall{Name: "exec"})
	if !strings.Contains(res.ForLLM, "none") {
		t.Fatalf("expected the key list to say none, got %q", res.ForLLM)
	}
}

// Providers disagree on where the name lives (top level vs. function.name), and
// a nameless message would leave the model unable to tell which call to retry.
func TestTruncatedArgumentsError_FallsBackToFunctionName(t *testing.T) {
	res := TruncatedArgumentsError(providers.ToolCall{
		Function: &providers.FunctionCall{Name: "append_file"},
	})
	if !strings.Contains(res.ForLLM, "append_file") {
		t.Fatalf("function name not used: %q", res.ForLLM)
	}
}

func TestTruncatedArgumentsError_UnknownNameDoesNotPanic(t *testing.T) {
	res := TruncatedArgumentsError(providers.ToolCall{})
	if !strings.Contains(res.ForLLM, "unknown") {
		t.Fatalf("expected a name placeholder, got %q", res.ForLLM)
	}
}

// The message has to carry the actionable part: chunk the payload. Without it
// the model re-sends the same call and is cut off in the same place.
func TestTruncatedArgumentsError_TellsModelHowToRecover(t *testing.T) {
	res := TruncatedArgumentsError(providers.ToolCall{Name: "write_file"})
	for _, want := range []string{"cut off", "output token limit", "Do not re-send", "append"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("message does not mention %q: %q", want, res.ForLLM)
		}
	}
}
