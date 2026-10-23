package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers/common"
)

func TestResolveProviderSelection(t *testing.T) {
	tests := []struct {
		name          string
		setup         func(*config.Config)
		wantType      providerType
		wantAPIBase   string
		wantProxy     string
		wantErrSubstr string
	}{
		{
			name: "explicit claude-cli provider routes to cli provider type",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "claude-cli"
				cfg.Agents.Defaults.Workspace = "/tmp/ws"
			},
			wantType: providerTypeClaudeCLI,
		},
		{
			name: "explicit copilot provider routes to github copilot type",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "copilot"
			},
			wantType:    providerTypeGitHubCopilot,
			wantAPIBase: "localhost:4321",
		},
		{
			name: "explicit deepseek provider uses deepseek defaults",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "deepseek"
				cfg.Agents.Defaults.Model = "deepseek:deepseek-chat"
				cfg.Providers.DeepSeek.APIKey = "deepseek-key"
				cfg.Providers.DeepSeek.Proxy = "http://127.0.0.1:7890"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://api.deepseek.com/v1",
			wantProxy:   "http://127.0.0.1:7890",
		},
		{
			name: "explicit shengsuanyun provider uses defaults",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "shengsuanyun"
				cfg.Providers.ShengSuanYun.APIKey = "ssy-key"
				cfg.Providers.ShengSuanYun.Proxy = "http://127.0.0.1:7890"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://router.shengsuanyun.com/api/v1",
			wantProxy:   "http://127.0.0.1:7890",
		},
		{
			name: "explicit nvidia provider uses defaults",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "nvidia"
				cfg.Providers.Nvidia.APIKey = "nvapi-test"
				cfg.Providers.Nvidia.Proxy = "http://127.0.0.1:7890"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://integrate.api.nvidia.com/v1",
			wantProxy:   "http://127.0.0.1:7890",
		},
		{
			name: "openrouter model uses openrouter defaults",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Model = "openrouter:auto"
				cfg.Providers.OpenRouter.APIKey = "sk-or-test"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://openrouter.ai/api/v1",
		},
		{
			name: "anthropic oauth routes to claude auth provider",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = ""
				cfg.Agents.Defaults.Model = "claude-sonnet-4-5-20250929"
				cfg.Providers.Anthropic.AuthMethod = "oauth"
			},
			wantType: providerTypeClaudeAuth,
		},
		{
			name: "openai oauth routes to codex auth provider",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = ""
				cfg.Agents.Defaults.Model = "gpt-4o"
				cfg.Providers.OpenAI.AuthMethod = "oauth"
			},
			wantType: providerTypeCodexAuth,
		},
		{
			name: "openai codex-cli auth routes to codex cli token provider",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = ""
				cfg.Agents.Defaults.Model = "gpt-4o"
				cfg.Providers.OpenAI.AuthMethod = "codex-cli"
			},
			wantType: providerTypeCodexCLIToken,
		},
		{
			name: "explicit codex-code provider routes to codex cli provider type",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "codex-code"
				cfg.Agents.Defaults.Workspace = "/tmp/ws"
			},
			wantType: providerTypeCodexCLI,
		},
		{
			name: "zhipu model uses zhipu base default",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = ""
				cfg.Agents.Defaults.Model = "glm-4.7"
				cfg.Providers.Zhipu.APIKey = "zhipu-key"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://open.bigmodel.cn/api/paas/v4",
		},
		{
			name: "groq model uses groq base default",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Model = "groq:llama-3.3-70b"
				cfg.Providers.Groq.APIKey = "gsk-key"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://api.groq.com/openai/v1",
		},
		{
			name: "ollama model uses ollama base default",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Model = "ollama:qwen2.5:14b"
				cfg.Providers.Ollama.APIKey = "ollama-key"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "http://localhost:11434/v1",
		},
		{
			name: "moonshot model keeps proxy and default base",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Model = "moonshot:kimi-k2.5"
				cfg.Providers.Moonshot.APIKey = "moonshot-key"
				cfg.Providers.Moonshot.Proxy = "http://127.0.0.1:7890"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://api.moonshot.cn/v1",
			wantProxy:   "http://127.0.0.1:7890",
		},
		{
			name: "gemini oauth routes to gemini code assist",
			setup: func(cfg *config.Config) {
				// The provider spelling matters here only because
				// resolveProviderSelection reads it: "gemini" is the canonical
				// config name for the provider whose API-key path the sibling
				// test below pins.
				cfg.Agents.Defaults.Provider = "gemini"
				cfg.Providers.Gemini.AuthMethod = "oauth"
			},
			wantType: providerTypeGeminiAuth,
		},
		{
			name: "google spelling selects code assist from the same gemini config",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "google"
				cfg.Providers.Gemini.AuthMethod = "oauth"
			},
			wantType: providerTypeGeminiAuth,
		},
		{
			name: "gemini oauth with an api key keeps the api key path",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "gemini"
				cfg.Providers.Gemini.AuthMethod = "oauth"
				cfg.Providers.Gemini.APIKey = "gemini-key"
			},
			wantType:    providerTypeHTTPCompat,
			wantAPIBase: "https://generativelanguage.googleapis.com/v1beta",
		},
		{
			name: "gemini auth_method token without an api key is not code assist",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Provider = "gemini"
				cfg.Providers.Gemini.AuthMethod = "token"
			},
			wantErrSubstr: "no API key configured for provider",
		},
		{
			name: "missing keys returns model config error",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Model = "custom-model"
			},
			wantErrSubstr: "no API key configured for provider",
		},
		{
			name: "openrouter prefix without key returns provider key error",
			setup: func(cfg *config.Config) {
				cfg.Agents.Defaults.Model = "openrouter:auto"
			},
			wantErrSubstr: "no API key configured for provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			tt.setup(cfg)

			got, err := resolveProviderSelection(cfg)
			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErrSubstr)
				}
				return
			}

			if err != nil {
				t.Fatalf("resolveProviderSelection() error = %v", err)
			}
			if got.providerType != tt.wantType {
				t.Fatalf("providerType = %v, want %v", got.providerType, tt.wantType)
			}
			if tt.wantAPIBase != "" && got.apiBase != tt.wantAPIBase {
				t.Fatalf("apiBase = %q, want %q", got.apiBase, tt.wantAPIBase)
			}
			if tt.wantProxy != "" && got.proxy != tt.wantProxy {
				t.Fatalf("proxy = %q, want %q", got.proxy, tt.wantProxy)
			}
		})
	}
}

