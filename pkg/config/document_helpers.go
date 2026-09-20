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

// keyringPlaceholderString renders a {{SECRET:name}} placeholder for saving.
func keyringPlaceholderString(secret SecretValue) string {
	return fmt.Sprintf("{{SECRET:%s}}", secret.SecretName)
}

// writeSecret writes a SecretValue into the given map under key, preserving
// ENV and keyring placeholders and emitting literal values directly.
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

// --- prune-on-save helpers -------------------------------------------------
//
// LoadConfig unmarshals the file OVER DefaultConfig(), so any omitted key
// keeps its code default. That makes it lossless to omit values that equal
// the runtime defaults when saving: toSerializable uses the helpers below to
// emit a key only when it differs from DefaultConfig(), and to drop a whole
// block (channel, gateway, tools engine, section) when nothing inside it
// differs. The one field whose prune value is NOT DefaultConfig() is
// session.ephemeral — see pinnedSessionEphemeral.

// putIfDiff writes key only when value differs from the runtime default def.
func putIfDiff[T comparable](m map[string]interface{}, key string, value, def T) {
	if value != def {
		m[key] = value
	}
}

// putSliceIfDiff is putIfDiff for string slices. nil and empty are treated as
// equal so an omitted list does not pin "[]" (or null) against the default.
// When a list is cleared below a non-empty default, an explicit [] is written:
// JSON null would make encoding/json skip the field and resurrect the default.
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

// putSecretIfDiff writes a SecretValue only when it differs from the runtime
// default string def. Env/keyring placeholders always count as configured
// (their resolved value is invisible to a static comparison) and are written
// via writeSecret exactly as before; a literal is compared against the
// default; an empty secret differs only when the default is non-empty
// (explicit clear).
func putSecretIfDiff(m map[string]interface{}, key string, secret SecretValue, def string) {
	switch secret.Mode {
	case SecretModeEnv, SecretModeKeyring:
		writeSecret(m, key, secret)
	case SecretModeLiteral:
		putIfDiff(m, key, secret.Value, def)
	default: // SecretModeEmpty or unset
		if def != "" {
			m[key] = ""
		}
	}
}

// putSection adds block to parent only when it contains at least one
// non-default key. An untouched channel therefore writes nothing, and a
// document that equals the defaults ends up with no "channels" section at
// all. The same rule is used for every nested block the pruned serializer
// builds (channels, tools.web engines, and the top-level sections).
func putSection(parent map[string]interface{}, name string, block map[string]interface{}) {
	if len(block) > 0 {
		parent[name] = block
	}
}

// pinnedSessionEphemeral reports whether an already-serialized document
// carries session.ephemeral explicitly. It mirrors LoadConfig's raw-JSON check
// (absent key => false), which is why the prune compares ephemeral against
// false instead of against DefaultConfig()'s true.
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
	if overlay.APIKey.Mode != "" {
		base.APIKey = overlay.APIKey
	}
	return base
}
