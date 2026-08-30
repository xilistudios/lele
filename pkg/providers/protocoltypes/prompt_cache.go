package protocoltypes

import "strings"

// Prompt-cache option keys passed through provider options maps.
const (
	// OptPromptCache enables explicit prompt-cache breakpoints. Value: bool.
	OptPromptCache = "prompt_cache"
	// OptPromptCacheTTL is the breakpoint TTL, "5m" or "1h". Value: string.
	// Anthropic's default (when empty) is "5m"; "1h" is billed at a higher
	// rate and requires the extended-cache-ttl beta on supported plans.
	OptPromptCacheTTL = "prompt_cache_ttl"

	cacheTTL1h = "1h"
)

// cacheTTL normalizes a TTL string to "5m" or "1h".
func cacheTTL(ttl string) string {
	if strings.EqualFold(strings.TrimSpace(ttl), cacheTTL1h) {
		return cacheTTL1h
	}
	return "5m"
}

// CacheTTLOptions extracts the normalized TTL from a provider options map.
func CacheTTLOptions(options map[string]interface{}) string {
	if options == nil {
		return "5m"
	}
	ttl, _ := options[OptPromptCacheTTL].(string)
	return cacheTTL(ttl)
}

// CacheEnabled reports whether explicit prompt caching is requested in options.
func CacheEnabled(options map[string]interface{}) bool {
	if options == nil {
		return false
	}
	enabled, _ := options[OptPromptCache].(bool)
	return enabled
}

// CacheControlJSON renders the wire form of a breakpoint for providers that
// build request payloads by hand (raw JSON), e.g. {"type":"ephemeral","ttl":"5m"}.
func CacheControlJSON(ttl string) map[string]string {
	return map[string]string{"type": "ephemeral", "ttl": cacheTTL(ttl)}
}
