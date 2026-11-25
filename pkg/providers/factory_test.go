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
	"github.com/xilistudios/lele/pkg/catalog"
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
	if !reflect.DeepEqual(cfg.Providers.Gemini, config.ProviderConfig{}) {
		t.Fatalf("typed Providers.Gemini = %+v, want the zero value: the test must exercise the Named-only path", cfg.Providers.Gemini)
	}
	return cfg, named
}

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
		{"openai", "https://api.openai.com/v1"},
		{"anthropic", defaultAnthropicAPIBase},
		{"github_copilot", "localhost:4321"},
		{"alibaba", "https://coding-intl.dashscope.aliyuncs.com/v1"},
		{"zai", "https://api.z.ai/api/paas/v4"},
		{"zai_coding_plan", "https://api.z.ai/api/coding/paas/v4"},
		{"unknown-provider-xyz", ""},
	}

	for _, tt := range tests {
		got := defaultAPIBaseByType(tt.typ)
		if got != tt.want {
			t.Errorf("defaultAPIBaseByType(%q) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}

func withoutCatalogIndex(t *testing.T) {
	t.Helper()
	previous := catalog.IndexSnapshot()
	catalog.RestoreIndex(nil)
	t.Cleanup(func() { catalog.RestoreIndex(previous) })
}

func TestWithoutCatalogIndexRestoresThePriorIndex(t *testing.T) {
	previous := catalog.IndexSnapshot()
	sentinel := &catalog.Index{Providers: map[string]catalog.IndexEntry{
		"zai": {ID: "zai", APIBase: "https://sentinel.example/v1"},
	}}
	catalog.RestoreIndex(sentinel)
	t.Cleanup(func() { catalog.RestoreIndex(previous) })

	t.Run("scope sees no index", func(t *testing.T) {
		withoutCatalogIndex(t)
		if got := catalog.IndexSnapshot(); got != nil {
			t.Fatalf("IndexSnapshot() inside the scope = %v, want nil", got)
		}
	})

	if got := catalog.IndexSnapshot(); got != sentinel {
		t.Fatalf("IndexSnapshot() after the scope = %v, want the prior index restored exactly", got)
	}
}

func TestDefaultAPIBase_ZAICodingPlan(t *testing.T) {
	withoutCatalogIndex(t)

	const want = "https://api.z.ai/api/coding/paas/v4"
	if got := defaultAPIBaseByType("zai_coding_plan"); got != want {
		t.Errorf("defaultAPIBaseByType(zai_coding_plan) = %q, want %q", got, want)
	}
}

func TestDefaultAPIBase_ZAI(t *testing.T) {
	withoutCatalogIndex(t)

	const want = "https://api.z.ai/api/paas/v4"
	if got := defaultAPIBaseByType("zai"); got != want {
		t.Errorf("defaultAPIBaseByType(zai) = %q, want %q", got, want)
	}
}

func TestFactory_LegacyZAICodingPlanFallback(t *testing.T) {
	withoutCatalogIndex(t)

	tests := []struct {
		name     string
		provider string
		want     string
	}{
		{name: "typed field via the zai alias", provider: "zai", want: catalog.ZAIAPIBase},
		{name: "named entry via zai_coding_plan", provider: "zai_coding_plan", want: catalog.ZAICodingPlanAPIBase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Agents.Defaults.Provider = tt.provider
			cfg.Agents.Defaults.Model = "glm-4.6"
			cfg.Providers = &config.ProvidersConfig{
				ZAICodingPlan: config.ProviderConfig{APIKey: "zai-key"},
			}

			got, err := resolveProviderSelection(cfg)
			if err != nil {
				t.Fatalf("resolveProviderSelection() error = %v", err)
			}
			if got.apiBase != tt.want {
				t.Errorf("apiBase = %q, want %q", got.apiBase, tt.want)
			}
		})
	}
}

func TestZaiCodingPlanConfiguredBase(t *testing.T) {
	const legacy = "https://api.z.ai/api/paas/v4"

	tests := []struct {
		name     string
		cfg      *config.Config
		provider string
		want     string
		wantOK   bool
	}{
		{
			name:     "typed block via the provider name",
			cfg:      zaiConfig(config.ProviderConfig{APIBase: legacy}),
			provider: "zai_coding_plan",
			want:     legacy,
			wantOK:   true,
		},
		{
			name:     "named entry whose id is zai_coding_plan",
			cfg:      zaiNamedConfig("zai_coding_plan", "zai_coding_plan", legacy),
			provider: "zai_coding_plan",
			want:     legacy,
			wantOK:   true,
		},
		{
			name:     "webui-named entry whose type is zai_coding_plan",
			cfg:      zaiNamedConfig("webui-zai", "zai_coding_plan", legacy),
			provider: "webui-zai",
			want:     legacy,
			wantOK:   true,
		},
		{
			name:     "webui-named entry with the z.ai id spelling",
			cfg:      zaiNamedConfig("z.ai", "zai_coding_plan", legacy),
			provider: "z.ai",
			want:     legacy,
			wantOK:   true,
		},
		{
			name:     "zai is not a coding plan provider",
			cfg:      zaiConfig(config.ProviderConfig{APIBase: legacy}),
			provider: "zai",
		},
		{
			name:     "named entry of another type",
			cfg:      zaiNamedConfig("proxy", "openai", "https://proxy.example/v1"),
			provider: "proxy",
		},
		{
			name:     "nil config",
			provider: "zai_coding_plan",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := zaiCodingPlanConfiguredBase(tt.cfg, tt.provider)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("zaiCodingPlanConfiguredBase(%q) = (%q, %v), want (%q, %v)", tt.provider, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func zaiConfig(provider config.ProviderConfig) *config.Config {
	return &config.Config{Providers: &config.ProvidersConfig{ZAICodingPlan: provider}}
}

func zaiNamedConfig(id, typ, apiBase string) *config.Config {
	return &config.Config{Providers: &config.ProvidersConfig{
		Named: map[string]config.NamedProviderConfig{
			id: {Type: typ, ProviderConfig: config.ProviderConfig{APIKey: "zai-key", APIBase: apiBase}},
		},
	}}
}

func TestIsLegacyZAICodingPlanBase(t *testing.T) {
	tests := []struct {
		name    string
		apiBase string
		want    bool
	}{
		{name: "canonical legacy base", apiBase: catalog.ZAIAPIBase, want: true},
		{name: "legacy base with trailing slash", apiBase: catalog.ZAIAPIBase + "/", want: true},
		{name: "legacy base with repeated trailing slashes", apiBase: catalog.ZAIAPIBase + "//", want: true},
		{name: "legacy base with surrounding whitespace", apiBase: "  " + catalog.ZAIAPIBase + "  ", want: true},
		{name: "legacy base in upper case", apiBase: "https://API.Z.AI/api/paas/v4", want: true},
		{name: "empty base keeps the catalog default", apiBase: "", want: false},
		{name: "coding base already fixed", apiBase: catalog.ZAICodingPlanAPIBase, want: false},
		{name: "custom base", apiBase: "https://proxy.example/v1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLegacyZAICodingPlanBase(tt.apiBase); got != tt.want {
				t.Errorf("isLegacyZAICodingPlanBase(%q) = %v, want %v", tt.apiBase, got, tt.want)
			}
		})
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
		wantRequired bool
	}{
		{
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
			name:         "opencode zen base is not detected as go",
			providerName: "opencode",
			typ:          "opencode",
			apiBase:      "https://opencode.ai/zen/v1",
			want:         "",
			wantRequired: false,
		},
		{
			name:         "longer path segment is not detected as go",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://opencode.ai/zen/gopher/v1",
			want:         "",
			wantRequired: false,
		},
		{
			name:         "lookalike host with go path no longer matches",
			providerName: "corp-proxy",
			typ:          "openai",
			apiBase:      "https://proxy.example.com/opencode.ai/zen/go",
			want:         "",
			wantRequired: false,
		},
		{
			name:         "explicit port on the opencode.ai host still matches",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://opencode.ai:443/zen/go/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
			name:         "subdomain of opencode.ai matches",
			providerName: "oc-alias",
			typ:          "openai",
			apiBase:      "https://chat.opencode.ai/zen/go/v1",
			want:         openCodeGoSessionHeaderName,
			wantRequired: false,
		},
		{
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

	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() without session id error = %v, want success (opportunistic header)", err)
	}
	if got := sessionHeader(); got != "" {
		t.Fatalf("x-opencode-session = %q, want omitted when the call carries no session id", got)
	}

	options := map[string]interface{}{OptWireSessionID: "abc"}
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", options); err != nil {
		t.Fatalf("Chat() with session id error = %v", err)
	}
	if got := sessionHeader(); got != "abc" {
		t.Fatalf("x-opencode-session = %q, want %q", got, "abc")
	}
}

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

type providerBaseWarning struct {
	message string
	fields  map[string]interface{}
}

func captureProviderBaseWarnings(t *testing.T) *[]providerBaseWarning {
	t.Helper()
	var captured []providerBaseWarning
	previous := providerBaseWarn
	providerBaseWarn = func(_ string, message string, extra map[string]interface{}) {
		captured = append(captured, providerBaseWarning{message: message, fields: extra})
	}
	zaiCodingPlanLegacyBaseWarned.Store(false)
	t.Cleanup(func() {
		providerBaseWarn = previous
		zaiCodingPlanLegacyBaseWarned.Store(false)
	})
	return &captured
}

func assertLegacyBaseWarning(t *testing.T, warnings []providerBaseWarning, wantConfigured string) {
	t.Helper()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want exactly 1: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0].message, catalog.ZAICodingPlanAPIBase) {
		t.Errorf("warning %q does not point at the coding base", warnings[0].message)
	}
	if got := warnings[0].fields["suggested_api_base"]; got != catalog.ZAICodingPlanAPIBase {
		t.Errorf("suggested_api_base = %v, want %q", got, catalog.ZAICodingPlanAPIBase)
	}
	if got := warnings[0].fields["configured_api_base"]; got != wantConfigured {
		t.Errorf("configured_api_base = %v, want the value found on disk %q", got, wantConfigured)
	}
}

func TestResolveProviderSelection_WarnsOnceOnLegacyZAICodingPlanBase(t *testing.T) {
	withoutCatalogIndex(t)
	warnings := captureProviderBaseWarnings(t)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "zai_coding_plan"
	cfg.Agents.Defaults.Model = "glm-4.6"
	cfg.Providers = &config.ProvidersConfig{
		ZAICodingPlan: config.ProviderConfig{
			APIKey:  "zai-key",
			APIBase: "  https://API.Z.AI/api/paas/v4/  ",
		},
	}

	for range 2 {
		if _, err := resolveProviderSelection(cfg); err != nil {
			t.Fatalf("resolveProviderSelection() error = %v", err)
		}
	}

	assertLegacyBaseWarning(t, *warnings, "https://API.Z.AI/api/paas/v4/")
}

func TestResolveProviderSelection_WarnsOnNamedLegacyZAICodingPlanBase(t *testing.T) {
	withoutCatalogIndex(t)
	warnings := captureProviderBaseWarnings(t)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "webui-zai"
	cfg.Agents.Defaults.Model = "glm-4.6"
	cfg.Providers = zaiNamedConfig("webui-zai", "zai_coding_plan", catalog.ZAIAPIBase).Providers

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}

	assertLegacyBaseWarning(t, *warnings, catalog.ZAIAPIBase)
	if got.apiBase != catalog.ZAIAPIBase {
		t.Errorf("apiBase = %q, want the stored value %q", got.apiBase, catalog.ZAIAPIBase)
	}
}

func TestResolveProviderSelection_NoWarningForZAICodingPlanCodingBase(t *testing.T) {
	withoutCatalogIndex(t)
	warnings := captureProviderBaseWarnings(t)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "zai_coding_plan"
	cfg.Agents.Defaults.Model = "glm-4.6"
	cfg.Providers = &config.ProvidersConfig{
		ZAICodingPlan: config.ProviderConfig{APIKey: "zai-key", APIBase: catalog.ZAICodingPlanAPIBase},
	}

	if _, err := resolveProviderSelection(cfg); err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %d, want none for the coding base: %v", len(*warnings), *warnings)
	}
}

