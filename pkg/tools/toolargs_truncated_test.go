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
	if !strings.Contains(res.ForLLM, "content, path") {
		t.Fatalf("surviving keys missing or unordered: %q", res.ForLLM)
	}
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

func TestTruncatedArgumentsError_TellsModelHowToRecover(t *testing.T) {
	res := TruncatedArgumentsError(providers.ToolCall{Name: "write_file"})
	for _, want := range []string{"cut off", "output token limit", "Do not re-send", "append"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("message does not mention %q: %q", want, res.ForLLM)
		}
	}
}
