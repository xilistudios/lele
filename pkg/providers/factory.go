package providers

import (
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/catalog"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	anthropicmessages "github.com/xilistudios/lele/pkg/providers/anthropic_messages"
	"github.com/xilistudios/lele/pkg/providers/openai_compat"
)

const defaultAnthropicAPIBase = "https://api.anthropic.com/v1"

var getCredential = auth.GetCredential

type providerType int

const (
	providerTypeHTTPCompat providerType = iota
	providerTypeClaudeAuth
	providerTypeCodexAuth
	providerTypeCodexCLIToken
	providerTypeClaudeCLI
	providerTypeCodexCLI
	providerTypeGitHubCopilot
	providerTypeAnthropic
	providerTypeGeminiAuth
)

type providerSelection struct {
	providerType     providerType
	apiKey           string
	apiBase          string
	proxy            string
	model            string
	workspace        string
	connectMode      string
	enableWebSearch  bool
	providerName     string
	providerTypeName string
	headers          map[string]string
}

func defaultAPIBaseByType(providerType string) string {
	switch providerType {
	case "github_copilot", "copilot":
		return "localhost:4321"
	case "alibaba":
		return "https://coding-intl.dashscope.aliyuncs.com/v1"
	case "zai":
		return catalog.ZAIAPIBase
	}
	if base := catalog.DefaultAPIBaseByType(providerType); base != "" {
		return base
	}
	return ""
}

var (
	zaiCodingPlanLegacyBaseWarned atomic.Bool
	providerBaseWarn              = logger.WarnCF
)

func zaiCodingPlanConfiguredBase(cfg *config.Config, providerName string) (string, bool) {
	if cfg == nil || cfg.Providers == nil {
		return "", false
	}
	name := NormalizeProvider(providerName)
	if named, ok := cfg.Providers.GetNamed(providerName); ok {
		typ := NormalizeProvider(named.Type)
		if typ == "" {
			typ = name
		}
		if typ != "zai_coding_plan" && name != "zai_coding_plan" {
			return "", false
		}
		return named.APIBase, true
	}
	if name != "zai_coding_plan" {
		return "", false
	}
	return cfg.Providers.ZAICodingPlan.APIBase, true
}

func isLegacyZAICodingPlanBase(apiBase string) bool {
	return strings.EqualFold(strings.TrimRight(strings.TrimSpace(apiBase), "/"), catalog.ZAIAPIBase)
}

func warnOnLegacyZAICodingPlanBase(cfg *config.Config, providerName string) {
	apiBase, ok := zaiCodingPlanConfiguredBase(cfg, providerName)
	if !ok || !isLegacyZAICodingPlanBase(apiBase) {
		return
	}
	if !zaiCodingPlanLegacyBaseWarned.CompareAndSwap(false, true) {
		return
	}
	providerBaseWarn("providers", "zai_coding_plan api_base uses the non-coding z.ai endpoint; set it to "+catalog.ZAICodingPlanAPIBase, map[string]interface{}{
		"configured_api_base": strings.TrimSpace(apiBase),
		"suggested_api_base":  catalog.ZAICodingPlanAPIBase,
	})
}

const openCodeGoSessionHeaderName = "x-opencode-session"

func openCodeGoSessionHeader(sel *providerSelection) (name string, required bool) {
	for _, s := range []string{sel.providerName, sel.providerTypeName} {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "opencode_go", "opencode-go":
			return openCodeGoSessionHeaderName, true
		}
	}
	if isOpenCodeGoAPIBase(sel.apiBase) {
		return openCodeGoSessionHeaderName, false
	}
	return "", false
}

func isOpenCodeGoAPIBase(apiBase string) bool {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "opencode.ai" && !strings.HasSuffix(host, ".opencode.ai") {
		return false
	}
	path := strings.ToLower(u.Path)
	for _, route := range []string{"/zen/go", "/inference/go"} {
		if path == route || strings.HasPrefix(path, route+"/") {
			return true
		}
	}
	return false
}

