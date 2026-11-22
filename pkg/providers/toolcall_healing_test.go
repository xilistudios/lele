package providers

import "testing"

func call(id string) ToolCall {
	return ToolCall{ID: id, Type: "function", Function: &FunctionCall{Name: "exec", Arguments: `{}`}}
}

func asst(ids ...string) Message {
	tcs := make([]ToolCall, 0, len(ids))
	for _, id := range ids {
		tcs = append(tcs, call(id))
	}
	return Message{Role: "assistant", ToolCalls: tcs}
}

func result(id, content string) Message {
	return Message{Role: "tool", ToolCallID: id, Content: content}
}

func user(content string) Message { return Message{Role: "user", Content: content} }

func countResults(msgs []Message, callID string) int {
	n := 0
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == callID {
			n++
		}
	}
	return n
}

func TestHealToolCallPairs_Cases(t *testing.T) {
	tests := []struct {
		name            string
		in              []Message
		wantRoles       []string
		wantResultFor   []string
		wantNoResultFor []string
		wantContentFor  map[string]string
	}{
		{
			name:      "clean history is untouched",
			in:        []Message{user("hi"), asst("c1"), result("c1", "ok"), assistantText("done")},
			wantRoles: []string{"user", "assistant", "tool", "assistant"},
		},
		{
			name:          "dangling call gets a synthetic result",
			in:            []Message{user("hi"), asst("c1")},
			wantRoles:     []string{"user", "assistant", "tool"},
			wantResultFor: []string{"c1"},
		},
		{
			name:            "orphan result is dropped",
			in:              []Message{user("hi"), result("gone", "stale"), assistantText("next")},
			wantRoles:       []string{"user", "assistant"},
			wantNoResultFor: []string{"gone"},
		},
		{
			name:           "duplicate results collapse to the first",
			in:             []Message{asst("c1"), result("c1", "first"), result("c1", "second"), assistantText("ok")},
			wantRoles:      []string{"assistant", "tool", "assistant"},
			wantContentFor: map[string]string{"c1": "first"},
		},
		{
			name:           "result before its assistant message is dropped",
			in:             []Message{user("hi"), result("c1", "early"), asst("c1"), result("c1", "real")},
			wantRoles:      []string{"user", "assistant", "tool"},
			wantContentFor: map[string]string{"c1": "real"},
		},
		{
			name:           "multi-call group synthesises only the missing one",
			in:             []Message{asst("c1", "c2"), result("c1", "ok"), assistantText("more")},
			wantRoles:      []string{"assistant", "tool", "tool", "assistant"},
			wantResultFor:  []string{"c1", "c2"},
			wantContentFor: map[string]string{"c1": "ok"},
		},
		{
			name:           "result interrupted by a user message stays attached",
			in:             []Message{asst("c1"), user("stop"), result("c1", "late"), assistantText("resumed")},
			wantRoles:      []string{"assistant", "tool", "user", "assistant"},
			wantResultFor:  []string{"c1"},
			wantContentFor: map[string]string{"c1": "late"},
		},
		{
			name:          "two sequential groups each close",
			in:            []Message{asst("c1"), result("c1", "a"), asst("c2"), result("c2", "b")},
			wantRoles:     []string{"assistant", "tool", "assistant", "tool"},
			wantResultFor: []string{"c1", "c2"},
		},
		{
			name:           "id-less result is dropped",
			in:             []Message{asst("c1"), result("", "no id"), result("c1", "ok")},
			wantRoles:      []string{"assistant", "tool"},
			wantContentFor: map[string]string{"c1": "ok"},
		},
		{
			name:      "empty history",
			in:        nil,
			wantRoles: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := HealToolCallPairs(tt.in)
			_ = changed

			if len(got) != len(tt.wantRoles) {
				t.Fatalf("role count: got %d (%s), want %d", len(got), roles(got), len(tt.wantRoles))
			}
			for i, want := range tt.wantRoles {
				if got[i].Role != want {
					t.Fatalf("roles[%d]: got %q, want %q (full: %s)", i, got[i].Role, want, roles(got))
				}
			}
			for _, id := range tt.wantResultFor {
				if n := countResults(got, id); n != 1 {
					t.Errorf("call %s: got %d results, want 1", id, n)
				}
			}
			for _, id := range tt.wantNoResultFor {
				if n := countResults(got, id); n != 0 {
					t.Errorf("call %s: got %d results, want 0", id, n)
				}
			}
			for id, wantContent := range tt.wantContentFor {
				found := ""
				for _, m := range got {
					if m.Role == "tool" && m.ToolCallID == id {
						found = m.Content
					}
				}
				if found != wantContent {
					t.Errorf("call %s result: got %q, want %q", id, found, wantContent)
				}
			}
		})
	}
}