func TestCreateProviderReturnsHTTPProviderForOpenRouter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "openrouter:auto"
	cfg.Providers.OpenRouter.APIKey = "sk-or-test"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := provider.(*HTTPProvider); !ok {
		t.Fatalf("provider type = %T, want *HTTPProvider", provider)
	}
}

func TestCreateProviderReturnsCodexCliProviderForCodexCode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "codex-code"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := provider.(*CodexCliProvider); !ok {
		t.Fatalf("provider type = %T, want *CodexCliProvider", provider)
	}
}

func TestCreateProviderReturnsCodexProviderForCodexCliAuthMethod(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Providers.OpenAI.AuthMethod = "codex-cli"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := provider.(*CodexProvider); !ok {
		t.Fatalf("provider type = %T, want *CodexProvider", provider)
	}
}

func TestCreateProviderReturnsClaudeProviderForAnthropicOAuth(t *testing.T) {
	originalGetCredential := getCredential
	t.Cleanup(func() { getCredential = originalGetCredential })

	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "anthropic" {
			t.Fatalf("provider = %q, want anthropic", provider)
		}
		return &auth.AuthCredential{
			AccessToken: "anthropic-token",
			AuthMethod:  "oauth",
		}, nil
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "anthropic"
	cfg.Providers.Anthropic.AuthMethod = "oauth"
	cfg.Providers.Anthropic.APIBase = "https://proxy.example.com/v1"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	claudeProvider, ok := provider.(*ClaudeProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *ClaudeProvider", provider)
	}
	if got := claudeProvider.delegate.BaseURL(); got != "https://proxy.example.com" {
		t.Fatalf("anthropic baseURL = %q, want %q", got, "https://proxy.example.com")
	}
	if !claudeProvider.delegate.OAuthMode() {
		t.Error("delegate.OAuthMode() = false, want true for an oauth credential")
	}
}

