package config

import (
	"encoding/json"
)

// parseToolsWithPlaceholders builds an EditableToolsConfig straight from raw
// JSON (no runtime defaults underneath). Callers that already merged the
// defaults must use overlayToolsWithPlaceholders instead.
func parseToolsWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableToolsConfig {
	var cfg EditableToolsConfig

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if webRaw, ok := rawMap["web"]; ok {
		cfg.Web = parseWebToolsWithPlaceholders(cfg.Web, webRaw, basePath+".web", secretsByPath)
	}
	if cronRaw, ok := rawMap["cron"]; ok {
		json.Unmarshal(cronRaw, &cfg.Cron)
	}
	if execRaw, ok := rawMap["exec"]; ok {
		json.Unmarshal(execRaw, &cfg.Exec)
	}

	return cfg
}

// overlayToolsWithPlaceholders re-reads the "tools" section of a saved file on
// top of the already-populated document, so secret fields keep their ENV /
// keyring placeholders instead of their resolved literals.
//
// It is a strict OVERLAY: every parser receives the current value as its base
// and only replaces the keys the file actually contains. toSerializable prunes
// default-valued keys, so "absent from the file" is the normal case and must
// never reset an engine to its zero value (that would silently disable
// duckduckgo or drop searxng's "general"/"auto" defaults on the next save).
func overlayToolsWithPlaceholders(cfg *EditableToolsConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return
	}
	if webRaw, ok := rawMap["web"]; ok {
		cfg.Web = parseWebToolsWithPlaceholders(cfg.Web, webRaw, basePath+".web", secretsByPath)
	}
	if cronRaw, ok := rawMap["cron"]; ok {
		json.Unmarshal(cronRaw, &cfg.Cron)
	}
	if execRaw, ok := rawMap["exec"]; ok {
		json.Unmarshal(execRaw, &cfg.Exec)
	}
}

// parseWebToolsWithPlaceholders overlays raw onto base: engines absent from
// the file keep their base value (see overlayToolsWithPlaceholders).
func parseWebToolsWithPlaceholders(base EditableWebToolsConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableWebToolsConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if braveRaw, ok := rawMap["brave"]; ok {
		cfg.Brave = parseBraveWithPlaceholders(cfg.Brave, braveRaw, basePath+".brave", secretsByPath)
	}
	if ddgRaw, ok := rawMap["duckduckgo"]; ok {
		json.Unmarshal(ddgRaw, &cfg.DuckDuckGo)
	}
	if perplexityRaw, ok := rawMap["perplexity"]; ok {
		cfg.Perplexity = parsePerplexityWithPlaceholders(cfg.Perplexity, perplexityRaw, basePath+".perplexity", secretsByPath)
	}
	if searxngRaw, ok := rawMap["searxng"]; ok {
		json.Unmarshal(searxngRaw, &cfg.SearXNG)
	}

	return cfg
}

// parseBraveWithPlaceholders overlays raw onto base (absent keys are kept).
func parseBraveWithPlaceholders(base EditableBraveConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableBraveConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if maxResults, ok := rawMap["max_results"]; ok {
		json.Unmarshal(maxResults, &cfg.MaxResults)
	}
	if apiKey, ok := rawMap["api_key"]; ok {
		cfg.APIKey = parseSecretValue(apiKey, basePath+".api_key", secretsByPath)
	}

	return cfg
}

// parsePerplexityWithPlaceholders overlays raw onto base (absent keys are kept).
func parsePerplexityWithPlaceholders(base EditablePerplexityConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) EditablePerplexityConfig {
	cfg := base

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if enabled, ok := rawMap["enabled"]; ok {
		json.Unmarshal(enabled, &cfg.Enabled)
	}
	if maxResults, ok := rawMap["max_results"]; ok {
		json.Unmarshal(maxResults, &cfg.MaxResults)
	}
	if apiKey, ok := rawMap["api_key"]; ok {
		cfg.APIKey = parseSecretValue(apiKey, basePath+".api_key", secretsByPath)
	}

	return cfg
}
