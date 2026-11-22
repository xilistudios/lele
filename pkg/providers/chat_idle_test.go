package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers/common"
)

type countingProvider struct {
	inner LLMProvider

	mu    sync.Mutex
	chats int
}

func (c *countingProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	c.mu.Lock()
	c.chats++
	c.mu.Unlock()
	return c.inner.Chat(ctx, messages, tools, model, options)
}

func (c *countingProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	sp, ok := c.inner.(StreamingLLMProvider)
	if !ok {
		return nil, errors.New("test wrapper: inner provider does not implement StreamingLLMProvider")
	}
	return sp.ChatStream(ctx, messages, tools, model, options, onChunk, onReasoning)
}

func (c *countingProvider) GetDefaultModel() string { return c.inner.GetDefaultModel() }

func (c *countingProvider) chatCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.chats
}

func newIdleTestProvider(t *testing.T, serverURL string) LLMProvider {
	t.Helper()
	cfg := config.DefaultConfig()
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["opencode_go"] = config.NamedProviderConfig{
		Type: "opencode_go",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "oc-key",
			APIBase: serverURL,
		},
	}
	p, err := CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}
	return p
}

func TestChatIdle_MissingWireSessionSkipsSafetyNet(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	requestCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}

	p := newIdleTestProvider(t, server.URL)
	wrapped := &countingProvider{inner: p}

	_, err := ChatIdle(t.Context(), wrapped, []Message{{Role: "user", Content: "hi"}}, nil, "gpt-5.6-luna", nil)
	if !errors.Is(err, common.ErrMissingWireSession) {
		t.Fatalf("ChatIdle() error = %v, want errors.Is(err, common.ErrMissingWireSession)", err)
	}
	if got := wrapped.chatCalls(); got != 0 {
		t.Fatalf("non-streaming safety net fired %d time(s), want 0: the sentinel is raised while building, retrying is pointless", got)
	}
	if got := requestCount(); got != 0 {
		t.Fatalf("server received %d request(s), want 0: the error is raised while building the request, before any transport touches the wire", got)
	}
}

func TestChatIdle_ServerRejectedStreamFallsBackToChat(t *testing.T) {
	var (
		mu       sync.Mutex
		requests int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		stream, _ := body["stream"].(bool)
		w.Header().Set("Content-Type", "application/json")
		if stream {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]interface{}{"message": "streaming not supported", "code": 400},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		})
	}))
	t.Cleanup(server.Close)
	requestCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return requests
	}

	p := newIdleTestProvider(t, server.URL)
	wrapped := &countingProvider{inner: p}

	options := map[string]interface{}{OptWireSessionID: "abc"}
	resp, err := ChatIdle(t.Context(), wrapped, []Message{{Role: "user", Content: "hi"}}, nil, "gpt-5.6-luna", options)
	if err != nil {
		t.Fatalf("ChatIdle() error = %v, want the non-streaming fallback to succeed", err)
	}
	if resp == nil {
		t.Fatal("ChatIdle() response = nil, want the fallback completion")
	}
	if got := wrapped.chatCalls(); got != 1 {
		t.Fatalf("non-streaming safety net fired %d time(s), want exactly 1 for a server-rejected stream", got)
	}
	if got := requestCount(); got != 2 {
		t.Fatalf("server received %d request(s), want 2 (SSE attempt + non-streaming retry)", got)
	}
}