// TestCreateProviderClaudeProviderTokenAuthMethodIsNotOAuth is the sibling of
// TestCreateProviderReturnsClaudeProviderForAnthropicOAuth: it takes the same
// claude-auth selection path but a credential whose AuthMethod is "token"
// (paste-token), so the delegate must not enable OAuth mode.
func TestCreateProviderClaudeProviderTokenAuthMethodIsNotOAuth(t *testing.T) {
	originalGetCredential := getCredential
	t.Cleanup(func() { getCredential = originalGetCredential })

	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "anthropic" {
			t.Fatalf("provider = %q, want anthropic", provider)
		}
		return &auth.AuthCredential{
			AccessToken: "anthropic-token",
			AuthMethod:  "token",
		}, nil
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "anthropic"
	cfg.Providers.Anthropic.AuthMethod = "token"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	claudeProvider, ok := provider.(*ClaudeProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *ClaudeProvider", provider)
	}
	if claudeProvider.delegate.OAuthMode() {
		t.Error("delegate.OAuthMode() = true, want false for a token credential")
	}
}

// TestCreateProviderReturnsGeminiCodeAssistForGeminiOAuth pins the
// providers.gemini.auth_method == "oauth" + no-API-key selection: the factory
// must build a Code Assist provider seeded with the stored credential's access
// token and wired to createGeminiTokenSource(), without any network access at
// construction time.
func TestCreateProviderReturnsGeminiCodeAssistForGeminiOAuth(t *testing.T) {
	originalGetCredential := getCredential
	t.Cleanup(func() { getCredential = originalGetCredential })

	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "gemini" {
			t.Fatalf("provider = %q, want gemini", provider)
		}
		return &auth.AuthCredential{
			AccessToken: "gemini-token",
			AuthMethod:  "oauth",
		}, nil
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "gemini"
	cfg.Providers.Gemini.AuthMethod = "oauth"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	codeAssist, ok := provider.(*GeminiCodeAssistProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *GeminiCodeAssistProvider", provider)
	}
	if codeAssist.token != "gemini-token" {
		t.Fatalf("token = %q, want %q", codeAssist.token, "gemini-token")
	}
	if codeAssist.tokenSource == nil {
		t.Fatal("tokenSource = nil, want createGeminiTokenSource()")
	}
}

// TestCreateProviderForCandidateRawGoogleSpellingUsesCodeAssist covers the
// un-normalized entry point: CreateProviderForCandidate does not normalize its
// argument, so the raw "google" spelling (a fallback candidate built from a
// "google/model" prefix) must select Code Assist from the same Providers.Gemini
// config the "gemini" spelling uses.
func TestCreateProviderForCandidateRawGoogleSpellingUsesCodeAssist(t *testing.T) {
	originalGetCredential := getCredential
	t.Cleanup(func() { getCredential = originalGetCredential })

	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "gemini" {
			t.Fatalf("provider = %q, want gemini", provider)
		}
		return &auth.AuthCredential{
			AccessToken: "gemini-token",
			AuthMethod:  "oauth",
		}, nil
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Gemini.AuthMethod = "oauth"

	provider, err := CreateProviderForCandidate(cfg, "google")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}
	if _, ok := provider.(*GeminiCodeAssistProvider); !ok {
		t.Fatalf("provider type = %T, want *GeminiCodeAssistProvider", provider)
	}
}

// TestCreateGeminiAuthProviderReportsMissingCredentials pins the wording of the
// OAuth-credential error. It must name gemini (not claude, whose provider
// createGeminiAuthProvider mirrors) and the exact login command to run.
func TestCreateGeminiAuthProviderReportsMissingCredentials(t *testing.T) {
	originalGetCredential := getCredential
	t.Cleanup(func() { getCredential = originalGetCredential })

	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "gemini" {
			t.Fatalf("provider = %q, want gemini", provider)
		}
		return nil, nil
	}

	_, err := createGeminiAuthProvider("")
	if err == nil {
		t.Fatal("createGeminiAuthProvider() error = nil, want a missing-credentials error")
	}
	want := "no credentials for gemini. Run: lele auth login --provider gemini"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestResolveProviderSelectionGeminiAPIKeyUnchanged is the regression guard for
// the other half of F2-T08: with no auth_method the gemini provider config still
// selects the plain API-key (HTTP-compat) transport against the public
// Generative Language base.
func TestResolveProviderSelectionGeminiAPIKeyUnchanged(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "gemini"
	cfg.Providers.Gemini.APIKey = "k"

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}
	if got.providerType != providerTypeHTTPCompat {
		t.Fatalf("providerType = %v, want providerTypeHTTPCompat", got.providerType)
	}
	if want := "https://generativelanguage.googleapis.com/v1beta"; got.apiBase != want {
		t.Fatalf("apiBase = %q, want %q", got.apiBase, want)
	}
	if got.apiKey != "k" {
		t.Fatalf("apiKey = %q, want %q", got.apiKey, "k")
	}
}

