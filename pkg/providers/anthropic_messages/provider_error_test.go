// Lele - Ultra-lightweight personal AI agent
// Copyright (c) 2026 Lele contributors

package anthropicmessages

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

// serverPath is the placeholder for the endpoint URL in expected messages.
const serverPath = "<<SERVER>>"

func errorServer(status int, headers map[string]string, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// The per-status branches of Chat keep their historical wording AND now carry
// the status + hint as data (previously the 429 branch dropped Retry-After).
func TestChat_StatusBranchesAreStructured(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		headers map[string]string
		wantMsg string
		hint    time.Duration
	}{
		{
			name:    "401 keeps its message and is auth-classified by status",
			status:  http.StatusUnauthorized,
			body:    "ignored by this branch",
			wantMsg: "authentication failed (401): check your API key",
		},
		{
			name:    "429 carries the Retry-After hint",
			status:  http.StatusTooManyRequests,
			body:    "slow down",
			headers: map[string]string{"Retry-After": "30"},
			wantMsg: "rate limited (429): slow down",
			hint:    30 * time.Second,
		},
		{
			name:    "400 keeps its message",
			status:  http.StatusBadRequest,
			body:    "invalid model",
			wantMsg: "bad request (400): invalid model",
		},
		{
			name:    "404 keeps its message",
			status:  http.StatusNotFound,
			body:    "nope",
			wantMsg: "endpoint not found (404): nope",
		},
		{
			name:    "500 keeps its message",
			status:  http.StatusInternalServerError,
			body:    "boom",
			wantMsg: "internal server error (500): boom",
		},
		{
			name:    "503 keeps its message and honours retry-after-ms",
			status:  http.StatusServiceUnavailable,
			body:    "maintenance",
			headers: map[string]string{"retry-after-ms": "2000"},
			wantMsg: "service unavailable (503): maintenance",
			hint:    2 * time.Second,
		},
		{
			name:    "unmapped status falls back to the canonical format",
			status:  http.StatusBadGateway,
			body:    "upstream died",
			wantMsg: "API request failed:\n  Status: 502\n  Body:   upstream died\n URL: " + serverPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := errorServer(tt.status, tt.headers, tt.body)
			defer server.Close()

			if tt.status == http.StatusBadGateway {
				tt.wantMsg = strings.Replace(tt.wantMsg, serverPath, server.URL+"/v1/messages", 1)
			}

			p := NewProvider("key", server.URL)
			_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6",
				map[string]any{"max_tokens": 1024})
			if err == nil {
				t.Fatal("expected error")
			}
			if err.Error() != tt.wantMsg {
				t.Errorf("Error() =\n%q\nwant\n%q", err.Error(), tt.wantMsg)
			}

			var apiErr *common.APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("Chat() error = %T, want *common.APIError", err)
			}
			if apiErr.HTTPStatus() != tt.status {
				t.Errorf("HTTPStatus() = %d, want %d", apiErr.HTTPStatus(), tt.status)
			}
			if apiErr.RetryAfterHint() != tt.hint {
				t.Errorf("RetryAfterHint() = %v, want %v", apiErr.RetryAfterHint(), tt.hint)
			}
		})
	}
}

func TestChatStream_NonOKReturnsAPIErrorWithRetryAfter(t *testing.T) {
	server := errorServer(http.StatusTooManyRequests, map[string]string{"Retry-After": "12"}, "slow down")
	defer server.Close()

	p := NewProvider("key", server.URL)
	_, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6",
		map[string]any{"max_tokens": 1024}, func(string, bool) {}, nil)
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
	if apiErr.RetryAfterHint() != 12*time.Second {
		t.Errorf("RetryAfterHint() = %v, want 12s", apiErr.RetryAfterHint())
	}
	if !strings.HasPrefix(err.Error(), "API request failed:\n  Status: 429\n  Body:   slow down\n URL: ") {
		t.Errorf("unexpected message: %q", err.Error())
	}
}
