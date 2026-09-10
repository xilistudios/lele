package providers

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
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
