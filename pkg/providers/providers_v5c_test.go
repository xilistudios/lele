package providers

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"github.com/xilistudios/lele/pkg/config"
)

// TestTranslateToolsForCodex_WebSearchSkip covers the branch in
// translateToolsForCodex that skips a "web_search" function tool when web
// search is enabled (the builtin web search tool replaces it).
func TestTranslateToolsForCodex_WebSearchSkip(t *testing.T) {
	tools := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "web_search"}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "read_file", Description: "Reads a file"}},
		{Type: "notfunction", Function: ToolFunctionDefinition{Name: "ignored"}},
	}
	out := translateToolsForCodex(tools, true)
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (1 function + builtin web_search)", len(out))
	}
	// There should be exactly one real function tool (web_search been skipped).
	fns := 0
	for _, o := range out {
		if o.OfFunction != nil {
			fns++
			if o.OfFunction.Name != "read_file" {
				t.Errorf("function tool name = %q, want read_file", o.OfFunction.Name)
			}
			if o.OfFunction.Description.Value != "Reads a file" {
				t.Errorf("description not propagated")
			}
		}
	}
	if fns != 1 {
		t.Errorf("function tools = %d, want 1", fns)
	}
}

// TestParseCodexResponse_IncompleteStatus covers the finishReason == "length"
// branch when the response status is "incomplete" but no finishReason would
// otherwise be tool_calls.
func TestParseCodexResponse_IncompleteStatus(t *testing.T) {
	respJSON := `{
		"id": "resp_inc",
		"object": "response",
		"status": "incomplete",
		"output": [
			{"id": "m", "type": "message", "role": "assistant", "status": "completed",
			 "content": [{"type": "output_text", "text": "partial"}]}
		]
	}`
	var resp responses.Response
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	res := parseCodexResponse(&resp)
	if res.FinishReason != "length" {
		t.Errorf("FinishReason = %q, want length", res.FinishReason)
	}
	if res.Content != "partial" {
		t.Errorf("Content = %q", res.Content)
	}
}

// TestBuildCodexParams_ToolRoleMessages pushes a full sweep through
// buildCodexParams including the "tool" role branch, invalid tool call skip,
// and the tool output item mapping.
func TestBuildCodexParams_ToolRoleSweep(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "run it"},
		{Role: "assistant", Content: "using tool", ToolCalls: []ToolCall{
			{ID: "call_good", Name: "read_file", Arguments: map[string]any{"p": "/x"}},
			{ID: "call_bad", Name: "", Arguments: nil},
		}},
		{Role: "tool", Content: "file contents", ToolCallID: "call_good"},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{}, false)
	if params.Input.OfInputItemList == nil {
		t.Fatal("input items nil")
	}
	// user(1) + assistant content(1) + good function call(1) + tool output(1) = 4
	if len(params.Input.OfInputItemList) != 4 {
		t.Errorf("input items = %d, want 4", len(params.Input.OfInputItemList))
	}
}

// TestResolveProviderSelection_VLLMNoKeyError reaches the final
// providerTypeHTTPCompat "no API key configured" guard: with VLLM.APIBase set
// but no API key, the model-fallback VLLM case fills apiBase while apiKey stays
// empty, so the function returns a "no API key" error.
func TestResolveProviderSelection_VLLMNoKeyError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.VLLM.APIBase = "http://vllm:8000/v1"
	cfg.Providers.VLLM.APIKey = ""
	_, err := resolveProviderSelectionByName(cfg, "", "generic-model")
	if err == nil {
		t.Fatal("expected 'no API key' error for VLLM base without key")
	}
}