// geminiNamedOnlyConfig builds a hand-assembled "Named-only caller" shape:
// Providers.Named["gemini"] carries the decision data while the typed
// Providers.Gemini field is deliberately left zero-valued.
//
// That shape is not what EditableDocument.ToConfig() produces, and no current
// production caller is it (see the note in selectionFromNamedProvider): every
// production config also fills the typed field. ToConfig() cannot hand back a
// Config whose Providers.Named["gemini"] is populated AND whose typed
// Providers.Gemini is not, because ToConfig returns its json round-tripped
// `validated` Config and ProvidersConfig.UnmarshalJSON fills the typed field
// from the same flat "providers.<name>" object (empirically verified for
// F2-T12: with providers.gemini.auth_method == "oauth", ToConfig() returns
// Providers.Gemini.AuthMethod == "oauth"). The helper therefore keeps only the
// half a Named-only caller sees, exactly like the reviewer's overlay test.
//
// Pinning it is still worth it: it is the only shape that exercises the new
// `case "gemini"` in selectionFromNamedProvider, which is defence-in-depth for
// exactly such a caller.
//
// The Named entry itself is produced through the public pkg/config API
// (LoadEditableDocument -> EditableDocument.ToConfig), the same route the
// finding used, so the decision data under test is parsed by the real
// provider-config path rather than by a hand-written literal.
func geminiNamedOnlyConfig(t *testing.T, providerEntryJSON string) (*config.Config, config.NamedProviderConfig) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.json")
	body := fmt.Sprintf(`{
  "agents": {"defaults": {"provider": "gemini", "model": "gemini-2.5-pro"}},
  "providers": {"gemini": %s}
}`, providerEntryJSON)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	doc, _, err := config.LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument() error = %v", err)
	}
	fromDoc, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig() error = %v", err)
	}
	named, ok := fromDoc.Providers.Named["gemini"]
	if !ok {
		t.Fatalf("Providers.Named has no %q entry after ToConfig(); got %v", "gemini", fromDoc.Providers.Named)
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "gemini"
	cfg.Agents.Defaults.Model = "gemini-2.5-pro"
	cfg.Providers = &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{"gemini": named},
	}
	// DeepEqual, not ==: ProviderConfig gained Headers (a map), which makes the
	// struct non-comparable; the intent — typed field must be zero — is unchanged.
	if !reflect.DeepEqual(cfg.Providers.Gemini, config.ProviderConfig{}) {
		t.Fatalf("typed Providers.Gemini = %+v, want the zero value: the test must exercise the Named-only path", cfg.Providers.Gemini)
	}
	return cfg, named
}

// TestResolveProviderSelectionGeminiOAuthFromNamedEntryOnly pins F2-T12: the
// Code Assist transport must be selected when the decision data lives in
// Providers.Named["gemini"] (auth_method "oauth", no api key) even though the
// typed Providers.Gemini field is zero. That is the hand-assembled Named-only
// caller shape described on geminiNamedOnlyConfig and, since every production
// config also fills the typed field, the only shape that reaches the new
// `case "gemini"`. Before it was added only the typed field was consulted (the
// pre-check), so this shape fell through selectionFromNamedProvider's generic
// tail and failed with "no API key configured for provider".
func TestResolveProviderSelectionGeminiOAuthFromNamedEntryOnly(t *testing.T) {
	cfg, named := geminiNamedOnlyConfig(t, `{"type": "gemini", "auth_method": "oauth"}`)
	if named.AuthMethod != "oauth" {
		t.Fatalf("named.AuthMethod = %q, want %q", named.AuthMethod, "oauth")
	}
	if named.APIKey != "" {
		t.Fatalf("named.APIKey = %q, want empty", named.APIKey)
	}

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}
	if got.providerType != providerTypeGeminiAuth {
		t.Fatalf("providerType = %v, want providerTypeGeminiAuth", got.providerType)
	}
	if got.apiKey != "" {
		t.Fatalf("apiKey = %q, want empty (the Code Assist transport carries no API key)", got.apiKey)
	}
}

