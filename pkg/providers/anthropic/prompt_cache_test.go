package anthropicprovider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestBuildParams_NoCacheControlByDefault(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "read_file"}},
	}
	params, err := buildParams(messages, tools, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"max_tokens": 1024,
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	// Zero cache_control must appear anywhere in the serialized payload.
	b, _ := json.Marshal(params)
	if got := string(b); containsCacheControl(got) {
		t.Errorf("cache_control present without prompt_cache option: %s", got)
	}
}

func TestBuildParams_CacheBreakpoints(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "static system prompt"},
		{Role: "user", Content: "turn 1"},
		{Role: "assistant", Content: "reply 1"},
		{Role: "user", Content: "turn 2"},
	}
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "alpha"}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "zeta"}},
	}
	params, err := buildParams(messages, tools, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"max_tokens":       1024,
		"prompt_cache":     true,
		"prompt_cache_ttl": "1h",
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}

	// System: last block carries the breakpoint.
	if n := len(params.System); n == 0 {
		t.Fatal("no system blocks")
	} else {
		last := params.System[n-1]
		if last.CacheControl.Type != "ephemeral" {
			t.Errorf("system cache_control type = %q", last.CacheControl.Type)
		}
		if last.CacheControl.TTL != anthropic.CacheControlEphemeralTTLTTL1h {
			t.Errorf("system TTL = %q, want 1h", last.CacheControl.TTL)
		}
	}

	// Tools: breakpoint only on the last tool.
	if len(params.Tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(params.Tools))
	}
	if params.Tools[0].OfTool != nil && params.Tools[0].OfTool.CacheControl.Type != "" {
		t.Error("first tool must not carry a breakpoint")
	}
	if params.Tools[1].OfTool == nil || params.Tools[1].OfTool.CacheControl.Type != "ephemeral" {
		t.Error("last tool must carry the breakpoint")
	}

	// History: breakpoint on last text block of the final message.
	lastMsg := params.Messages[len(params.Messages)-1]
	tb := lastMsg.Content[len(lastMsg.Content)-1].OfText
	if tb == nil || tb.CacheControl.Type != "ephemeral" {
		t.Error("last history message must carry the breakpoint")
	}
	for _, m := range params.Messages[:len(params.Messages)-1] {
		for _, blk := range m.Content {
			if blk.OfText != nil && blk.OfText.CacheControl.Type != "" {
				t.Error("only the final message may carry a history breakpoint")
			}
		}
	}

	// Wire format check: cache_control objects serialize with type+ttl.
	b, _ := json.Marshal(params)
	if !containsCacheControl(string(b)) {
		t.Fatalf("serialized params missing cache_control: %s", b)
	}
}

func TestApplyCacheBreakpoints_SkipsToolResultTail(t *testing.T) {
	// Final message is a tool result (no text blocks): no history breakpoint,
	// but system/tools breakpoints must still be applied.
	params := anthropic.MessageNewParams{}
	params.System = []anthropic.TextBlockParam{{Text: "sys"}}
	params.Messages = []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewToolResultBlock("call_1", "output", false)),
	}
	applyCacheBreakpoints(&params, "5m")

	if params.System[0].CacheControl.Type != "ephemeral" {
		t.Error("system breakpoint expected")
	}
	last := params.Messages[len(params.Messages)-1]
	for _, blk := range last.Content {
		if blk.OfToolResult != nil && blk.OfToolResult.CacheControl.Type != "" {
			t.Error("tool_result must not carry a breakpoint")
		}
	}
}

func TestSdkCacheControlTTL(t *testing.T) {
	if sdkCacheControl("1h").TTL != anthropic.CacheControlEphemeralTTLTTL1h {
		t.Error("1h TTL not mapped")
	}
	if sdkCacheControl("junk").TTL != anthropic.CacheControlEphemeralTTLTTL5m {
		t.Error("invalid TTL must fall back to 5m")
	}
}

func TestParseResponse_UsageCacheTokens(t *testing.T) {
	raw := `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":8000,"cache_creation_input_tokens":500,"cache_creation":{"ephemeral_5m_input_tokens":500,"ephemeral_1h_input_tokens":0}}}`
	var msg anthropic.Message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatal(err)
	}
	resp := parseResponse(&msg)
	u := resp.Usage
	if u.CacheReadInputTokens != 8000 {
		t.Errorf("CacheReadInputTokens = %d, want 8000", u.CacheReadInputTokens)
	}
	if u.CacheCreationInputTokens != 500 {
		t.Errorf("CacheCreationInputTokens = %d, want 500", u.CacheCreationInputTokens)
	}
	// PromptTokens must include cached input (true context size).
	if u.PromptTokens != 100+8000+500 {
		t.Errorf("PromptTokens = %d, want 8600", u.PromptTokens)
	}
	if u.TotalTokens != 8600+20 {
		t.Errorf("TotalTokens = %d, want 8620", u.TotalTokens)
	}
}

func containsCacheControl(s string) bool {
	return strings.Contains(s, "cache_control")
}
