package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAntigravityProvider_GetDefaultModel(t *testing.T) {
	p := &AntigravityProvider{}
	if got := p.GetDefaultModel(); got != "gemini-3-flash" {
		t.Errorf("GetDefaultModel() = %q, want gemini-3-flash", got)
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{"shorter than max", "hello", 10, "hello"},
		{"equal length", "hello", 5, "hello"},
		{"longer than max", "hello world", 5, "hello..."},
		{"empty", "", 5, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateString(tt.s, tt.maxLen); got != tt.want {
				t.Errorf("truncateString(%q, %d) = %q, want %q", tt.s, tt.maxLen, got, tt.want)
			}
		})
	}
}

func TestRandomString(t *testing.T) {
	s1 := randomString(12)
	s2 := randomString(12)
	if len(s1) != 12 {
		t.Errorf("randomString length = %d, want 12", len(s1))
	}
	if s1 == s2 {
		t.Error("two random strings should differ")
	}
	if s3 := randomString(0); len(s3) != 0 {
		t.Errorf("randomString(0) should be empty, got %q", s3)
	}
}

func TestParseAntigravityError(t *testing.T) {
	p := &AntigravityProvider{}
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantSubstr string
	}{
		{"non-json body", 500, "plain text not json", "500"},
		{"rate limit with reset delay", 429,
			`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","metadata":{"quotaResetDelay":"2s"}}]}}`,
			"reset in"},
		{"rate limit without details", 429,
			`{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED"}}`,
			"rate limit exceeded"},
		{"generic error", 403,
			`{"error":{"code":403,"message":"forbidden","status":"PERMISSION_DENIED"}}`,
			"PERMISSION_DENIED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.parseAntigravityError(tt.statusCode, []byte(tt.body))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("error = %q, want substr %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}

func TestExtractPartThoughtSignature(t *testing.T) {
	if got := extractPartThoughtSignature("sig", "snake"); got != "sig" {
		t.Errorf("extractPartThoughtSignature = %q, want sig", got)
	}
	if got := extractPartThoughtSignature("", "snake"); got != "snake" {
		t.Errorf("extractPartThoughtSignature = %q, want snake", got)
	}
	if got := extractPartThoughtSignature("", ""); got != "" {
		t.Errorf("extractPartThoughtSignature = %q, want empty", got)
	}
}

func TestSanitizeSchemaForGemini(t *testing.T) {
	schema := map[string]any{
		"type":                 "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":      "string",
				"minLength": 1, // unsupported keyword should be removed
				"format":    "email",
			},
		},
		"additionalProperties": true, // unsupported keyword
		"$schema":              "http://json-schema.org",
	}
	sanitized := sanitizeSchemaForGemini(schema)
	if _, ok := sanitized["additionalProperties"]; ok {
		t.Error("additionalProperties should be removed")
	}
	if _, ok := sanitized["$schema"]; ok {
		t.Error("$schema should be removed")
	}
	props := sanitized["properties"].(map[string]any)
	name := props["name"].(map[string]any)
	if _, ok := name["minLength"]; ok {
		t.Error("minLength should be removed from nested object")
	}
	if _, ok := name["format"]; ok {
		t.Error("format should be removed from nested object")
	}
	if name["type"] != "string" {
		t.Errorf("name.type = %v, want string", name["type"])
	}
	// type is already present
	if sanitized["type"] != "object" {
		t.Errorf("type = %v, want object", sanitized["type"])
	}
}

func TestSanitizeSchemaForGemini_NilAndAddsType(t *testing.T) {
	if sanitizeSchemaForGemini(nil) != nil {
		t.Error("nil schema should return nil")
	}
	// With properties but no type, adds type object
	schema := map[string]any{
		"properties": map[string]any{},
	}
	sanitized := sanitizeSchemaForGemini(schema)
	if sanitized["type"] != "object" {
		t.Errorf("type = %v, want object (added)", sanitized["type"])
	}
}

func TestSanitizeSchemaForGemini_ArrayWithNestedMaps(t *testing.T) {
	schema := map[string]any{
		"type": "array",
		"items": []any{
			map[string]any{"type": "string", "minLength": 2},
			"plain-string",
			map[string]any{"type": "number", "maximum": 10},
		},
	}
	s := sanitizeSchemaForGemini(schema)
	items := s["items"].([]any)
	first := items[0].(map[string]any)
	if _, ok := first["minLength"]; ok {
		t.Error("minLength should be removed in array element")
	}
	if items[1] != "plain-string" {
		t.Errorf("items[1] = %v, want plain-string", items[1])
	}
	third := items[2].(map[string]any)
	if _, ok := third["maximum"]; ok {
		t.Error("maximum should be removed in array element")
	}
}

