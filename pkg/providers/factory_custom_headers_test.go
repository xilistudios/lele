// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// TestFactoryCustomHeadersFromTypedProviderField pins the header path of every
// legacy typed-field arm the MINOR-1 audit touches, on each spelling a caller
// can hand it.
//
// A config written as "providers": {"anthropic": {"headers": {…}}} lives in the
// typed Providers.Anthropic field. The canonical spelling reaches it through
// the Named entry ensureNamedDefaults seeds from that field, but an alias does
// not whenever GetNamed misses: the legacy switch then hand-builds the
// selection from the typed fields, and unless the headers are carried there the
// map is silently dropped. Two shapes exist and both are covered here:
//
//   - aliases NormalizeProvider collapses onto the seeded key ("claude" →
//     "anthropic", "gpt" → "openai"): the single copy above the switch fixes
//     them, so pinning one of them proves the copy works;
//   - aliases NormalizeProvider leaves alone ("zai" vs the seeded
//     "zai_coding_plan", "modelark" vs "modelark_coding_plan"): the copy cannot
//     see them, so the arms themselves must set sel.headers — the MINOR-1 bug.
//
// Every case runs end-to-end against an httptest server: the assertion is on
// the header that reaches the wire, not on an internal field.
func TestFactoryCustomHeadersFromTypedProviderField(t *testing.T) {
	const sessionKey = "conv-1"

	tests := []struct {
		name     string
		spelling string
		// typed puts the fixture into the legacy field the arm reads. Nothing
		// else is configured: ensureNamedDefaults derives the Named copy.
		typed func(p *config.ProvidersConfig, cfg config.ProviderConfig)
		// anthropicWire selects the Messages transport (which needs
		// max_tokens) instead of the OpenAI-compatible one.
		anthropicWire bool
		model         string
	}{
		{
			name:     "anthropic canonical (Named entry seeded from the typed field)",
			spelling: "anthropic",
			typed:    func(p *config.ProvidersConfig, c config.ProviderConfig) { p.Anthropic = c },
			model:    "claude-sonnet-4.6", anthropicWire: true,
		},
		{
			name:     "claude alias (NormalizeProvider collapses to anthropic)",
			spelling: "claude",
			typed:    func(p *config.ProvidersConfig, c config.ProviderConfig) { p.Anthropic = c },
			model:    "claude-sonnet-4.6", anthropicWire: true,
		},
		{
			name:     "zai_coding_plan canonical (Named entry seeded from the typed field)",
			spelling: "zai_coding_plan",
			typed:    func(p *config.ProvidersConfig, c config.ProviderConfig) { p.ZAICodingPlan = c },
			model:    "glm-4.6",
		},
		{
			name:     "zai alias (MINOR-1: NormalizeProvider does not collapse it)",
			spelling: "zai",
			typed:    func(p *config.ProvidersConfig, c config.ProviderConfig) { p.ZAICodingPlan = c },
			model:    "glm-4.6",
		},
		{
			name:     "modelark_coding_plan canonical (Named entry seeded from the typed field)",
			spelling: "modelark_coding_plan",
			typed:    func(p *config.ProvidersConfig, c config.ProviderConfig) { p.ModelArkCodingPlan = c },
			model:    "ark-code-latest",
		},
		{
			name:     "modelark alias (MINOR-1: NormalizeProvider does not collapse it)",
			spelling: "modelark",
			typed:    func(p *config.ProvidersConfig, c config.ProviderConfig) { p.ModelArkCodingPlan = c },
			model:    "ark-code-latest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiBase, header := startCustomHeaderServer(t)

			cfg := config.DefaultConfig()
			fixture := config.ProviderConfig{
				APIKey:  "k",
				APIBase: apiBase,
				Headers: map[string]string{"x-opencode-session": "{{session}}"},
			}
			cfg.Providers = &config.ProvidersConfig{}
			tt.typed(cfg.Providers, fixture)

			p, err := CreateProviderForCandidate(cfg, tt.spelling)
			if err != nil {
				t.Fatalf("CreateProviderForCandidate(%q) error = %v", tt.spelling, err)
			}

			options := map[string]interface{}{}
			if tt.anthropicWire {
				options["max_tokens"] = 128
			}
			options = WithWireSessionID(options, sessionKey)
			if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, tt.model, options); err != nil {
				t.Fatalf("Chat() error = %v", err)
			}

			if got, want := header("X-Opencode-Session"), WireSessionID(sessionKey); got != want {
				t.Fatalf("x-opencode-session = %q, want WireSessionID(%q) = %q: the typed-field path dropped the headers map", got, sessionKey, want)
			}
		})
	}
}

// TestFactoryCustomHeadersCopilotAliasSelection pins the third MINOR-1 arm,
// "copilot" (the seeded Named key is "github_copilot", which NormalizeProvider
// does not produce). It is asserted on the selection rather than on the wire:
// that arm returns providerTypeGitHubCopilot, whose transport talks to the
// copilot SDK over gRPC/stdio and reads no HTTP headers, so no httptest server
// can observe the map either way. The copy exists so the selection states what
// config says — the same answer the canonical spelling gives — and so the map
// is already in place if that transport ever becomes header-aware.
func TestFactoryCustomHeadersCopilotAliasSelection(t *testing.T) {
	want := map[string]string{"x-tenant": "acme"}

	for _, spelling := range []string{"github_copilot", "copilot"} {
		t.Run(spelling, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Providers = &config.ProvidersConfig{
				GitHubCopilot: config.ProviderConfig{
					APIBase: "localhost:4321",
					Headers: want,
				},
			}

			sel, err := resolveProviderSelectionForProvider(cfg, spelling)
			if err != nil {
				t.Fatalf("resolveProviderSelectionForProvider(%q) error = %v", spelling, err)
			}
			if !reflect.DeepEqual(sel.headers, want) {
				t.Fatalf("sel.headers = %v, want %v: the %q arm dropped the typed field's map", sel.headers, want, spelling)
			}
		})
	}
}
