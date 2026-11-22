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

func TestFactoryCustomHeadersReachTheWire(t *testing.T) {
	const sessionKey = "conv-1"

	tests := []struct {
		name           string
		typ            string
		model          string
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

func TestFactoryCustomHeadersFromTypedProviderField(t *testing.T) {
	const sessionKey = "conv-1"

	tests := []struct {
		name          string
		spelling      string
		typed         func(p *config.ProvidersConfig, cfg config.ProviderConfig)
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
