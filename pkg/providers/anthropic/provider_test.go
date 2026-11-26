package anthropicprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func TestBuildParams_BasicMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
	}
	params, err := buildParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"max_tokens": 1024,
	})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if string(params.Model) != "claude-sonnet-4-5-20250929" {
		t.Errorf("Model = %q, want %q", params.Model, "claude-sonnet-4-5-20250929")
	}
	if params.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d, want 1024", params.MaxTokens)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(params.Messages))
	}
}

func TestBuildParams_SystemMessage(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hi"},
	}
	params, err := buildParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if len(params.System) != 1 {
		t.Fatalf("len(System) = %d, want 1", len(params.System))
	}
	if params.System[0].Text != "You are helpful" {
		t.Errorf("System[0].Text = %q, want %q", params.System[0].Text, "You are helpful")
	}
	if len(params.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(params.Messages))
	}
}

func TestBuildParams_ToolCallMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{
					ID:        "call_1",
					Name:      "get_weather",
					Arguments: map[string]interface{}{"city": "SF"},
				},
			},
		},
		{Role: "tool", Content: `{"temp": 72}`, ToolCallID: "call_1"},
	}
	params, err := buildParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if len(params.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(params.Messages))
	}
}

func TestBuildParams_WithTools(t *testing.T) {
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{"type": "string"},
					},
					"required": []interface{}{"city"},
				},
			},
		},
	}
	params, err := buildParams([]Message{{Role: "user", Content: "Hi"}}, tools, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if len(params.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(params.Tools))
	}
}

func TestBuildParams_WithVisionImage(t *testing.T) {
	params, err := buildParams([]Message{{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "Describe this image"},
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,abcd"}},
		},
	}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	data, err := json.Marshal(params.Messages)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	jsonBody := string(data)
	if !strings.Contains(jsonBody, `"type":"image"`) {
		t.Fatalf("expected image block in anthropic payload: %s", jsonBody)
	}
	if !strings.Contains(jsonBody, `"media_type":"image/png"`) {
		t.Fatalf("expected image/png media type in payload: %s", jsonBody)
	}
}

func TestBuildAnthropicContentBlocks_DropsVideoParts(t *testing.T) {
	textParts := []protocoltypes.ContentPart{
		{Type: "text", Text: "Describe this"},
	}
	withVideoParts := []protocoltypes.ContentPart{
		{Type: "text", Text: "Describe this"},
		{Type: "video_url", VideoURL: &protocoltypes.VideoURL{URL: "https://example.com/clip.mp4", FPS: 2}},
	}

	textOnly := buildAnthropicContentBlocks(Message{Role: "user", ContentParts: textParts})
	withVideo := buildAnthropicContentBlocks(Message{Role: "user", ContentParts: withVideoParts})

	if len(textOnly) == 0 {
		t.Fatal("expected at least one block for the text-only message")
	}
	if len(withVideo) != len(textOnly) {
		t.Fatalf("video part changed block count: got %d, want %d", len(withVideo), len(textOnly))
	}

	data, err := json.Marshal(withVideo)
	if err != nil {
		t.Fatalf("marshal blocks: %v", err)
	}
	if strings.Contains(string(data), "video") {
		t.Fatalf("video content leaked into anthropic payload: %s", data)
	}
}

func TestParseResponse_TextOnly(t *testing.T) {
	resp := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{},
		Usage: anthropic.Usage{
			InputTokens:  10,
			OutputTokens: 20,
		},
	}
	result := parseResponse(resp)
	if result.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", result.Usage.CompletionTokens)
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, "stop")
	}
}

func TestParseResponse_StopReasons(t *testing.T) {
	tests := []struct {
		stopReason anthropic.StopReason
		want       string
	}{
		{anthropic.StopReasonEndTurn, "stop"},
		{anthropic.StopReasonMaxTokens, "length"},
		{anthropic.StopReasonToolUse, "tool_calls"},
	}
	for _, tt := range tests {
		resp := &anthropic.Message{
			StopReason: tt.stopReason,
		}
		result := parseResponse(resp)
		if result.FinishReason != tt.want {
			t.Errorf("StopReason %q: FinishReason = %q, want %q", tt.stopReason, result.FinishReason, tt.want)
		}
	}
}

