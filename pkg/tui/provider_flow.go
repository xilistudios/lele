package tui

import (
	"strings"
)

// providerPreset describes a known provider type offered in the /connect
// flow. Choosing a preset fills in the API base URL automatically and gives
// the user a hint about what API key is expected.
type providerPreset struct {
	typ       string // value stored in config (NamedProviderConfig.Type)
	label     string // human-friendly label shown in the picker
	apiBase   string // default API base URL ("" = leave blank)
	keyHint   string // hint shown next to the API Key step
	modelHint string // hint shown next to the Model alias step
}

// providerPresets is the ordered list of known providers offered in /connect.
// Keep in sync with pkg/providers/factory.go defaultAPIBaseByType so the
// auto-filled base URL matches what the backend would use.
var providerPresets = []providerPreset{
	{typ: "openai", label: "OpenAI", apiBase: "https://api.openai.com/v1", keyHint: "sk-...", modelHint: "gpt-4o, gpt-4o-mini, o3..."},
	{typ: "anthropic", label: "Anthropic", apiBase: "https://api.anthropic.com/v1", keyHint: "sk-ant-...", modelHint: "claude-sonnet-4, claude-opus-4..."},
	{typ: "openrouter", label: "OpenRouter", apiBase: "https://openrouter.ai/api/v1", keyHint: "sk-or-...", modelHint: "deepseek/deepseek-v4-pro, openai/gpt-4o..."},
	{typ: "gemini", label: "Google Gemini", apiBase: "https://generativelanguage.googleapis.com/v1beta", keyHint: "AIza...", modelHint: "gemini-2.5-pro, gemini-2.5-flash..."},
	{typ: "deepseek", label: "DeepSeek", apiBase: "https://api.deepseek.com/v1", keyHint: "sk-...", modelHint: "deepseek-chat, deepseek-reasoner..."},
	{typ: "zhipu", label: "Zhipu (GLM)", apiBase: "https://open.bigmodel.cn/api/paas/v4", keyHint: "API key", modelHint: "glm-4-plus, glm-4-air..."},
	{typ: "groq", label: "Groq", apiBase: "https://api.groq.com/openai/v1", keyHint: "gsk_...", modelHint: "llama-3.3-70b-versatile..."},
	{typ: "moonshot", label: "Moonshot (Kimi)", apiBase: "https://api.moonshot.cn/v1", keyHint: "sk-...", modelHint: "moonshot-v1-8k, kimi-k2..."},
	{typ: "nvidia", label: "NVIDIA", apiBase: "https://integrate.api.nvidia.com/v1", keyHint: "nvapi-...", modelHint: "meta/llama-3.3-70b-instruct..."},
	{typ: "ollama", label: "Ollama (local)", apiBase: "http://localhost:11434/v1", keyHint: "none (local)", modelHint: "llama3.2, qwen2.5..."},
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
