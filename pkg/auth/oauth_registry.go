package auth

import (
	"sort"
	"strings"
)

// This file is the single source of truth for the OAuth provider names the CLI
// accepts. Callers (today `cmd/lele/auth.go`) resolve a provider through
// OAuthProviderConfigByName and build their help/error text from
// OAuthProviderNames, so there is no duplicated hardcoded provider list to keep
// in sync when a new preset is added.
//
// Design rules:
//
//   - Presets are stored once (package-level) and handed out **by value with a
//     deep-cloned ExtraAuthParams map**. OAuthProviderConfig embeds a map, so
//     returning the stored value as-is would let a caller write
//     cfg.ExtraAuthParams["x"] = "y" and silently change the behavior of every
//     other consumer of that preset. The clone makes the stored presets
//     effectively immutable.
//   - Provider identity is decided by the registry key / cfg.Name, never by
//     sniffing URL substrings.
//   - Only the standard library is used: pkg/auth gains no dependencies (not on
//     pkg/providers, pkg/mcp or cmd).

// oauthProviderPresets maps a canonical provider name to its OAuth preset.
//
// The canonical names are the identity used on the wire (cfg.Name), in the
// credential store keys (auth.SetCredential / auth.GetCredential) and by
// pkg/providers when it looks credentials up. They therefore match the `Name`
// field of the corresponding preset and the keys written by the CLI.
//
// The presets are built once at package initialization from their constructors
// (OpenAIOAuthConfig / GoogleAntigravityOAuthConfig / ClaudeOAuthConfig /
// GeminiOAuthConfig) and stored as values: that is what makes them a *shared*
// preset, and therefore what makes the ExtraAuthParams clone in
// OAuthProviderConfigByName load-bearing. The map is never mutated after init.
var oauthProviderPresets = map[string]OAuthProviderConfig{
	"anthropic":          ClaudeOAuthConfig(),
	"gemini":             GeminiOAuthConfig(),
	"openai":             OpenAIOAuthConfig(),
	"google-antigravity": GoogleAntigravityOAuthConfig(),
}

// oauthProviderAliases maps user-facing shortcuts to canonical provider names.
// Aliases are accepted on input only: they are never returned by
// OAuthProviderNames, so the CLI keeps advertising exactly one spelling per
// provider.
var oauthProviderAliases = map[string]string{
	// `claude` selects the Claude.ai OAuth flow (LoginBrowserWithPasteFallback /
	// LoginPasteCode). The canonical registry and credential-store key stays
	// `anthropic` — the name the preset advertises in Name, the key credentials
	// are stored under and the name pkg/providers already looks up — so both
	// spellings must resolve to the same preset.
	"claude": "anthropic",
	// `codex` is the name of the OpenAI OAuth flow in lele/jcode: the Codex CLI
	// shares this preset's client ID and authorize endpoint, so users coming from
	// that tool type `--provider codex` and mean this preset.
	"codex": "openai",
	// `antigravity` is the short name used by pkg/providers/antigravity_provider.go
	// (it accepts the model ids "antigravity", "antigravity/…" and
	// "google-antigravity"), so both spellings must resolve to the same preset.
	"antigravity": "google-antigravity",
	// `gemini-cli` is the spelling the official @google/gemini-cli tool (and
	// jcode's accepted auth-map keys: "google-gemini-cli" | "gemini-cli" |
	// "gemini") uses, so users arriving from it type `--provider gemini-cli` and
	// mean the Google Code Assist flow. The canonical key stays `gemini` — the
	// name the preset advertises in Name, the key the credential is stored under
	// and the selector pkg/providers looks up — so the alias is a spelling, never
	// a second provider.
	//
	// There is deliberately NO `google` alias: `google` already means something
	// else in lele, because pkg/config normalizes it to the *config* provider
	// name `gemini` (config.NormalizeProvider). Accepting it here as an alias
	// would turn a config provider name into a silent switch to this OAuth flow,
	// i.e. change behaviour just because of how a user spelled the provider.
	"gemini-cli": "gemini",
}

// OAuthProviderNames returns the canonical names of every registered OAuth
// preset, sorted ascending.
//
// A fresh slice is allocated on every call, so callers may freely sort, append
// to or mutate the result without affecting the registry or other callers.
func OAuthProviderNames() []string {
	names := make([]string, 0, len(oauthProviderPresets))
	for name := range oauthProviderPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// OAuthProviderConfigByName resolves a user-supplied provider name to its OAuth
// preset.
//
// The lookup is case- and whitespace-insensitive (strings.ToLower +
// strings.TrimSpace) and accepts the documented aliases (`claude` → `anthropic`,
// `codex` → `openai`, `antigravity` → `google-antigravity` and `gemini-cli` →
// `gemini`). An unknown name returns the zero config and ok=false; it never
// falls back to a default provider.
//
// The returned config is safe to modify: it is a copy of the stored preset whose
// ExtraAuthParams map has been cloned, so writes to it cannot corrupt the
// registry (or any other caller).
func OAuthProviderConfigByName(name string) (OAuthProviderConfig, bool) {
	preset, ok := oauthProviderPresets[normalizeOAuthProviderName(name)]
	if !ok {
		return OAuthProviderConfig{}, false
	}
	return cloneOAuthProviderPreset(preset), true
}

// normalizeOAuthProviderName lowercases and trims a provider name and resolves
// the documented aliases to their canonical name. Names that are neither a
// canonical name nor an alias are returned normalized-but-unresolved, so the
// caller's map lookup fails as expected.
func normalizeOAuthProviderName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := oauthProviderAliases[normalized]; ok {
		return canonical
	}
	return normalized
}

// cloneOAuthProviderPreset returns a copy of preset that shares nothing mutable
// with the stored value. ExtraAuthParams is the only reference-typed field of
// OAuthProviderConfig (HTTPClient is an optional, concurrency-safe client and is
// deliberately shared), so copying it entry by entry makes the copy independent.
// It is a shallow copy of a map of strings, which for this type is a full deep
// clone: there is nothing below map[string]string.
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