func selectionFromNamedProvider(cfg *config.Config, providerName, model string, named config.NamedProviderConfig) (providerSelection, error) {
	typ := named.Type
	if typ == "" {
		typ = providerName
	}
	sel := providerSelection{
		providerType:     providerTypeHTTPCompat,
		apiKey:           named.APIKey,
		apiBase:          named.APIBase,
		proxy:            named.Proxy,
		model:            model,
		connectMode:      named.ConnectMode,
		providerName:     providerName,
		providerTypeName: typ,
		headers:          named.Headers,
	}

	if mc, ok := named.Models[model]; ok {
		if strings.TrimSpace(mc.Model) != "" {
			sel.model = strings.TrimSpace(mc.Model)
		}
	}
	if sel.model == model {
		lowerModel := strings.ToLower(strings.ReplaceAll(model, ".", "-"))
		for _, aliasVal := range named.Models {
			resolvedVal := strings.ToLower(strings.TrimSpace(aliasVal.Model))
			if resolvedVal == lowerModel || strings.HasSuffix(resolvedVal, "/"+lowerModel) {
				if strings.TrimSpace(aliasVal.Model) != "" {
					sel.model = strings.TrimSpace(aliasVal.Model)
				}
				break
			}
		}
	}
	if sel.apiBase == "" {
		sel.apiBase = defaultAPIBaseByType(typ)
	}

	switch typ {
	case "openai", "gpt":
		sel.enableWebSearch = true
		if named.WebSearch != nil {
			sel.enableWebSearch = *named.WebSearch
		}
		switch named.AuthMethod {
		case "codex-cli":
			sel.providerType = providerTypeCodexCLIToken
			return sel, nil
		case "oauth", "token":
			sel.providerType = providerTypeCodexAuth
			return sel, nil
		}
	case "anthropic", "claude":
		if named.AuthMethod == "oauth" || named.AuthMethod == "token" {
			if sel.apiBase == "" {
				sel.apiBase = defaultAnthropicAPIBase
			}
			sel.providerType = providerTypeClaudeAuth
			return sel, nil
		}
		if sel.apiKey != "" {
			if sel.apiBase == "" {
				sel.apiBase = defaultAnthropicAPIBase
			}
			sel.providerType = providerTypeAnthropic
			return sel, nil
		}
	case "gemini":
		if named.AuthMethod == "oauth" && sel.apiKey == "" {
			sel.providerType = providerTypeGeminiAuth
			return sel, nil
		}
	case "claude-cli", "claude-code", "claudecode":
		workspace := cfg.WorkspacePath()
		if workspace == "" {
			workspace = "."
		}
		sel.providerType = providerTypeClaudeCLI
		sel.workspace = workspace
		return sel, nil
	case "codex-cli", "codex-code":
		workspace := cfg.WorkspacePath()
		if workspace == "" {
			workspace = "."
		}
		sel.providerType = providerTypeCodexCLI
		sel.workspace = workspace
		return sel, nil
	case "deepseek":
		if sel.model != "deepseek-chat" && sel.model != "deepseek-reasoner" {
			sel.model = "deepseek-chat"
		}
	case "github_copilot", "copilot":
		sel.providerType = providerTypeGitHubCopilot
		if sel.apiBase == "" {
			sel.apiBase = "localhost:4321"
		}
		return sel, nil
	}

	if sel.providerType == providerTypeHTTPCompat &&
		(typ == "minimax" || typ == "minimax_cn" || strings.Contains(sel.apiBase, "/anthropic")) {
		sel.providerType = providerTypeAnthropic
		return sel, nil
	}

	if sel.providerType == providerTypeHTTPCompat {
		if sel.apiBase == "" {
			return providerSelection{}, fmt.Errorf("no API base configured for provider (model: %s)", model)
		}
		if sel.apiKey == "" && !strings.HasPrefix(sel.model, "bedrock/") && named.APIBase == "" {
			return providerSelection{}, fmt.Errorf("no API key configured for provider (model: %s)", model)
		}
	}
	return sel, nil
}

