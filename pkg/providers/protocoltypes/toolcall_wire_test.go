// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- regression: the exact production failure -------------------------------

// TestToolCallWire_NilArgumentsNeverEmitsNull reproduces the production bug:
// a provider that streamed empty tool-call deltas leaves Arguments nil, and
// json.Marshal(nil map) produced the string "null" for function.arguments.
// The upstream then answered 400 invalid_parameter_error:
//
//	The "function.arguments" parameter of the code model must be in JSON format.
//
// and because the assistant message was persisted, every later turn replayed
// it and the agent was permanently stuck.
func TestToolCallWire_NilArgumentsNeverEmitsNull(t *testing.T) {
	tc := ToolCall{ID: "call_deadbeef", Type: "function"} // nil Name, nil Arguments

	msg := Message{Role: "assistant", ToolCalls: []ToolCall{tc}}
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(out)

	if strings.Contains(wire, `"arguments":"null"`) {
		t.Fatalf("wire still carries arguments:\"null\": %s", wire)
	}
	if strings.Contains(wire, `"arguments":null`) {
		t.Fatalf("wire still carries a null arguments value: %s", wire)
	}

	var decoded struct {
		ToolCalls []struct {
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	if got := decoded.ToolCalls[0].Function.Arguments; got != "{}" {
		t.Fatalf("function.arguments = %q, want %q", got, "{}")
	}
}

// TestToolCallWire_NoDuplicateTopLevelArguments guards the second half of the
// bug: the wire used to carry function.arguments (a string) AND a top-level
// arguments (an object) for the same call. Strict gateways reject that and it
// doubled the token cost of every replayed turn.
func TestToolCallWire_NoDuplicateTopLevelArguments(t *testing.T) {
	tc := ToolCall{
		ID:        "call_1",
		Type:      "function",
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "ls"},
		Function:  &FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`},
	}
	out, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(out, &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, dup := envelope["arguments"]; dup {
		t.Fatalf("tool call wire carries a duplicate top-level arguments: %s", out)
	}
	if _, ok := envelope["function"]; !ok {
		t.Fatalf("tool call wire lost function: %s", out)
	}
	if _, ok := envelope["name"]; ok {
		t.Fatalf("tool call wire carries a duplicate top-level name: %s", out)
	}
}

// TestToolCallWire_CanonicalShapeStable checks the whole emitted shape, so a
// future field addition cannot silently change the wire again.
func TestToolCallWire_CanonicalShapeStable(t *testing.T) {
	tc := ToolCall{
		ID:        "call_1",
		Type:      "function",
		Name:      "list_dir",
		Arguments: map[string]interface{}{"path": "/tmp"},
	}
	out, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"id":"call_1","type":"function","function":{"name":"list_dir","arguments":"{\"path\":\"/tmp\"}"}}`
	if string(out) != want {
		t.Fatalf("wire = %s\nwant %s", out, want)
	}
}

// --- round trip -------------------------------------------------------------

// TestToolCallRoundTrip_KeepsDecodedMap is what protects every non-OpenAI
// provider (Anthropic, Bedrock, Codex, Antigravity) plus tool execution: they
// read tc.Arguments, so unmarshalling must rehydrate it from function.arguments.
func TestToolCallRoundTrip_KeepsDecodedMap(t *testing.T) {
	original := ToolCall{
		ID:        "call_1",
		Type:      "function",
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "ls -la", "timeout": float64(30)},
	}
	original.Function = &FunctionCall{Name: "exec", Arguments: `{"command":"ls -la","timeout":30}`}

	out, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back ToolCall
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if back.Name != "exec" {
		t.Fatalf("Name = %q, want exec", back.Name)
	}
	if back.Function == nil || back.Function.Name != "exec" {
		t.Fatalf("Function lost after round trip: %+v", back.Function)
	}
	if back.Arguments == nil || back.Arguments["command"] != "ls -la" {
		t.Fatalf("decoded Arguments not rehydrated: %#v", back.Arguments)
	}
}

// TestMessageRoundTrip_WithToolCalls covers the container: Message has its own
// MarshalJSON, so the nested canonicalization must survive it.
func TestMessageRoundTrip_WithToolCalls(t *testing.T) {
	msg := Message{
		Role:    "assistant",
		Content: "checking",
		ToolCalls: CanonicalToolCalls([]ToolCall{{
			ID:        "call_1",
			Name:      "read_file",
			Arguments: map[string]interface{}{"path": "/etc/hosts"},
		}}),
	}
	out, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.ToolCalls) != 1 {
		t.Fatalf("tool calls lost: %+v", back.ToolCalls)
	}
	tc := back.ToolCalls[0]
	if tc.Name != "read_file" || tc.Function == nil || tc.Function.Name != "read_file" {
		t.Fatalf("name not round-tripped: %#v", tc)
	}
	if tc.Arguments["path"] != "/etc/hosts" {
		t.Fatalf("arguments not round-tripped: %#v", tc.Arguments)
	}
	if tc.Function.Arguments != `{"path":"/etc/hosts"}` {
		t.Fatalf("canonical arguments wrong: %q", tc.Function.Arguments)
	}
}

