package config

import (
	"encoding/json"
)

func parseAgentsWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableAgentsConfig {
	var cfg EditableAgentsConfig

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	// Parse defaults.
	if defaultsRaw, ok := rawMap["defaults"]; ok {
		json.Unmarshal(defaultsRaw, &cfg.Defaults)
	}

	// Parse list.
	// thinking_level and tools are decoded straight from the struct tags on
	// EditableAgentConfig (see document_types.go), the same way skills already
	// are: an absent or JSON-null "tools" yields a nil slice, which means
	// "all tools" downstream. An empty array means the same thing, so the
	// editor is free to write either without changing behavior.
	if listRaw, ok := rawMap["list"]; ok {
		json.Unmarshal(listRaw, &cfg.List)
	}

	return cfg
}
