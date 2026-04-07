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
	if listRaw, ok := rawMap["list"]; ok {
		json.Unmarshal(listRaw, &cfg.List)
	}

	return cfg
}
