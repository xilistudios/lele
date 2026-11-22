package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var wantProxyHeaders = map[string]string{"x-opencode-session": "{{session}}", "x-tenant": "acme"}

func proxyHeaders() map[string]string {
	out := make(map[string]string, len(wantProxyHeaders))
	for k, v := range wantProxyHeaders {
		out[k] = v
	}
	return out
}

func configFromJSON(t *testing.T, data string) *Config {
	t.Helper()
	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(data), cfg); err != nil {
		t.Fatalf("unmarshal config %s: %v", data, err)
	}
	return cfg
}

const llmproxyJSON = `{"providers":{"llmproxy":{"type":"openai","api_base":"https://proxy.example/v1","api_key":"k","headers":{"x-opencode-session":"{{session}}","x-tenant":"acme"}}}}`

func configWithProxyHeaders(t *testing.T) *Config {
	t.Helper()
	return configFromJSON(t, llmproxyJSON)
}

func setNamedProvider(t *testing.T, cfg *Config, name string, p NamedProviderConfig) {
	t.Helper()
	cfg.Providers.ensureNamedDefaults()
	cfg.Providers.Named[name] = p
}

func TestEditableDocument_ProviderHeadersRoundTrip(t *testing.T) {
	cfg := configWithProxyHeaders(t)

	doc := editableDocumentFromConfig(cfg)
	entry, ok := doc.Providers["llmproxy"]
	if !ok {
		t.Fatalf("document lost named provider llmproxy; providers=%v", keysOf(toRawJSONMap(doc.Providers)))
	}
	if !reflect.DeepEqual(entry.Headers, wantProxyHeaders) {
		t.Fatalf("document headers = %v, want %v", entry.Headers, wantProxyHeaders)
	}

	back, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	named, ok := back.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("ToConfig lost named provider llmproxy")
	}
	if !reflect.DeepEqual(named.Headers, wantProxyHeaders) {
		t.Fatalf("round-trip headers = %v, want %v", named.Headers, wantProxyHeaders)
	}
}

func TestEditableDocument_HeadersOnlyProviderReachesDocument(t *testing.T) {
	cfg := DefaultConfig()
	setNamedProvider(t, cfg, "gw", NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: ProviderConfig{APIBase: "http://127.0.0.1:8080/v1", Headers: proxyHeaders()},
	})

	doc := editableDocumentFromConfig(cfg)
	entry, ok := doc.Providers["gw"]
	if !ok {
		t.Fatal("headers-only provider dropped from the document")
	}
	if !reflect.DeepEqual(entry.Headers, wantProxyHeaders) {
		t.Fatalf("document headers = %v, want %v", entry.Headers, wantProxyHeaders)
	}
}

func TestEditableDocument_WebUIPartialSaveKeepsProviderHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	onDisk := `{
		"agents": {"defaults": {"workspace": "/w", "provider": "openai", "model": "gpt-4", "max_tokens": 1024}},
		"providers": {"llmproxy": {
			"type": "openai",
			"api_key": "k",
			"api_base": "https://proxy.example/v1",
			"headers": {"x-opencode-session": "{{session}}", "x-tenant": "acme"}
		}}
	}`
	if err := os.WriteFile(path, []byte(onDisk), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}

	doc.Agents.Defaults.Model = "gpt-4o"
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal browser payload: %v", err)
	}

	stored, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("reload for PUT: %v", err)
	}
	if err := json.Unmarshal(payload, stored); err != nil {
		t.Fatalf("overlay payload: %v", err)
	}
	if errs := ValidateEditableDocument(stored); len(errs) > 0 {
		t.Fatalf("ValidateEditableDocument: %+v", errs)
	}
	if _, err := stored.ToConfig(); err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	if err := SaveEditableDocument(path, stored); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	reloaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if reloaded.Agents.Defaults.Model != "gpt-4o" {
		t.Fatalf("agents.defaults.model = %q, want gpt-4o", reloaded.Agents.Defaults.Model)
	}

	if got := reloaded.Providers["llmproxy"].Headers; !reflect.DeepEqual(got, wantProxyHeaders) {
		t.Fatalf("document headers after a WebUI save = %v, want %v", got, wantProxyHeaders)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	named, ok := cfg.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("runtime config lost named provider llmproxy")
	}
	if !reflect.DeepEqual(named.Headers, wantProxyHeaders) {
		t.Fatalf("runtime headers after a WebUI save = %v, want %v", named.Headers, wantProxyHeaders)
	}

	saved := readSavedJSON(t, path)
	providers := savedSection(t, saved, "providers")
	block := savedBlock(t, providers, "llmproxy",
		"type", "api_base", "proxy", "auth_method", "api_key", "headers")
	if got := block["headers"]; !reflect.DeepEqual(got, toRawJSONMap(wantProxyHeaders)) {
		t.Fatalf("saved headers = %v, want %v", got, wantProxyHeaders)
	}
}

