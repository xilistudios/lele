package channels

import (
	"encoding/json"
	"testing"
)

func TestMarshalWithID(t *testing.T) {
	raw := marshalWithID("test.event", map[string]interface{}{"k": "v"}, "evt-1")
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Version != WSProtocolVersion {
		t.Errorf("version = %q", msg.Version)
	}
	if msg.Event != "test.event" {
		t.Errorf("event = %q", msg.Event)
	}
	if msg.ID != "evt-1" {
		t.Errorf("id = %q", msg.ID)
	}
	var inner map[string]interface{}
	if err := json.Unmarshal(msg.Data, &inner); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if inner["k"] != "v" {
		t.Errorf("data = %v", inner)
	}
}

func TestMarshalWithID_EmptyID(t *testing.T) {
	raw := marshalWithID("e", struct{}{}, "")
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.ID != "" {
		t.Errorf("id = %q, want empty", msg.ID)
	}
}

func TestBoolToString(t *testing.T) {
	if boolToString(true) != "true" {
		t.Errorf("true = %q", boolToString(true))
	}
	if boolToString(false) != "false" {
		t.Errorf("false = %q", boolToString(false))
	}
}

func TestSessionKeyMatches(t *testing.T) {
	if !sessionKeyMatches("abc", "abc") {
		t.Error("identical keys should match")
	}
	if sessionKeyMatches("abc", "123") {
		t.Error("different plain keys should not match")
	}
	if !sessionKeyMatches("native:abc", "abc") {
		t.Error("native-prefixed should match plain")
	}
	if !sessionKeyMatches("abc", "native:abc") {
		t.Error("plain should match native-prefixed")
	}
	if !sessionKeyMatches("native:abc", "native:abc") {
		t.Error("both native should match")
	}
	if sessionKeyMatches("native:foo", "foo:bar") {
		t.Error("different key after strip should not match")
	}
}

func TestIsValidSessionKeyFormat(t *testing.T) {
	valid := []string{
		"abc", "abc123", "client-id_x.y", "native:abc", "native:abc:xyz",
		"subagent:subagent-123", "subagent:subagent-42", " ",
		"native:prefix:another", "UPPER_case-01",
	}
	// " " is valid (space is >= 32 and <=126, not empty after TrimSpace? No—
	// TrimSpace makes it empty so it is actually invalid). Remove it.
	for _, s := range valid {
		if s == " " {
			continue
		}
		if !isValidSessionKeyFormat(s) {
			t.Errorf("expected valid: %q", s)
		}
	}

	invalid := []string{
		"", "   ", "a..b", "a/b", "a\\b", "http://x",
		"subagent:task-abc", "subagent:subagent-", "subagent:subagent-abc",
		"native:", "native::suffix", "native:abc::",
		string(make([]byte, 257)), // > 256
		"with\x00null", "with\nnewline",
	}
	for _, s := range invalid {
		if isValidSessionKeyFormat(s) {
			t.Errorf("expected invalid (got valid): %q (len=%d)", s, len(s))
		}
	}
}
