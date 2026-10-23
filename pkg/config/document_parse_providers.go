package config

import (
	"encoding/json"
)

func overlayProvidersWithPlaceholders(cfg EditableProvidersConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) {

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return
	}

	for name, providerRaw := range rawMap {
		providerPath := basePath + "." + name
		provider := cfg[name]
		provider = mergeNamedProvider(provider, parseNamedProviderWithPlaceholders(providerRaw, providerPath, secretsByPath))
		cfg[name] = provider
	}
}

func parseNamedProviderWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) EditableNamedProviderConfig {
	var cfg EditableNamedProviderConfig

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg
	}

	if typ, ok := rawMap["type"]; ok {
		json.Unmarshal(typ, &cfg.Type)
	}
	if apiBase, ok := rawMap["api_base"]; ok {
		json.Unmarshal(apiBase, &cfg.APIBase)
	}
	if proxy, ok := rawMap["proxy"]; ok {
		json.Unmarshal(proxy, &cfg.Proxy)
	}
	if authMethod, ok := rawMap["auth_method"]; ok {
		json.Unmarshal(authMethod, &cfg.AuthMethod)
	}
	if connectMode, ok := rawMap["connect_mode"]; ok {
		json.Unmarshal(connectMode, &cfg.ConnectMode)
	}
	if webSearch, ok := rawMap["web_search"]; ok {
		json.Unmarshal(webSearch, &cfg.WebSearch)
	}
	if models, ok := rawMap["models"]; ok {
		json.Unmarshal(models, &cfg.Models)
	}
	// headers is read straight from the file, not from the runtime config:
	// LoadConfig expands ENV placeholders before parsing, so the document has
	// to take the raw value to stay faithful to what the user wrote.
	if headers, ok := rawMap["headers"]; ok {
		json.Unmarshal(headers, &cfg.Headers)
	}
	if apiKey, ok := rawMap["api_key"]; ok {
		cfg.APIKey = parseSecretValue(apiKey, basePath+".api_key", secretsByPath)
	}

	return cfg
}
