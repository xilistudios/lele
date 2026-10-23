// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package openai_compat

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

// These tests pin the config `headers` feature on this transport. The shape
// they exist to defend: Chat and ChatStream are separate code paths that both
// go through setRequestHeaders, so a header applied to only one of them (or a
// stage of setRequestHeaders that returns early and skips the custom headers)
// is a silent, partial behaviour — invisible until a gateway rejects the calls
// that happen to be streamed. Every case therefore runs the same assertion on
// both paths.
//
// headerCapture and newOpenAICompletionServer come from session_headers_test.go
// (same package): one capture helper, not two.

const (
	customTagHeader = "x-routing-tag"
	customTagValue  = "prod"
)

// customHeaderOptions is the minimum call options a custom header needs: the
// transport reads the same map for the wire session id.
func customHeaderOptions(sessionID string) map[string]interface{} {
	options := map[string]interface{}{}
	if sessionID != "" {
		options[protocoltypes.OptWireSessionID] = sessionID
	}
	return options
}

func TestCustomHeaders_ChatSendsStaticHeader(t *testing.T) {
	server, captured := newOpenAICompletionServer(t, "application/json")

	// No SessionHeader on purpose: the provider is NOT OpenCode Go, and this is
	// the exact regression the restructure guards — the old code returned before
	// the custom-header stage whenever no session header was configured.
	p := NewProviderWithOptions("key", server, "", Options{
		Headers: map[string]string{customTagHeader: customTagValue},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Routing-Tag"); got != customTagValue {
		t.Fatalf("%s = %q, want %q", customTagHeader, got, customTagValue)
	}
}

func TestCustomHeaders_ChatStreamSendsStaticHeader(t *testing.T) {
	server, captured := newOpenAICompletionServer(t, "text/event-stream")

	p := NewProviderWithOptions("key", server, "", Options{
		Headers: map[string]string{customTagHeader: customTagValue},
	})
	onChunk := func(string, bool) {}
	if _, err := p.ChatStream(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil, onChunk, nil); err != nil {
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
			server, captured := newOpenAICompletionServer(t, contentTypeForStream(tt.stream))

			p := NewProviderWithOptions("key", server, "", Options{
				Headers: map[string]string{"x-opencode-session": protocoltypes.SessionHeaderPlaceholder},
			})
			options := customHeaderOptions(sessionID)
			messages := []Message{{Role: "user", Content: "hi"}}
			var err error
			if tt.stream {
				onChunk := func(string, bool) {}
				_, err = p.ChatStream(t.Context(), messages, nil, "gpt-4o", options, onChunk, nil)
			} else {
				_, err = p.Chat(t.Context(), messages, nil, "gpt-4o", options)
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
	// An unstamped call must never produce an empty session header: Go does
	// send a header with an empty value, and an empty x-opencode-session
	// defeats the gateway's session routing (it then answers its own opaque
	// 400). The value lands in the shared bucket WireSessionID("") yields.
	server, captured := newOpenAICompletionServer(t, "application/json")

	p := NewProviderWithOptions("key", server, "", Options{
		Headers: map[string]string{"x-opencode-session": protocoltypes.SessionHeaderPlaceholder},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != protocoltypes.UnkeyedSessionBucket {
		t.Fatalf("x-opencode-session = %q, want the unkeyed bucket %q", got, protocoltypes.UnkeyedSessionBucket)
	}
}

func TestCustomHeaders_OverrideTransportDefaults(t *testing.T) {
	server, captured := newOpenAICompletionServer(t, "application/json")

	// The documented ordering: custom headers are applied last, so explicit user
	// intent wins over the transport default — the case that motivates it is a
	// gateway whose auth scheme is not "Bearer <api_key>".
	p := NewProviderWithOptions("the-api-key", server, "", Options{
		Headers: map[string]string{"Authorization": "Bearer custom", "User-Agent": "custom-agent/1.0"},
	})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("Authorization"); got != "Bearer custom" {
		t.Fatalf("Authorization = %q, want the custom header to win over the api key", got)
	}
	if got := captured.get("User-Agent"); got != "custom-agent/1.0" {
		t.Fatalf("User-Agent = %q, want the custom header to win over the transport default", got)
	}
}

func TestCustomHeaders_NilHeadersSendsNothingExtra(t *testing.T) {
	server, captured := newOpenAICompletionServer(t, "application/json")

	// The byte-identical guarantee: an empty Headers map must not change this
	// transport's wire format at all (session_headers_test.go runs untouched for
	// the same reason).
	p := NewProviderWithOptions("the-api-key", server, "", Options{})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	// has(), not get(): get returns "" for both "absent" and "present but
	// empty", and the claim here is ABSENCE.
	if captured.has("X-Routing-Tag") {
		t.Fatalf("x-routing-tag is present on the wire (value %q), want absent", captured.get("X-Routing-Tag"))
	}
	if got := captured.get("Authorization"); got != "Bearer the-api-key" {
		t.Fatalf("Authorization = %q, want the transport default", got)
	}
	if got := captured.get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want the transport default", got)
	}
}

func TestCustomHeaders_OverrideDetectedSessionHeader(t *testing.T) {
	// Stage ordering inside setRequestHeaders: the session header (stage 2) is
	// written first and a custom entry naming the same header (stage 3) replaces
	// it — the user's explicit value, not the per-call hash, is what goes out.
	server, captured := newOpenAICompletionServer(t, "application/json")

	p := NewProviderWithOptions("key", server, "", Options{
		SessionHeader:         "x-opencode-session",
		SessionHeaderRequired: true,
		Headers:               map[string]string{"x-opencode-session": "explicit-value"},
	})
	options := customHeaderOptions(strings.Repeat("b", 64))
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != "explicit-value" {
		t.Fatalf("x-opencode-session = %q, want the custom header to win over the session header", got)
	}
}

// contentTypeForStream maps the table-driven stream flag onto the content type
// newOpenAICompletionServer understands.
func contentTypeForStream(stream bool) string {
	if stream {
		return "text/event-stream"
	}
	return "application/json"
}
