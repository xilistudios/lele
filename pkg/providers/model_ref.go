package providers

import "strings"

type ModelRef struct {
	Provider string
	Model    string
}

func (m *ModelRef) APIModel() string {
	if m == nil {
		return ""
	}
	return m.Model
}

func (m *ModelRef) String() string {
	if m == nil {
		return ""
	}
	if m.Provider == "" {
		return m.Model
	}
	return m.Provider + ":" + m.Model
}

func ParseModelRef(raw string, defaultProvider string) *ModelRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if idx := strings.Index(raw, ":"); idx > 0 {
		parts := strings.SplitN(raw, ":", 2)
		provider := NormalizeProvider(parts[0])
		model := strings.TrimSpace(parts[1])
		if model == "" {
			return nil
		}
		return &ModelRef{Provider: provider, Model: model}
	}

	return &ModelRef{
		Provider: NormalizeProvider(defaultProvider),
		Model:    raw,
	}
}

func NormalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))

	switch p {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen", "zen":
		return "opencode"
	case "opencode-go":
		return "opencode_go"
	case "qwen":
		return "qwen-portal"
	case "alibaba-token-plan", "qwen-token-plan", "qwen-cloud-token-plan", "qwen_cloud_token_plan", "alibaba-token":
		return "alibaba_token_plan"
	case "alibaba-token-plan-cn", "qwen-token-plan-cn", "alibaba-token-cn":
		return "alibaba_token_plan_cn"
	case "kimi-code", "kimi-coding", "kimi-for-coding":
		return "kimi_for_coding"
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "glm":
		return "zhipu"
	case "google":
		return "gemini"
	case "grok", "x.ai", "x-ai":
		return "xai"
	case "nous-portal", "nousresearch":
		return "nous"
	case "lm-studio":
		return "lmstudio"
	case "step":
		return "stepfun"
	case "ai-gateway", "aigateway":
		return "vercel"
	case "hf":
		return "huggingface"
	case "novita-ai":
		return "novita"
	case "mimo":
		return "xiaomi"
	case "tokenhub", "tencent":
		return "tencent_tokenhub"
	case "gmi-cloud":
		return "gmi"
	case "togetherai":
		return "together"
	case "fireworks-ai":
		return "fireworks"
	case "ollama-cloud":
		return "ollama_cloud"
	case "amazon-bedrock", "aws":
		return "bedrock"
	case "azure-foundry":
		return "azure_foundry"
	}

	return p
}

func ModelKey(provider, model string) string {
	return NormalizeProvider(provider) + ":" + strings.ToLower(strings.TrimSpace(model))
}

func StripProviderPrefix(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	if idx := strings.Index(model, ":"); idx > 0 {
		return model[idx+1:]
	}
	return model
}
