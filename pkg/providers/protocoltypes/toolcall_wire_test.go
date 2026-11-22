package protocoltypes

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestToolCallWire_NilArgumentsNeverEmitsNull(t *testing.T) {
	tc := ToolCall{ID: "call_deadbeef", Type: "function"}

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

func TestToolCallUnmarshal_LegacyShapes(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
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
			if call.Arguments == nil {
				t.Fatalf("decoded Arguments map is nil for %s", tc.json)
			}
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

func TestCanonicalToolCalls_DropsNamelessEntries(t *testing.T) {
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
	tc := ToolCall{
		Name:      "exec",
		Arguments: map[string]interface{}{"command": "from-map"},
		Function:  &FunctionCall{Name: "exec", Arguments: `{"command":"from-string"}`},
	}
	if got := tc.ArgumentJSON(); got != `{"command":"from-string"}` {
		t.Fatalf("ArgumentJSON() = %q, want the canonical function.arguments", got)
	}

	only := ToolCall{Name: "exec", Arguments: map[string]interface{}{"command": "ls"}}
	if got := only.ArgumentJSON(); got != `{"command":"ls"}` {
		t.Fatalf("ArgumentJSON() map fallback = %q", got)
	}

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

func TestRepairTruncatedObject_Cases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		lost bool
	}{
		{"cut inside a string value", `{"path":"/tmp/a","content":"hello`, `{"path":"/tmp/a"}`, true},
		{"cut after a comma", `{"a":1,`, `{"a":1}`, true},
		{"cut right after the brace", `{`, `{}`, true},
		{"cut inside a key", `{"path":"/tmp/a","con`, `{"path":"/tmp/a"}`, true},
		{"cut inside a nested object", `{"a":{"b":1,"c":2`, `{"a":{"b":1}}`, true},
		{"cut inside an array", `{"files":["a","b"`, `{"files":["a"]}`, true},
		{"escaped quote inside the cut string", `{"cmd":"echo \\"hi`, `{}`, true},
		{"only a dangling key", `{"a":`, `{}`, true},
		{"number mid-write", `{"a":12`, `{}`, true},
		{"nested object completes then cuts", `{"a":{"b":1},"c":"x`, `{"a":{"b":1}}`, true},
		{"array element mid-write", `{"a":[1,2,3`, `{"a":[1,2]}`, true},
		{"complete object is not truncated", `{"a":1}`, `{"a":1}`, false},
		{"trailing garbage after close", `{"a":1} junk`, `{"a":1}`, false},
		{"mismatched brackets", `{"a":[1,2}`, `{}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok, lost := repairTruncatedObject(tc.in)
			if tc.in[0] != '{' && !ok {
				return
			}
			if !ok {
				t.Fatalf("repair refused a truncated object: %q", tc.in)
			}
			if got != tc.want {
				t.Fatalf("repair(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if lost != tc.lost {
				t.Fatalf("repair(%q) reported lost=%v, want lost=%v", tc.in, lost, tc.lost)
			}
			if !json.Valid([]byte(got)) {
				t.Fatalf("repair produced invalid JSON: %q", got)
			}
		})
	}
}

func TestRepairTruncatedObject_LinearOnLargePayload(t *testing.T) {
	body := strings.Repeat("x", 400_000)
	in := `{"path":"/tmp/a","content":"` + body
	got, ok, _ := repairTruncatedObject(in)
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

func TestNormalizeArgumentsJSON_CutInsideEscapedString(t *testing.T) {
	got := normalizeArgumentsJSON(`{"a":"x\",\"b\":\"y`)
	if got != emptyArgumentsJSON {
		t.Fatalf("escaped-quote confusion produced %q", got)
	}
}

func TestCanonicalToolCalls_FlagsTruncatedArguments(t *testing.T) {
	got := CanonicalToolCalls([]ToolCall{{
		ID:       "c1",
		Function: &FunctionCall{Name: "write_file", Arguments: `{"path":"/tmp/a","content":"hello`},
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if !got[0].ArgumentsTruncated {
		t.Fatal("a repaired payload must be flagged as truncated")
	}
	if got[0].Arguments["path"] != "/tmp/a" {
		t.Fatalf("lost the completed argument: %#v", got[0].Arguments)
	}
	if _, ok := got[0].Arguments["content"]; ok {
		t.Fatalf("the cut member must not be invented: %#v", got[0].Arguments)
	}
}

func TestCanonicalToolCalls_CompleteArgumentsNotFlagged(t *testing.T) {
	got := CanonicalToolCalls([]ToolCall{{
		ID:       "c1",
		Function: &FunctionCall{Name: "write_file", Arguments: `{"path":"/tmp/a","content":"hello"}`},
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if got[0].ArgumentsTruncated {
		t.Fatal("a complete payload must not be flagged")
	}
}

func TestCanonicalToolCalls_TrailingJunkNotFlagged(t *testing.T) {
	got := CanonicalToolCalls([]ToolCall{{
		ID:       "c1",
		Function: &FunctionCall{Name: "exec", Arguments: `{"command":"ls"} trailing`},
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if got[0].ArgumentsTruncated {
		t.Fatalf("complete-then-junk payload flagged as truncated: %q", got[0].Function.Arguments)
	}
}

func TestCanonicalToolCalls_PreservesProviderFlag(t *testing.T) {
	got := CanonicalToolCalls([]ToolCall{{
		ID:                 "c1",
		Function:           &FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`},
		ArgumentsTruncated: true,
	}})
	if len(got) != 1 {
		t.Fatalf("expected 1 call, got %d", len(got))
	}
	if !got[0].ArgumentsTruncated {
		t.Fatal("the provider's truncation signal was dropped")
	}
}

func TestNormalizeArgumentsJSON_FlagsUnrecoverableObject(t *testing.T) {
	normalized, truncated := normalizeArgumentsJSONWithFlag(`{"a":[1,2}`)
	if normalized != emptyArgumentsJSON {
		t.Fatalf("normalized = %q, want %q", normalized, emptyArgumentsJSON)
	}
	if !truncated {
		t.Fatal("an object that opened but could not be rebuilt is incomplete")
	}
}

func TestNormalizeArgumentsJSON_AbsentPayloadNotFlagged(t *testing.T) {
	for _, in := range []string{"", "   ", "null", `{"a":1}`} {
		if _, truncated := normalizeArgumentsJSONWithFlag(in); truncated {
			t.Errorf("normalizeArgumentsJSONWithFlag(%q) reported truncation", in)
		}
	}
}

func TestToolCallArgumentsTruncatedNotSerialized(t *testing.T) {
	encoded, err := json.Marshal(ToolCall{
		ID:                 "c1",
		Function:           &FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`},
		ArgumentsTruncated: true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "ArgumentsTruncated") || strings.Contains(string(encoded), "arguments_truncated") {
		t.Fatalf("internal flag leaked to the wire: %s", encoded)
	}

	var keys map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &keys); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"function", "id", "type"}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("wire keys = %v, want %v", got, want)
	}
}

func TestToolCallUnmarshalJSON_FlagsTruncatedArguments(t *testing.T) {
	var tc ToolCall
	raw := []byte(`{"id":"c1","type":"function","function":{"name":"write_file","arguments":"{\"path\":\"/tmp/a\",\"content\":\"hel"}}`)
	if err := json.Unmarshal(raw, &tc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !tc.ArgumentsTruncated {
		t.Fatalf("cut-off arguments not flagged: %#v", tc)
	}
	if tc.Arguments["path"] != "/tmp/a" {
		t.Fatalf("surviving argument lost: %#v", tc.Arguments)
	}
}
