package config

import (
	"encoding/json"
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

var namedHostPaths = [][]string{
	{"gateway", "host"},
	{"channels", "native", "host"},
}

func treeValueAt(tree map[string]interface{}, path ...string) (interface{}, bool) {
	var current interface{} = tree
	for _, key := range path {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func setTreeValueAt(tree map[string]interface{}, value interface{}, path ...string) {
	key := path[0]
	if len(path) == 1 {
		tree[key] = value
		return
	}
	child, ok := tree[key].(map[string]interface{})
	if !ok {
		child = map[string]interface{}{}
		tree[key] = child
	}
	setTreeValueAt(child, value, path[1:]...)
}

// keepNamedHosts restores the bind-host keys a file already names when the
// editable document drops them for equalling the default, so the WebUI writer
// never turns an explicit gateway.host / channels.native.host into an unset
// one. The restored value is the default itself, because that is exactly when
// the document omits the key. server.host needs no such help: the document does
// not model the server section, so the delta merge preserves it as written.
func keepNamedHosts(next map[string]interface{}, raw map[string]json.RawMessage) error {
	defaults, err := jsonTreeOf(DefaultConfig())
	if err != nil {
		return err
	}
	for _, path := range namedHostPaths {
		if !rawPathSet(raw, path...) {
			continue
		}
		if _, present := treeValueAt(next, path...); present {
			continue
		}
		defValue, ok := treeValueAt(defaults, path...)
		if !ok {
			continue
		}
		setTreeValueAt(next, defValue, path...)
	}
	return nil
}
