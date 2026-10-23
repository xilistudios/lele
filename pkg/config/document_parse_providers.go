package config

import (
	"encoding/json"
	"fmt"
)

// overlayProvidersWithPlaceholders merges the RAW providers section of the file
// into the document (the runtime config it is seeded from has already been
// ENV-expanded, so only the file keeps what the user actually wrote).
//
// It returns an error when a provider's `headers` cannot be read: see
// parseNamedProviderWithPlaceholders. The caller (overlaySecretsFromRaw →
// LoadEditableDocument) surfaces it, so a malformed section fails the load
// instead of silently losing data on the next save.
func overlayProvidersWithPlaceholders(cfg EditableProvidersConfig, raw json.RawMessage, basePath string, secretsByPath map[string]string) error {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		// Pre-existing leniency, unchanged: a providers section that is not an
		// object has nothing to overlay, so the document keeps what the runtime
		// config derived. Only a readable provider object with an unreadable
		// `headers` value is worth failing the load for (below).
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
		// Pre-existing leniency, unchanged: the provider is not an object, so
		// there is no headers field to read and nothing to lose.
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
	// headers is read straight from the file, not from the runtime config:
	// LoadConfig expands ENV placeholders before parsing, so the document has
	// to take the raw value to stay faithful to what the user wrote.
	if headers, ok := rawMap["headers"]; ok {
		// The error is propagated, not swallowed: a non-object value (a string,
		// an array) unmarshals into a nil map, and the next save would then
		// delete the key it could not read — silent data loss on the user's
		// config. Failing the load names the path instead.
		if err := json.Unmarshal(headers, &cfg.Headers); err != nil {
			// The path is the same shape ValidateEditableDocument reports
			// ("providers.<name>.headers"), so a user — and the WebUI — sees
			// the identical pointer from either door.
			return cfg, fmt.Errorf("%s.headers: %w", basePath, err)
		}
	}
	if apiKey, ok := rawMap["api_key"]; ok {
		cfg.APIKey = parseSecretValue(apiKey, basePath+".api_key", secretsByPath)
	}

	return cfg, nil
}
