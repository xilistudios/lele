package config

import (
	"fmt"
	"strings"
)

func literalOrEmptySecret(value string) SecretValue {
	if strings.TrimSpace(value) == "" {
		return SecretValue{Mode: SecretModeEmpty}
	}
	return SecretValue{Mode: SecretModeLiteral, Value: value}
}

func envPlaceholderString(secret SecretValue) string {
	if secret.EnvDefault != nil {
		return fmt.Sprintf("{{ENV_%s:%s}}", secret.EnvName, *secret.EnvDefault)
	}
	return fmt.Sprintf("{{ENV_%s}}", secret.EnvName)
}

func mergeNamedProvider(base, overlay EditableNamedProviderConfig) EditableNamedProviderConfig {
	if overlay.Type != "" {
		base.Type = overlay.Type
	}
	if overlay.APIBase != "" || base.APIBase == "" {
		base.APIBase = overlay.APIBase
	}
	if overlay.Proxy != "" || base.Proxy == "" {
		base.Proxy = overlay.Proxy
	}
	if overlay.AuthMethod != "" || base.AuthMethod == "" {
		base.AuthMethod = overlay.AuthMethod
	}
	if overlay.ConnectMode != "" || base.ConnectMode == "" {
		base.ConnectMode = overlay.ConnectMode
	}
	if overlay.WebSearch != nil {
		base.WebSearch = overlay.WebSearch
	}
	if overlay.Models != nil {
		base.Models = overlay.Models
	}
	if overlay.APIKey.Mode != "" {
		base.APIKey = overlay.APIKey
	}
	return base
}
