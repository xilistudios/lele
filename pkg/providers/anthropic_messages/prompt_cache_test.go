package anthropicmessages

import (
	"encoding/json"
	"strings"
	"testing"
)

func cacheOpts(ttl string) map[string]any {
	return map[string]any{"max_tokens": 1024, "prompt_cache": true, "prompt_cache_ttl": ttl}
}

func TestBuildRequestBody_NoCacheByDefault(t *testing.T) {
	body, err := buildRequestBody(
		[]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
		[]ToolDefinition{{Type: "function", Function: ToolFunctionDefinition{Name: "t"}}},
		"claude-sonnet-4-5",
		map[string]any{"max_tokens": 1024},
	)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(body)
	if strings.Contains(string(b), "cache_control") {
		t.Errorf("cache_control without opt-in: %s", b)
	}
	// System stays a plain string when caching is off.
	if _, ok := body["system"].(string); !ok {
		t.Errorf("system should remain string, got %T", body["system"])
	}
}

func TestBuildRequestBody_CacheBreakpoints(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "static prompt"},
		{Role: "user", Content: "hello"},
	}
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "alpha"}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "zeta"}},
	}
	body, err := buildRequestBody(messages, tools, "claude-sonnet-4-5", cacheOpts("1h"))
	if err != nil {
		t.Fatal(err)
	}

	// System became a block array with a breakpoint.
	sysBlocks, ok := body["system"].([]map[string]any)
	if !ok || len(sysBlocks) != 1 {
		t.Fatalf("system not converted to blocks: %#v", body["system"])
	}
	cc := sysBlocks[0]["cache_control"].(map[string]string)
	if cc["type"] != "ephemeral" || cc["ttl"] != "1h" {
		t.Errorf("system cache_control = %v", cc)
	}

	// Only the last tool carries the breakpoint.
	toolsOut := body["tools"].([]any)
	if _, exists := toolsOut[0].(map[string]any)["cache_control"]; exists {
		t.Error("first tool must not be marked")
	}
	if _, exists := toolsOut[1].(map[string]any)["cache_control"]; !exists {
		t.Error("last tool must carry the breakpoint")
	}

	// Last message (plain string content) is upgraded to a text block with breakpoint.
	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	content, ok := last["content"].([]map[string]any)
	if !ok || len(content) != 1 {
		t.Fatalf("last message content not upgraded: %#v", last["content"])
	}
	if content[0]["cache_control"] == nil {
		t.Error("history breakpoint missing")
	}
}

func TestBuildRequestBody_CacheSkipsToolResultTail(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: "calling", ToolCalls: []ToolCall{{ID: "call_1", Name: "exec"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "output"},
	}
	body, err := buildRequestBody(messages, nil, "claude-sonnet-4-5", cacheOpts(""))
	if err != nil {
		t.Fatal(err)
	}
	msgs := body["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	// tool_result content must not receive a breakpoint.
	for _, blk := range last["content"].([]map[string]any) {
		if blk["type"] == "tool_result" && blk["cache_control"] != nil {
			t.Error("tool_result must not carry a breakpoint")
		}
	}
	// System breakpoint still applied.
	if _, ok := body["system"].([]map[string]any); !ok {
		t.Error("system should still be converted with breakpoint")
	}
}

func TestBuildRequestBody_CacheDefaultTTL(t *testing.T) {
	body, err := buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}}, nil, "m", cacheOpts(""),
	)
	if err != nil {
		t.Fatal(err)
	}
	msgs := body["messages"].([]any)
	last := msgs[0].(map[string]any)
	content := last["content"].([]map[string]any)
	cc := content[0]["cache_control"].(map[string]string)
	if cc["ttl"] != "5m" {
		t.Errorf("default TTL = %q, want 5m", cc["ttl"])
	}
}

func TestParseResponseBody_CacheUsage(t *testing.T) {
	raw := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":8000,"cache_creation_input_tokens":500}}`
	resp, err := parseResponseBody([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	u := resp.Usage
	if u.CacheReadInputTokens != 8000 || u.CacheCreationInputTokens != 500 {
		t.Errorf("cache usage not parsed: %+v", u)
	}
	if u.PromptTokens != 8600 {
		t.Errorf("PromptTokens = %d, want 8600 (input + cached)", u.PromptTokens)
	}
	if u.TotalTokens != 8620 {
		t.Errorf("TotalTokens = %d, want 8620", u.TotalTokens)
	}
}
