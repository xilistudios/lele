package providers

import (
	"strings"
	"testing"
)

// These tests cover the text-embedded tool-call helpers in
// tool_call_extract.go. They previously lived in claude_cli_provider_test.go;
// the helpers outlived that provider (CodexCliProvider still uses them), so the
// coverage moved with the code.

// --- extractToolCallsFromText tests ---

func TestExtractToolCalls_NoToolCalls(t *testing.T) {
	got := extractToolCallsFromText("Just a regular response.")
	if len(got) != 0 {
		t.Errorf("extractToolCallsFromText() = %d, want 0", len(got))
	}
}

func TestExtractToolCalls_WithToolCalls(t *testing.T) {
	text := `Here's the result:
{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]}`

	got := extractToolCallsFromText(text)
	if len(got) != 1 {
		t.Fatalf("extractToolCallsFromText() = %d, want 1", len(got))
	}
	if got[0].ID != "call_1" {
		t.Errorf("ID = %q, want %q", got[0].ID, "call_1")
	}
	if got[0].Name != "test" {
		t.Errorf("Name = %q, want %q", got[0].Name, "test")
	}
	if got[0].Type != "function" {
		t.Errorf("Type = %q, want %q", got[0].Type, "function")
	}
}

func TestExtractToolCalls_InvalidJSON(t *testing.T) {
	got := extractToolCallsFromText(`{"tool_calls":invalid}`)
	if len(got) != 0 {
		t.Errorf("extractToolCallsFromText() with invalid JSON = %d, want 0", len(got))
	}
}

func TestExtractToolCalls_MultipleToolCalls(t *testing.T) {
	text := `{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/test\"}"}},{"id":"call_2","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"/tmp/out\",\"content\":\"hello\"}"}}]}`

	got := extractToolCallsFromText(text)
	if len(got) != 2 {
		t.Fatalf("extractToolCallsFromText() = %d, want 2", len(got))
	}
	if got[0].Name != "read_file" {
		t.Errorf("[0].Name = %q, want %q", got[0].Name, "read_file")
	}
	if got[1].Name != "write_file" {
		t.Errorf("[1].Name = %q, want %q", got[1].Name, "write_file")
	}
	// Verify arguments were parsed
	if got[0].Arguments["path"] != "/tmp/test" {
		t.Errorf("[0].Arguments[path] = %v, want /tmp/test", got[0].Arguments["path"])
	}
	if got[1].Arguments["content"] != "hello" {
		t.Errorf("[1].Arguments[content] = %v, want hello", got[1].Arguments["content"])
	}
}

func TestExtractToolCalls_UnmatchedBrace(t *testing.T) {
	got := extractToolCallsFromText(`{"tool_calls":[{"id":"call_1"`)
	if len(got) != 0 {
		t.Errorf("extractToolCallsFromText() with unmatched brace = %d, want 0", len(got))
	}
}

func TestExtractToolCalls_ToolCallArgumentsParsing(t *testing.T) {
	text := `{"tool_calls":[{"id":"c1","type":"function","function":{"name":"fn","arguments":"{\"num\":42,\"flag\":true,\"name\":\"test\"}"}}]}`

	got := extractToolCallsFromText(text)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	// Verify different argument types
	if got[0].Arguments["num"] != float64(42) {
		t.Errorf("Arguments[num] = %v (%T), want 42", got[0].Arguments["num"], got[0].Arguments["num"])
	}
	if got[0].Arguments["flag"] != true {
		t.Errorf("Arguments[flag] = %v, want true", got[0].Arguments["flag"])
	}
	if got[0].Arguments["name"] != "test" {
		t.Errorf("Arguments[name] = %v, want test", got[0].Arguments["name"])
	}
	// Verify raw arguments string is preserved in FunctionCall
	if got[0].Function.Arguments == "" {
		t.Error("Function.Arguments should contain raw JSON string")
	}
}

// --- stripToolCallsFromText tests ---

func TestStripToolCallsJSON(t *testing.T) {
	text := `Let me check the weather.
{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"test","arguments":"{}"}}]}
Done.`

	got := stripToolCallsFromText(text)
	if strings.Contains(got, "tool_calls") {
		t.Errorf("should remove tool_calls JSON, got %q", got)
	}
	if !strings.Contains(got, "Let me check the weather.") {
		t.Errorf("should keep text before, got %q", got)
	}
	if !strings.Contains(got, "Done.") {
		t.Errorf("should keep text after, got %q", got)
	}
}

func TestStripToolCallsJSON_NoToolCalls(t *testing.T) {
	text := "Just regular text."
	got := stripToolCallsFromText(text)
	if got != text {
		t.Errorf("stripToolCallsFromText() = %q, want %q", got, text)
	}
}

func TestStripToolCallsJSON_OnlyToolCalls(t *testing.T) {
	text := `{"tool_calls":[{"id":"c1","type":"function","function":{"name":"fn","arguments":"{}"}}]}`
	got := stripToolCallsFromText(text)
	if got != "" {
		t.Errorf("stripToolCallsFromText() = %q, want empty", got)
	}
}

// --- findMatchingBrace tests ---

func TestFindMatchingBrace(t *testing.T) {
	tests := []struct {
		text string
		pos  int
		want int
	}{
		{`{"a":1}`, 0, 7},
		{`{"a":{"b":2}}`, 0, 13},
		{`text {"a":1} more`, 5, 12},
		{`{unclosed`, 0, 0},      // no match returns pos
		{`{}`, 0, 2},             // empty object
		{`{{{}}}`, 0, 6},         // deeply nested
		{`{"a":"b{c}d"}`, 0, 13}, // braces in strings (ignored, string-aware)

		// Regression: braces inside JSON string values must not affect brace matching
		{`{"tool_calls":[{"id":"c1","type":"function","function":{"name":"exec","arguments":"{\"command\":\"echo }\"}"}}]}`, 0, 112}, // escaped } in arguments
		{`{"a":"unbalanced { brace"}`, 0, 26},     // unbalanced { in string
		{`{"a":"escaped quote \" and }"}`, 0, 30}, // escaped quote + } in string
	}
	for _, tt := range tests {
		got := findMatchingBrace(tt.text, tt.pos)
		if got != tt.want {
			t.Errorf("findMatchingBrace(%q, %d) = %d, want %d", tt.text, tt.pos, got, tt.want)
		}
	}
}
