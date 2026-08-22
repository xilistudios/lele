package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	anthropicprovider "github.com/xilistudios/lele/pkg/providers/anthropic"
)

func TestClaudeProvider_ChatRoundTrip(t *testing.T) {
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

	delegate := anthropicprovider.NewProviderWithClient(createAnthropicTestClient(server.URL, "test-token"))
	provider := newClaudeProviderWithDelegate(delegate)

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

func TestClaudeProvider_GetDefaultModel(t *testing.T) {
	p := NewClaudeProvider("test-token")
	if got := p.GetDefaultModel(); got != "claude-sonnet-4-5-20250929" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "claude-sonnet-4-5-20250929")
	}
}

func createAnthropicTestClient(baseURL, token string) *anthropic.Client {
	c := anthropic.NewClient(
		anthropicoption.WithAuthToken(token),
		anthropicoption.WithBaseURL(baseURL),
	)
	return &c
}
func TestClaudeProvider_NewClaudeProviderWithBaseURL(t *testing.T) {
	p := NewClaudeProviderWithBaseURL("token", "https://custom.example.com/v1")
	if p == nil || p.delegate == nil {
		t.Fatal("NewClaudeProviderWithBaseURL() returned nil provider/delegate")
	}
	if got := p.delegate.BaseURL(); got != "https://custom.example.com" {
		t.Errorf("BaseURL() = %q, want %q", got, "https://custom.example.com")
	}
}

func TestClaudeProvider_NewClaudeProviderWithTokenSource(t *testing.T) {
	p := NewClaudeProviderWithTokenSource("stale", func() (string, error) { return "fresh", nil })
	if p == nil || p.delegate == nil {
		t.Fatal("NewClaudeProviderWithTokenSource() returned nil provider/delegate")
	}
	// Verifying the token source is wired: a failing token source must surface
	// its error through Chat.
	p2 := NewClaudeProviderWithTokenSource("stale", func() (string, error) {
		return "", fmt.Errorf("refresh boom")
	})
	_, err := p2.Chat(context.Background(), nil, nil, "test-model", nil)
	if err == nil {
		t.Fatal("expected token source error from Chat")
	}
	if !strings.Contains(err.Error(), "refreshing token") {
		t.Errorf("error = %v, want refreshing token error", err)
	}
}

func TestClaudeProvider_NewClaudeProviderWithTokenSourceAndBaseURL(t *testing.T) {
	p := NewClaudeProviderWithTokenSourceAndBaseURL("stale", func() (string, error) { return "fresh", nil }, "https://custom.example.com")
	if p == nil || p.delegate == nil {
		t.Fatal("NewClaudeProviderWithTokenSourceAndBaseURL() returned nil provider/delegate")
	}
	if got := p.delegate.BaseURL(); got != "https://custom.example.com" {
		t.Errorf("BaseURL() = %q, want %q", got, "https://custom.example.com")
	}
}

func TestClaudeProvider_Chat_TokenSourceError(t *testing.T) {
	// Surface token source error through the delegate chain.
	p := NewClaudeProviderWithTokenSource("stale", func() (string, error) {
		return "", fmt.Errorf("no token")
	})
	_, err := p.Chat(context.Background(), nil, nil, "test-model", nil)
	if err == nil {
		t.Fatal("expected token source error from Chat")
	}
}
