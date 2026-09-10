package catalog

import (
	"embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed models.json
var embeddedModels embed.FS

// DefaultAPIBaseByType returns the default OpenAI-compatible base URL for a
// known provider type. Empty string means the type has no built-in default.
//
// This is the single source of truth used by the factory, REST API, TUI
// presets, and onboarding. Keep aliases in sync with providers.NormalizeProvider.
func DefaultAPIBaseByType(providerType string) string {
	t := normalizeType(providerType)
	if p, ok := Providers()[t]; ok && p.APIBase != "" {
		return p.APIBase
	}
	// Fallback table for types that may not have catalog models.
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
	case "moonshot", "kimi", "kimi-coding", "kimi_for_coding":
		return "https://api.moonshot.cn/v1"
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
func KnownProviderTypes() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(snapshot.Providers))
	for id := range snapshot.Providers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Provider returns a catalog provider by ID/alias, or false.
func ProviderByID(id string) (Provider, bool) {
	key := normalizeType(id)
	mu.RLock()
	defer mu.RUnlock()
	p, ok := snapshot.Providers[key]
	return p, ok
}

// Providers returns a copy of the provider map keyed by normalized ID.
func Providers() map[string]Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]Provider, len(snapshot.Providers))
	for k, v := range snapshot.Providers {
		out[k] = v
	}
	return out
}

// ModelsForProvider returns catalog models for a provider ID/alias.
// Prefers the live overlay (prefetch cache) when present.
func ModelsForProvider(provider string) []Model {
	key := normalizeType(provider)
	mu.RLock()
	defer mu.RUnlock()
	if p, ok := liveProviders[key]; ok && len(p.Models) > 0 {
		return p.Models
	}
	if p, ok := snapshot.Providers[key]; ok {
		return p.Models
	}
	return nil
}

// FindModel looks up a model by provider + model ID (case-insensitive).
func FindModel(provider, modelID string) (Model, bool) {
	want := strings.ToLower(strings.TrimSpace(modelID))
	for _, m := range ModelsForProvider(provider) {
		if strings.EqualFold(m.ID, modelID) || strings.EqualFold(m.Name, modelID) {
			return m, true
		}
		// suffix / fuzzy match for names like "openai/gpt-4o" vs "gpt-4o"
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

// ToProviderModelConfig maps a catalog model onto config.ProviderModelConfig fields
// without importing pkg/config (avoids an import cycle). Callers wire the result.
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
	case "kimi", "kimi-coding", "moonshot-ai":
		return "moonshot"
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
	}
	return t
}

var (
	mu            sync.RWMutex
	snapshot      Snapshot
	liveProviders map[string]Provider
)

func init() {
	snapshot = Snapshot{Providers: map[string]Provider{}}
	liveProviders = map[string]Provider{}
	_ = loadEmbedded()
}

func loadEmbedded() error {
	data, err := embeddedModels.ReadFile("models.json")
	if err != nil {
		return err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s.Providers == nil {
		s.Providers = map[string]Provider{}
	}
	// Normalize keys.
	norm := make(map[string]Provider, len(s.Providers))
	for k, p := range s.Providers {
		id := normalizeType(k)
		p.ID = id
		norm[id] = p
	}
	s.Providers = norm
	mu.Lock()
	snapshot = s
	mu.Unlock()
	return nil
}

// CacheDir returns the lele catalog cache directory (~/.lele/cache).
func CacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "lele-cache")
	}
	return filepath.Join(home, ".lele", "cache")
}

// CachePath returns the models.dev prefetch cache file path.
func CachePath() string {
	return filepath.Join(CacheDir(), "models_dev.json")
}
