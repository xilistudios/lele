// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// legacyToolCall builds a tool call the way the old message builders left it in
// memory and on disk: Function.Arguments holds whatever json.Marshal produced
// (including the four bytes "null") and the decoded map may be nil.
func legacyToolCall(id, name, arguments string, decoded map[string]any) providers.ToolCall {
	return providers.ToolCall{
		ID:        id,
		Type:      "function",
		Name:      name,
		Function:  &providers.FunctionCall{Name: name, Arguments: arguments},
		Arguments: decoded,
	}
}

// wireArguments returns the arguments value as the provider would receive it.
func wireArguments(t *testing.T, tc providers.ToolCall) string {
	t.Helper()
	out, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal tool call: %v", err)
	}
	var parsed struct {
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse wire: %v (%s)", err, out)
	}
	return parsed.Function.Arguments
}

// A provider that streamed empty tool-call deltas left Arguments nil, so the
// old builder persisted function.arguments:"null". Every later turn replayed it
// and the session was stuck on 400 forever.
func TestHealAssistantToolCalls_RepairsNullArguments(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "run it"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			legacyToolCall("call_1", "exec", "null", nil),
		}},
		{Role: "tool", Content: "ok", ToolCallID: "call_1"},
	}

	healed, changed := healAssistantToolCalls(history)
	if !changed {
		t.Fatal("expected the poisoned session to be reported as changed")
	}
	if len(healed) != 3 {
		t.Fatalf("healing must keep the conversation, got %d messages", len(healed))
	}
	if got := wireArguments(t, healed[1].ToolCalls[0]); got != "{}" {
		t.Fatalf("expected repaired arguments {}, got %s", got)
	}
	if healed[1].ToolCalls[0].FunctionName() != "exec" {
		t.Fatal("healing must preserve the tool name")
	}
	if healed[1].ToolCalls[0].Arguments == nil {
		t.Fatal("healing must populate the decoded map that tools execute from")
	}
}

// A tool call with no name cannot be repaired - there is no tool to name - so
// both it and its tool result must go. Leaving the result behind is an orphan
// tool message, which fails the request with a different 400.
func TestHealAssistantToolCalls_DropsNamelessCallAndOrphanResult(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			legacyToolCall("call_dead", "", "{}", map[string]any{}),
		}},
		{Role: "tool", Content: "result", ToolCallID: "call_dead"},
		{Role: "assistant", Content: "done"},
	}

	healed, changed := healAssistantToolCalls(history)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if len(healed) != 2 {
		t.Fatalf("expected user + final assistant, got %d: %+v", len(healed), healed)
	}
	for _, m := range healed {
		if m.Role == "tool" {
			t.Fatalf("orphaned tool result survived healing: %+v", m)
		}
	}
	if healed[1].Content != "done" {
		t.Fatalf("the last assistant reply was lost: %+v", healed[1])
	}
}

// When a nameless call shares an assistant message with a healthy one, the
// healthy call and its result must survive.
func TestHealAssistantToolCalls_KeepsHealthySiblings(t *testing.T) {
	history := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			legacyToolCall("call_bad", "", "{}", nil),
			legacyToolCall("call_good", "read_file", `{"path":"x"}`, map[string]any{"path": "x"}),
		}},
		{Role: "tool", Content: "a", ToolCallID: "call_bad"},
		{Role: "tool", Content: "b", ToolCallID: "call_good"},
	}

	healed, changed := healAssistantToolCalls(history)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if n := len(healed[0].ToolCalls); n != 1 {
		t.Fatalf("expected 1 surviving tool call, got %d", n)
	}
	if got := healed[0].ToolCalls[0].ID; got != "call_good" {
		t.Fatalf("wrong call survived: %s", got)
	}
	if len(healed) != 2 || healed[1].ToolCallID != "call_good" {
		t.Fatalf("expected only the healthy tool result, got %+v", healed)
	}
}

// A clean session must be reported as unchanged and returned as the same slice,
// otherwise every turn would rewrite (and bump the epoch of) every session it
// loads.
func TestHealAssistantToolCalls_CleanSessionIsNoOp(t *testing.T) {
	clean := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: providers.CanonicalToolCalls([]providers.ToolCall{
			legacyToolCall("call_1", "exec", `{"command":"ls"}`, map[string]any{"command": "ls"}),
		})},
		{Role: "tool", Content: "ok", ToolCallID: "call_1"},
	}

	healed, changed := healAssistantToolCalls(clean)
	if changed {
		t.Fatal("a canonical session must not be reported as changed")
	}
	if &healed[0] != &clean[0] {
		t.Fatal("a canonical session must be returned untouched")
	}
}

// Arguments that are valid JSON but not an object (or malformed) are the other
// shape the provider rejects; they must all heal into a JSON object.
func TestHealAssistantToolCalls_NormalisesNonObjectArguments(t *testing.T) {
	for _, args := range []string{"null", "", "not json", "[1,2]", "123", `"double encoded"`} {
		history := []providers.Message{
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				legacyToolCall("call_1", "exec", args, nil),
			}},
			{Role: "tool", Content: "ok", ToolCallID: "call_1"},
		}
		healed, changed := healAssistantToolCalls(history)
		if !changed {
			t.Fatalf("arguments %q must be reported as needing repair", args)
		}
		got := wireArguments(t, healed[0].ToolCalls[0])
		if !json.Valid([]byte(got)) || !strings.HasPrefix(got, "{") {
			t.Fatalf("arguments %q healed into %q, which is not a JSON object", args, got)
		}
		if healed[0].ToolCalls[0].Arguments == nil {
			t.Fatalf("arguments %q healed without a decoded map", args)
		}
	}
}

// Truncated arguments (cut off by max_tokens) are recoverable and carry real
// intent, so healing keeps what parsed.
func TestHealAssistantToolCalls_RepairsTruncatedArguments(t *testing.T) {
	history := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			legacyToolCall("call_1", "write_file", `{"path":"a.txt","content":"hal`, nil),
		}},
	}

	healed, changed := healAssistantToolCalls(history)
	if !changed {
		t.Fatal("expected changed=true")
	}
	got := wireArguments(t, healed[0].ToolCalls[0])
	if !json.Valid([]byte(got)) {
		t.Fatalf("repaired arguments are not valid JSON: %s", got)
	}
	if healed[0].ToolCalls[0].Arguments["path"] != "a.txt" {
		t.Fatalf("truncation repair lost the parsed keys: %s", got)
	}
}

// Messages without tool calls are not this function's concern; blank assistant
// turns in particular are handled by dropBlankAssistantMessages.
func TestHealAssistantToolCalls_IgnoresPlainMessages(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: ""},
		{Role: "assistant", Content: "hello"},
	}
	healed, changed := healAssistantToolCalls(history)
	if changed {
		t.Fatal("plain messages must not be modified")
	}
	if len(healed) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(healed))
	}
}
