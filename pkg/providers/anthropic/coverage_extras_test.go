package anthropicprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func TestBuildParams_UserWithToolResultAndAssistantTools(t *testing.T) {
	// Covers:
	// - user message with ToolCallID != "" branch
	// - assistant message with ToolCalls + text (NewTextBlock + NewToolUseBlock)
	// - assistant message not empty but no tool calls
	messages := []Message{
		{Role: "user", Content: "", ToolCallID: "call_1"},
		{
			Role:    "assistant",
			Content: "thinking out loud",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: map[string]interface{}{"city": "SF"}},
			},
		},
		{Role: "assistant", Content: "plain reply"},
	}
	params, err := buildParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	// user w/toolresult + assistant w/toolcalls + assistant text = 3 messages
	if len(params.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(params.Messages))
	}
}

func TestBuildParams_EmptyContentParts(t *testing.T) {
	// buildAnthropicContentBlocks with no ContentParts and empty text -> nil
	params, err := buildParams([]Message{{Role: "user", Content: ""}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if len(params.Messages) != 0 {
		t.Fatalf("len(Messages) = %d, want 0 (empty user message dropped)", len(params.Messages))
	}
}

func TestBuildAnthropicContentBlocks_EdgeImages(t *testing.T) {
	// Covers image_url branches: nil ImageURL, splitDataURL !ok, etc.
	blocks := buildAnthropicContentBlocks(Message{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "   "},                                                                      // trimmed empty -> skipped
			{Type: "text", Text: "real text"},                                                                // kept
			{Type: "image_url", ImageURL: nil},                                                               // nil -> continue
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "not-a-data-url"}},                    // prefix fail
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,"}},            // empty encoded
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:;base64,abcd"}},                 // empty media type
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;charset=utf-8,abcd"}}, // not base64 suffix
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,abcd"}},        // valid
		},
	})
	// Expect: "real text" + valid image = 2 blocks
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
}

func TestSplitDataURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		media   string
		encoded string
		ok      bool
	}{
		{"valid", "data:image/png;base64,abcd", "image/png", "abcd", true},
		{"no prefix", "http://example.com/x", "", "", false},
		{"no comma", "data:image/png;base64", "", "", false},
		{"not base64", "data:image/png;plain,abcd", "", "", false},
		{"uppercase base64 not trimmed by TrimSuffix", "data:image/jpeg;Base64,xyz", "image/jpeg;Base64", "xyz", true},
		{"whitespace around", "  data:image/gif;base64,z  ", "image/gif", "z", true},
		{"empty media", "data:;base64,abcd", "", "", false},
		{"empty encoded", "data:image/png;base64,", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media, encoded, ok := splitDataURL(tt.raw)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !tt.ok {
				return
			}
			if media != tt.media || encoded != tt.encoded {
				t.Errorf("splitDataURL(%q) = (%q,%q), want (%q,%q)", tt.raw, media, encoded, tt.media, tt.encoded)
			}
		})
	}
}

func TestNormalizeBaseURL_EdgeCases(t *testing.T) {
	if got := normalizeBaseURL("   "); got != defaultBaseURL {
		t.Errorf("whitespace => %q, want default", got)
	}
	if got := normalizeBaseURL(""); got != defaultBaseURL {
		t.Errorf("empty => %q, want default", got)
	}
	if got := normalizeBaseURL("/"); got != defaultBaseURL {
		t.Errorf("only slash => %q, want default", got)
	}
}

func makeContentBlockUnion(t *testing.T, raw string) anthropic.ContentBlockUnion {
	t.Helper()
	var b anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return b
}

func TestParseResponse_ToolUseInputs(t *testing.T) {
	// tool_use with input data
	withInput := makeContentBlockUnion(t, `{"type":"tool_use","id":"call_1","name":"get_weather","input":{"city":"SF"}}`)
	// tool_use with null input (no Input unmarshaled)
	noInput := makeContentBlockUnion(t, `{"type":"tool_use","id":"call_2","name":"get_time"}`)

	resp := &anthropic.Message{
		Content:    []anthropic.ContentBlockUnion{noInput, withInput},
		StopReason: anthropic.StopReasonToolUse,
		Usage: anthropic.Usage{
			InputTokens:  5,
			OutputTokens: 3,
		},
	}
	result := parseResponse(resp)
	if len(result.ToolCalls) != 2 {
		t.Fatalf("len(ToolCalls) = %d, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Name != "get_time" {
		t.Errorf("ToolCalls[0].Name = %q, want get_time", result.ToolCalls[0].Name)
	}
	if result.ToolCalls[0].Arguments == nil {
		t.Error("ToolCalls[0].Arguments should be empty (non-nil) map when input missing")
	}
	if result.ToolCalls[1].Name != "get_weather" {
		t.Errorf("ToolCalls[1].Name = %q, want get_weather", result.ToolCalls[1].Name)
	}
	if city, _ := result.ToolCalls[1].Arguments["city"]; city != "SF" {
		t.Errorf("city arg = %v, want SF", city)
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", result.FinishReason)
	}
	if result.Usage.TotalTokens != 8 {
		t.Errorf("TotalTokens = %d, want 8", result.Usage.TotalTokens)
	}
}

func TestParseResponse_MultipleTextBlocks(t *testing.T) {
	blocks := []anthropic.ContentBlockUnion{
		makeContentBlockUnion(t, `{"type":"text","text":"Hello "}`),
		makeContentBlockUnion(t, `{"type":"text","text":"world"}`),
	}
	resp := &anthropic.Message{
		Content:    blocks,
		StopReason: anthropic.StopReasonEndTurn,
	}
	result := parseResponse(resp)
	if result.Content != "Hello world" {
		t.Errorf("Content = %q, want %q (concatenated)", result.Content, "Hello world")
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", result.FinishReason)
	}
}

func TestChat_APICallError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewProviderWithClient(createAnthropicTestClient(srv.URL, "token"))
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected API call error")
	}
}
