package catalog

import (
	"sort"
	"strings"
	"sync"
)

// DefaultAPIBaseByType returns the default OpenAI-compatible base URL for a
// known provider type. Empty string means the type has no built-in default.
//
// This table is intentionally hardcoded (not embedded catalog models) so
// factory/onboard work offline before any catalog download. Disk index values
// are preferred when present.
func DefaultAPIBaseByType(providerType string) string {
	t := normalizeType(providerType)
	if base, ok := indexAPIBase(t); ok && base != "" {
		return base
	}
	switch t {
	case "openai", "gpt":
		return "https://api.openai.com/v1"
	case "anthropic", "claude":
		return "https://api.anthropic.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "gemini", "google":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "zhipu", "glm":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "zai", "zai_coding_plan", "z.ai":
		return "https://api.z.ai/api/paas/v4"
	case "moonshot", "kimi", "kimi-coding":
		return "https://api.moonshot.cn/v1"
	case "kimi_for_coding":
		return "https://api.kimi.com/coding/v1"
	case "nvidia", "nim":
		return "https://integrate.api.nvidia.com/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "ollama_cloud", "ollama-cloud":
		return "https://ollama.com/v1"
	case "github_copilot", "copilot", "github-copilot":
		return "https://api.githubcopilot.com/v1"
	case "chutes":
		return "https://llm.chutes.ai/v1"
	case "alibaba", "qwen", "dashscope":
		return "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	case "alibaba_coding_plan", "alibaba-coding-plan":
		return "https://coding-intl.dashscope.aliyuncs.com/v1"
	case "xai", "grok", "x.ai", "x-ai":
		return "https://api.x.ai/v1"
	case "nous":
		return "https://inference-api.nousresearch.com/v1"
	case "lmstudio", "lm-studio":
		return "http://127.0.0.1:1234/v1"
	case "stepfun", "step":
		return "https://api.stepfun.ai/step_plan/v1"
	case "minimax":
		return "https://api.minimax.io/anthropic/v1"
	case "minimax_cn", "minimax-cn":
		return "https://api.minimaxi.com/anthropic/v1"
	case "vercel", "ai-gateway", "aigateway":
		return "https://ai-gateway.vercel.sh/v1"
	case "opencode", "opencode-zen", "zen":
		return "https://opencode.ai/zen/v1"
	case "opencode_go", "opencode-go":
		return "https://opencode.ai/zen/go/v1"
	case "huggingface", "hf":
		return "https://router.huggingface.co/v1"
	case "novita", "novita-ai", "novitaai":
		return "https://api.novita.ai/openai"
	case "xiaomi", "mimo":
		return "https://api.xiaomimimo.com/v1"
	case "tencent_tokenhub", "tencent-tokenhub", "tencent", "tokenhub":
		return "https://tokenhub.tencentmaas.com/v1"
	case "arcee":
		return "https://api.arcee.ai/api/v1"
	case "gmi", "gmi-cloud", "gmicloud":
		return "https://api.gmi-serving.com/v1"
	case "cerebras":
		return "https://api.cerebras.ai/v1"
	case "together", "togetherai":
		return "https://api.together.xyz/v1"
	case "fireworks", "fireworks-ai":
		return "https://api.fireworks.ai/inference/v1"
	case "mistral":
		return "https://api.mistral.ai/v1"
	case "siliconflow":
		return "https://api.siliconflow.com/v1"
	case "perplexity", "perplexity-agent":
		return "https://api.perplexity.ai/v1"
	case "shengsuanyun":
		return "https://router.shengsuanyun.com/api/v1"
	case "nanogpt":
		return "https://nano-gpt.com/api/v1"
	case "modelark", "modelark_coding_plan":
		return "https://ark.ap-southeast.bytepluses.com/api/coding/v3"
	case "vllm":
		return "http://localhost:8000/v1"
	case "qwen_portal", "qwen-oauth", "qwen-oauth-portal":
		return "https://portal.qwen.ai/v1"
	}
	return ""
}