// TestResolveProviderSelectionGeminiNamedEntryWithAPIKeyStaysAPIKey pins the
// other half of the "gemini" case added to selectionFromNamedProvider: an entry
// with auth_method "oauth" AND an api_key must keep the developer API-key path
// (providerTypeHTTPCompat against the Generative Language base), i.e. the new
// case must not hijack a keyed config.
func TestResolveProviderSelectionGeminiNamedEntryWithAPIKeyStaysAPIKey(t *testing.T) {
	cfg, named := geminiNamedOnlyConfig(t, `{"type": "gemini", "auth_method": "oauth", "api_key": "named-gemini-key"}`)
	if named.APIKey != "named-gemini-key" {
		t.Fatalf("named.APIKey = %q, want %q", named.APIKey, "named-gemini-key")
	}
	if named.AuthMethod != "oauth" {
		t.Fatalf("named.AuthMethod = %q, want %q", named.AuthMethod, "oauth")
	}

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}
	if got.providerType != providerTypeHTTPCompat {
		t.Fatalf("providerType = %v, want providerTypeHTTPCompat (an api_key keeps the API-key path)", got.providerType)
	}
	if want := "https://generativelanguage.googleapis.com/v1beta"; got.apiBase != want {
		t.Fatalf("apiBase = %q, want %q", got.apiBase, want)
	}
	if got.apiKey != "named-gemini-key" {
		t.Fatalf("apiKey = %q, want %q", got.apiKey, "named-gemini-key")
	}
}

func TestCreateProviderReturnsCodexProviderForOpenAIOAuth(t *testing.T) {
	originalGetCredential := getCredential
	t.Cleanup(func() { getCredential = originalGetCredential })

	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "openai" {
			t.Fatalf("provider = %q, want openai", provider)
		}
		return &auth.AuthCredential{
			AccessToken: "openai-token",
			AccountID:   "acct_123",
		}, nil
	}

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Providers.OpenAI.AuthMethod = "oauth"

	provider, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := provider.(*CodexProvider); !ok {
		t.Fatalf("provider type = %T, want *CodexProvider", provider)
	}
}

func TestResolveProviderSelection_UsesNamedProviderAndModelAlias(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Model = "my-openai-compatible:fast"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"my-openai-compatible": {
			Type: "openai",
			ProviderConfig: config.ProviderConfig{
				APIKey:  "sk-test",
				APIBase: "https://example.test/v1",
			},
			Models: map[string]config.ProviderModelConfig{
				"fast": {Model: "gpt-4o-mini"},
			},
		},
	}

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}
	if got.apiBase != "https://example.test/v1" {
		t.Fatalf("apiBase = %q, want https://example.test/v1", got.apiBase)
	}
	if got.model != "gpt-4o-mini" {
		t.Fatalf("model = %q, want gpt-4o-mini", got.model)
	}
}