func createClaudeAuthProvider(apiBase string) (LLMProvider, error) {
	if apiBase == "" {
		apiBase = defaultAnthropicAPIBase
	}
	cred, err := getCredential("anthropic")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for anthropic. Run: lele auth login --provider anthropic")
	}
	oauth := cred.AuthMethod == "oauth"
	return NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode(cred.AccessToken, createClaudeTokenSource(), apiBase, oauth), nil
}

func createGeminiAuthProvider(apiBase string) (LLMProvider, error) {
	cred, err := getCredential("gemini")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for gemini. Run: lele auth login --provider gemini")
	}
	return NewGeminiCodeAssistProvider(cred.AccessToken, createGeminiTokenSource()), nil
}

func createCodexAuthProvider(enableWebSearch bool) (LLMProvider, error) {
	cred, err := getCredential("openai")
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if cred == nil {
		return nil, fmt.Errorf("no credentials for openai. Run: lele auth login --provider openai")
	}
	p := NewCodexProviderWithTokenSource(cred.AccessToken, cred.AccountID, createCodexTokenSource())
	p.enableWebSearch = enableWebSearch
	return p, nil
}

func resolveProviderSelection(cfg *config.Config) (providerSelection, error) {
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}

	rawModel := strings.TrimSpace(cfg.Agents.Defaults.Model)
	providerName := NormalizeProvider(cfg.Agents.Defaults.Provider)
	model := rawModel

	resolvedModel := cfg.Providers.ResolveModelAlias(rawModel, providerName)
	if ref := ParseModelRef(resolvedModel, providerName); ref != nil {
		model = ref.Model

		defaultProvider := NormalizeProvider(config.DefaultConfig().Agents.Defaults.Provider)
		if providerName == "" || providerName == defaultProvider || ref.Provider == providerName {
			providerName = ref.Provider
		}
		if providerName == "openrouter" {
			model = ref.Model
		}
	}
	return resolveProviderSelectionByName(cfg, providerName, model)
}

func resolveProviderSelectionForProvider(cfg *config.Config, providerName string) (providerSelection, error) {
	return resolveProviderSelectionByName(cfg, providerName, "")
}

