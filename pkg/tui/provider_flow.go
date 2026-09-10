package tui

import (
	"strings"

	"github.com/xilistudios/lele/pkg/catalog"
)

// providerPreset describes a known provider type offered in the /connect
// flow. Choosing a preset fills in the API base URL automatically and gives
// the user a hint about what API key is expected.
type providerPreset struct {
	typ               string // value stored in config (NamedProviderConfig.Type)
	label             string // human-friendly label shown in the picker
	apiBase           string // default API base URL ("" = leave blank)
	keyHint           string // hint shown next to the API Key step
	modelHint         string // hint shown next to the Model alias step
	defaultModel      string // default actual model name to pre-fill
	defaultModelAlias string // default model alias to pre-fill
}

// catalogPreset builds a providerPreset from the catalog, pulling the default
// API base and the first catalog model when available.
func catalogPreset(typ, label, keyHint, modelHint string) providerPreset {
	p := providerPreset{
		typ:       typ,
		label:     label,
		apiBase:   catalog.DefaultAPIBaseByType(typ),
		keyHint:   keyHint,
		modelHint: modelHint,
	}
	if models := catalog.ModelsForProvider(typ); len(models) > 0 && models[0].ID != "" {
		p.defaultModel = models[0].ID
		p.defaultModelAlias = models[0].ID
	}
	return p
}

// providerPresets is the ordered list of known providers offered in /connect.
// Keep in sync with pkg/providers/factory.go defaultAPIBaseByType so the
// auto-filled base URL matches what the backend would use.
var providerPresets = []providerPreset{
	{typ: "openai", label: "OpenAI", apiBase: "https://api.openai.com/v1", keyHint: "sk-...", modelHint: "gpt-4o, gpt-4o-mini, o3...", defaultModel: "gpt-4o", defaultModelAlias: "gpt-4o"},
	{typ: "anthropic", label: "Anthropic", apiBase: "https://api.anthropic.com/v1", keyHint: "sk-ant-...", modelHint: "claude-sonnet-4, claude-opus-4...", defaultModel: "claude-sonnet-4-20250514", defaultModelAlias: "claude-sonnet"},
	{typ: "openrouter", label: "OpenRouter", apiBase: "https://openrouter.ai/api/v1", keyHint: "sk-or-...", modelHint: "deepseek/deepseek-v4-pro, openai/gpt-4o...", defaultModel: "deepseek/deepseek-chat-v3-0324", defaultModelAlias: "deepseek-v4"},
	{typ: "gemini", label: "Google Gemini", apiBase: "https://generativelanguage.googleapis.com/v1beta", keyHint: "AIza...", modelHint: "gemini-2.5-pro, gemini-2.5-flash...", defaultModel: "gemini-2.5-flash", defaultModelAlias: "gemini-flash"},
	{typ: "deepseek", label: "DeepSeek", apiBase: "https://api.deepseek.com/v1", keyHint: "sk-...", modelHint: "deepseek-chat, deepseek-reasoner...", defaultModel: "deepseek-chat", defaultModelAlias: "deepseek"},
	{typ: "zhipu", label: "Zhipu (GLM)", apiBase: "https://open.bigmodel.cn/api/paas/v4", keyHint: "API key", modelHint: "glm-4-plus, glm-4-air...", defaultModel: "glm-4-flash", defaultModelAlias: "glm-4"},
	{typ: "groq", label: "Groq", apiBase: "https://api.groq.com/openai/v1", keyHint: "gsk_...", modelHint: "llama-3.3-70b-versatile...", defaultModel: "llama-3.3-70b-versatile", defaultModelAlias: "llama-70b"},
	{typ: "moonshot", label: "Moonshot (Kimi)", apiBase: "https://api.moonshot.cn/v1", keyHint: "sk-...", modelHint: "moonshot-v1-8k, kimi-k2...", defaultModel: "moonshot-v1-8k", defaultModelAlias: "kimi"},
	{typ: "nvidia", label: "NVIDIA", apiBase: "https://integrate.api.nvidia.com/v1", keyHint: "nvapi-...", modelHint: "meta/llama-3.3-70b-instruct...", defaultModel: "meta/llama-3.1-8b-instruct", defaultModelAlias: "llama-8b"},
	{typ: "ollama", label: "Ollama (local)", apiBase: "http://localhost:11434/v1", keyHint: "none (local)", modelHint: "llama3.2, qwen2.5...", defaultModel: "llama3.2", defaultModelAlias: "llama"},
	// Catalog-backed providers (pending hermes-agent set).
	catalogPreset("xai", "xAI (Grok)", "xai-...", "grok-4, grok-3-mini..."),
	catalogPreset("nous", "Nous Portal", "API key", "Hermes-4-405B..."),
	catalogPreset("lmstudio", "LM Studio (local)", "none (local)", "qwen/qwen3-30b-a3b..."),
	catalogPreset("stepfun", "StepFun", "API key", "step-3.7-flash..."),
	catalogPreset("minimax", "MiniMax", "API key", "MiniMax-M3..."),
	catalogPreset("vercel", "Vercel AI Gateway", "vck_...", "openai/gpt-4o, anthropic/claude..."),
	catalogPreset("opencode", "OpenCode Zen", "API key", "kimi-k2.5..."),
	catalogPreset("huggingface", "Hugging Face", "hf_...", "moonshotai/Kimi-K2.5..."),
	catalogPreset("novita", "NovitaAI", "API key", "moonshotai/kimi-k2.5..."),
	catalogPreset("xiaomi", "Xiaomi MiMo", "API key", "mimo-v2.5..."),
	catalogPreset("tencent_tokenhub", "Tencent TokenHub", "API key", "hy3..."),
	catalogPreset("arcee", "Arcee", "API key", "trinity-large-thinking..."),
	catalogPreset("gmi", "GMI Cloud", "API key", "moonshotai/Kimi-K2.6..."),
	catalogPreset("cerebras", "Cerebras", "csk-...", "qwen-3.8-27b..."),
	catalogPreset("together", "Together AI", "API key", "moonshotai/Kimi-K2.6..."),
	catalogPreset("fireworks", "Fireworks", "fw-...", "accounts/fireworks/models/..."),
	catalogPreset("mistral", "Mistral", "API key", "mistral-large-2512..."),
	catalogPreset("siliconflow", "SiliconFlow", "sk-...", "moonshotai/Kimi-K2.5..."),
	catalogPreset("perplexity", "Perplexity", "pplx-...", "sonar-pro..."),
	catalogPreset("ollama_cloud", "Ollama Cloud", "API key", "kimi-k2.5..."),
	catalogPreset("kimi_for_coding", "Kimi For Coding", "sk-...", "k3..."),
	catalogPreset("alibaba_token_plan", "Qwen Cloud Token Plan", "API key", "qwen3.7-max, deepseek-v4-pro..."),
	catalogPreset("alibaba_token_plan_cn", "Qwen Cloud Token Plan (CN)", "API key", "qwen3.7-max, deepseek-v4-pro..."),
}

// providerPresetByType returns the preset matching typ, or nil.
func providerPresetByType(typ string) *providerPreset {
	typ = strings.ToLower(strings.TrimSpace(typ))
	for i := range providerPresets {
		if providerPresets[i].typ == typ {
			return &providerPresets[i]
		}
	}
	return nil
}

// isKnownProviderType reports whether typ is one of the preset types.
func isKnownProviderType(typ string) bool {
	return providerPresetByType(typ) != nil
}
