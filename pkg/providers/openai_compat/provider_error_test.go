// Lele - Ultra-lightweight personal AI agent
// Copyright (c) 2026 Lele contributors

package openai_compat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers/common"
)

// errorServer replies to any request with status and the given retry headers.
func errorServer(t *testing.T, status int, headers map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"please wait"}}`))
	}))
}

// Chat must hand the classifier structured data (status + Retry-After) instead
// of a formatted string, while keeping the message text it has always emitted.
func TestChat_NonOKReturnsAPIErrorWithRetryAfter(t *testing.T) {
	server := errorServer(t, http.StatusTooManyRequests, map[string]string{"Retry-After": "30"})
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error")
	}

	var apiErr *common.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Chat() error = %T, want *common.APIError", err)
	}
	if apiErr.HTTPStatus() != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus() = %d, want 429", apiErr.HTTPStatus())
	}
	if apiErr.RetryAfterHint() != 30*time.Second {
		t.Errorf("RetryAfterHint() = %v, want 30s", apiErr.RetryAfterHint())
	}
	// Message text is unchanged: no URL line, body rendered verbatim.
	if !strings.HasPrefix(err.Error(), "API request failed:\n  Status: 429\n  Body:   ") {
		t.Errorf("unexpected message: %q", err.Error())
	}
	if strings.Contains(err.Error(), " URL: ") {
		t.Errorf("Chat() error must not carry a URL line (historical format): %q", err.Error())
	}
}

func TestChatStream_NonOKReturnsAPIErrorWithRetryAfter(t *testing.T) {
	server := errorServer(t, http.StatusTooManyRequests, map[string]string{"retry-after-ms": "1500"})
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil,
		func(string, bool) {}, nil)
	if err == nil {
		t.Fatal("expected error")
	}

	var apiErr *common.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ChatStream() error = %T, want *common.APIError", err)
	}
	if apiErr.HTTPStatus() != http.StatusTooManyRequests {
		t.Errorf("HTTPStatus() = %d, want 429", apiErr.HTTPStatus())
	}
	if apiErr.RetryAfterHint() != 1500*time.Millisecond {
		t.Errorf("RetryAfterHint() = %v, want 1.5s", apiErr.RetryAfterHint())
	}
	if !strings.HasPrefix(err.Error(), "API request failed:\n  Status: 429\n  Body:   ") {
		t.Errorf("unexpected message: %q", err.Error())
	}
	if !strings.Contains(err.Error(), " URL: ") {
		t.Errorf("ChatStream() keeps its historical URL line, got %q", err.Error())
	}
}

// No Retry-After anywhere must mean "no hint", not a fabricated one.
func TestChat_NonOKWithoutRetryAfterHasZeroHint(t *testing.T) {
	server := errorServer(t, http.StatusBadGateway, nil)
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var apiErr *common.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Chat() error = %T, want *common.APIError", err)
	}
	if apiErr.RetryAfterHint() != 0 {
		t.Errorf("RetryAfterHint() = %v, want 0", apiErr.RetryAfterHint())
	}
}