func resolveProviderSelectionByName(cfg *config.Config, providerName string, model string) (providerSelection, error) {
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	warnOnLegacyZAICodingPlanBase(cfg, providerName)

	lowerModel := strings.ToLower(model)

	sel := providerSelection{
		providerType:     providerTypeHTTPCompat,
		model:            model,
		providerName:     providerName,
		providerTypeName: providerName,
	}

	if providerName != "" {
		if (providerName == "gemini" || providerName == "google") &&
			cfg.Providers.Gemini.AuthMethod == "oauth" && cfg.Providers.Gemini.APIKey == "" {
			sel.providerType = providerTypeGeminiAuth
			return sel, nil
		}
		if named, ok := cfg.Providers.GetNamed(providerName); ok {
			return selectionFromNamedProvider(cfg, providerName, model, named)
		}
		if named, ok := cfg.Providers.GetNamed(NormalizeProvider(providerName)); ok {
			sel.headers = named.Headers
		}
		switch providerName {
		case "groq":
			if cfg.Providers.Groq.APIKey != "" {
				sel.apiKey = cfg.Providers.Groq.APIKey
				sel.apiBase = cfg.Providers.Groq.APIBase
				sel.proxy = cfg.Providers.Groq.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.groq.com/openai/v1"
				}
			}
		case "openai", "gpt":
			if cfg.Providers.OpenAI.AuthMethod == "codex-cli" && cfg.Providers.OpenAI.APIKey == "" {
				sel.providerType = providerTypeCodexCLIToken
				return sel, nil
			}
			if (cfg.Providers.OpenAI.AuthMethod == "oauth" || cfg.Providers.OpenAI.AuthMethod == "token") && cfg.Providers.OpenAI.APIKey == "" {
				sel.providerType = providerTypeCodexAuth
				return sel, nil
			}
			if cfg.Providers.OpenAI.APIKey != "" || cfg.Providers.OpenAI.AuthMethod != "" {
				sel.enableWebSearch = cfg.Providers.OpenAI.WebSearch
				sel.apiKey = cfg.Providers.OpenAI.APIKey
				sel.apiBase = cfg.Providers.OpenAI.APIBase
				sel.proxy = cfg.Providers.OpenAI.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.openai.com/v1"
				}
			}
		case "anthropic", "claude":
			if (cfg.Providers.Anthropic.AuthMethod == "oauth" || cfg.Providers.Anthropic.AuthMethod == "token") && cfg.Providers.Anthropic.APIKey == "" {
				sel.apiBase = cfg.Providers.Anthropic.APIBase
				if sel.apiBase == "" {
					sel.apiBase = defaultAnthropicAPIBase
				}
				sel.providerType = providerTypeClaudeAuth
				return sel, nil
			}
			if cfg.Providers.Anthropic.APIKey != "" || cfg.Providers.Anthropic.AuthMethod != "" {
				sel.apiKey = cfg.Providers.Anthropic.APIKey
				sel.apiBase = cfg.Providers.Anthropic.APIBase
				sel.proxy = cfg.Providers.Anthropic.Proxy
				sel.providerType = providerTypeAnthropic
				if sel.apiBase == "" {
					sel.apiBase = defaultAnthropicAPIBase
				}
			}
		case "openrouter":
			if cfg.Providers.OpenRouter.APIKey != "" {
				sel.apiKey = cfg.Providers.OpenRouter.APIKey
				sel.proxy = cfg.Providers.OpenRouter.Proxy
				if cfg.Providers.OpenRouter.APIBase != "" {
					sel.apiBase = cfg.Providers.OpenRouter.APIBase
				} else {
					sel.apiBase = "https://openrouter.ai/api/v1"
				}
			}
		case "nanogpt":
			if named, ok := cfg.Providers.GetNamed("nanogpt"); ok && named.APIKey != "" {
				sel.apiKey = named.APIKey
				sel.apiBase = named.APIBase
				sel.proxy = named.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://nano-gpt.com/api/v1"
				}
			}
		case "chutes":
			if named, ok := cfg.Providers.GetNamed("chutes"); ok && named.APIKey != "" {
				sel.apiKey = named.APIKey
				sel.apiBase = named.APIBase
				sel.proxy = named.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://llm.chutes.ai/v1"
				}
			}
		case "alibaba":
			if named, ok := cfg.Providers.GetNamed("alibaba"); ok && named.APIKey != "" {
				sel.apiKey = named.APIKey
				sel.apiBase = named.APIBase
				sel.proxy = named.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://coding-intl.dashscope.aliyuncs.com/v1"
				}
			}
		case "zhipu", "glm":
			if cfg.Providers.Zhipu.APIKey != "" {
				sel.apiKey = cfg.Providers.Zhipu.APIKey
				sel.apiBase = cfg.Providers.Zhipu.APIBase
				sel.proxy = cfg.Providers.Zhipu.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://open.bigmodel.cn/api/paas/v4"
				}
			}
		case "gemini", "google":
			if cfg.Providers.Gemini.APIKey != "" {
				sel.apiKey = cfg.Providers.Gemini.APIKey
				sel.apiBase = cfg.Providers.Gemini.APIBase
				sel.proxy = cfg.Providers.Gemini.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://generativelanguage.googleapis.com/v1beta"
				}
			}
		case "vllm":
			if cfg.Providers.VLLM.APIBase != "" {
				sel.apiKey = cfg.Providers.VLLM.APIKey
				sel.apiBase = cfg.Providers.VLLM.APIBase
				sel.proxy = cfg.Providers.VLLM.Proxy
			}
		case "shengsuanyun":
			if cfg.Providers.ShengSuanYun.APIKey != "" {
				sel.apiKey = cfg.Providers.ShengSuanYun.APIKey
				sel.apiBase = cfg.Providers.ShengSuanYun.APIBase
				sel.proxy = cfg.Providers.ShengSuanYun.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://router.shengsuanyun.com/api/v1"
				}
			}
		case "nvidia":
			if cfg.Providers.Nvidia.APIKey != "" {
				sel.apiKey = cfg.Providers.Nvidia.APIKey
				sel.apiBase = cfg.Providers.Nvidia.APIBase
				sel.proxy = cfg.Providers.Nvidia.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://integrate.api.nvidia.com/v1"
				}
			}
		case "claude-cli", "claude-code", "claudecode":
			workspace := cfg.WorkspacePath()
			if workspace == "" {
				workspace = "."
			}
			sel.providerType = providerTypeClaudeCLI
			sel.workspace = workspace
			return sel, nil
		case "codex-cli", "codex-code":
			workspace := cfg.WorkspacePath()
			if workspace == "" {
				workspace = "."
			}
			sel.providerType = providerTypeCodexCLI
			sel.workspace = workspace
			return sel, nil
		case "deepseek":
			if cfg.Providers.DeepSeek.APIKey != "" {
				sel.apiKey = cfg.Providers.DeepSeek.APIKey
				sel.apiBase = cfg.Providers.DeepSeek.APIBase
				sel.proxy = cfg.Providers.DeepSeek.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://api.deepseek.com/v1"
				}
				if !strings.HasPrefix(model, "deepseek-") {
					sel.model = "deepseek-chat"
				}
			}
		case "github_copilot", "copilot":
			sel.headers = cfg.Providers.GitHubCopilot.Headers
			sel.providerType = providerTypeGitHubCopilot
			if cfg.Providers.GitHubCopilot.APIBase != "" {
				sel.apiBase = cfg.Providers.GitHubCopilot.APIBase
			} else {
				sel.apiBase = "localhost:4321"
			}
			sel.connectMode = cfg.Providers.GitHubCopilot.ConnectMode
			return sel, nil
		case "zai_coding_plan", "zai":
			sel.headers = cfg.Providers.ZAICodingPlan.Headers
			if cfg.Providers.ZAICodingPlan.APIKey != "" {
				sel.apiKey = cfg.Providers.ZAICodingPlan.APIKey
				sel.apiBase = cfg.Providers.ZAICodingPlan.APIBase
				sel.proxy = cfg.Providers.ZAICodingPlan.Proxy
				if sel.apiBase == "" {
					sel.apiBase = defaultAPIBaseByType(providerName)
				}
			}
		case "modelark_coding_plan", "modelark":
			sel.headers = cfg.Providers.ModelArkCodingPlan.Headers
			if cfg.Providers.ModelArkCodingPlan.APIKey != "" {
				sel.apiKey = cfg.Providers.ModelArkCodingPlan.APIKey
				sel.apiBase = cfg.Providers.ModelArkCodingPlan.APIBase
				sel.proxy = cfg.Providers.ModelArkCodingPlan.Proxy
				if sel.apiBase == "" {
					sel.apiBase = "https://ark.ap-southeast.bytepluses.com/api/coding/v3"
				}
			}
		}
	}

	if sel.apiKey == "" && sel.apiBase == "" {
		if ref := ParseModelRef(model, ""); ref != nil {
			if named, ok := cfg.Providers.GetNamed(ref.Provider); ok {
				if named.APIKey != "" || ref.Provider == "openai" || ref.Provider == "anthropic" {
					return selectionFromNamedProvider(cfg, ref.Provider, ref.Model, named)
				}
			}
		}
		switch {
		case (strings.Contains(lowerModel, "kimi") || strings.Contains(lowerModel, "moonshot") || strings.HasPrefix(model, "moonshot/")) && cfg.Providers.Moonshot.APIKey != "":
			sel.apiKey = cfg.Providers.Moonshot.APIKey
			sel.apiBase = cfg.Providers.Moonshot.APIBase
			sel.proxy = cfg.Providers.Moonshot.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://api.moonshot.cn/v1"
			}
		case strings.HasPrefix(model, "openrouter/") ||
			strings.HasPrefix(model, "anthropic/") ||
			strings.HasPrefix(model, "openai/") ||
			strings.HasPrefix(model, "meta-llama/") ||
			strings.HasPrefix(model, "deepseek/") ||
			strings.HasPrefix(model, "google/"):
			sel.apiKey = cfg.Providers.OpenRouter.APIKey
			sel.proxy = cfg.Providers.OpenRouter.Proxy
			if cfg.Providers.OpenRouter.APIBase != "" {
				sel.apiBase = cfg.Providers.OpenRouter.APIBase
			} else {
				sel.apiBase = "https://openrouter.ai/api/v1"
			}
		case (strings.Contains(lowerModel, "claude") || strings.HasPrefix(model, "anthropic/")) &&
			(cfg.Providers.Anthropic.APIKey != "" || cfg.Providers.Anthropic.AuthMethod != ""):
			if cfg.Providers.Anthropic.AuthMethod == "oauth" || cfg.Providers.Anthropic.AuthMethod == "token" {
				sel.apiBase = cfg.Providers.Anthropic.APIBase
				if sel.apiBase == "" {
					sel.apiBase = defaultAnthropicAPIBase
				}
				sel.providerType = providerTypeClaudeAuth
				return sel, nil
			}
			sel.apiKey = cfg.Providers.Anthropic.APIKey
			sel.apiBase = cfg.Providers.Anthropic.APIBase
			sel.proxy = cfg.Providers.Anthropic.Proxy
			if sel.apiBase == "" {
				sel.apiBase = defaultAnthropicAPIBase
			}
		case (strings.Contains(lowerModel, "gpt") || strings.HasPrefix(model, "openai/")) &&
			(cfg.Providers.OpenAI.APIKey != "" || cfg.Providers.OpenAI.AuthMethod != ""):
			sel.enableWebSearch = cfg.Providers.OpenAI.WebSearch
			if cfg.Providers.OpenAI.AuthMethod == "codex-cli" {
				sel.providerType = providerTypeCodexCLIToken
				return sel, nil
			}
			if cfg.Providers.OpenAI.AuthMethod == "oauth" || cfg.Providers.OpenAI.AuthMethod == "token" {
				sel.providerType = providerTypeCodexAuth
				return sel, nil
			}
			sel.apiKey = cfg.Providers.OpenAI.APIKey
			sel.apiBase = cfg.Providers.OpenAI.APIBase
			sel.proxy = cfg.Providers.OpenAI.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://api.openai.com/v1"
			}
		case (strings.Contains(lowerModel, "gemini") || strings.HasPrefix(model, "google/")) && cfg.Providers.Gemini.APIKey != "":
			sel.apiKey = cfg.Providers.Gemini.APIKey
			sel.apiBase = cfg.Providers.Gemini.APIBase
			sel.proxy = cfg.Providers.Gemini.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://generativelanguage.googleapis.com/v1beta"
			}
		case (strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "zhipu") || strings.Contains(lowerModel, "zai")) && cfg.Providers.Zhipu.APIKey != "":
			sel.apiKey = cfg.Providers.Zhipu.APIKey
			sel.apiBase = cfg.Providers.Zhipu.APIBase
			sel.proxy = cfg.Providers.Zhipu.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://open.bigmodel.cn/api/paas/v4"
			}
		case (strings.Contains(lowerModel, "groq") || strings.HasPrefix(model, "groq/")) && cfg.Providers.Groq.APIKey != "":
			sel.apiKey = cfg.Providers.Groq.APIKey
			sel.apiBase = cfg.Providers.Groq.APIBase
			sel.proxy = cfg.Providers.Groq.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://api.groq.com/openai/v1"
			}
		case (strings.Contains(lowerModel, "nvidia") || strings.HasPrefix(model, "nvidia/")) && cfg.Providers.Nvidia.APIKey != "":
			sel.apiKey = cfg.Providers.Nvidia.APIKey
			sel.apiBase = cfg.Providers.Nvidia.APIBase
			sel.proxy = cfg.Providers.Nvidia.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "https://integrate.api.nvidia.com/v1"
			}
		case (strings.Contains(lowerModel, "ollama") || strings.HasPrefix(model, "ollama/")) && cfg.Providers.Ollama.APIKey != "":
			sel.apiKey = cfg.Providers.Ollama.APIKey
			sel.apiBase = cfg.Providers.Ollama.APIBase
			sel.proxy = cfg.Providers.Ollama.Proxy
			if sel.apiBase == "" {
				sel.apiBase = "http://localhost:11434/v1"
			}
		case cfg.Providers.VLLM.APIBase != "":
			sel.apiKey = cfg.Providers.VLLM.APIKey
			sel.apiBase = cfg.Providers.VLLM.APIBase
			sel.proxy = cfg.Providers.VLLM.Proxy
		default:
			if cfg.Providers.OpenRouter.APIKey != "" {
				sel.apiKey = cfg.Providers.OpenRouter.APIKey
				sel.proxy = cfg.Providers.OpenRouter.Proxy
				if cfg.Providers.OpenRouter.APIBase != "" {
					sel.apiBase = cfg.Providers.OpenRouter.APIBase
				} else {
					sel.apiBase = "https://openrouter.ai/api/v1"
				}
			} else {
				return providerSelection{}, fmt.Errorf("no API key configured for model: %s", model)
			}
		}
	}

	if sel.providerType == providerTypeHTTPCompat && sel.apiBase != "" &&
		(providerName == "minimax" || providerName == "minimax_cn" || strings.Contains(sel.apiBase, "/anthropic")) {
		sel.providerType = providerTypeAnthropic
	}

	if sel.providerType == providerTypeHTTPCompat {
		if sel.apiKey == "" && !strings.HasPrefix(model, "bedrock/") {
			return providerSelection{}, fmt.Errorf("no API key configured for provider (model: %s)", model)
		}
		if sel.apiBase == "" {
			return providerSelection{}, fmt.Errorf("no API base configured for provider (model: %s)", model)
		}
	}

	return sel, nil
}

