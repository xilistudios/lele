// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package anthropicmessages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
	"github.com/xilistudios/lele/pkg/version"
)

// anthropicTestOptions returns the options every Chat call in this file needs
// (max_tokens is mandatory in the Anthropic wire format).
func anthropicTestOptions() map[string]any {
	return map[string]any{"max_tokens": 128}
}

func writeAnthropicCompletion(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"content":     []map[string]any{{"type": "text", "text": "ok"}},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
	})
}

func writeAnthropicSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Write([]byte(
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"))
}

func TestProviderChat_SetsLeleUserAgent(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		writeAnthropicCompletion(w)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL)
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	want := "lele/" + version.Get()
	if got := captured.Get("User-Agent"); got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestProviderChatStream_SetsLeleUserAgent(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		writeAnthropicSSE(w)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL)
	onChunk := func(string, bool) {}
	if _, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions(), onChunk, nil); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	want := "lele/" + version.Get()
	if got := captured.Get("User-Agent"); got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestProviderChat_SendsConfiguredSessionHeader(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		writeAnthropicCompletion(w)
	}))
	defer server.Close()

	p := NewProviderWithOptions("key", server.URL, Options{SessionHeader: "x-opencode-session"})
	options := anthropicTestOptions()
	options[protocoltypes.OptWireSessionID] = "session-123"
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.Get("X-Opencode-Session"); got != "session-123" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "session-123")
	}
	if got := captured.Get("X-Api-Key"); got != "key" {
		t.Fatalf("X-Api-Key = %q, want %q", got, "key")
	}
}

func TestProviderChatStream_SendsConfiguredSessionHeader(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		writeAnthropicSSE(w)
	}))
	defer server.Close()

	p := NewProviderWithOptions("key", server.URL, Options{SessionHeader: "x-opencode-session"})
	options := anthropicTestOptions()
	options[protocoltypes.OptWireSessionID] = "session-123"
	onChunk := func(string, bool) {}
	if _, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options, onChunk, nil); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if got := captured.Get("X-Opencode-Session"); got != "session-123" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "session-123")
	}
}

func TestProviderChat_NoSessionHeaderWhenNotConfigured(t *testing.T) {
	var captured http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Header.Clone()
		writeAnthropicCompletion(w)
	}))
	defer server.Close()

	// A plain provider (not OpenCode Go): the option is set but must never be
	// stamped — other providers' wire format stays unchanged (D6).
	p := NewProvider("key", server.URL)
	options := anthropicTestOptions()
	options[protocoltypes.OptWireSessionID] = "session-123"
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.Get("X-Opencode-Session"); got != "" {
		t.Fatalf("x-opencode-session = %q, want absent", got)
	}
}

func TestProviderChat_ErrorsWhenSessionIdMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be reached: the call fails locally")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewProviderWithOptions("key", server.URL, Options{SessionHeader: "x-opencode-session"})
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions())
	if err == nil {
		t.Fatal("Chat() expected error, got nil")
	}
	if !errors.Is(err, common.ErrMissingWireSession) {
		t.Fatalf("Chat() error = %v, want errors.Is(err, common.ErrMissingWireSession)", err)
	}
	if !strings.Contains(err.Error(), "x-opencode-session") {
		t.Fatalf("Chat() error = %q, want it to name the required header", err.Error())
	}
	if !strings.Contains(err.Error(), protocoltypes.OptWireSessionID) {
		t.Fatalf("Chat() error = %q, want it to name the missing option key", err.Error())
	}
}
