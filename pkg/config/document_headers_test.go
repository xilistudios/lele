package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The per-provider `headers` map lives on the runtime config
// (ProviderConfig.Headers). The editable document is a SEPARATE type tree, and
// its serializer re-emits every provider field by field, so a field the
// document does not model is not merely invisible to the UIs — it is deleted
// from config.json on the next save, because PUT /api/v1/config writes the
// whole providers block from the document.
//
// These tests pin the map to the document in both directions and across every
// conversion the document layer owns. Each one fails if
// EditableNamedProviderConfig loses the field, or if a copy site stops
// carrying it.

// wantProxyHeaders is the map the motivating config carries: the {{session}}
// placeholder plus a plain literal, so a conversion that keeps only one of the
// two shapes cannot pass. proxyHeaders returns a fresh copy — tests must not
// alias the expected value into the fixture they are asserting on.
var wantProxyHeaders = map[string]string{"x-opencode-session": "{{session}}", "x-tenant": "acme"}

func proxyHeaders() map[string]string {
	out := make(map[string]string, len(wantProxyHeaders))
	for k, v := range wantProxyHeaders {
		out[k] = v
	}
	return out
}

// configFromJSON is the package convention for building a runtime config with
// named providers: DefaultConfig leaves Named nil, and only the unmarshal path
// seeds it (ensureNamedDefaults) the way a real load does.
func configFromJSON(t *testing.T, data string) *Config {
	t.Helper()
	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(data), cfg); err != nil {
		t.Fatalf("unmarshal config %s: %v", data, err)
	}
	return cfg
}

// llmproxyJSON is the motivating provider, in the shape a config.json holds it.
const llmproxyJSON = `{"providers":{"llmproxy":{"type":"openai","api_base":"https://proxy.example/v1","api_key":"k","headers":{"x-opencode-session":"{{session}}","x-tenant":"acme"}}}}`

func configWithProxyHeaders(t *testing.T) *Config {
	t.Helper()
	return configFromJSON(t, llmproxyJSON)
}

// setNamedProvider adds a named entry to a runtime config. DefaultConfig leaves
// Named nil, so it has to go through the same seeding a real load performs.
func setNamedProvider(t *testing.T, cfg *Config, name string, p NamedProviderConfig) {
	t.Helper()
	cfg.Providers.ensureNamedDefaults()
	cfg.Providers.Named[name] = p
}

// --- config → document → config -------------------------------------------

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

// A headers-only provider (no key, no models) must reach the document too: the
// builder skips "providers that have actual data", and headers are data — the
// same reasoning that put the len(Headers) clause into IsEmpty.
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

// --- the WebUI scenario that motivated the fix -----------------------------

// PUT /api/v1/config loads the on-disk document, unmarshals the browser's
// payload on top of it, then writes the whole providers block back. The browser
// sends what GET handed it, so if the document does not carry headers the save
// drops them — even when the user only touched an unrelated field.
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

	// GET /api/v1/config.
	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}

	// The browser edits one unrelated field and PUTs the whole document.
	doc.Agents.Defaults.Model = "gpt-4o"
	payload, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal browser payload: %v", err)
	}

	// handlePutConfig: load the current document, overlay the payload, validate,
	// save.
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

	// The edit landed...
	reloaded, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("reload after save: %v", err)
	}
	if reloaded.Agents.Defaults.Model != "gpt-4o" {
		t.Fatalf("agents.defaults.model = %q, want gpt-4o", reloaded.Agents.Defaults.Model)
	}

	// ...and the map survived in the document...
	if got := reloaded.Providers["llmproxy"].Headers; !reflect.DeepEqual(got, wantProxyHeaders) {
		t.Fatalf("document headers after a WebUI save = %v, want %v", got, wantProxyHeaders)
	}

	// ...in the runtime config the provider factory reads...
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

	// ...and on disk, asserted on the raw JSON rather than through a type.
	saved := readSavedJSON(t, path)
	providers := savedSection(t, saved, "providers")
	block := savedBlock(t, providers, "llmproxy",
		"type", "api_base", "proxy", "auth_method", "api_key", "headers")
	if got := block["headers"]; !reflect.DeepEqual(got, toRawJSONMap(wantProxyHeaders)) {
		t.Fatalf("saved headers = %v, want %v", got, wantProxyHeaders)
	}
}

// --- omitempty: documents of existing installs stay byte-identical ---------

// A provider without a map must not gain a "headers" key at all — neither null
// nor {} — in the typed document or in the saved file. Every existing document
// (and every golden/round-trip test) depends on that.
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

			// The typed document: providers.plain has no "headers" key.
			out, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal document: %v", err)
			}
			if _, found := jsonPath(t, out, "providers", "plain", "headers"); found {
				t.Fatalf("document JSON carries a headers key for a provider with %s: %s", tt.name, out)
			}

			// The saved file goes through the pruned serializer, which is a
			// different code path from the struct tags — assert it too.
			path := saveDoc(t, t.TempDir(), doc)
			savedBlock(t, savedSection(t, readSavedJSON(t, path), "providers"), "plain",
				"type", "api_base", "proxy", "auth_method", "api_key")

			// And the empty map must not be resurrected on the way back.
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

// --- the typed/legacy spelling ---------------------------------------------

// providers.anthropic.headers is the typed section, not a Named entry; it must
// survive the same round trip, because the factory reads the Named copy that
// ensureNamedDefaults seeds from it.
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
	// The typed section itself keeps it too.
	if got := back.Providers.Anthropic.Headers["anthropic-beta"]; got != "foo" {
		t.Fatalf("typed anthropic headers = %q, want foo", got)
	}
}

