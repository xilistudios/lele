package anthropicmessages

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
	"github.com/xilistudios/lele/pkg/version"
)

func anthropicTestOptions() map[string]any {
	return map[string]any{"max_tokens": 128}
}

type headerCapture struct {
	mu     sync.Mutex
	header http.Header
}

func (c *headerCapture) record(r *http.Request) {
	h := r.Header.Clone()
	c.mu.Lock()
	c.header = h
	c.mu.Unlock()
}

func (c *headerCapture) get(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.header.Get(name)
}

func (c *headerCapture) has(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.header.Values(name)) > 0
}

func newAnthropicCompletionServer(t *testing.T, contentType string) (string, *headerCapture) {
	t.Helper()
	captured := &headerCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.record(r)
		if contentType == "text/event-stream" {
			writeAnthropicSSE(w)
			return
		}
		writeAnthropicCompletion(w)
	}))
	t.Cleanup(server.Close)
	return server.URL, captured
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
	server, captured := newAnthropicCompletionServer(t, "application/json")

	p := NewProvider("key", server)
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions()); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	want := "lele/" + version.Get()
	if got := captured.get("User-Agent"); got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestProviderChatStream_SetsLeleUserAgent(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "text/event-stream")

	p := NewProvider("key", server)
	onChunk := func(string, bool) {}
	if _, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions(), onChunk, nil); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	want := "lele/" + version.Get()
	if got := captured.get("User-Agent"); got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestProviderChat_SendsConfiguredSessionHeader(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

	p := NewProviderWithOptions("key", server, Options{SessionHeader: "x-opencode-session"})
	options := anthropicTestOptions()
	options[protocoltypes.OptWireSessionID] = "session-123"
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != "session-123" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "session-123")
	}
	if got := captured.get("X-Api-Key"); got != "key" {
		t.Fatalf("X-Api-Key = %q, want %q", got, "key")
	}
}

func TestProviderChatStream_SendsConfiguredSessionHeader(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "text/event-stream")

	p := NewProviderWithOptions("key", server, Options{SessionHeader: "x-opencode-session"})
	options := anthropicTestOptions()
	options[protocoltypes.OptWireSessionID] = "session-123"
	onChunk := func(string, bool) {}
	if _, err := p.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options, onChunk, nil); err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != "session-123" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "session-123")
	}
}

func TestProviderChat_NoSessionHeaderWhenNotConfigured(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

	p := NewProvider("key", server)
	options := anthropicTestOptions()
	options[protocoltypes.OptWireSessionID] = "session-123"
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := captured.get("X-Opencode-Session"); got != "" {
		t.Fatalf("x-opencode-session = %q, want absent", got)
	}
}

func TestProviderChat_OmitsOpportunisticSessionHeaderWhenMissing(t *testing.T) {
	server, captured := newAnthropicCompletionServer(t, "application/json")

	p := NewProviderWithOptions("key", server, Options{SessionHeader: "x-opencode-session"})
	if _, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", anthropicTestOptions()); err != nil {
		t.Fatalf("Chat() error = %v, want success: an opportunistic header must be omitted, not required", err)
	}

	if got := captured.get("X-Opencode-Session"); got != "" {
		t.Fatalf("x-opencode-session = %q, want omitted when the session id is absent", got)
	}
}

func TestProviderChat_ErrorsWhenSessionIdMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be reached: the call fails locally")
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewProviderWithOptions("key", server.URL, Options{SessionHeader: "x-opencode-session", SessionHeaderRequired: true})
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
