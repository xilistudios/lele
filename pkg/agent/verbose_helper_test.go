package agent

import (
	"strings"
	"testing"
)

// TestFormatBasicToolMessage_UpdateMemory: update_memory must render through
// its own case (mode + content preview) instead of falling back to the
// truncated raw-JSON args preview of the default branch (review finding L4).
func TestFormatBasicToolMessage_UpdateMemory(t *testing.T) {
	got := formatBasicToolMessage("update_memory", map[string]interface{}{
		"content": "persisted the fix\nround",
		"mode":    "append",
	})
	if !strings.HasPrefix(got, "🛠️ Memory: append") {
		t.Errorf("expected '🛠️ Memory: append …' rendering, got: %q", got)
	}
	if strings.Contains(got, "content") {
		t.Errorf("expected no raw-JSON keys in the rendered message, got: %q", got)
	}

	// Default mode when the arg is absent, replace when present.
	def := formatBasicToolMessage("update_memory", map[string]interface{}{
		"content": "x",
	})
	if !strings.Contains(def, "Memory: append") {
		t.Errorf("expected default append mode in: %q", def)
	}
	rep := formatBasicToolMessage("update_memory", map[string]interface{}{
		"content": "x",
		"mode":    "replace",
	})
	if !strings.Contains(rep, "Memory: replace") {
		t.Errorf("expected replace mode in: %q", rep)
	}

	// Long/multi-line content is sanitized and clamped to MaxBasicMessageSize.
	long := formatBasicToolMessage("update_memory", map[string]interface{}{
		"content": strings.Repeat("y", 200),
		"mode":    "append",
	})
	if len(long) > MaxBasicMessageSize {
		t.Errorf("message length %d exceeds MaxBasicMessageSize %d", len(long), MaxBasicMessageSize)
	}
}
