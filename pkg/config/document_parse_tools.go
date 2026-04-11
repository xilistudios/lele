package config

import (
	"encoding/json"
)

func parseToolsWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableToolsConfig {
	var cfg EditableToolsConfig

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if webRaw, ok := rawMap["web"]; ok {
		cfg.Web = parseWebToolsWithPlaceholders(webRaw, basePath+".web", secretsByPath)
	}
	if cronRaw, ok := rawMap["cron"]; ok {
		json.Unmarshal(cronRaw, &cfg.Cron)
	}
	if execRaw, ok := rawMap["exec"]; ok {
		json.Unmarshal(execRaw, &cfg.Exec)
	}

	return cfg
}

func overlayToolsWithPlaceholders(cfg *EditableToolsConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return
	}
	if webRaw, ok := rawMap["web"]; ok {
		cfg.Web = parseWebToolsWithPlaceholders(webRaw, basePath+".web", secretsByPath)
	}
	if cronRaw, ok := rawMap["cron"]; ok {
		json.Unmarshal(cronRaw, &cfg.Cron)
	}
	if execRaw, ok := rawMap["exec"]; ok {
		json.Unmarshal(execRaw, &cfg.Exec)
	}
}

func parseWebToolsWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableWebToolsConfig {
	var cfg EditableWebToolsConfig

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if braveRaw, ok := rawMap["brave"]; ok {
		cfg.Brave = parseBraveWithPlaceholders(braveRaw, basePath+".brave", secretsByPath)
	}
	if ddgRaw, ok := rawMap["duckduckgo"]; ok {
		json.Unmarshal(ddgRaw, &cfg.DuckDuckGo)
	}
	if perplexityRaw, ok := rawMap["perplexity"]; ok {
		cfg.Perplexity = parsePerplexityWithPlaceholders(perplexityRaw, basePath+".perplexity", secretsByPath)
	}

	return cfg
}

func parseBraveWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableBraveConfig {
	var cfg EditableBraveConfig

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

func parsePerplexityWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditablePerplexityConfig {
	var cfg EditablePerplexityConfig

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