func assistantText(content string) Message {
	return Message{Role: "assistant", Content: content}
}

func roles(msgs []Message) string {
	out := ""
	for _, m := range msgs {
		out += m.Role + " "
	}
	return out
}

func TestHealToolCallPairs_IsIdempotent(t *testing.T) {
	in := []Message{user("hi"), asst("c1", "c2"), result("c1", "ok"), result("orphan", "x")}

	once, changed1 := HealToolCallPairs(in)
	if !changed1 {
		t.Fatal("broken history must report a change")
	}
	twice, changed2 := HealToolCallPairs(once)
	if changed2 {
		t.Fatal("healed history must report no further change")
	}

	if len(once) != len(twice) {
		t.Fatalf("not idempotent: first pass %d messages (%s), second %d (%s)",
			len(once), roles(once), len(twice), roles(twice))
	}
	for i := range once {
		if once[i].Role != twice[i].Role || once[i].Content != twice[i].Content ||
			once[i].ToolCallID != twice[i].ToolCallID {
			t.Fatalf("message %d differs between passes", i)
		}
	}
	if n := countResults(once, "c2"); n != 1 {
		t.Fatalf("c2 results after first pass: got %d, want 1", n)
	}
}

func TestHealToolCallPairs_CleanIsIdentity(t *testing.T) {
	clean := []Message{
		user("hi"),
		asst("c1"),
		result("c1", "ok"),
		assistantText("answer"),
	}
	got, changed := HealToolCallPairs(clean)
	if changed {
		t.Fatal("clean history must be reported as unchanged")
	}
	if &got[0] != &clean[0] {
		t.Fatal("clean history must be returned as the same slice")
	}
	if len(got) != len(clean) {
		t.Fatalf("clean history modified: %d -> %d", len(clean), len(got))
	}
	for i := range clean {
		if got[i].Role != clean[i].Role || got[i].Content != clean[i].Content {
			t.Fatalf("message %d changed", i)
		}
	}
}

func TestMissingResultMessage_CarriesCallID(t *testing.T) {
	m := missingResultMessage("call_abc")
	if m.ToolCallID != "call_abc" {
		t.Fatalf("ToolCallID = %q, want call_abc", m.ToolCallID)
	}
	if m.Role != "tool" {
		t.Fatalf("Role = %q, want tool", m.Role)
	}
	if m.Content == "" {
		t.Fatal("synthetic result must explain itself to the model")
	}
}

func TestHealToolCallPairs_DropsUnanswerableCalls(t *testing.T) {
	in := []Message{
		user("hi"),
		{Role: "assistant", Content: "working", ToolCalls: []ToolCall{
			{ID: "", Type: "function", Function: &FunctionCall{Name: "exec", Arguments: "{}"}},
			{ID: "c_noname", Type: "function", Function: &FunctionCall{Name: "  ", Arguments: "{}"}},
			call("c_good"),
		}},
		result("c_good", "ok"),
	}

	got, changed := HealToolCallPairs(in)
	if !changed {
		t.Fatal("unanswerable calls must be reported as a change")
	}
	for _, m := range got {
		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				t.Errorf("request still carries an id-less call: %+v", tc)
			}
			if tc.FunctionName() == "" {
				t.Errorf("request still carries a nameless call: %+v", tc)
			}
		}
	}
	if n := countResults(got, "c_good"); n != 1 {
		t.Errorf("healthy call: got %d results, want 1", n)
	}
	if n := countResults(got, "c_noname"); n != 0 {
		t.Errorf("dropped call got a synthetic result (%d)", n)
	}
}