// KnownProviderTypes returns catalog provider IDs sorted for UI pickers.
// Prefers the disk index; falls back to hardcoded known IDs.
func KnownProviderTypes() []string {
	if idx := loadIndex(); idx != nil {
		out := make([]string, 0, len(idx.Providers))
		for id := range idx.Providers {
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}
	out := make([]string, 0, len(hardcodedProviderIDs))
	out = append(out, hardcodedProviderIDs...)
	sort.Strings(out)
	return out
}

// ProviderByID returns a catalog provider by ID/alias.
// Models are loaded lazily from the per-provider cache file.
func ProviderByID(id string) (Provider, bool) {
	key := normalizeType(id)
	p, ok := providerMeta(key)
	if !ok {
		return Provider{}, false
	}
	p.Models = ModelsForProvider(key)
	return p, true
}

// Providers returns provider metadata (models omitted) keyed by normalized ID.
func Providers() map[string]Provider {
	if idx := loadIndex(); idx != nil {
		out := make(map[string]Provider, len(idx.Providers))
		for k, meta := range idx.Providers {
			out[k] = Provider{
				ID:      meta.ID,
				Name:    meta.Name,
				Type:    meta.Type,
				APIBase: meta.APIBase,
			}
		}
		return out
	}
	out := make(map[string]Provider, len(hardcodedProviderIDs))
	for _, id := range hardcodedProviderIDs {
		if p, ok := providerMeta(id); ok {
			out[id] = p
		}
	}
	return out
}

// ModelsForProvider returns catalog models for a provider ID/alias.
// Loads only that provider's file from disk cache (or in-memory overlay).
func ModelsForProvider(provider string) []Model {
	key := normalizeType(provider)

	mu.RLock()
	if p, ok := liveProviders[key]; ok && len(p.Models) > 0 {
		mu.RUnlock()
		return p.Models
	}
	mu.RUnlock()

	p := loadProviderFromDisk(key)
	if p == nil {
		// Kick a background download for this provider if nothing is cached.
		if !offlineMode() {
			EnsureProviderAsync(key)
		}
		return nil
	}
	return p.Models
}

func offlineMode() bool {
	return envOr("LELE_CATALOG_OFFLINE", "") == "1" ||
		envOr("LELE_CATALOG_OFFLINE", "") == "true"
}

// FindModel looks up a model by provider + model ID (case-insensitive).
func FindModel(provider, modelID string) (Model, bool) {
	want := strings.ToLower(strings.TrimSpace(modelID))
	for _, m := range ModelsForProvider(provider) {
		if strings.EqualFold(m.ID, modelID) || strings.EqualFold(m.Name, modelID) {
			return m, true
		}
		if want != "" && (strings.HasSuffix(strings.ToLower(m.ID), "/"+want) ||
			strings.HasSuffix(want, "/"+strings.ToLower(m.ID))) {
			return m, true
		}
	}
	return Model{}, false
}

// SearchModels filters provider models by substring query (ID or name).
func SearchModels(provider, query string) []Model {
	models := ModelsForProvider(provider)
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return models
	}
	out := make([]Model, 0, len(models))
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.ID), q) ||
			strings.Contains(strings.ToLower(m.Name), q) {
			out = append(out, m)
		}
	}
	return out
}

// ModelDefaults are prefill values when adding a model to config.
type ModelDefaults struct {
	ContextWindow  int
	MaxTokens      int
	Vision         bool
	ThinkingLevels []string
	Reasoning      bool
}

// DefaultsFor returns prefill defaults for adding a model to config.
func DefaultsFor(provider, modelID string) (ModelDefaults, bool) {
	m, ok := FindModel(provider, modelID)
	if !ok {
		return ModelDefaults{}, false
	}
	maxTok := m.MaxOutput
	if maxTok == 0 {
		maxTok = 8192
	}
	cw := m.ContextWindow
	if cw == 0 {
		cw = 128000
	}
	return ModelDefaults{
		ContextWindow:  cw,
		MaxTokens:      maxTok,
		Vision:         m.Vision,
		ThinkingLevels: m.ThinkingLevels,
		Reasoning:      m.Reasoning,
	}, true
}

func normalizeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	t = strings.ReplaceAll(t, ".", "-")
	t = strings.ReplaceAll(t, " ", "_")
	switch t {
	case "gpt":
		return "openai"
	case "claude", "claude-code", "claudecode":
		return "anthropic"
	case "glm", "z-ai", "z.ai":
		return "zhipu"
	case "google", "google-gemini":
		return "gemini"
	case "kimi", "kimi-coding", "moonshot-ai", "kimi-for-coding":
		return "kimi_for_coding"
	case "nim", "nvidia-nim":
		return "nvidia"
	case "copilot", "github", "github-copilot":
		return "github_copilot"
	case "qwen", "dashscope", "aliyun", "alibaba-cloud":
		return "alibaba"
	case "alibaba-coding":
		return "alibaba_coding_plan"
	case "grok", "x.ai", "x-ai":
		return "xai"
	case "lm-studio", "lm_studio":
		return "lmstudio"
	case "step":
		return "stepfun"
	case "minimax-china", "minimax_cn":
		return "minimax_cn"
	case "ai-gateway", "aigateway", "vercel-ai-gateway":
		return "vercel"
	case "zen", "opencode-zen":
		return "opencode"
	case "hf", "hugging-face":
		return "huggingface"
	case "novita-ai", "novitaai":
		return "novita"
	case "mimo", "xiaomi-mimo":
		return "xiaomi"
	case "tencent", "tokenhub", "tencent-cloud", "tencentmaas":
		return "tencent_tokenhub"
	case "gmi-cloud", "gmicloud":
		return "gmi"
	case "togetherai":
		return "together"
	case "fireworks-ai":
		return "fireworks"
	case "perplexity-agent":
		return "perplexity"
	case "ollama-cloud":
		return "ollama_cloud"
	case "azure", "azure-foundry":
		return "azure_foundry"
	case "aws", "aws-bedrock", "amazon", "amazon-bedrock":
		return "bedrock"
	case "kimi-code":
		return "kimi_for_coding"
	}
	return t
}

// hardcodedProviderIDs is the offline-known provider set (no models).
var hardcodedProviderIDs = []string{
	"alibaba", "alibaba_coding_plan", "anthropic", "arcee", "azure_foundry",
	"bedrock", "cerebras", "chutes", "deepseek", "fireworks", "gemini", "gmi",
	"github_copilot", "groq", "huggingface", "kimi_for_coding", "lmstudio",
	"minimax", "minimax_cn", "mistral", "modelark", "moonshot", "nanogpt",
	"novita", "nous", "nvidia", "ollama", "ollama_cloud", "opencode",
	"opencode_go", "openai", "openrouter", "perplexity", "qwen_portal",
	"shengsuanyun", "siliconflow", "stepfun", "tencent_tokenhub", "together",
	"vercel", "vllm", "xiaomi", "xai", "zai", "zai_coding_plan", "zhipu",
}

func providerMeta(key string) (Provider, bool) {
	if idx := loadIndex(); idx != nil {
		if meta, ok := idx.Providers[key]; ok {
			return Provider{
				ID:      meta.ID,
				Name:    meta.Name,
				Type:    meta.Type,
				APIBase: meta.APIBase,
			}, true
		}
	}
	for _, id := range hardcodedProviderIDs {
		if id == key {
			return Provider{
				ID:      key,
				Name:    key,
				Type:    "openai",
				APIBase: DefaultAPIBaseByType(key),
			}, true
		}
	}
	return Provider{}, false
}

func indexAPIBase(key string) (string, bool) {
	idx := loadIndex()
	if idx == nil {
		return "", false
	}
	meta, ok := idx.Providers[key]
	if !ok {
		return "", false
	}
	return meta.APIBase, true
}

var (
	mu            sync.RWMutex
	liveProviders map[string]Provider
)

func init() {
	liveProviders = map[string]Provider{}
	// Best-effort: load index from disk if present. No network.
	_ = LoadIndexFromCache()
}