// --- legacy stored shapes ---------------------------------------------------

// TestToolCallUnmarshal_LegacyShapes checks sessions already on disk. They hold
// a mix of shapes written by older code; all of them must load into something
// whose re-marshalled function.arguments is valid JSON.
func TestToolCallUnmarshal_LegacyShapes(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string // expected function.arguments after canonicalization
	}{
		{
			name: "object top-level only (no function)",
			json: `{"id":"c1","name":"exec","arguments":{"command":"ls"}}`,
			want: `{"command":"ls"}`,
		},
		{
			name: "string function.arguments (canonical)",
			json: `{"id":"c1","function":{"name":"exec","arguments":"{\"command\":\"ls\"}"}}`,
			want: `{"command":"ls"}`,
		},
		{
			name: "duplicate: string function + object top-level",
			json: `{"id":"c1","type":"function","function":{"name":"exec","arguments":"{\"command\":\"ls\"}"},"name":"exec","arguments":{"command":"ls"}}`,
			want: `{"command":"ls"}`,
		},
		{
			name: "poisoned: function.arguments literal null",
			json: `{"id":"c1","function":{"name":"exec","arguments":"null"}}`,
			want: `{}`,
		},
		{
			name: "poisoned: function.arguments empty string",
			json: `{"id":"c1","function":{"name":"exec","arguments":""}}`,
			want: `{}`,
		},
		{
			name: "double-encoded arguments string",
			json: `{"id":"c1","function":{"name":"exec","arguments":"\"{\\\"command\\\":\\\"ls\\\"}\""}}`,
			want: `{"command":"ls"}`,
		},
		{
			name: "top-level arguments is JSON null",
			json: `{"id":"c1","name":"exec","arguments":null}`,
			want: `{}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var call ToolCall
			if err := json.Unmarshal([]byte(tc.json), &call); err != nil {
				t.Fatalf("unmarshal %s: %v", tc.json, err)
			}
			if call.Function == nil {
				t.Fatalf("Function is nil after loading %s", tc.json)
			}
			if call.Function.Arguments != tc.want {
				t.Fatalf("function.arguments = %q, want %q", call.Function.Arguments, tc.want)
			}
			if !json.Valid([]byte(call.Function.Arguments)) {
				t.Fatalf("function.arguments is not valid JSON: %q", call.Function.Arguments)
			}
			// The decoded map must agree with the canonical string, otherwise
			// tool execution and non-OpenAI providers see something else.
			if call.Arguments == nil {
				t.Fatalf("decoded Arguments map is nil for %s", tc.json)
			}
			// And re-marshalling must be clean: no duplicate, no null.
			out, err := json.Marshal(call)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			s := string(out)
			if strings.Contains(s, `"arguments":null`) || strings.Contains(s, `"arguments":"null"`) {
				t.Fatalf("re-marshalled wire is poisoned again: %s", s)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(out, &envelope); err != nil {
				t.Fatalf("unmarshal re-marshalled: %v", err)
			}
			if _, dup := envelope["arguments"]; dup {
				t.Fatalf("re-marshalled wire carries duplicate top-level arguments: %s", s)
			}
		})
	}
}

// --- CanonicalToolCalls ------------------------------------------------------

func TestCanonicalToolCalls_DropsNamelessEntries(t *testing.T) {
	// A provider that streamed a tool-call delta with only an index produces an
	// empty shell. It cannot be executed and replaying it breaks the request.
	calls := []ToolCall{
		{ID: "call_bad"},
		{ID: "call_ok", Name: "exec", Arguments: map[string]interface{}{"command": "ls"}},
		{ID: "call_blank", Name: "   "},
	}
	got := CanonicalToolCalls(calls)
	if len(got) != 1 {
		t.Fatalf("expected only the named call to survive, got %d: %#v", len(got), got)
	}
	if got[0].ID != "call_ok" {
		t.Fatalf("survivor = %q, want call_ok", got[0].ID)
	}
}

func TestCanonicalToolCalls_ReturnsNilWhenEmpty(t *testing.T) {
	if got := CanonicalToolCalls(nil); got != nil {
		t.Fatalf("CanonicalToolCalls(nil) = %#v, want nil", got)
	}
	if got := CanonicalToolCalls([]ToolCall{{ID: "only_nameless"}}); got != nil {
		t.Fatalf("all-nameless input must yield nil (so omitempty drops the field), got %#v", got)
	}
}

func TestCanonicalToolCalls_PopulatesBothFields(t *testing.T) {
	got := CanonicalToolCalls([]ToolCall{{
		ID:       "c1",
		Function: &FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`},
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	tc := got[0]
	if tc.Name != "exec" {
		t.Fatalf("Name = %q, want exec (must be usable by name-only consumers)", tc.Name)
	}
	if tc.Type != "function" {
		t.Fatalf("Type = %q, want function", tc.Type)
	}
	if tc.Function == nil || tc.Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("Function.Arguments = %#v", tc.Function)
	}
	if tc.Arguments == nil || tc.Arguments["command"] != "ls" {
		t.Fatalf("decoded Arguments = %#v, want command=ls", tc.Arguments)
	}
}

