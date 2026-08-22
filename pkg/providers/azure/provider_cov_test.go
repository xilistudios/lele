package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProviderChat_AzureUsesTemperature covers the temperature option branch.
func TestProviderChat_AzureUsesTemperature(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		writeValidResponse(w)
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"deployment",
		map[string]any{"temperature": 0.7},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if temp, ok := requestBody["temperature"].(float64); !ok || temp != 0.7 {
		t.Errorf("temperature = %v, want 0.7", requestBody["temperature"])
	}
}

// TestProviderChat_AzureIncludesTools covers tools + tool_choice branch.
func TestProviderChat_AzureIncludesTools(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		writeValidResponse(w)
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, "")
	tools := []ToolDefinition{{}}
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		tools,
		"deployment",
		nil,
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if _, has := requestBody["tools"]; !has {
		t.Error("expected tools in request body")
	}
	if requestBody["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", requestBody["tool_choice"])
	}
}

// TestProviderChat_AzureServerErrorNonJSON covers HandleErrorResponse non-200 path with plain body.
func TestProviderChat_AzureServerErrorPlainBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gateway timeout", http.StatusBadGateway)
	}))
	defer server.Close()

	p := NewProvider("bad-key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "deployment", nil)
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("expected 502 error, got %v", err)
	}
}

// TestProviderChat_AzureEmptyAPIKeyStillSendsNoApiKeyHeader.
func TestProviderChat_AzureNoAPIKeyHeader(t *testing.T) {
	var capturedAPIKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPIKey = r.Header.Get("Api-Key")
		writeValidResponse(w)
	}))
	defer server.Close()

	p := NewProvider("", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "deployment", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if capturedAPIKey != "" {
		t.Errorf("api-key header = %q, want empty", capturedAPIKey)
	}
}

// TestProviderChat_AzureParseUsageAndStop covers the happy path response parsing
// with a stop reason (ReadAndParseResponse + ParseResponse).
func TestProviderChat_AzureParseUsageAndStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "hello"}, "finish_reason": "length"},
			},
			"usage": map[string]any{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, "")
	out, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "deployment", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if out.Content != "hello" {
		t.Errorf("content = %q, want hello", out.Content)
	}
	if out.FinishReason != "truncated" {
		t.Errorf("finish_reason = %q, want truncated", out.FinishReason)
	}
	if out.Usage == nil || out.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", out.Usage)
	}
}

// TestProviderChat_AzureReqCreationError covers impossible-marshal? body is
// marshalable, but we can verify a malformed context request errors.
func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// TestProviderChat_AzureUnterminatedContext covers a cancelled streaming context.
func TestProviderChat_AzureContextCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeValidResponse(w)
	}))
	defer server.Close()

	p := NewProvider("test-key", server.URL, "")
	ctx, cancel := contextWithCancel()
	cancel()
	_, err := p.Chat(ctx, []Message{{Role: "user", Content: "hi"}}, nil, "deployment", nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
