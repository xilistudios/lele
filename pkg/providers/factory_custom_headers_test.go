// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// End-to-end tests for config `headers`: the map must survive the factory's
// selection step and reach the transport, on BOTH transports, with
// {{session}} expanded per call. They exist because the wiring has two
// distinct resolution paths (a Named entry and the legacy typed-field switch)
// and the map is carried by a selection field, not by the config struct the
// transports read — a break in either path is invisible until a gateway
// rejects the calls.
//
// No network: every case talks to an httptest server.

// startCustomHeaderServer records the headers of the inference request and
// answers in the shape the transport under test parses (the path is the only
// thing the two transports agree on: /chat/completions vs /messages). The
// capture is mutex-guarded and cloned inside the handler, like the transport
// tests do: reading request state after the handler returns races with the
// server's connection cleanup.
func startCustomHeaderServer(t *testing.T) (string, func(name string) string) {
	t.Helper()

	var (
		mu       sync.Mutex
		captured http.Header
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.Header.Clone()
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/messages") {
			json.NewEncoder(w).Encode(map[string]any{
				"content":     []map[string]any{{"type": "text", "text": "ok"}},
				"stop_reason": "end_turn",
				"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
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

	return server.URL, func(name string) string {
		mu.Lock()
		defer mu.Unlock()
		return captured.Get(name)
	}
}

// TestFactoryCustomHeadersReachTheWire pins the whole path for a Named entry
// on each transport: config → providerSelection.headers → Options.Headers →
// the wire, with the placeholder expanded from the call's session key.
func TestFactoryCustomHeadersReachTheWire(t *testing.T) {
	const sessionKey = "conv-1"

	tests := []struct {
		name  string
		typ   string
		model string
		// needsMaxTokens: the Anthropic wire format requires it, the
		// OpenAI-compatible one does not.
		needsMaxTokens bool
	}{
		{name: "openai compatible", typ: "openai", model: "gpt-4o"},
		{name: "anthropic messages", typ: "anthropic", model: "claude-sonnet-4.6", needsMaxTokens: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiBase, header := startCustomHeaderServer(t)

			cfg := config.DefaultConfig()
			if cfg.Providers.Named == nil {
				cfg.Providers.Named = map[string]config.NamedProviderConfig{}
			}
			cfg.Providers.Named["gw"] = config.NamedProviderConfig{
				Type: tt.typ,
				ProviderConfig: config.ProviderConfig{
					APIKey:  "k",
					APIBase: apiBase,
					Headers: map[string]string{"x-opencode-session": "{{session}}"},
				},
			}

			p, err := CreateProviderForCandidate(cfg, "gw")
			if err != nil {
				t.Fatalf("CreateProviderForCandidate() error = %v", err)
			}

			options := map[string]interface{}{}
			if tt.needsMaxTokens {
				options["max_tokens"] = 128
			}
			options = WithWireSessionID(options, sessionKey)
			if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, tt.model, options); err != nil {
				t.Fatalf("Chat() error = %v", err)
			}

			if got, want := header("X-Opencode-Session"), WireSessionID(sessionKey); got != want {
				t.Fatalf("x-opencode-session = %q, want WireSessionID(%q) = %q", got, sessionKey, want)
			}
		})
	}
}

// TestFactoryCustomHeadersFromTypedProviderField pins the single-copy decision
// in resolveProviderSelectionByName.
//
// A config written as "providers": {"anthropic": {"headers": {…}}} lives in the
// typed Providers.Anthropic field. The canonical spelling reaches it through
// the Named entry ensureNamedDefaults seeds from that field, but the alias
// spelling ("claude") does not: GetNamed("claude") misses, so the legacy switch
// hand-builds the selection from the typed fields — and without the one copy at
// the top of that switch, the map is silently dropped for every alias.
func TestFactoryCustomHeadersFromTypedProviderField(t *testing.T) {
	const sessionKey = "conv-1"

	for _, spelling := range []string{"anthropic", "claude"} {
		t.Run(spelling, func(t *testing.T) {
			apiBase, header := startCustomHeaderServer(t)

			cfg := config.DefaultConfig()
			// Typed field only: no Named entry for this provider is created by
			// the test (ensureNamedDefaults derives it from the typed field).
			cfg.Providers = &config.ProvidersConfig{
				Anthropic: config.ProviderConfig{
					APIKey:  "k",
					APIBase: apiBase,
					Headers: map[string]string{"x-opencode-session": "{{session}}"},
				},
			}

			p, err := CreateProviderForCandidate(cfg, spelling)
			if err != nil {
				t.Fatalf("CreateProviderForCandidate(%q) error = %v", spelling, err)
			}

			options := WithWireSessionID(map[string]interface{}{"max_tokens": 128}, sessionKey)
			if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options); err != nil {
				t.Fatalf("Chat() error = %v", err)
			}

			if got, want := header("X-Opencode-Session"), WireSessionID(sessionKey); got != want {
				t.Fatalf("x-opencode-session = %q, want WireSessionID(%q) = %q: the typed-field path dropped the headers map", got, sessionKey, want)
			}
		})
	}
}