func TestProvider_ChatRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := map[string]interface{}{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"model":       reqBody["model"],
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": "Hello! How can I help you?"},
			},
			"usage": map[string]interface{}{
				"input_tokens":  15,
				"output_tokens": 8,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewProviderWithClient(createAnthropicTestClient(server.URL, "test-token"))
	messages := []Message{{Role: "user", Content: "Hello"}}
	resp, err := provider.Chat(t.Context(), messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! How can I help you?")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage.PromptTokens != 15 {
		t.Errorf("PromptTokens = %d, want 15", resp.Usage.PromptTokens)
	}
}

func TestProvider_GetDefaultModel(t *testing.T) {
	p := NewProvider("test-token")
	if got := p.GetDefaultModel(); got != "claude-sonnet-4-5-20250929" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "claude-sonnet-4-5-20250929")
	}
}

func TestProvider_NewProviderWithBaseURL_NormalizesV1Suffix(t *testing.T) {
	p := NewProviderWithBaseURL("token", "https://api.anthropic.com/v1/")
	if got := p.BaseURL(); got != "https://api.anthropic.com" {
		t.Fatalf("BaseURL() = %q, want %q", got, "https://api.anthropic.com")
	}
}

func TestProvider_ChatUsesTokenSource(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		atomic.AddInt32(&requests, 1)

		if got := r.Header.Get("Authorization"); got != "Bearer refreshed-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := map[string]interface{}{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"model":       reqBody["model"],
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": "ok"},
			},
			"usage": map[string]interface{}{
				"input_tokens":  1,
				"output_tokens": 1,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProviderWithTokenSourceAndBaseURL("stale-token", func() (string, error) {
		return "refreshed-token", nil
	}, server.URL)

	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func createAnthropicTestClient(baseURL, token string) *anthropic.Client {
	c := anthropic.NewClient(
		anthropicoption.WithAuthToken(token),
		anthropicoption.WithBaseURL(baseURL),
	)
	return &c
}

func writeAnthropicTestResponse(w http.ResponseWriter, r *http.Request) {
	var reqBody map[string]interface{}
	json.NewDecoder(r.Body).Decode(&reqBody)

	resp := map[string]interface{}{
		"id":          "msg_test",
		"type":        "message",
		"role":        "assistant",
		"model":       reqBody["model"],
		"stop_reason": "end_turn",
		"content": []map[string]interface{}{
			{"type": "text", "text": "ok"},
		},
		"usage": map[string]interface{}{
			"input_tokens":  1,
			"output_tokens": 1,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func TestProviderOAuthModeHeadersAndBetaQuery(t *testing.T) {
	var captured *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		writeAnthropicTestResponse(w, r)
	}))
	defer server.Close()

	p := NewProviderWithTokenSourceBaseURLAndOAuthMode("t", func() (string, error) { return "tok", nil }, server.URL, true)
	if !p.OAuthMode() {
		t.Fatal("OAuthMode() = false, want true")
	}

	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{}); err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if captured == nil {
		t.Fatal("no request captured")
	}

	if got := captured.URL.Query().Get("beta"); got != "true" {
		t.Errorf("beta query = %q, want %q", got, "true")
	}
	if got := captured.Header.Get("Anthropic-Beta"); got != oauthBetaHeaders {
		t.Errorf("anthropic-beta header = %q, want %q", got, oauthBetaHeaders)
	}
	if got := captured.Header.Get("User-Agent"); got != claudeCLIUserAgent {
		t.Errorf("User-Agent = %q, want %q", got, claudeCLIUserAgent)
	}
}

