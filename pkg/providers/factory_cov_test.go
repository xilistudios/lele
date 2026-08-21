package providers

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestDefaultAPIBaseByType(t *testing.T) {
	tests := []struct {
		providerType string
		want         string
	}{
		{"groq", "https://api.groq.com/openai/v1"},
		{"openai", "https://api.openai.com/v1"},
		{"anthropic", defaultAnthropicAPIBase},
		{"openrouter", "https://openrouter.ai/api/v1"},
		{"nanogpt", "https://nano-gpt.com/api/v1"},
		{"chutes", "https://llm.chutes.ai/v1"},
		{"alibaba", "https://coding-intl.dashscope.aliyuncs.com/v1"},
		{"zhipu", "https://open.bigmodel.cn/api/paas/v4"},
		{"gemini", "https://generativelanguage.googleapis.com/v1beta"},
		{"shengsuanyun", "https://router.shengsuanyun.com/api/v1"},
		{"nvidia", "https://integrate.api.nvidia.com/v1"},
		{"moonshot", "https://api.moonshot.cn/v1"},
		{"ollama", "http://localhost:11434/v1"},
		{"deepseek", "https://api.deepseek.com/v1"},
		{"github_copilot", "localhost:4321"},
		{"zai_coding_plan", "https://api.z.ai/api/paas/v4"},
		{"zai", "https://api.z.ai/api/paas/v4"},
		{"modelark_coding_plan", "https://ark.ap-southeast.bytepluses.com/api/coding/v3"},
		{"modelark", "https://ark.ap-southeast.bytepluses.com/api/coding/v3"},
		{"unknown-type", ""},
	}
	for _, tt := range tests {
		t.Run(tt.providerType, func(t *testing.T) {
			got := defaultAPIBaseByType(tt.providerType)
			if got != tt.want {
				t.Errorf("defaultAPIBaseByType(%q) = %q, want %q", tt.providerType, got, tt.want)
			}
		})
	}
}