func TestDefaultAPIBaseByType_CatalogProviders(t *testing.T) {
	tests := []struct {
		typ  string
		want string
	}{
		{"xai", "https://api.x.ai/v1"},
		{"grok", "https://api.x.ai/v1"},
		{"nous", "https://inference-api.nousresearch.com/v1"},
		{"lmstudio", "http://127.0.0.1:1234/v1"},
		{"stepfun", "https://api.stepfun.ai/step_plan/v1"},
		{"minimax", "https://api.minimax.io/anthropic/v1"},
		{"minimax_cn", "https://api.minimaxi.com/anthropic/v1"},
		{"vercel", "https://ai-gateway.vercel.sh/v1"},
		{"opencode", "https://opencode.ai/zen/v1"},
		{"opencode_go", "https://opencode.ai/zen/go/v1"},
		{"huggingface", "https://router.huggingface.co/v1"},
		{"novita", "https://api.novita.ai/openai"},
		{"xiaomi", "https://api.xiaomimimo.com/v1"},
		{"tencent_tokenhub", "https://tokenhub.tencentmaas.com/v1"},
		{"arcee", "https://api.arcee.ai/api/v1"},
		{"gmi", "https://api.gmi-serving.com/v1"},
		{"ollama_cloud", "https://ollama.com/v1"},
		{"cerebras", "https://api.cerebras.ai/v1"},
		{"together", "https://api.together.xyz/v1"},
		{"fireworks", "https://api.fireworks.ai/inference/v1"},
		{"mistral", "https://api.mistral.ai/v1"},
		{"siliconflow", "https://api.siliconflow.com/v1"},
		{"perplexity", "https://api.perplexity.ai/v1"},
		{"kimi_for_coding", "https://api.kimi.com/coding/v1"},
		{"alibaba_token_plan", "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"},
		{"qwen-token-plan", "https://token-plan.ap-southeast-1.maas.aliyuncs.com/compatible-mode/v1"},
		{"alibaba_token_plan_cn", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"},
		// Existing providers still resolve
		{"openai", "https://api.openai.com/v1"},
		{"anthropic", defaultAnthropicAPIBase},
		{"github_copilot", "localhost:4321"},
		{"alibaba", "https://coding-intl.dashscope.aliyuncs.com/v1"},
		{"zai", "https://api.z.ai/api/paas/v4"},
		{"unknown-provider-xyz", ""},
	}

	for _, tt := range tests {
		got := defaultAPIBaseByType(tt.typ)
		if got != tt.want {
			t.Errorf("defaultAPIBaseByType(%q) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func TestSelectionFromNamedProvider_MiniMaxUsesAnthropicTransport(t *testing.T) {
	tests := []struct {
		name    string
		typ     string
		apiBase string
	}{
		{"minimax default base", "minimax", ""},
		{"minimax_cn default base", "minimax_cn", ""},
		{"explicit anthropic path", "custom", "https://proxy.example.com/anthropic/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			named := config.NamedProviderConfig{
				Type: tt.typ,
				ProviderConfig: config.ProviderConfig{
					APIKey:  "mm-key",
					APIBase: tt.apiBase,
				},
			}
			sel, err := selectionFromNamedProvider(cfg, "mm", "MiniMax-M3", named)
			if err != nil {
				t.Fatalf("selectionFromNamedProvider() error = %v", err)
			}
			if sel.providerType != providerTypeAnthropic {
				t.Fatalf("providerType = %v, want providerTypeAnthropic", sel.providerType)
			}
			if sel.apiBase == "" {
				t.Fatal("apiBase is empty")
			}
			if !strings.Contains(sel.apiBase, "/anthropic") {
				t.Fatalf("apiBase = %q, want path containing /anthropic", sel.apiBase)
			}
		})
	}
}

func TestSelectionFromNamedProvider_NonAnthropicStaysHTTPCompat(t *testing.T) {
	cfg := config.DefaultConfig()
	named := config.NamedProviderConfig{
		Type: "xai",
		ProviderConfig: config.ProviderConfig{
			APIKey: "xai-key",
		},
	}
	sel, err := selectionFromNamedProvider(cfg, "xai", "grok-4", named)
	if err != nil {
		t.Fatalf("selectionFromNamedProvider() error = %v", err)
	}
	if sel.providerType != providerTypeHTTPCompat {
		t.Fatalf("providerType = %v, want providerTypeHTTPCompat", sel.providerType)
	}
	if sel.apiBase != "https://api.x.ai/v1" {
		t.Fatalf("apiBase = %q, want https://api.x.ai/v1", sel.apiBase)
	}
}

func TestOpenCodeGoSessionHeader(t *testing.T) {
	tests := []struct {
		name         string
		providerName string
		typ          string
		apiBase      string
		want         string
		// wantRequired: true ⇒ a missing session id fails the call fast
		// (ErrMissingWireSession), false ⇒ the header is opportunistic (sent
		// when present, silently omitted when not).
		wantRequired bool
	}{
		{
			// Explicit name/type configuration: required regardless of base.
			name:         "canonical name and type",
			providerName: "opencode_go",
			typ:          "opencode_go",
			apiBase:      "https://opencode.ai/zen/go/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: true,
		},
		{
			name:         "detected by type",
			providerName: "my-oc",
			typ:          "opencode_go",
			apiBase:      "https://proxy.internal/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: true,
		},
		{
			name:         "detected by type, dash spelling",
			providerName: "my-oc",
			typ:          "opencode-go",
			apiBase:      "https://proxy.internal/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: true,
		},
		{
			name:         "explicit name wins over a non-go base",
			providerName: "opencode_go",
			typ:          "openai",
			apiBase:      "https://example.test/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: true,
		},
		{
			// URL sniff ⇒ opportunistic, never fail-fast (FIX-B).
			name:         "detected by api base, inference go path",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://opencode.ai/inference/go/openai/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
			name:         "detected by api base, zen go path",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://opencode.ai/zen/go/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
			name:         "api base match is case-insensitive",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://OpenCode.AI/Zen/Go/V1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
			// FIX-A: the first-class Zen base shares the host but not the Go
			// path. It used to over-match and, with the fail-fast, would have
			// turned "works" into a local error on unkeyed egresses.
			name:         "opencode zen base is not detected as go",
			providerName: "opencode",
			typ:          "opencode",
			apiBase:      "https://opencode.ai/zen/v1",
			want:         "",
			wantRequired: false,
		},
		{
			// Path-segment boundary: "/zen/gopher" is not "/zen/go".
			name:         "longer path segment is not detected as go",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://opencode.ai/zen/gopher/v1",
			want:         "",
			wantRequired: false,
		},
		{
			// NIT-2 flip (deliberate): with the parsed-host matcher
			// (isOpenCodeGoAPIBase uses net/url) a lookalike host carrying
			// the Go path NO LONGER matches at all. The old substring match
			// matched here but only ever yielded an inert opportunistic
			// header ("matched but inert"); no match is strictly better —
			// the sniff cannot claim a foreign host anymore.
			name:         "lookalike host with go path no longer matches",
			providerName: "corp-proxy",
			typ:          "openai",
			apiBase:      "https://proxy.example.com/opencode.ai/zen/go",
			want:         "",
			wantRequired: false,
		},
		{
			// NIT-2: an explicit port on the real host must not break the
			// check — the literal substring matcher missed
			// "opencode.ai:443/zen/go" and the header was silently omitted
			// (upstream 400 MissingSessionID). Hostname() strips any port.
			name:         "explicit port on the opencode.ai host still matches",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://opencode.ai:443/zen/go/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
			// NIT-2 pinned decision: subdomains of opencode.ai count (host is
			// "opencode.ai or a subdomain of it").
			name:         "subdomain of opencode.ai matches",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://chat.opencode.ai/zen/go/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
			// NIT-2 pinned consequence: an apiBase that net/url cannot parse
			// falls back to no-match (no header) instead of matching by
			// accident. "http://[::1/zen/go" has an unclosed IPv6 bracket.
			name:         "unparseable api base falls back to no match",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "http://[::1/zen/go",
			want:         "",
			wantRequired: false,
		},
		{
			name:         "openai untouched",
			providerName: "openai",
			typ:          "openai",
			apiBase:      "https://api.openai.com/v1",
			want:         "",
			wantRequired: false,
		},
		{
			name:         "local proxy untouched",
			providerName: "myproxy",
			typ:          "openai",
			apiBase:      "http://127.0.0.1:8080/v1",
			want:         "",
			wantRequired: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := &providerSelection{
				providerName:     tt.providerName,
				providerTypeName: tt.typ,
				apiBase:          tt.apiBase,
			}
			got, required := openCodeGoSessionHeader(sel)
			if got != tt.want {
				t.Fatalf("openCodeGoSessionHeader() = %q, want %q", got, tt.want)
			}
			if required != tt.wantRequired {
				t.Fatalf("openCodeGoSessionHeader() required = %v, want %v", required, tt.wantRequired)
			}
		})
	}
}

// startFactoryHeaderServer starts a server that records the session header of
// the first inference request and answers with a minimal valid completion.
// Returns the server URL and a getter for the captured header value. The
// capture is mutex-guarded and copied inside the handler (the plan's rule:
// reading request state after the handler returns races).
func startFactoryHeaderServer(t *testing.T) (string, func() string) {
	t.Helper()
	var (
		mu       sync.Mutex
		captured string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.Header.Get("X-Opencode-Session")
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
	return server.URL, func() string {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func TestCreateProviderForCandidate_OpenCodeGoSendsSessionHeader(t *testing.T) {
	apiBase, sessionHeader := startFactoryHeaderServer(t)

	cfg := config.DefaultConfig()
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["opencode_go"] = config.NamedProviderConfig{
		Type: "opencode_go",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "oc-key",
			APIBase: apiBase,
		},
	}

	p, err := CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}
	options := map[string]interface{}{OptWireSessionID: "abc"}
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-5.6-luna", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := sessionHeader(); got != "abc" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "abc")
	}
}

func TestCreateProviderForCandidate_NonOpenCodeDoesNotSendSessionHeader(t *testing.T) {
	apiBase, sessionHeader := startFactoryHeaderServer(t)

	cfg := config.DefaultConfig()
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["openai"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-test",
			APIBase: apiBase,
		},
	}

	p, err := CreateProviderForCandidate(cfg, "openai")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}
	options := map[string]interface{}{OptWireSessionID: "abc"}
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := sessionHeader(); got != "" {
		t.Fatalf("x-opencode-session = %q, want absent", got)
	}
}