func TestHealToolCallPairs_BlankAssistantAfterStripIsDropped(t *testing.T) {
	in := []Message{
		user("hi"),
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "", Type: "function", Function: &FunctionCall{Name: "exec", Arguments: "{}"}},
		}},
		assistantText("answer"),
	}

	got, _ := HealToolCallPairs(in)
	for _, m := range got {
		if m.Role == "assistant" && len(m.ToolCalls) == 0 && m.Content == "" {
			t.Fatalf("blank assistant message survived healing: %+v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want user + assistant, got %d messages (%s)", len(got), roles(got))
	}
}

func TestHealToolCallPairs_KeepsReasoningOnlyAssistant(t *testing.T) {
	in := []Message{
		{Role: "assistant", ReasoningContent: "thinking", ToolCalls: []ToolCall{
			{ID: "", Type: "function", Function: &FunctionCall{Name: "exec", Arguments: "{}"}},
		}},
	}
	got, _ := HealToolCallPairs(in)
	if len(got) != 1 || got[0].ReasoningContent != "thinking" {
		t.Fatalf("reasoning-only assistant was dropped: %+v", got)
	}
}

func TestHealToolCallPairs_DoesNotMutateInput(t *testing.T) {
	in := []Message{
		{Role: "assistant", Content: "working", ToolCalls: []ToolCall{
			{ID: "", Type: "function", Function: &FunctionCall{Name: "exec", Arguments: "{}"}},
			call("c_keep"),
		}},
	}
	before := len(in[0].ToolCalls)

	got, _ := HealToolCallPairs(in)
	if len(in[0].ToolCalls) != before {
		t.Fatalf("input tool calls mutated: %d -> %d", before, len(in[0].ToolCalls))
	}
	if len(got[0].ToolCalls) != 1 || got[0].ToolCalls[0].ID != "c_keep" {
		t.Fatalf("output should keep only the answerable call, got %+v", got[0].ToolCalls)
	}
}

func syntheticResult(id string) string { return missingResultMessage(id).Content }

func TestHealToolCallPairs_InterleavedUserMessageKeepsRealResult(t *testing.T) {
	in := []Message{
		user("q0"),
		{Role: "assistant", Content: "calling tool", ToolCalls: []ToolCall{call("call1")}},
		user("PIN"),
		result("call1", "REAL_TOOL_OUTPUT"),
	}

	got, changed := HealToolCallPairs(in)
	if !changed {
		t.Fatal("an interleaved history is not provider-valid, so healing must report a change")
	}

	wantRoles := []string{"user", "assistant", "tool", "user"}
	if len(got) != len(wantRoles) {
		t.Fatalf("got %d messages (%s), want %d", len(got), roles(got), len(wantRoles))
	}
	for i, want := range wantRoles {
		if got[i].Role != want {
			t.Fatalf("roles[%d] = %q, want %q (full: %s)", i, got[i].Role, want, roles(got))
		}
	}
	if got[2].ToolCallID != "call1" || got[2].Content != "REAL_TOOL_OUTPUT" {
		t.Fatalf("call1 result = {id %q, content %q}, want {call1, REAL_TOOL_OUTPUT}",
			got[2].ToolCallID, got[2].Content)
	}
	if got[3].Content != "PIN" {
		t.Fatalf("interleaved message = %q, want PIN", got[3].Content)
	}

	if again, stillChanged := HealToolCallPairs(got); stillChanged {
		t.Fatalf("healed history is not stable, second pass rewrote it: %s", roles(again))
	}
}

type wantResult struct{ id, content string }