func TestProviderNonOAuthOmitsBetaMarkers(t *testing.T) {
	var captured *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		writeAnthropicTestResponse(w, r)
	}))
	defer server.Close()

	p := NewProviderWithTokenSourceAndBaseURL("t", func() (string, error) { return "tok", nil }, server.URL)
	if p.OAuthMode() {
		t.Fatal("OAuthMode() = true, want false")
	}

	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hello"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{}); err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if captured == nil {
		t.Fatal("no request captured")
	}

	if raw, ok := captured.URL.Query()["beta"]; ok {
		t.Errorf("beta query present = %v, want absent", raw)
	}
	if got := captured.Header.Get("Anthropic-Beta"); got != "" {
		t.Errorf("anthropic-beta header = %q, want empty", got)
	}
	ua := captured.Header.Get("User-Agent")
	if !strings.HasPrefix(ua, "Anthropic/Go") {
		t.Errorf("User-Agent = %q, want prefix %q", ua, "Anthropic/Go")
	}
}

func TestProviderOAuthModePrependsIdentityBlock(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hi"},
	}

	oauth := NewProviderWithTokenSourceBaseURLAndOAuthMode("t", func() (string, error) { return "tok", nil }, "http://example.invalid", true)
	params, err := oauth.buildParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if len(params.System) != 2 {
		t.Fatalf("len(System) = %d, want 2", len(params.System))
	}
	if params.System[0].Text != claudeIdentityLine {
		t.Errorf("System[0].Text = %q, want %q", params.System[0].Text, claudeIdentityLine)
	}
	if params.System[1].Text != "You are helpful" {
		t.Errorf("System[1].Text = %q, want %q", params.System[1].Text, "You are helpful")
	}

	plain := NewProviderWithTokenSourceAndBaseURL("t", func() (string, error) { return "tok", nil }, "http://example.invalid")
	params, err = plain.buildParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if len(params.System) != 1 {
		t.Fatalf("len(System) = %d, want 1", len(params.System))
	}
	if params.System[0].Text != "You are helpful" {
		t.Errorf("System[0].Text = %q, want %q", params.System[0].Text, "You are helpful")
	}
}
func TestTranslateToolsRequiredSurvives(t *testing.T) {
	tests := []struct {
		name       string
		parameters map[string]interface{}
		wantJSON   []string
	}{
		{
			name: "required as []string from built-in tools",
			parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
					"text": map[string]interface{}{"type": "string"},
				},
				"required": []string{"path", "text"},
			},
			wantJSON: []string{`"required":["path","text"]`},
		},
		{
			name: "required as []interface{} from MCP tools",
			parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{"type": "string"},
				},
				"required": []interface{}{"path"},
			},
			wantJSON: []string{`"required":["path"]`},
		},
		{
			name: "extra schema keywords are forwarded",
			parameters: map[string]interface{}{
				"type":                 "object",
				"properties":           map[string]interface{}{"path": map[string]interface{}{"type": "string"}},
				"required":             []string{"path"},
				"additionalProperties": false,
				"$defs":                map[string]interface{}{"Opts": map[string]interface{}{"type": "object"}},
				"anyOf":                []interface{}{map[string]interface{}{"type": "object"}},
			},
			wantJSON: []string{
				`"required":["path"]`,
				`"additionalProperties":false`,
				`"$defs":{"Opts":{"type":"object"}}`,
				`"anyOf":[{"type":"object"}]`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := []ToolDefinition{{
				Type: "function",
				Function: ToolFunctionDefinition{
					Name:        "some_tool",
					Description: "does things",
					Parameters:  tt.parameters,
				},
			}}

			params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, tools, "claude-sonnet-4-5-20250929", map[string]interface{}{})
			if err != nil {
				t.Fatalf("buildParams() error: %v", err)
			}
			data, err := json.Marshal(params.Tools)
			if err != nil {
				t.Fatalf("marshal tools: %v", err)
			}
			wire := string(data)
			for _, want := range tt.wantJSON {
				if !strings.Contains(wire, want) {
					t.Fatalf("tool schema missing %s: %s", want, wire)
				}
			}
			if !strings.Contains(wire, `"type":"object"`) {
				t.Fatalf("tool schema lost its object type: %s", wire)
			}
		})
	}
}