// TestCreateProviderForCandidate_OpenCodeGoNameTypeRequiresSessionHeader pins
// the REQUIRED branch (FIX-B): detection from explicit name/type config means
// the user asked for OpenCode Go, so an unstamped call (no wire_session_id
// option) must fail locally with ErrMissingWireSession and never reach the
// server — the error is the honest signal that a call site forgot
// providers.WithWireSessionID.
func TestCreateProviderForCandidate_OpenCodeGoNameTypeRequiresSessionHeader(t *testing.T) {
	apiBase, sessionHeader := startFactoryHeaderServer(t)

	cfg := config.DefaultConfig()
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["opencode_go"] = config.NamedProviderConfig{
		Type: "opencode_go",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "oc-key",
			APIBase: apiBase,
		},
	}

	p, err := CreateProviderForCandidate(cfg, "opencode_go")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}
	_, err = p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-5.6-luna", nil)
	if !errors.Is(err, common.ErrMissingWireSession) {
		t.Fatalf("Chat() error = %v, want errors.Is(err, common.ErrMissingWireSession)", err)
	}
	if got := sessionHeader(); got != "" {
		t.Fatalf("x-opencode-session = %q, want the server never reached", got)
	}
}

// TestCreateProviderForCandidate_OpenCodeGoURLSniffIsOpportunistic pins the
// OPPORTUNISTIC branch (FIX-B): detection from api_base alone must never fail
// the call locally. Without a session id the header is silently omitted (the
// upstream answers its own descriptive 400 if it cares); with one it is sent.
//
// NIT-2 changed how the api_base is matched (parsed host, not substring), so
// a local httptest URL can no longer be spoofed by appending
// "/opencode.ai/zen/go" to its path — that lookalike pattern now correctly
// does NOT match. To keep this end-to-end with the REAL host, the api_base is
// http://opencode.ai/zen/go/v1 and the httptest server plays HTTP proxy: for
// plain-http targets Go's transport sends the absolute-form request to the
// configured proxy, so no DNS lookup of opencode.ai ever happens.
func TestCreateProviderForCandidate_OpenCodeGoURLSniffIsOpportunistic(t *testing.T) {
	proxy, sessionHeader := startFactoryHeaderServer(t)

	cfg := config.DefaultConfig()
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["oc-proxy"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "sk-test",
			APIBase: "http://opencode.ai/zen/go/v1",
			Proxy:   proxy,
		},
	}

	p, err := CreateProviderForCandidate(cfg, "oc-proxy")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}

	// No session id: the call proceeds and the header stays off the wire.
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() without session id error = %v, want success (opportunistic header)", err)
	}
	if got := sessionHeader(); got != "" {
		t.Fatalf("x-opencode-session = %q, want omitted when the call carries no session id", got)
	}

	// With a session id: the opportunistic header is sent.
	options := map[string]interface{}{OptWireSessionID: "abc"}
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", options); err != nil {
		t.Fatalf("Chat() with session id error = %v", err)
	}
	if got := sessionHeader(); got != "abc" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "abc")
	}
}

