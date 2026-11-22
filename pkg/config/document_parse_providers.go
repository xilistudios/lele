package config

import (
	"encoding/json"
	"fmt"
)

func overlayProvidersWithPlaceholders(cfg EditableProvidersConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) error {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil //nolint:nilerr // tolerant by design: nothing to overlay
	}

	for name, providerRaw := range rawMap {
		providerPath := basePath + "." + name
		provider := cfg[name]
		parsed, err := parseNamedProviderWithPlaceholders(providerRaw, providerPath, secretsByPath)
		if err != nil {
			return err
		}
		provider = mergeNamedProvider(provider, parsed)
		cfg[name] = provider
	}
	return nil
}

func parseNamedProviderWithPlaceholders(raw json.RawMessage, basePath string, secretsByPath map[string]string) (EditableNamedProviderConfig, error) {
	var cfg EditableNamedProviderConfig

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		json.Unmarshal(raw, &cfg)
		return cfg, nil //nolint:nilerr // tolerant by design: no field to read
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
	if headers, ok := rawMap["headers"]; ok {
		if err := json.Unmarshal(headers, &cfg.Headers); err != nil {
			return cfg, fmt.Errorf("%s.headers: %w", basePath, err)
		}
	}
	if apiKey, ok := rawMap["api_key"]; ok {
		cfg.APIKey = parseSecretValue(apiKey, basePath+".api_key", secretsByPath)
	}

	return cfg, nil
}