func CreateProvider(cfg *config.Config) (LLMProvider, error) {
	sel, err := resolveProviderSelection(cfg)
	if err != nil {
		return nil, err
	}

	return createProviderFromSelection(&sel)
}

func CreateProviderForCandidate(cfg *config.Config, providerName string) (LLMProvider, error) {
	sel, err := resolveProviderSelectionForProvider(cfg, providerName)
	if err != nil {
		return nil, err
	}
	return createProviderFromSelection(&sel)
}

func createProviderFromSelection(sel *providerSelection) (LLMProvider, error) {
	sessionHeader, sessionHeaderRequired := openCodeGoSessionHeader(sel)
	switch sel.providerType {
	case providerTypeClaudeAuth:
		return createClaudeAuthProvider(sel.apiBase)
	case providerTypeGeminiAuth:
		return createGeminiAuthProvider(sel.apiBase)
	case providerTypeCodexAuth:
		return createCodexAuthProvider(sel.enableWebSearch)
	case providerTypeCodexCLIToken:
		c := NewCodexProviderWithTokenSource("", "", CreateCodexCliTokenSource())
		c.enableWebSearch = sel.enableWebSearch
		return c, nil
	case providerTypeClaudeCLI:
		return NewClaudeCodeProvider(sel.workspace), nil
	case providerTypeCodexCLI:
		return NewCodexCliProvider(sel.workspace), nil
	case providerTypeGitHubCopilot:
		return NewGitHubCopilotProvider(sel.apiBase, sel.connectMode, sel.model)
	case providerTypeAnthropic:
		return anthropicmessages.NewProviderWithOptions(sel.apiKey, sel.apiBase, anthropicmessages.Options{SessionHeader: sessionHeader, SessionHeaderRequired: sessionHeaderRequired, Headers: sel.headers}), nil
	default:
		return NewHTTPProviderWithOptions(sel.apiKey, sel.apiBase, sel.proxy, openai_compat.Options{SessionHeader: sessionHeader, SessionHeaderRequired: sessionHeaderRequired, Headers: sel.headers}), nil
	}
}
