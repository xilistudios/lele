package tui

import (
	"strings"

	"github.com/xilistudios/lele/pkg/catalog"
)

type providerPreset struct {
	typ               string
	label             string
	apiBase           string
	keyHint           string
	modelHint         string
	defaultModel      string
	defaultModelAlias string
}

func catalogPreset(typ, label, keyHint, modelHint string) providerPreset {
	p := providerPreset{
		typ:       typ,
		label:     label,
		apiBase:   catalog.DefaultAPIBaseByType(typ),
		keyHint:   keyHint,
		modelHint: modelHint,
	}
	if id := catalogDefaultModel(catalog.ModelsForProvider(typ)); id != "" {
		p.defaultModel = id
		p.defaultModelAlias = id
	}
	return p
}

func catalogDefaultModel(models []catalog.Model) string {
	servable := catalog.ChatCompletionsModels(models)
	if len(servable) == 0 {
		return ""
	}
	return servable[0].ID
}

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
	catalogPreset("xai", "xAI (Grok)", "xai-...", "grok-4, grok-3-mini..."),
	catalogPreset("nous", "Nous Portal", "API key", "Hermes-4-405B..."),
	catalogPreset("lmstudio", "LM Studio (local)", "none (local)", "qwen/qwen3-30b-a3b..."),
	catalogPreset("stepfun", "StepFun", "API key", "step-3.7-flash..."),
	catalogPreset("minimax", "MiniMax", "API key", "MiniMax-M3..."),
	catalogPreset("vercel", "Vercel AI Gateway", "vck_...", "openai/gpt-4o, anthropic/claude..."),
	catalogPreset("opencode", "OpenCode Zen", "API key", "kimi-k2.5..."),
	catalogPreset("opencode_go", "OpenCode Go", "API key", "deepseek-v4.1-flash, kimi-k2.7-code, glm-5.3..."),
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

func providerPresetByType(typ string) *providerPreset {
	typ = strings.ToLower(strings.TrimSpace(typ))
	for i := range providerPresets {
		if providerPresets[i].typ == typ {
			return &providerPresets[i]
		}
	}
	return nil
}

func isKnownProviderType(typ string) bool {
	return providerPresetByType(typ) != nil
}
