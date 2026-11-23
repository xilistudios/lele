package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func legacyToolCall(id, name, arguments string, decoded map[string]any) providers.ToolCall {
	return providers.ToolCall{
		ID:        id,
		Type:      "function",
		Name:      name,
		Function:  &providers.FunctionCall{Name: name, Arguments: arguments},
		Arguments: decoded,
	}
}

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
func attachmentOnlyAssistant() providers.Message {
	return providers.Message{
		Role: "assistant",
		Attachments: []providers.MessageAttachment{{
			Name:     "chart.png",
			Path:     "/home/u/.lele/attachments/0123456789abcdef_chart.png",
			MIMEType: "image/png",
			Kind:     "image",
		}},
	}
}

func fileDisplayRow() providers.Message {
	return providers.Message{
		Role:               "tool",
		ToolCallID:         "file:0123456789abcdef",
		Content:            "here is the chart",
		ExcludeFromContext: true,
		Attachments: []providers.MessageAttachment{{
			Name:     "chart.png",
			Path:     "/home/u/.lele/attachments/0123456789abcdef_chart.png",
			MIMEType: "image/png",
			Kind:     "image",
		}},
	}
}

func TestDropBlankAssistantMessages_RemovesAttachmentOnlyRow(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "send me the chart"},
		attachmentOnlyAssistant(),
	}

	cleaned, removed := dropBlankAssistantMessages(history)
	if !removed {
		t.Fatal("an assistant row carrying only attachments must be dropped: it would vanish on reload")
	}
	if len(cleaned) != 1 || cleaned[0].Role != "user" {
		t.Fatalf("cleaned history = %+v, want only the user message", cleaned)
	}
}

func TestDropBlankAssistantMessages_KeepsFileDisplayRow(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "send me the chart"},
		fileDisplayRow(),
	}

	cleaned, removed := dropBlankAssistantMessages(history)
	if removed {
		t.Fatalf("a file: tool row must survive dropBlankAssistantMessages, got %+v", cleaned)
	}
	if len(cleaned) != 2 {
		t.Fatalf("cleaned history = %+v, want 2 messages", cleaned)
	}
}

func TestHealAssistantToolCalls_DropsAttachmentOnlyRowAndKeepsFileRow(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "send me the chart"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			legacyToolCall("call_dead", "", "{}", map[string]any{}),
		}},
		{Role: "tool", Content: "result", ToolCallID: "call_dead"},
		attachmentOnlyAssistant(),
		fileDisplayRow(),
	}

	healed, changed := healAssistantToolCalls(history)
	if !changed {
		t.Fatal("expected changed=true")
	}

	var sawAssistantAttachment bool
	var sawFileRow bool
	for _, m := range healed {
		if m.Role == "assistant" && len(m.Attachments) > 0 {
			sawAssistantAttachment = true
		}
		if m.Role == "tool" && strings.HasPrefix(m.ToolCallID, "file:") {
			sawFileRow = true
			if !m.ExcludeFromContext {
				t.Error("healing dropped exclude_from_context from the file row")
			}
			if len(m.Attachments) != 1 {
				t.Errorf("healing dropped the file row attachments: %+v", m.Attachments)
			}
		}
	}
	if sawAssistantAttachment {
		t.Error("an attachment-only assistant row survived healing: it would vanish on reload")
	}
	if !sawFileRow {
		t.Errorf("the file: display row did not survive healing: %+v", healed)
	}
	if len(healed) != 2 {
		t.Errorf("healed history len = %d, want 2 (user + file row): %+v", len(healed), healed)
	}
}
