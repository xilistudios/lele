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

func keyringPlaceholderString(secret SecretValue) string {
	return fmt.Sprintf("{{SECRET:%s}}", secret.SecretName)
}

func writeSecret(m map[string]interface{}, key string, secret SecretValue) {
	switch secret.Mode {
	case SecretModeEnv:
		m[key] = envPlaceholderString(secret)
	case SecretModeKeyring:
		m[key] = keyringPlaceholderString(secret)
	case SecretModeLiteral:
		if secret.Value != "" {
			m[key] = secret.Value
		}
	}
}

func putIfDiff[T comparable](m map[string]interface{}, key string, value, def T) {
	if value != def {
		m[key] = value
	}
}

func putSliceIfDiff(m map[string]interface{}, key string, value, def []string) {
	if stringSlicesEqual(value, def) {
		return
	}
	if value == nil {
		value = []string{}
	}
	m[key] = value
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func putSecretIfDiff(m map[string]interface{}, key string, secret SecretValue, def string) {
	switch secret.Mode {
	case SecretModeEnv, SecretModeKeyring:
		writeSecret(m, key, secret)
	case SecretModeLiteral:
		putIfDiff(m, key, secret.Value, def)
	default:
		if def != "" {
			m[key] = ""
		}
	}
}

func putSection(parent map[string]interface{}, name string, block map[string]interface{}) {
	if len(block) > 0 {
		parent[name] = block
	}
}

func pinnedSessionEphemeral(serializable map[string]interface{}) bool {
	session, ok := serializable["session"].(map[string]interface{})
	if !ok {
		return false
	}
	_, ok = session["ephemeral"]
	return ok
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
	if overlay.Headers != nil {
		base.Headers = overlay.Headers
	}
	if overlay.APIKey.Mode != "" {
		base.APIKey = overlay.APIKey
	}
	return base
}