func TestCanonicalToolCalls_RepairsTruncatedArguments(t *testing.T) {
	// max_tokens can cut a large argument payload mid-write. The old code
	// forwarded the broken string verbatim and the provider rejected the turn.
	got := CanonicalToolCalls([]ToolCall{{
		ID:       "c1",
		Function: &FunctionCall{Name: "write_file", Arguments: `{"path":"/tmp/a","content":"hello`},
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	args := got[0].Function.Arguments
	if !json.Valid([]byte(args)) {
		t.Fatalf("truncated arguments were not repaired: %q", args)
	}
	if !strings.HasPrefix(args, "{") {
		t.Fatalf("repaired arguments are not an object: %q", args)
	}
}

func TestCanonicalToolCalls_PreservesThoughtSignature(t *testing.T) {
	// Gemini-style thought_signature must survive canonicalization; dropping it
	// silently would break multi-turn tool calls on that provider.
	got := CanonicalToolCalls([]ToolCall{{
		ID:               "c1",
		Name:             "exec",
		Function:         &FunctionCall{Name: "exec", Arguments: "{}", ThoughtSignature: "sig123"},
		ThoughtSignature: "sig123",
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if got[0].ThoughtSignature != "sig123" || got[0].Function.ThoughtSignature != "sig123" {
		t.Fatalf("thought_signature lost: %#v", got[0])
	}
}

// --- helpers ---------------------------------------------------------------

func TestNormalizeArgumentsJSON(t *testing.T) {
	cases := map[string]string{
		"":            "{}",
		"   ":         "{}",
		"null":        "{}",
		`"null"`:      "{}",
		`{"a":1}`:     `{"a":1}`,
		` {"a":1} `:   `{"a":1}`,
		`[1,2,3]`:     "{}",
		`42`:          "{}",
		"true":        "{}",
		`"{\"a\":1}"`: `{"a":1}`,
	}
	for in, want := range cases {
		if got := normalizeArgumentsJSON(in); got != want {
			t.Errorf("normalizeArgumentsJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeArgumentsJSON_AlwaysValidObject(t *testing.T) {
	// Property-style sweep: whatever we get from a provider or an old session,
	// the output must be a valid JSON object.
	inputs := []string{
		"", "null", "NULL", "{", "}", "[]", "{}", `{"a"}`, "1", `"1"`, `"{}"`,
		`{"a":1}`, `{"a":"b"`, "{a:1}", "\x00", `{"nested":{"deep":[1,2,{"k":"v"}]}}`,
	}
	for _, in := range inputs {
		got := normalizeArgumentsJSON(in)
		if !json.Valid([]byte(got)) {
			t.Errorf("normalizeArgumentsJSON(%q) = %q: not valid JSON", in, got)
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(got), &obj); err != nil {
			t.Errorf("normalizeArgumentsJSON(%q) = %q: not a JSON object", in, got)
		}
	}
}

func TestArgumentJSON_PrefersCanonicalString(t *testing.T) {
	// function.arguments is authoritative when it is a valid object; the decoded
	// map is only the fallback for calls that never populated Function.
	tc := ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "from-map"},
		Function:  &FunctionCall{Name: "exec", Arguments: `{"command":"from-string"}`},
	}
	if got := tc.ArgumentJSON(); got != `{"command":"from-string"}` {
		t.Fatalf("ArgumentJSON() = %q, want the canonical function.arguments", got)
	}

	// No Function at all -> encode the map.
	only := ToolCall{Name: "exec", Arguments: map[string]interface{}{"command": "ls"}}
	if got := only.ArgumentJSON(); got != `{"command":"ls"}` {
		t.Fatalf("ArgumentJSON() map fallback = %q", got)
	}

	// Nothing at all -> empty object, never "" or "null".
	empty := ToolCall{ID: "c1"}
	if got := empty.ArgumentJSON(); got != "{}" {
		t.Fatalf("ArgumentJSON() empty = %q, want {}", got)
	}
}

func TestFunctionName_PrefersFunction(t *testing.T) {
	both := ToolCall{Function: &FunctionCall{Name: "a"}, Name: "b"}
	if got := both.FunctionName(); got != "a" {
		t.Fatalf("FunctionName() = %q, want a", got)
	}
	fallback := ToolCall{Name: "b"}
	if got := fallback.FunctionName(); got != "b" {
		t.Fatalf("FunctionName() fallback = %q, want b", got)
	}
	var nilCall *ToolCall
	if got := nilCall.FunctionName(); got != "" {
		t.Fatalf("FunctionName() on nil = %q, want empty", got)
	}
}

// --- truncated-argument repair ----------------------------------------------

func TestRepairTruncatedObject_Cases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // expected canonical arguments
	}{
		{"cut inside a string value", `{"path":"/tmp/a","content":"hello`, `{"path":"/tmp/a"}`},
		{"cut after a comma", `{"a":1,`, `{"a":1}`},
		{"cut right after the brace", `{`, `{}`},
		{"cut inside a key", `{"path":"/tmp/a","con`, `{"path":"/tmp/a"}`},
		{"cut inside a nested object", `{"a":{"b":1,"c":2`, `{"a":{"b":1}}`},
		{"cut inside an array", `{"files":["a","b"`, `{"files":["a"]}`},
		{"escaped quote inside the cut string", `{"cmd":"echo \\"hi`, `{}`},
		{"only a dangling key", `{"a":`, `{}`},
		{"number mid-write", `{"a":12`, `{}`},
		{"nested object completes then cuts", `{"a":{"b":1},"c":"x`, `{"a":{"b":1}}`},
		{"array element mid-write", `{"a":[1,2,3`, `{"a":[1,2]}`},
		{"complete object is not truncated", `{"a":1}`, `{"a":1}`},
		{"trailing garbage after close", `{"a":1} junk`, `{"a":1}`},
		{"mismatched brackets", `{"a":[1,2}`, `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := repairTruncatedObject(tc.in)
			if tc.in[0] != '{' && !ok {
				return
			}
			if !ok {
				t.Fatalf("repair refused a truncated object: %q", tc.in)
			}
			if got != tc.want {
				t.Fatalf("repair(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !json.Valid([]byte(got)) {
				t.Fatalf("repair produced invalid JSON: %q", got)
			}
		})
	}
}

// The repair must not be quadratic: arguments payloads (file bodies, long
// commands) reach hundreds of kilobytes and this runs on every malformed call.
func TestRepairTruncatedObject_LinearOnLargePayload(t *testing.T) {
	body := strings.Repeat("x", 400_000)
	in := `{"path":"/tmp/a","content":"` + body
	got, ok := repairTruncatedObject(in)
	if !ok {
		t.Fatal("repair refused a truncated object")
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("repaired payload is invalid: %v", err)
	}
	if parsed["path"] != "/tmp/a" {
		t.Fatalf("lost the completed key: %s", got)
	}
}

// A truncated string member is lost entirely, so an escaped quote must not
// make the scanner treat the following brace as still-inside-the-string.
func TestNormalizeArgumentsJSON_CutInsideEscapedString(t *testing.T) {
	got := normalizeArgumentsJSON(`{"a":"x\",\"b\":\"y`)
	if got != emptyArgumentsJSON {
		t.Fatalf("escaped-quote confusion produced %q", got)
	}
}
