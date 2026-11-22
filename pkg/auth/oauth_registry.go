package auth

import (
	"sort"
	"strings"
)

var oauthProviderPresets = map[string]OAuthProviderConfig{
	"anthropic":          ClaudeOAuthConfig(),
	"gemini":             GeminiOAuthConfig(),
	"openai":             OpenAIOAuthConfig(),
	"google-antigravity": GoogleAntigravityOAuthConfig(),
}

var oauthProviderAliases = map[string]string{
	"claude":      "anthropic",
	"codex":       "openai",
	"antigravity": "google-antigravity",
	"gemini-cli":  "gemini",
}

func OAuthProviderNames() []string {
	names := make([]string, 0, len(oauthProviderPresets))
	for name := range oauthProviderPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func OAuthProviderConfigByName(name string) (OAuthProviderConfig, bool) {
	preset, ok := oauthProviderPresets[normalizeOAuthProviderName(name)]
	if !ok {
		return OAuthProviderConfig{}, false
	}
	return cloneOAuthProviderPreset(preset), true
}

func normalizeOAuthProviderName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := oauthProviderAliases[normalized]; ok {
		return canonical
	}
	return normalized
}

func cloneOAuthProviderPreset(preset OAuthProviderConfig) OAuthProviderConfig {
	if preset.ExtraAuthParams == nil {
		return preset
	}
	cloned := make(map[string]string, len(preset.ExtraAuthParams))
	for key, value := range preset.ExtraAuthParams {
		cloned[key] = value
	}
	preset.ExtraAuthParams = cloned
	return preset
}