// --- the validation door ---------------------------------------------------

// submittedProviderDoc builds the document a browser PUTs: the default document
// carrying one provider whose headers are head. The agents defaults are filled
// because ValidateEditableDocument requires them — they are not what these
// tests are about, but a document that fails for an unrelated reason cannot
// prove the header door works.
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

// POST /api/v1/config/validate and PUT /api/v1/config both run
// ValidateEditableDocument and then doc.ToConfig(). Both doors must reject a bad
// map: the document validator now reports it field by field (MINOR-4 — before
// that it answered {path:"config", code:"config_invalid"} from ToConfig, which
// names no field and leaves the WebUI nothing to highlight), and
// ProvidersConfig.UnmarshalJSON inside ToConfig stays the load-time backstop,
// the single choke point shared with config load.
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
			// The submitted document is built directly: an invalid map cannot
			// arrive through a config load (T2 rejects it there), so this is
			// exactly what a browser PUTs.
			doc := submittedProviderDoc(tt.header)

			// Door 1: the document validator, with a field path.
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
			// The provider is in the Path, the header in the Message: together
			// they identify the culprit without parsing the value.
			for _, want := range tt.want {
				if !strings.Contains(errs[0].Path+errs[0].Message, want) {
					t.Fatalf("error = %+v, want it to mention %q", errs[0], want)
				}
			}

			// Door 2 (backstop): ToConfig must reject it too, so a caller that
			// only converts the document cannot bypass the check.
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

// The same door must let a valid map through, or the check above is vacuous.
func TestEditableDocument_ValidProviderHeaderPassesDocumentDoor(t *testing.T) {
	doc := submittedProviderDoc(proxyHeaders())

	if errs := ValidateEditableDocument(doc); len(errs) > 0 {
		t.Fatalf("ValidateEditableDocument = %+v, want none", errs)
	}
	if _, err := doc.ToConfig(); err != nil {
		t.Fatalf("ToConfig rejected a valid headers map: %v", err)
	}
}

// --- MAJOR-1: {{ENV_*}} is a legal token, not a save-blocking error ----------

// envHeaderJSON is the motivating file: the secret stays in the environment and
// out of config.json, which is the documented purpose of the placeholder
// (pkg/config expandEnvVars substitutes it at load).
const envHeaderJSON = `{
	"agents": {"defaults": {"workspace": "/w", "provider": "openai", "model": "gpt-4", "max_tokens": 1024}},
	"providers": {"llmproxy": {
		"type": "openai",
		"api_key": "k",
		"api_base": "https://proxy.example/v1",
		"headers": {"x-gw-token": "{{ENV_LELE_TEST_GW_TOKEN}}", "x-opencode-session": "{{session}}"}
	}}
}`

// TestEditableDocument_EnvPlaceholderHeaderSurvivesWebUIPartialSave is the
// functional proof of MAJOR-1. The editable document holds the RAW value, so
// GET /api/v1/config hands the browser "{{ENV_LELE_TEST_GW_TOKEN}}"; PUT sends
// it back through doc.ToConfig(), and until the validator recognised the env
// grammar that conversion rejected it as an "unknown placeholder" — a 422 on
// EVERY save of that config, including one that only edits
// agents.defaults.model, with no UI control to remove the map.
func TestEditableDocument_EnvPlaceholderHeaderSurvivesWebUIPartialSave(t *testing.T) {
	t.Setenv("LELE_TEST_GW_TOKEN", "s3cr3t")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(envHeaderJSON), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Load expands the placeholder: that is the value the runtime uses...
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

	// ...while the document keeps the RAW token (that is the whole point: the
	// secret must never round-trip through the browser).
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

	// The 422 scenario: an unrelated edit, PUT through the same doors the
	// handler runs. Both must stay silent.
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

	// And the save writes the token back verbatim — never the secret — so the
	// next load expands it again.
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

// --- local helpers ----------------------------------------------------------

// toRawJSONMap converts a map to the decoded-JSON shape, so a test can compare
// against a value read back from a file, and so the existing keysOf helper
// (built for decoded JSON) can report document map keys in failures.
func toRawJSONMap[V any](m map[string]V) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// jsonPath walks decoded JSON and reports whether the path exists, so a test
// can assert on the absence of a key instead of on an unmarshal target.
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

// --- NIT-8: a malformed headers value must fail the document load -----------

// The document parser used to ignore the unmarshal error of the headers value.
// A non-object ("headers": "x") then produced a nil map, and the pruned
// serializer emits no key for a nil map — so the next save deleted the user's
// headers from config.json with no diagnostic at all. The error is propagated
// now, naming the path.
//
// It is asserted on the parser directly: LoadEditableDocument runs LoadConfig
// first, and that door already rejects the same file, so an end-to-end test
// could not tell which layer answered.
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

	// The happy path still merges: an object value is read, no error.
	cfg := EditableProvidersConfig{}
	raw := json.RawMessage(`{"llmproxy":{"type":"openai","headers":{"x-a":"v"}}}`)
	if err := overlayProvidersWithPlaceholders(cfg, raw, "providers", map[string]string{}); err != nil {
		t.Fatalf("overlay rejected a valid headers object: %v", err)
	}
	if got := cfg["llmproxy"].Headers["x-a"]; got != "v" {
		t.Fatalf("merged headers x-a = %q, want v", got)
	}
}