func TestEditableDocument_HeadersOmitEmptyKeepsDocumentsIdentical(t *testing.T) {
	tests := []struct {
		name string
		head map[string]string
	}{
		{"nil map", nil},
		{"empty map", map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			setNamedProvider(t, cfg, "plain", NamedProviderConfig{
				Type:           "openai",
				ProviderConfig: ProviderConfig{APIKey: "k", APIBase: "https://api.example/v1", Headers: tt.head},
			})

			doc := editableDocumentFromConfig(cfg)

			out, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal document: %v", err)
			}
			if _, found := jsonPath(t, out, "providers", "plain", "headers"); found {
				t.Fatalf("document JSON carries a headers key for a provider with %s: %s", tt.name, out)
			}

			path := saveDoc(t, t.TempDir(), doc)
			savedBlock(t, savedSection(t, readSavedJSON(t, path), "providers"), "plain",
				"type", "api_base", "proxy", "auth_method", "api_key")

			back, err := doc.ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			named, ok := back.Providers.GetNamed("plain")
			if !ok {
				t.Fatal("ToConfig lost named provider plain")
			}
			if len(named.Headers) != 0 {
				t.Fatalf("round-trip headers = %v, want none", named.Headers)
			}
		})
	}
}

func TestEditableDocument_TypedProviderHeadersRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	onDisk := `{"providers": {"anthropic": {"api_key": "k", "headers": {"anthropic-beta": "foo"}}}}`
	if err := os.WriteFile(path, []byte(onDisk), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	entry, ok := doc.Providers["anthropic"]
	if !ok {
		t.Fatalf("document lost provider anthropic; providers=%v", keysOf(toRawJSONMap(doc.Providers)))
	}
	if got := entry.Headers["anthropic-beta"]; got != "foo" {
		t.Fatalf("document anthropic headers = %v, want anthropic-beta=foo", entry.Headers)
	}

	back, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	named, ok := back.Providers.GetNamed("anthropic")
	if !ok {
		t.Fatal("ToConfig lost named provider anthropic")
	}
	want := map[string]string{"anthropic-beta": "foo"}
	if !reflect.DeepEqual(named.Headers, want) {
		t.Fatalf("typed round-trip headers = %v, want %v", named.Headers, want)
	}
	if got := back.Providers.Anthropic.Headers["anthropic-beta"]; got != "foo" {
		t.Fatalf("typed anthropic headers = %q, want foo", got)
	}
}

func submittedProviderDoc(head map[string]string) *EditableDocument {
	doc := defaultEditableDocument()
	doc.Agents.Defaults.Workspace = "/w"
	doc.Agents.Defaults.MaxTokens = 1024
	doc.Providers = EditableProvidersConfig{"llmproxy": {
		Type:    "openai",
		APIKey:  SecretValue{Mode: SecretModeLiteral, Value: "k"},
		APIBase: "https://proxy.example/v1",
		Headers: head,
	}}
	return doc
}