func checkHealed(t *testing.T, got []Message, wantRoles []string, wantResults []wantResult, wantTexts []string) {
	t.Helper()
	if len(got) != len(wantRoles) {
		t.Fatalf("got %d messages (%s), want %d", len(got), roles(got), len(wantRoles))
	}
	var results []wantResult
	var texts []string
	for i, m := range got {
		if m.Role != wantRoles[i] {
			t.Fatalf("roles[%d] = %q, want %q (full: %s)", i, m.Role, wantRoles[i], roles(got))
		}
		if m.Role == "tool" {
			results = append(results, wantResult{m.ToolCallID, m.Content})
			continue
		}
		texts = append(texts, m.Content)
	}
	if len(results) != len(wantResults) {
		t.Fatalf("got %d tool results (%v), want %d (%v)", len(results), results, len(wantResults), wantResults)
	}
	for i, want := range wantResults {
		if results[i] != want {
			t.Errorf("tool result[%d] = {%q, %q}, want {%q, %q}",
				i, results[i].id, results[i].content, want.id, want.content)
		}
	}
	if len(texts) != len(wantTexts) {
		t.Fatalf("got %d non-tool messages (%v), want %d (%v)", len(texts), texts, len(wantTexts), wantTexts)
	}
	for i, want := range wantTexts {
		if texts[i] != want {
			t.Errorf("message text[%d] = %q, want %q", i, texts[i], want)
		}
	}
}

func TestHealToolCallPairs_InterleavedMessageCases(t *testing.T) {
	tests := []struct {
		name        string
		in          []Message
		wantRoles   []string
		wantResults []wantResult
		wantTexts   []string
	}{
		{
			name:        "result arrives after the first of two calls",
			in:          []Message{asst("c1", "c2"), result("c1", "one"), user("PIN"), result("c2", "two"), assistantText("done")},
			wantRoles:   []string{"assistant", "tool", "tool", "user", "assistant"},
			wantResults: []wantResult{{"c1", "one"}, {"c2", "two"}},
			wantTexts:   []string{"", "PIN", "done"},
		},
		{
			name:        "no result ever arrives",
			in:          []Message{asst("c1"), user("PIN")},
			wantRoles:   []string{"assistant", "tool", "user"},
			wantResults: []wantResult{{"c1", syntheticResult("c1")}},
			wantTexts:   []string{"", "PIN"},
		},
		{
			name:        "second result never arrives",
			in:          []Message{asst("c1", "c2"), result("c1", "one"), user("PIN")},
			wantRoles:   []string{"assistant", "tool", "tool", "user"},
			wantResults: []wantResult{{"c1", "one"}, {"c2", syntheticResult("c2")}},
			wantTexts:   []string{"", "PIN"},
		},
		{
			name:        "orphan result after an interleave is still dropped",
			in:          []Message{asst("c1"), user("PIN"), result("gone", "stale")},
			wantRoles:   []string{"assistant", "tool", "user"},
			wantResults: []wantResult{{"c1", syntheticResult("c1")}},
			wantTexts:   []string{"", "PIN"},
		},
		{
			name:        "duplicate result after the block closed is still dropped",
			in:          []Message{asst("c1"), result("c1", "first"), user("PIN"), result("c1", "second")},
			wantRoles:   []string{"assistant", "tool", "user"},
			wantResults: []wantResult{{"c1", "first"}},
			wantTexts:   []string{"", "PIN"},
		},
		{
			name: "a later assistant closes the block, so its result is an orphan",
			in: []Message{
				asst("c1"), user("PIN"), asst("c2"), result("c1", "late"), result("c2", "two"),
			},
			wantRoles:   []string{"assistant", "tool", "user", "assistant", "tool"},
			wantResults: []wantResult{{"c1", syntheticResult("c1")}, {"c2", "two"}},
			wantTexts:   []string{"", "PIN", ""},
		},
		{
			name:        "several interleaved messages keep their order",
			in:          []Message{asst("c1"), user("PIN"), user("PING"), result("c1", "real")},
			wantRoles:   []string{"assistant", "tool", "user", "user"},
			wantResults: []wantResult{{"c1", "real"}},
			wantTexts:   []string{"", "PIN", "PING"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := HealToolCallPairs(tt.in)
			if !changed {
				t.Error("broken history must report a change")
			}
			checkHealed(t, got, tt.wantRoles, tt.wantResults, tt.wantTexts)
			if again, stillChanged := HealToolCallPairs(got); stillChanged {
				t.Errorf("healed history is not stable, second pass rewrote it: %s", roles(again))
			}
		})
	}
}
