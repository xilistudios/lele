// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package anthropicmessages

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// Mirror of openai_compat/custom_headers_test.go for the Anthropic Messages
// transport: the same three stages live in this package's setRequestHeaders and
// the same regression applies (Chat and ChatStream are separate code paths, and
// an early return before the custom-header stage silently drops the map for
// every provider that is not OpenCode Go).
//
// headerCapture, newAnthropicCompletionServer and anthropicTestOptions come
// from session_headers_test.go (same package) — one capture helper and the
// mandatory max_tokens, not duplicates.

const (
	customTagHeader = "x-routing-tag"
	customTagValue  = "prod"
)

// customHeaderOptions returns the options a call in this file needs: max_tokens
// (mandatory on the Anthropic wire format) plus the wire session id when the
// case supplies one.
func customHeaderOptions(sessionID string) map[string]any {
	options := anthropicTestOptions()
	if sessionID != "" {
		options[protocoltypes.OptWireSessionID] = sessionID
	}
	return options
}

// contentTypeForStream maps the table-driven stream flag onto the content type
// newAnthropicCompletionServer understands.
func contentTypeForStream(stream bool) string {
	if stream {
		return "text/event-stream"
	}
	return "application/json"
}

func TestCustomHeaders_ChatSendsStaticHeader(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

	// No SessionHeader on purpose: the provider is NOT OpenCode Go, and this is
	// the exact regression the restructure guards — the old code returned before
	// the custom-header stage whenever no session header was configured.
	p := NewProviderWithOptions("key", server, Options{
		Headers: map[string]string{customTagHeader: customTagValue},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", customHeaderOptions("")); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Routing-Tag"); got != customTagValue {
		t.Fatalf("%s = %q, want %q", customTagHeader, got, customTagValue)
	}
}

func TestCustomHeaders_ChatStreamSendsStaticHeader(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "text/event-stream")

	p := NewProviderWithOptions("key", server, Options{
		Headers: map[string]string{customTagHeader: customTagValue},
	})
	onChunk := func(string, bool) {}
	if _, err := p.ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", customHeaderOptions(""), onChunk, nil); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if got := captured.get("X-Routing-Tag"); got != customTagValue {
		t.Fatalf("%s = %q, want %q (streaming must send exactly what non-streaming sends)", customTagHeader, got, customTagValue)
	}
}

func TestCustomHeaders_SessionPlaceholderExpands(t *testing.T) {
	// A real gateway id: 64 hex chars, so a truncated or re-hashed value fails
	// the equality below instead of passing a Contains check.
	sessionID := strings.Repeat("a", 64)

	tests := []struct {
		name   string
		stream bool
	}{
		{name: "Chat"},
		{name: "ChatStream", stream: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, captured := newAnthropicCompletionServer(t, contentTypeForStream(tt.stream))

			p := NewProviderWithOptions("key", server, Options{
				Headers: map[string]string{"x-opencode-session": protocoltypes.SessionHeaderPlaceholder},
			})
			options := customHeaderOptions(sessionID)
			messages := []Message{{Role: "user", Content: "hi"}}
			var err error
			if tt.stream {
				onChunk := func(string, bool) {}
				_, err = p.ChatStream(t.Context(), messages, nil, "claude-sonnet-4.6", options, onChunk, nil)
			} else {
				_, err = p.Chat(t.Context(), messages, nil, "claude-sonnet-4.6", options)
			}
			if err != nil {
				t.Fatalf("call failed: %v", err)
			}

			if got := captured.get("X-Opencode-Session"); got != sessionID {
				t.Fatalf("x-opencode-session = %q, want the id from the options (%q)", got, sessionID)
			}
		})
	}
}

func TestCustomHeaders_SessionPlaceholderWithoutSessionIDUsesUnkeyedBucket(t *testing.T) {
	// An unstamped call must never produce an empty header (an empty value drops
	// the header on the wire and the gateway then answers its own opaque 400):
	// it lands in the same shared bucket WireSessionID("") yields.
	server, captured := newAnthropicCompletionServer(t, "application/json")

	p := NewProviderWithOptions("key", server, Options{
		Headers: map[string]string{"x-opencode-session": protocoltypes.SessionHeaderPlaceholder},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != protocoltypes.UnkeyedSessionBucket {
		t.Fatalf("x-opencode-session = %q, want the unkeyed bucket %q", got, protocoltypes.UnkeyedSessionBucket)
	}
}

func TestCustomHeaders_OverrideTransportDefaults(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

	// The documented ordering: custom headers are applied last, so explicit user
	// intent wins over the transport defaults. X-Api-Key is this transport's
	// auth header (the sibling of Authorization on the OpenAI-compatible one).
	p := NewProviderWithOptions("the-api-key", server, Options{
		Headers: map[string]string{"X-Api-Key": "custom-key", "Anthropic-Version": "2099-01-01", "User-Agent": "custom-agent/1.0"},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", customHeaderOptions("")); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Api-Key"); got != "custom-key" {
		t.Fatalf("X-Api-Key = %q, want the custom header to win over the api key", got)
	}
	if got := captured.get("Anthropic-Version"); got != "2099-01-01" {
		t.Fatalf("Anthropic-Version = %q, want the custom header to win over the transport default", got)
	}
	if got := captured.get("User-Agent"); got != "custom-agent/1.0" {
		t.Fatalf("User-Agent = %q, want the custom header to win over the transport default", got)
	}
}

func TestCustomHeaders_NilHeadersSendsNothingExtra(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

	// The byte-identical guarantee: an empty Headers map must not change this
	// transport's wire format at all (session_headers_test.go runs untouched for
	// the same reason).
	p := NewProviderWithOptions("the-api-key", server, Options{})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", customHeaderOptions("")); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Routing-Tag"); got != "" {
		t.Fatalf("x-routing-tag = %q, want absent", got)
	}
	if got := captured.get("X-Api-Key"); got != "the-api-key" {
		t.Fatalf("X-Api-Key = %q, want the transport default", got)
	}
	if got := captured.get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want the transport default", got)
	}
}

func TestCustomHeaders_OverrideDetectedSessionHeader(t *testing.T) {
	// Stage ordering inside setRequestHeaders: the session header (stage 2) is
	// written first and a custom entry naming the same header (stage 3) replaces
	// it — the user's explicit value, not the per-call hash, is what goes out.
	server, captured := newAnthropicCompletionServer(t, "application/json")

	p := NewProviderWithOptions("key", server, Options{
		SessionHeader:         "x-opencode-session",
		SessionHeaderRequired: true,
		Headers:               map[string]string{"x-opencode-session": "explicit-value"},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", customHeaderOptions(strings.Repeat("b", 64))); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != "explicit-value" {
		t.Fatalf("x-opencode-session = %q, want the custom header to win over the session header", got)
	}
}