// TestCreateProviderForCandidate_OpenCodeZenNotDetectedAsGo pins FIX-A at the
// factory level: the first-class Zen base (https://opencode.ai/zen/v1) shares
// the opencode.ai host but not the Go path, so it must NOT be detected as
// OpenCode Go — even a stamped call must keep the header off the wire (before
// the fix, Zen was falsely detected and unkeyed egresses failed locally).
//
// NIT-2: same local-proxy setup as the opportunistic test above — the parsed
// host check means the real opencode.ai host is required for the path
// comparison to be exercised at all.
func TestCreateProviderForCandidate_OpenCodeZenNotDetectedAsGo(t *testing.T) {
	proxy, sessionHeader := startFactoryHeaderServer(t)

	cfg := config.DefaultConfig()
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["opencode"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIKey:  "zen-key",
			APIBase: "http://opencode.ai/zen/v1",
			Proxy:   proxy,
		},
	}

	p, err := CreateProviderForCandidate(cfg, "opencode")
	if err != nil {
		t.Fatalf("CreateProviderForCandidate() error = %v", err)
	}
	options := map[string]interface{}{OptWireSessionID: "abc"}
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "grok-4.7", options); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if got := sessionHeader(); got != "" {
		t.Fatalf("x-opencode-session = %q, want absent: /zen/v1 is Zen, not OpenCode Go", got)
	}
}