func TestResolveProviderSelection_NoWarningForGeneralZAIProvider(t *testing.T) {
	withoutCatalogIndex(t)
	warnings := captureProviderBaseWarnings(t)

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "zai"
	cfg.Agents.Defaults.Model = "glm-4.6"
	cfg.Providers = &config.ProvidersConfig{
		ZAICodingPlan: config.ProviderConfig{APIKey: "zai-key", APIBase: catalog.ZAIAPIBase},
	}

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %d, want none for the general zai endpoint: %v", len(*warnings), *warnings)
	}
	if got.apiBase != catalog.ZAIAPIBase {
		t.Errorf("apiBase = %q, want the general endpoint %q", got.apiBase, catalog.ZAIAPIBase)
	}
}

func TestResolveProviderSelection_KeepsExplicitZAICodingPlanBase(t *testing.T) {
	withoutCatalogIndex(t)
	warnings := captureProviderBaseWarnings(t)

	const explicit = "https://proxy.example/zaicoding/v1"

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Provider = "zai_coding_plan"
	cfg.Agents.Defaults.Model = "glm-4.6"
	cfg.Providers = &config.ProvidersConfig{
		ZAICodingPlan: config.ProviderConfig{APIKey: "zai-key", APIBase: explicit},
	}

	got, err := resolveProviderSelection(cfg)
	if err != nil {
		t.Fatalf("resolveProviderSelection() error = %v", err)
	}
	if got.apiBase != explicit {
		t.Errorf("apiBase = %q, want the explicit value %q", got.apiBase, explicit)
	}
	if cfg.Providers.ZAICodingPlan.APIBase != explicit {
		t.Errorf("cfg.Providers.ZAICodingPlan.APIBase = %q, want the explicit value %q to survive untouched", cfg.Providers.ZAICodingPlan.APIBase, explicit)
	}
	if len(*warnings) != 0 {
		t.Fatalf("warnings = %d, want none for a custom base: %v", len(*warnings), *warnings)
	}
}