func TestEditableDocument_InvalidProviderHeaderRejectedThroughDocument(t *testing.T) {
	tests := []struct {
		name   string
		header map[string]string
		want   []string
	}{
		{"bad name", map[string]string{"bad name": "v"}, []string{"llmproxy", "bad name", "RFC 7230"}},
		{"forbidden Host", map[string]string{"Host": "evil.example"}, []string{"llmproxy", "Host", "cannot be overridden"}},
		{"CRLF value", map[string]string{"x-a": "a\r\nb"}, []string{"llmproxy", "x-a", "control character"}},
		{"unknown placeholder", map[string]string{"x-a": "{{user}}"}, []string{"llmproxy", "x-a", "unknown placeholder"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := submittedProviderDoc(tt.header)

			errs := ValidateEditableDocument(doc)
			if len(errs) != 1 {
				t.Fatalf("ValidateEditableDocument = %+v, want exactly one error", errs)
			}
			if got := errs[0].Path; got != "providers.llmproxy.headers" {
				t.Fatalf("Path = %q, want providers.llmproxy.headers", got)
			}
			if got := errs[0].Code; got != "invalid_header" {
				t.Fatalf("Code = %q, want invalid_header", got)
			}
			for _, want := range tt.want {
				if !strings.Contains(errs[0].Path+errs[0].Message, want) {
					t.Fatalf("error = %+v, want it to mention %q", errs[0], want)
				}
			}

			_, err := doc.ToConfig()
			if err == nil {
				t.Fatalf("ToConfig accepted headers %v, want rejection", tt.header)
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

func TestEditableDocument_ValidProviderHeaderPassesDocumentDoor(t *testing.T) {
	doc := submittedProviderDoc(proxyHeaders())

	if errs := ValidateEditableDocument(doc); len(errs) > 0 {
		t.Fatalf("ValidateEditableDocument = %+v, want none", errs)
	}
	if _, err := doc.ToConfig(); err != nil {
		t.Fatalf("ToConfig rejected a valid headers map: %v", err)
	}
}

const envHeaderJSON = `{
	"agents": {"defaults": {"workspace": "/w", "provider": "openai", "model": "gpt-4", "max_tokens": 1024}},
	"providers": {"llmproxy": {
		"type": "openai",
		"api_key": "k",
		"api_base": "https://proxy.example/v1",
		"headers": {"x-gw-token": "{{ENV_LELE_TEST_GW_TOKEN}}", "x-opencode-session": "{{session}}"}
	}}
}`

func TestEditableDocument_EnvPlaceholderHeaderSurvivesWebUIPartialSave(t *testing.T) {
	t.Setenv("LELE_TEST_GW_TOKEN", "s3cr3t")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(envHeaderJSON), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	named, ok := cfg.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("runtime config lost named provider llmproxy")
	}
	if got := named.Headers["x-gw-token"]; got != "s3cr3t" {
		t.Fatalf("loaded x-gw-token = %q, want the expanded env value", got)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	entry, ok := doc.Providers["llmproxy"]
	if !ok {
		t.Fatal("document lost provider llmproxy")
	}
	if got := entry.Headers["x-gw-token"]; got != "{{ENV_LELE_TEST_GW_TOKEN}}" {
		t.Fatalf("document x-gw-token = %q, want the raw placeholder", got)
	}

	doc.Agents.Defaults.Model = "gpt-4o"
	if errs := ValidateEditableDocument(doc); len(errs) > 0 {
		t.Fatalf("ValidateEditableDocument = %+v, want none: an env placeholder must not block a save", errs)
	}
	back, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig = %v, want no error: the document round-trip must accept {{ENV_*}}", err)
	}
	if got := back.Providers.Named["llmproxy"].Headers["x-gw-token"]; got != "{{ENV_LELE_TEST_GW_TOKEN}}" {
		t.Fatalf("ToConfig headers x-gw-token = %q, want the raw placeholder preserved", got)
	}

	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}
	saved := readSavedJSON(t, path)
	block := savedBlock(t, savedSection(t, saved, "providers"), "llmproxy",
		"type", "api_base", "proxy", "auth_method", "api_key", "headers")
	want := map[string]string{"x-gw-token": "{{ENV_LELE_TEST_GW_TOKEN}}", "x-opencode-session": "{{session}}"}
	if got := block["headers"]; !reflect.DeepEqual(got, toRawJSONMap(want)) {
		t.Fatalf("saved headers = %v, want %v", got, want)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	again, ok := reloaded.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("reload lost named provider llmproxy")
	}
	if got := again.Headers["x-gw-token"]; got != "s3cr3t" {
		t.Fatalf("reloaded x-gw-token = %q, want the env value expanded again", got)
	}
	if got := again.Headers["x-opencode-session"]; got != "{{session}}" {
		t.Fatalf("reloaded x-opencode-session = %q, want {{session}} untouched by expandEnvVars", got)
	}
}

func toRawJSONMap[V any](m map[string]V) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func jsonPath(t *testing.T, data []byte, path ...string) (interface{}, bool) {
	t.Helper()
	var root interface{}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, data)
	}
	node := root
	for _, key := range path {
		obj, ok := node.(map[string]interface{})
		if !ok {
			return nil, false
		}
		node, ok = obj[key]
		if !ok {
			return nil, false
		}
	}
	return node, true
}

func TestOverlayProvidersWithPlaceholders_HeaderTypeError(t *testing.T) {
	tests := []struct {
		name    string
		headers string
	}{
		{"string", `"x"`},
		{"array", `["x"]`},
		{"number", `3`},
		{"nested object", `{"x":{"y":"z"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := EditableProvidersConfig{}
			raw := json.RawMessage(`{"llmproxy":{"type":"openai","headers":` + tt.headers + `}}`)
			err := overlayProvidersWithPlaceholders(cfg, raw, "providers", map[string]string{})
			if err == nil {
				t.Fatalf("overlay accepted headers %s, want an error naming the path", tt.headers)
			}
			if !strings.Contains(err.Error(), "providers.llmproxy.headers") {
				t.Fatalf("error = %q, want it to name providers.llmproxy.headers", err)
			}
		})
	}

	cfg := EditableProvidersConfig{}
	raw := json.RawMessage(`{"llmproxy":{"type":"openai","headers":{"x-a":"v"}}}`)
	if err := overlayProvidersWithPlaceholders(cfg, raw, "providers", map[string]string{}); err != nil {
		t.Fatalf("overlay rejected a valid headers object: %v", err)
	}
	if got := cfg["llmproxy"].Headers["x-a"]; got != "v" {
		t.Fatalf("merged headers x-a = %q, want v", got)
	}
}