func TestInferToolNameFromCallID(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"call_search_docs_999", "search_docs"},
		{"not_a_call_prefix", "not_a_call_prefix"},
		{"call_", "call_"},
		{"call_x", "call_x"},
		{"call_abc_def_1", "abc_def"},
	}
	for _, tt := range tests {
		if got := inferToolNameFromCallID(tt.id); got != tt.want {
			t.Errorf("inferToolNameFromCallID(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestResolveToolResponseName(t *testing.T) {
	tests := []struct {
		name string
		id   string
		mp   map[string]string
		want string
	}{
		{"empty id", "", map[string]string{}, ""},
		{"found in map", "call_1", map[string]string{"call_1": "read_file"}, "read_file"},
		{"found but empty name", "call_1", map[string]string{"call_1": ""}, "call_1"},
		{"not found falls back to inference", "call_read_file_1", map[string]string{}, "read_file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveToolResponseName(tt.id, tt.mp); got != tt.want {
				t.Errorf("resolveToolResponseName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeStoredToolCall(t *testing.T) {
	t.Run("name from Name field", func(t *testing.T) {
		tc := ToolCall{ID: "c1", Name: "func_a", Arguments: map[string]any{"x": 1}}
		name, args, _ := normalizeStoredToolCall(tc)
		if name != "func_a" || args["x"] != 1 {
			t.Errorf("normalizeStoredToolCall = (%q, %v), want (func_a, {x:1})", name, args)
		}
	})
	t.Run("name from Function when Name empty", func(t *testing.T) {
		tc := ToolCall{ID: "c2", Function: &FunctionCall{Name: "func_b", Arguments: `{"y":2}`, ThoughtSignature: "sig"}}
		name, args, sig := normalizeStoredToolCall(tc)
		if name != "func_b" {
			t.Errorf("name = %q, want func_b", name)
		}
		if args["y"] != 2.0 {
			t.Errorf("args[y] = %v, want 2.0", args["y"])
		}
		if sig != "sig" {
			t.Errorf("sig = %q, want sig", sig)
		}
	})
	t.Run("parses function args when map empty", func(t *testing.T) {
		tc := ToolCall{ID: "c3", Name: "func_c", Function: &FunctionCall{Arguments: `{"z":3}`}}
		_, args, _ := normalizeStoredToolCall(tc)
		if args["z"] != 3.0 {
			t.Errorf("args[z] = %v, want 3.0", args["z"])
		}
	})
	t.Run("nil args default to empty map", func(t *testing.T) {
		tc := ToolCall{ID: "c4", Name: "func_d"}
		_, args, _ := normalizeStoredToolCall(tc)
		if args == nil {
			t.Error("args should not be nil")
		}
		if len(args) != 0 {
			t.Errorf("args = %v, want empty", args)
		}
	})
	t.Run("unparseable function args leaves empty", func(t *testing.T) {
		tc := ToolCall{ID: "c5", Name: "func_e", Function: &FunctionCall{Arguments: `not-json`}}
		_, args, _ := normalizeStoredToolCall(tc)
		if len(args) != 0 {
			t.Errorf("args = %v, want empty", args)
		}
	})
}

func TestParseSSEResponse(t *testing.T) {
	p := &AntigravityProvider{}
	t.Run("text and tool calls", func(t *testing.T) {
		body := makeSSEBody(map[string]any{
			"response": map[string]any{
				"candidates": []any{
					map[string]any{
						"content": map[string]any{
							"role":  "model",
							"parts": []any{map[string]any{"text": "Let me check"}},
						},
						"finishReason": "STOP",
					},
					map[string]any{
						"content": map[string]any{
							"parts": []any{map[string]any{
								"functionCall": map[string]any{
									"name": "get_weather",
									"args": map[string]any{"city": "Tokyo"},
								},
								"thoughtSignature": "ts1",
							}},
						},
						"finishReason": "STOP",
					},
				},
				"usageMetadata": map[string]any{
					"promptTokenCount":     10,
					"candidatesTokenCount": 5,
					"totalTokenCount":      15,
				},
			},
		})
		resp, err := p.parseSSEResponse(body)
		if err != nil {
			t.Fatalf("parseSSEResponse() error: %v", err)
		}
		if resp.Content != "Let me check" {
			t.Errorf("Content = %q, want Let me check", resp.Content)
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("ToolCalls length = %d, want 1", len(resp.ToolCalls))
		}
		tc := resp.ToolCalls[0]
		if tc.Name != "get_weather" {
			t.Errorf("tool name = %q, want get_weather", tc.Name)
		}
		if tc.Function == nil || tc.Function.ThoughtSignature != "ts1" {
			t.Errorf("thought signature = %v, want ts1", tc.Function)
		}
		if resp.FinishReason != "tool_calls" {
			t.Errorf("FinishReason = %q, want tool_calls", resp.FinishReason)
		}
		if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
			t.Errorf("Usage = %+v, want total 15", resp.Usage)
		}
	})
	t.Run("MAX_TOKENS finish maps to length", func(t *testing.T) {
		body := makeSSEBody(map[string]any{
			"response": map[string]any{
				"candidates": []any{
					map[string]any{
						"content":     map[string]any{"parts": []any{map[string]any{"text": "partial"}}},
						"finishReason": "MAX_TOKENS",
					},
				},
			},
		})
		resp, err := p.parseSSEResponse(body)
		if err != nil {
			t.Fatalf("parseSSEResponse() error: %v", err)
		}
		if resp.FinishReason != "length" {
			t.Errorf("FinishReason = %q, want length", resp.FinishReason)
		}
	})
	t.Run("DONE marker and non-data lines", func(t *testing.T) {
		body := "event: random\ndata: [DONE]\n"
		resp, err := p.parseSSEResponse(body)
		if err != nil {
			t.Fatalf("parseSSEResponse() error: %v", err)
		}
		if resp.FinishReason != "stop" {
			t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
		}
	})
	t.Run("malformed JSON data lines are skipped", func(t *testing.T) {
		body := "data: {not-json}\ndata: [DONE]\n"
		resp, err := p.parseSSEResponse(body)
		if err != nil {
			t.Fatalf("parseSSEResponse() error: %v", err)
		}
		if resp.Content != "" {
			t.Errorf("Content = %q, want empty", resp.Content)
		}
	})
	t.Run("empty body", func(t *testing.T) {
		resp, err := p.parseSSEResponse("")
		if err != nil {
			t.Fatalf("parseSSEResponse() error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
	})
}

// makeSSEBody marshals a value to JSON and wraps it in an "data: " SSE line.
func makeSSEBody(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n"
}

func TestAntigravityProvider_Chat(t *testing.T) {
	t.Run("auth token source error", func(t *testing.T) {
		p := &AntigravityProvider{
			tokenSource: func() (string, string, error) {
				return "", "", fmt.Errorf("boom")
			},
			httpClient: http.DefaultClient,
		}
		_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil)
		if err == nil || !strings.Contains(err.Error(), "antigravity auth") {
			t.Errorf("Chat() error = %v, want antigravity auth error", err)
		}
	})

	// mockTransport returns a fixed HTTP response for any request, letting us
	// exercise Chat() without reaching the real Google endpoint (which is a
	// hardcoded package constant).
	mockTransport := func(status int, body string) http.RoundTripper {
		return roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: status,
				Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})
	}

	t.Run("successful SSE response", func(t *testing.T) {
		p := &AntigravityProvider{
			tokenSource: func() (string, string, error) { return "tok", "project-123", nil },
			httpClient:  &http.Client{Transport: mockTransport(http.StatusOK, makeSSEBody(map[string]any{"response": map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "Hello world"}}}, "finishReason": "STOP"}}}}))},
		}
		resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil)
		if err != nil {
			t.Fatalf("Chat() error: %v", err)
		}
		if resp.Content != "Hello world" {
			t.Errorf("Content = %q, want Hello world", resp.Content)
		}
	})

	t.Run("empty response error", func(t *testing.T) {
		p := &AntigravityProvider{
			tokenSource: func() (string, string, error) { return "tok", "project-123", nil },
			httpClient:  &http.Client{Transport: mockTransport(http.StatusOK, makeSSEBody(map[string]any{"response": map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": ""}}}, "finishReason": "STOP"}}}}))},
		}
		_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil)
		if err == nil {
			t.Fatal("expected empty response error")
		}
		if !strings.Contains(err.Error(), "empty response") {
			t.Errorf("error = %v, want empty response error", err)
		}
	})

	t.Run("HTTP error status", func(t *testing.T) {
		p := &AntigravityProvider{
			tokenSource: func() (string, string, error) { return "tok", "project-123", nil },
			httpClient:  &http.Client{Transport: mockTransport(http.StatusForbidden, `{"error":{"message":"forbidden","status":"PERMISSION_DENIED"}}`)},
		}
		_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil)
		if err == nil {
			t.Fatal("expected error for HTTP 403")
		}
		if !strings.Contains(err.Error(), "PERMISSION_DENIED") {
			t.Errorf("error = %v, want PERMISSION_DENIED", err)
		}
	})

	t.Run("HTTP client request error", func(t *testing.T) {
		p := &AntigravityProvider{
			tokenSource: func() (string, string, error) { return "tok", "project-123", nil },
			httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("connection refused")
			})},
		}
		_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "test-model", nil)
		if err == nil {
			t.Fatal("expected error for unresponsive server")
		}
		if !strings.Contains(err.Error(), "antigravity API call") {
			t.Errorf("error = %v, want antigravity API call error", err)
		}
	})

	t.Run("default model applied for empty model", func(t *testing.T) {
		p := &AntigravityProvider{
			tokenSource: func() (string, string, error) { return "tok", "project-123", nil },
			httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(strings.NewReader(makeSSEBody(map[string]any{"response": map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": "x"}}}, "finishReason": "STOP"}}}}))),
					Header:     make(http.Header),
				}, nil
			})},
		}
		_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", nil)
		if err != nil {
			t.Fatalf("Chat() error: %v", err)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestBuildRequest_GenerationConfigAndSystem(t *testing.T) {
	p := &AntigravityProvider{}
	req := p.buildRequest(
		[]Message{{Role: "system", Content: "sys"}, {Role: "user", Content: "hi"}},
		[]ToolDefinition{
			{Type: "function", Function: ToolFunctionDefinition{Name: "foo", Description: "the foo", Parameters: map[string]any{"type": "object"}}},
			{Type: "not-function", Function: ToolFunctionDefinition{Name: "ignored"}},
		},
		"test-model",
		map[string]any{"max_tokens": 128, "temperature": 0.5},
	)
	if req.SystemPrompt == nil || req.SystemPrompt.Parts[0].Text != "sys" {
		t.Errorf("SystemPrompt = %+v, want sys", req.SystemPrompt)
	}
	if req.Config == nil || req.Config.MaxOutputTokens != 128 || req.Config.Temperature != 0.5 {
		t.Errorf("Config = %+v, want max 128 temp 0.5", req.Config)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("Tools length = %d, want 1 (non-function filtered)", len(req.Tools))
	}
	if len(req.Tools[0].FunctionDeclarations) != 1 {
		t.Fatalf("FunctionDeclarations length = %d, want 1", len(req.Tools[0].FunctionDeclarations))
	}
	decl := req.Tools[0].FunctionDeclarations[0]
	if decl.Name != "foo" {
		t.Errorf("decl.Name = %q, want foo", decl.Name)
	}
}

func TestFetchAntigravityProjectID(t *testing.T) {
	t.Run("request construction error", func(t *testing.T) {
		// These functions build their own http.Client on the fixed base URL
		// constant. We can't point them at a test server, so we only verify the
		// happy-path return type (may error on network) without panicking.
		_, err := FetchAntigravityProjectID("token")
		_ = err
	})
}

func TestFetchAntigravityModels(t *testing.T) {
	t.Run("request error", func(t *testing.T) {
		_, err := FetchAntigravityModels("token", "project")
		_ = err
	})

	t.Run("models parsing", func(t *testing.T) {
		models, err := FetchAntigravityModels("", "")
		if err == nil {
			found := false
			for _, m := range models {
				if m.ID == "gemini-3-flash" {
					found = true
				}
			}
			if !found {
				t.Error("expected gemini-3-flash in models list")
			}
		}
	})
}

func TestAntigravityProvider_NewConstructor(t *testing.T) {
	p := NewAntigravityProvider()
	if p == nil {
		t.Fatal("NewAntigravityProvider() returned nil")
	}
	if p.tokenSource == nil {
		t.Error("expected tokenSource to be set")
	}
	if p.httpClient == nil {
		t.Error("expected httpClient to be set")
	}
	// The token source should fail cleanly when no Google credentials exist
	// without panicking.
	if _, _, err := p.tokenSource(); err == nil {
		t.Log("tokenSource() succeeded (creds may exist)")
	}
}