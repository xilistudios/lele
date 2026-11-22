package protocoltypes

import "strings"

const (
	OptPromptCache    = "prompt_cache"
	OptPromptCacheTTL = "prompt_cache_ttl"

	cacheTTL1h = "1h"
)

func cacheTTL(ttl string) string {
	if strings.EqualFold(strings.TrimSpace(ttl), cacheTTL1h) {
		return cacheTTL1h
	}
	return "5m"
}

func CacheTTLOptions(options map[string]interface{}) string {
	if options == nil {
		return "5m"
	}
	ttl, _ := options[OptPromptCacheTTL].(string)
	return cacheTTL(ttl)
}

func CacheEnabled(options map[string]interface{}) bool {
	if options == nil {
		return false
	}
	enabled, _ := options[OptPromptCache].(bool)
	return enabled
}

func CacheControlJSON(ttl string) map[string]string {
	return map[string]string{"type": "ephemeral", "ttl": cacheTTL(ttl)}
}