func TestResolveProviderSelection_KeepsStoredLegacyZAICodingPlanBase(t *testing.T) {
	withoutCatalogIndex(t)

	tests := []struct {
		name     string
		cfg      func() *config.Config
		stored   func(*config.Config) string
		provider string
	}{
		{
			name: "typed block",
			cfg: func() *config.Config {
				cfg := config.DefaultConfig()
				cfg.Agents.Defaults.Provider = "zai_coding_plan"
				cfg.Agents.Defaults.Model = "glm-4.6"
				cfg.Providers = &config.ProvidersConfig{
					ZAICodingPlan: config.ProviderConfig{APIKey: "zai-key", APIBase: catalog.ZAIAPIBase},
				}
				return cfg
			},
			stored:   func(cfg *config.Config) string { return cfg.Providers.ZAICodingPlan.APIBase },
			provider: "",
		},
		{
			name: "named entry",
			cfg: func() *config.Config {
				cfg := config.DefaultConfig()
				cfg.Agents.Defaults.Provider = "webui-zai"
				cfg.Agents.Defaults.Model = "glm-4.6"
				cfg.Providers = zaiNamedConfig("webui-zai", "zai_coding_plan", catalog.ZAIAPIBase).Providers
				return cfg
			},
			stored:   func(cfg *config.Config) string { return cfg.Providers.Named["webui-zai"].APIBase },
			provider: "webui-zai",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := captureProviderBaseWarnings(t)

			cfg := tt.cfg()
			got, err := resolveProviderSelection(cfg)
			if err != nil {
				t.Fatalf("resolveProviderSelection() error = %v", err)
			}

			if stored := tt.stored(cfg); stored != catalog.ZAIAPIBase {
				t.Errorf("stored api_base = %q, want the legacy value %q to survive untouched", stored, catalog.ZAIAPIBase)
			}
			if got.apiBase != catalog.ZAIAPIBase {
				t.Errorf("apiBase = %q, want the stored legacy value %q to be used as is", got.apiBase, catalog.ZAIAPIBase)
			}
			assertLegacyBaseWarning(t, *warnings, catalog.ZAIAPIBase)
		})
	}
}