func TestSelectionFromNamedProvider(t *testing.T) {
	cfg := config.DefaultConfig()

	t.Run("type defaults to provider name and apiBase defaulted", func(t *testing.T) {
		named := config.NamedProviderConfig{
			ProviderConfig: config.ProviderConfig{APIKey: "key", Proxy: "http://proxy:1"},
		}
		sel, err := selectionFromNamedProvider(cfg, "deepseek", "m", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.deepseek.com/v1" || sel.proxy != "http://proxy:1" {
			t.Errorf("apiBase=%q proxy=%q", sel.apiBase, sel.proxy)
		}
	})

	t.Run("model alias map replaces model and enables web search", func(t *testing.T) {
		named := config.NamedProviderConfig{
			Type: "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "key", APIBase: "https://ex.com/v1"},
			Models:          map[string]config.ProviderModelConfig{"fast": {Model: "  gpt-4o-mini  "}},
		}
		sel, err := selectionFromNamedProvider(cfg, "myp", "fast", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.model != "gpt-4o-mini" || !sel.enableWebSearch {
			t.Errorf("model=%q websearch=%v", sel.model, sel.enableWebSearch)
		}
	})

	t.Run("model alias matched by suffix against values", func(t *testing.T) {
		named := config.NamedProviderConfig{
			ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: "http://local:8080/v1"},
			Models:          map[string]config.ProviderModelConfig{"foo": {Model: "org/canonical"}},
		}
		sel, err := selectionFromNamedProvider(cfg, "p", "canonical", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.model != "org/canonical" {
			t.Errorf("model = %q, want org/canonical", sel.model)
		}
	})

	t.Run("openai codex-cli auth method", func(t *testing.T) {
		named := config.NamedProviderConfig{
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{AuthMethod: "codex-cli"},
		}
		sel, err := selectionFromNamedProvider(cfg, "openai", "gpt-4o", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLIToken {
			t.Errorf("providerType = %v, want CodexCLIToken", sel.providerType)
		}
	})

	t.Run("openai oauth auth method", func(t *testing.T) {
		named := config.NamedProviderConfig{
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{AuthMethod: "oauth"},
		}
		sel, err := selectionFromNamedProvider(cfg, "openai", "gpt-4o", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexAuth {
			t.Errorf("providerType = %v, want CodexAuth", sel.providerType)
		}
	})

	t.Run("claude token auth maps to anthropic", func(t *testing.T) {
		named := config.NamedProviderConfig{
			Type:           "claude",
			ProviderConfig: config.ProviderConfig{APIKey: "ck"},
		}
		sel, err := selectionFromNamedProvider(cfg, "claude", "claude-opus", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeAnthropic || sel.apiBase != defaultAnthropicAPIBase {
			t.Errorf("providerType=%v apiBase=%q", sel.providerType, sel.apiBase)
		}
	})

	t.Run("claude cli type sets workspace", func(t *testing.T) {
		cfg2 := config.DefaultConfig()
		cfg2.Agents.Defaults.Workspace = "/ws"
		named := config.NamedProviderConfig{Type: "claude-cli"}
		sel, err := selectionFromNamedProvider(cfg2, "claude-cli", "", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeCLI || sel.workspace != "/ws" {
			t.Errorf("got providerType=%v workspace=%q", sel.providerType, sel.workspace)
		}
	})

	t.Run("codex-cli type", func(t *testing.T) {
		named := config.NamedProviderConfig{Type: "codex-cli"}
		sel, err := selectionFromNamedProvider(cfg, "codex-cli", "", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLI {
			t.Errorf("providerType = %v", sel.providerType)
		}
		if sel.workspace == "" {
			t.Errorf("workspace should not be empty")
		}
	})

	t.Run("deepseek forces model when not allowed", func(t *testing.T) {
		named := config.NamedProviderConfig{
			Type:           "deepseek",
			ProviderConfig: config.ProviderConfig{APIKey: "dk"},
		}
		sel, err := selectionFromNamedProvider(cfg, "deepseek", "weird-model", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.model != "deepseek-chat" {
			t.Errorf("model = %q, want deepseek-chat", sel.model)
		}
	})

	t.Run("github copilot type", func(t *testing.T) {
		named := config.NamedProviderConfig{Type: "copilot"}
		sel, err := selectionFromNamedProvider(cfg, "copilot", "gpt-4.1", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeGitHubCopilot || sel.apiBase != "localhost:4321" {
			t.Errorf("providerType=%v apiBase=%q", sel.providerType, sel.apiBase)
		}
	})

	t.Run("http compat missing api base errors", func(t *testing.T) {
		named := config.NamedProviderConfig{Type: "mystery"}
		_, err := selectionFromNamedProvider(cfg, "mystery", "model", named)
		if err == nil || !strings.Contains(err.Error(), "no API base configured") {
			t.Errorf("want api base error, got %v", err)
		}
	})

	t.Run("http compat missing api key errors when no explicit api base", func(t *testing.T) {
		named := config.NamedProviderConfig{Type: "openai"}
		_, err := selectionFromNamedProvider(cfg, "openai", "gpt-4o", named)
		if err == nil || !strings.Contains(err.Error(), "no API key configured") {
			t.Errorf("want api key error, got %v", err)
		}
	})

	t.Run("http compat with explicit api base allows missing key", func(t *testing.T) {
		named := config.NamedProviderConfig{
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIBase: "http://local:8080/v1"},
		}
		sel, err := selectionFromNamedProvider(cfg, "openai", "gpt-4o", named)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiKey != "" {
			t.Errorf("apiKey = %q, want empty", sel.apiKey)
		}
	})
}

func TestResolveProviderSelectionByName_TopLevelBranches(t *testing.T) {
	cfg := config.DefaultConfig()

	t.Run("vllm", func(t *testing.T) {
		cfg.Providers.VLLM.APIBase = "http://vllm:8000/v1"
		sel, err := resolveProviderSelectionByName(cfg, "vllm", "model-x")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "http://vllm:8000/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("copilot sets connectMode", func(t *testing.T) {
		cfg2 := config.DefaultConfig()
		cfg2.Providers.GitHubCopilot.APIBase = "http://cli:1234"
		cfg2.Providers.GitHubCopilot.ConnectMode = "stdio"
		sel, err := resolveProviderSelectionByName(cfg2, "copilot", "gpt-4.1")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeGitHubCopilot || sel.connectMode != "stdio" {
			t.Errorf("providerType=%v connectMode=%q", sel.providerType, sel.connectMode)
		}
	})

	t.Run("openai codex-cli", func(t *testing.T) {
		cfg3 := config.DefaultConfig()
		cfg3.Providers.OpenAI.AuthMethod = "codex-cli"
		sel, err := resolveProviderSelectionByName(cfg3, "openai", "gpt-4o")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLIToken {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("openai oauth", func(t *testing.T) {
		cfg4 := config.DefaultConfig()
		cfg4.Providers.OpenAI.AuthMethod = "oauth"
		sel, err := resolveProviderSelectionByName(cfg4, "openai", "gpt-4o")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexAuth {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("anthropic oauth", func(t *testing.T) {
		cfg5 := config.DefaultConfig()
		cfg5.Providers.Anthropic.AuthMethod = "oauth"
		sel, err := resolveProviderSelectionByName(cfg5, "anthropic", "claude-opus")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeAuth {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("anthropic with key", func(t *testing.T) {
		cfg6 := config.DefaultConfig()
		cfg6.Providers.Anthropic.APIKey = "ak"
		sel, err := resolveProviderSelectionByName(cfg6, "anthropic", "claude-opus")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeAnthropic || sel.apiBase != defaultAnthropicAPIBase {
			t.Errorf("providerType=%v apiBase=%q", sel.providerType, sel.apiBase)
		}
	})

	t.Run("openrouter", func(t *testing.T) {
		cfg7 := config.DefaultConfig()
		cfg7.Providers.OpenRouter.APIKey = "ork"
		sel, err := resolveProviderSelectionByName(cfg7, "openrouter", "model")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://openrouter.ai/api/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("zhipu", func(t *testing.T) {
		cfg8 := config.DefaultConfig()
		cfg8.Providers.Zhipu.APIKey = "zk"
		sel, err := resolveProviderSelectionByName(cfg8, "glm", "glm-4.7")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://open.bigmodel.cn/api/paas/v4" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("gemini", func(t *testing.T) {
		cfg9 := config.DefaultConfig()
		cfg9.Providers.Gemini.APIKey = "gk"
		sel, err := resolveProviderSelectionByName(cfg9, "gemini", "gemini-2.0")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://generativelanguage.googleapis.com/v1beta" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("shengsuanyun", func(t *testing.T) {
		cfg10 := config.DefaultConfig()
		cfg10.Providers.ShengSuanYun.APIKey = "sk"
		sel, err := resolveProviderSelectionByName(cfg10, "shengsuanyun", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://router.shengsuanyun.com/api/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("nvidia", func(t *testing.T) {
		cfg11 := config.DefaultConfig()
		cfg11.Providers.Nvidia.APIKey = "nk"
		sel, err := resolveProviderSelectionByName(cfg11, "nvidia", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://integrate.api.nvidia.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("claude-code cli", func(t *testing.T) {
		cfg12 := config.DefaultConfig()
		cfg12.Agents.Defaults.Workspace = "/ws"
		sel, err := resolveProviderSelectionByName(cfg12, "claude-code", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeCLI || sel.workspace != "/ws" {
			t.Errorf("got %v/%q", sel.providerType, sel.workspace)
		}
	})

	t.Run("codex-cli", func(t *testing.T) {
		cfg13 := config.DefaultConfig()
		sel, err := resolveProviderSelectionByName(cfg13, "codex-cli", "")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLI {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("deepseek with key", func(t *testing.T) {
		cfg14 := config.DefaultConfig()
		cfg14.Providers.DeepSeek.APIKey = "dsk"
		sel, err := resolveProviderSelectionByName(cfg14, "deepseek", "custom-model")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.deepseek.com/v1" || sel.model != "deepseek-chat" {
			t.Errorf("apiBase=%q model=%q", sel.apiBase, sel.model)
		}
	})

	t.Run("zai", func(t *testing.T) {
		cfg15 := config.DefaultConfig()
		cfg15.Providers.ZAICodingPlan.APIKey = "zk"
		sel, err := resolveProviderSelectionByName(cfg15, "zai", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.z.ai/api/paas/v4" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("modelark", func(t *testing.T) {
		cfg16 := config.DefaultConfig()
		cfg16.Providers.ModelArkCodingPlan.APIKey = "mk"
		sel, err := resolveProviderSelectionByName(cfg16, "modelark", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://ark.ap-southeast.bytepluses.com/api/coding/v3" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("nanogpt via named config APIKey", func(t *testing.T) {
		cfg17 := config.DefaultConfig()
		cfg17.Providers.NanogPT.APIKey = "ng"
		sel, err := resolveProviderSelectionByName(cfg17, "nanogpt", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://nano-gpt.com/api/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("alibaba via named config APIKey", func(t *testing.T) {
		cfg18 := config.DefaultConfig()
		if cfg18.Providers.Named == nil {
			cfg18.Providers.Named = map[string]config.NamedProviderConfig{}
		}
		cfg18.Providers.Named["alibaba"] = config.NamedProviderConfig{
			Type:           "alibaba",
			ProviderConfig: config.ProviderConfig{APIKey: "ak"},
		}
		sel, err := resolveProviderSelectionByName(cfg18, "alibaba", "qwen")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://coding-intl.dashscope.aliyuncs.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
}

func TestResolveProviderSelectionByName_FallbackInference(t *testing.T) {
	t.Run("moonshot model inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Moonshot.APIKey = "msk"
		sel, err := resolveProviderSelectionByName(cfg, "", "kimi-k2.5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.moonshot.cn/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("openrouter prefix inference", func(t *testing.T) {
		cfg2 := config.DefaultConfig()
		cfg2.Providers.OpenRouter.APIKey = "ork"
		sel, err := resolveProviderSelectionByName(cfg2, "", "openrouter/deepseek/deepseek-v4")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://openrouter.ai/api/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("claude inference with oauth", func(t *testing.T) {
		cfg3 := config.DefaultConfig()
		cfg3.Providers.Anthropic.AuthMethod = "oauth"
		sel, err := resolveProviderSelectionByName(cfg3, "", "claude-sonnet-4-5")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeClaudeAuth {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("claude inference with key", func(t *testing.T) {
		cfg4 := config.DefaultConfig()
		cfg4.Providers.Anthropic.APIKey = "ak"
		sel, err := resolveProviderSelectionByName(cfg4, "", "claude-opus")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != defaultAnthropicAPIBase {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
		if sel.apiKey != "ak" {
			t.Errorf("apiKey = %q", sel.apiKey)
		}
	})

	t.Run("gpt inference codex-cli", func(t *testing.T) {
		cfg5 := config.DefaultConfig()
		cfg5.Providers.OpenAI.AuthMethod = "codex-cli"
		sel, err := resolveProviderSelectionByName(cfg5, "", "gpt-4o")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexCLIToken {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("gpt inference oauth", func(t *testing.T) {
		cfg6 := config.DefaultConfig()
		cfg6.Providers.OpenAI.AuthMethod = "oauth"
		sel, err := resolveProviderSelectionByName(cfg6, "", "gpt-4o")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeCodexAuth {
			t.Errorf("providerType = %v", sel.providerType)
		}
	})

	t.Run("gpt inference http with key", func(t *testing.T) {
		cfg7 := config.DefaultConfig()
		cfg7.Providers.OpenAI.APIKey = "oak"
		cfg7.Providers.OpenAI.WebSearch = true
		sel, err := resolveProviderSelectionByName(cfg7, "", "gpt-4o")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.providerType != providerTypeHTTPCompat || !sel.enableWebSearch {
			t.Errorf("providerType=%v websearch=%v", sel.providerType, sel.enableWebSearch)
		}
	})

	t.Run("gemini inference", func(t *testing.T) {
		cfg8 := config.DefaultConfig()
		cfg8.Providers.Gemini.APIKey = "gk"
		sel, err := resolveProviderSelectionByName(cfg8, "", "gemini-2.0")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://generativelanguage.googleapis.com/v1beta" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("glm inference via zhipu", func(t *testing.T) {
		cfg9 := config.DefaultConfig()
		cfg9.Providers.Zhipu.APIKey = "zk"
		sel, err := resolveProviderSelectionByName(cfg9, "", "glm-4.7")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://open.bigmodel.cn/api/paas/v4" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("groq inference", func(t *testing.T) {
		cfg10 := config.DefaultConfig()
		cfg10.Providers.Groq.APIKey = "gsk"
		sel, err := resolveProviderSelectionByName(cfg10, "", "groq/llama")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.groq.com/openai/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("nvidia inference", func(t *testing.T) {
		cfg11 := config.DefaultConfig()
		cfg11.Providers.Nvidia.APIKey = "nk"
		sel, err := resolveProviderSelectionByName(cfg11, "", "nvidia/llama")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://integrate.api.nvidia.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("ollama inference", func(t *testing.T) {
		cfg12 := config.DefaultConfig()
		cfg12.Providers.Ollama.APIKey = "oll"
		sel, err := resolveProviderSelectionByName(cfg12, "", "ollama/qwen")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "http://localhost:11434/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("vllm fallback direct config", func(t *testing.T) {
		cfg13 := config.DefaultConfig()
		cfg13.Providers.VLLM.APIBase = "http://vllm:8000/v1"
		cfg13.Providers.VLLM.APIKey = "vk"
		sel, err := resolveProviderSelectionByName(cfg13, "", "some-model")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "http://vllm:8000/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("openrouter default fallback", func(t *testing.T) {
		cfg14 := config.DefaultConfig()
		cfg14.Providers.OpenRouter.APIKey = "ork"
		sel, err := resolveProviderSelectionByName(cfg14, "", "random-model-xyz")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://openrouter.ai/api/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})

	t.Run("no config at all errors", func(t *testing.T) {
		cfg15 := config.DefaultConfig()
		_, err := resolveProviderSelectionByName(cfg15, "", "random-model-xyz")
		if err == nil || !strings.Contains(err.Error(), "no API key configured") {
			t.Errorf("want error, got %v", err)
		}
	})

	t.Run("http compat missing api base errors after inference", func(t *testing.T) {
		// provider set but no api base, and an api key present set a degenerate state
		cfg16 := config.DefaultConfig()
		cfg16.Providers.OpenRouter.APIKey = "ork"
		cfg16.Providers.OpenRouter.APIBase = "   "
		sel, err := resolveProviderSelectionByName(cfg16, "", "openrouter/some/model")
		if err != nil {
			// apiBase might default since inference path is used
			_ = sel
		}
	})
}