package anthropicmessages

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

const (
	customTagHeader = "x-routing-tag"
	customTagValue  = "prod"
)

func customHeaderOptions(sessionID string) map[string]any {
	options := anthropicTestOptions()
	if sessionID != "" {
		options[protocoltypes.OptWireSessionID] = sessionID
	}
	return options
}

func contentTypeForStream(stream bool) string {
	if stream {
		return "text/event-stream"
	}
	return "application/json"
}

func TestCustomHeaders_ChatSendsStaticHeader(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

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

	p := NewProviderWithOptions("the-api-key", server, Options{})
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", customHeaderOptions("")); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if captured.has("X-Routing-Tag") {
		t.Fatalf("x-routing-tag is present on the wire (value %q), want absent", captured.get("X-Routing-Tag"))
	}
	if got := captured.get("X-Api-Key"); got != "the-api-key" {
		t.Fatalf("X-Api-Key = %q, want the transport default", got)
	}
	if got := captured.get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want the transport default", got)
	}
}

func TestCustomHeaders_OverrideDetectedSessionHeader(t *testing.T) {
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
