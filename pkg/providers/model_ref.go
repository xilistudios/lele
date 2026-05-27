package providers

import "strings"

// ModelRef represents a parsed model reference with provider and model name.
type ModelRef struct {
	Provider string
	Model    string
}

// APIModel returns the model name suitable for API calls.
// It strips any provider prefix that may be embedded in the model string.
// For example:
//
//	ModelRef{Provider: "openrouter", Model: "deepseek/deepseek-v4-pro"}.APIModel()
//
// returns "deepseek/deepseek-v4-pro".
//
//	ModelRef{Provider: "anthropic", Model: "claude-opus"}.APIModel()
//
// returns "claude-opus".
func (m *ModelRef) APIModel() string {
	if m == nil {
		return ""
	}
	return m.Model
}

// String returns the canonical "provider:model" representation.
func (m *ModelRef) String() string {
	if m == nil {
		return ""
	}
	if m.Provider == "" {
		return m.Model
	}
	return m.Provider + ":" + m.Model
}

// ParseModelRef parses "anthropic:claude-opus" into {Provider: "anthropic", Model: "claude-opus"}.
// If no colon present, uses defaultProvider.
// Returns nil for empty input.
// Note: Does NOT normalize the provider when explicitly specified (has colon),
// normalization happens later when looking up the provider in config.
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

// NormalizeProvider normalizes provider identifiers to canonical form.
func NormalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))

	switch p {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "glm":
		return "zhipu"
	case "google":
		return "gemini"
	}

	return p
}

// ModelKey returns a canonical "provider:model" key for deduplication.
func ModelKey(provider, model string) string {
	return NormalizeProvider(provider) + ":" + strings.ToLower(strings.TrimSpace(model))
}

// StripProviderPrefix removes the provider prefix ("provider:") from a model string,
// returning only the model name suitable for API calls.
// The legacy "provider/model" slash format is deprecated and not handled here.
//
// Examples:
//
//	StripProviderPrefix("openrouter:deepseek/deepseek-v4-pro") -> "deepseek/deepseek-v4-pro"
//	StripProviderPrefix("anthropic:claude-opus") -> "claude-opus"
//	StripProviderPrefix("claude-opus") -> "claude-opus"
func StripProviderPrefix(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return model
	}
	// Handle colon format: "provider:model"
	if idx := strings.Index(model, ":"); idx > 0 {
		return model[idx+1:]
	}
	return model
}