func TestBuildParams_ThinkingEnabled(t *testing.T) {
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6", map[string]interface{}{
		"max_tokens": 1024,
		"reasoning": map[string]interface{}{
			"enabled": true,
			"effort":  "high",
		},
	})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.Thinking.OfAdaptive == nil {
		t.Fatalf("thinking = %#v, want adaptive", params.Thinking)
	}
	if params.OutputConfig.Effort != anthropic.OutputConfigEffortHigh {
		t.Fatalf("output_config.effort = %q, want %q", params.OutputConfig.Effort, anthropic.OutputConfigEffortHigh)
	}

	wire := marshalParams(t, params)
	for _, want := range []string{`"thinking":{"type":"adaptive"}`, `"output_config":{"effort":"high"}`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("request body missing %s: %s", want, wire)
		}
	}
}

func TestBuildParams_ThinkingEnabledDefaultEffort(t *testing.T) {
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6", map[string]interface{}{
		"reasoning": map[string]interface{}{"enabled": true},
	})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.Thinking.OfAdaptive == nil {
		t.Fatalf("thinking = %#v, want adaptive", params.Thinking)
	}
	if params.OutputConfig.Effort != anthropic.OutputConfigEffortHigh {
		t.Fatalf("output_config.effort = %q, want the %q default", params.OutputConfig.Effort, anthropic.OutputConfigEffortHigh)
	}
}

func TestBuildParams_ThinkingDisabled(t *testing.T) {
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6", map[string]interface{}{
		"reasoning": map[string]interface{}{"enabled": false, "effort": "high"},
	})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.Thinking.OfDisabled == nil {
		t.Fatalf("thinking = %#v, want disabled", params.Thinking)
	}
	if params.OutputConfig.Effort != "" {
		t.Fatalf("output_config.effort = %q, want unset when thinking is disabled", params.OutputConfig.Effort)
	}
	wire := marshalParams(t, params)
	if !strings.Contains(wire, `"thinking":{"type":"disabled"}`) {
		t.Fatalf("request body missing the disable marker: %s", wire)
	}
	if strings.Contains(wire, `"output_config"`) {
		t.Fatalf("request body must not carry output_config when thinking is disabled: %s", wire)
	}
}

func TestBuildParams_NoReasoning(t *testing.T) {
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6", map[string]interface{}{
		"max_tokens": 1024,
	})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.Thinking.OfAdaptive != nil || params.Thinking.OfDisabled != nil || params.Thinking.OfEnabled != nil {
		t.Fatalf("thinking = %#v, want no thinking config", params.Thinking)
	}
	if params.OutputConfig.Effort != "" {
		t.Fatalf("output_config.effort = %q, want unset", params.OutputConfig.Effort)
	}
	wire := marshalParams(t, params)
	if strings.Contains(wire, `"thinking"`) || strings.Contains(wire, `"output_config"`) {
		t.Fatalf("request body must omit thinking/output_config without a reasoning option: %s", wire)
	}
}

func marshalParams(t *testing.T, params anthropic.MessageNewParams) string {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return string(data)
}
func TestBuildParams_ThinkingFloorsXhighEffort(t *testing.T) {
	tests := []struct {
		name   string
		effort string
		want   anthropic.OutputConfigEffort
	}{
		{name: "xhigh floors to high", effort: "xhigh", want: anthropic.OutputConfigEffortHigh},
		{name: "max stays max", effort: "max", want: anthropic.OutputConfigEffortMax},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-6", map[string]interface{}{
				"reasoning": map[string]interface{}{"enabled": true, "effort": tc.effort},
			})
			if err != nil {
				t.Fatalf("buildParams() error: %v", err)
			}
			if params.Thinking.OfAdaptive == nil {
				t.Fatalf("thinking = %#v, want adaptive", params.Thinking)
			}
			if params.OutputConfig.Effort != tc.want {
				t.Fatalf("output_config.effort = %q, want %q", params.OutputConfig.Effort, tc.want)
			}
			wire := marshalParams(t, params)
			if !strings.Contains(wire, `"output_config":{"effort":"`+string(tc.want)+`"}`) {
				t.Fatalf("request body missing output_config.effort=%s: %s", tc.want, wire)
			}
		})
	}
}
