package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests here cover the per-provider `headers` field: it must survive every
// JSON path a config takes (load, clone/Save, MarshalJSON) and bad maps must
// fail at unmarshal, naming both the provider and the header.

func TestProvidersConfig_UnmarshalCustomHeaders(t *testing.T) {
	cfg := DefaultConfig()
	data := []byte(`{
		"providers": {
			"llmproxy": {
				"type": "openai",
				"api_base": "https://proxy.example/v1",
				"api_key": "k",
				"headers": {"x-opencode-session": "{{session}}"}
			}
		}
	}`)
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	named, ok := cfg.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("expected named provider llmproxy")
	}
	if got := named.Headers["x-opencode-session"]; got != "{{session}}" {
		t.Fatalf("headers[x-opencode-session] = %q, want %q", got, "{{session}}")
	}
	if len(named.Headers) != 1 {
		t.Fatalf("headers = %v, want exactly one entry", named.Headers)
	}
}

func TestProvidersConfig_CustomHeadersRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	// "gw" carries ONLY headers: it must still be persisted, which is what the
	// len(p.Headers) clause in IsEmpty guarantees (MarshalJSON skips empty).
	data := []byte(`{
		"providers": {
			"llmproxy": {
				"type": "openai",
				"api_base": "https://proxy.example/v1",
				"api_key": "k",
				"headers": {"x-opencode-session": "{{session}}", "x-tenant": "acme"}
			},
			"gw": {"type": "openai", "headers": {"x-a": "v"}}
		}
	}`)
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	cloned := cfg.clone()
	if cloned == nil {
		t.Fatal("clone() = nil")
	}
	named, ok := cloned.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("cloned config lost named provider llmproxy")
	}
	if named.Headers["x-opencode-session"] != "{{session}}" || named.Headers["x-tenant"] != "acme" {
		t.Fatalf("cloned headers = %v, want x-opencode-session={{session}} x-tenant=acme", named.Headers)
	}

	out, err := json.Marshal(cfg.Providers)
	if err != nil {
		t.Fatalf("marshal providers: %v", err)
	}
	for _, want := range []string{`"llmproxy"`, `"x-opencode-session"`, `"{{session}}"`, `"gw"`, `"x-a"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("marshalled providers %s missing %s", out, want)
		}
	}

	// And the marshalled JSON unmarshals back to the same maps.
	back := DefaultConfig()
	if err := json.Unmarshal(out, &back.Providers); err != nil {
		t.Fatalf("re-unmarshal providers: %v", err)
	}
	again, ok := back.Providers.GetNamed("llmproxy")
	if !ok || again.Headers["x-opencode-session"] != "{{session}}" {
		t.Fatalf("round-trip lost headers: named=%+v ok=%v", again, ok)
	}
}

func TestProvidersConfig_CustomHeadersTypedSection(t *testing.T) {
	cfg := DefaultConfig()
	data := []byte(`{"providers":{"anthropic":{"api_key":"k","headers":{"anthropic-beta":"foo"}}}}`)
	if err := json.Unmarshal(data, cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// The typed field keeps the map...
	if got := cfg.Providers.Anthropic.Headers["anthropic-beta"]; got != "foo" {
		t.Fatalf("typed anthropic headers = %q, want %q", got, "foo")
	}
	// ...and so does the named entry the factory reads (ensureNamedDefaults
	// seeds typed sections into Named; this is what makes legacy typed fields
	// reach the provider selection).
	named, ok := cfg.Providers.GetNamed("anthropic")
	if !ok {
		t.Fatal("expected named provider anthropic")
	}
	if named.Headers["anthropic-beta"] != "foo" {
		t.Fatalf("named anthropic headers = %v, want anthropic-beta=foo", named.Headers)
	}

	// A typed section set programmatically (no JSON involved) must also become
	// visible through GetNamed.
	p := &ProvidersConfig{}
	p.Anthropic.Headers = map[string]string{"anthropic-beta": "bar"}
	got, ok := p.GetNamed("anthropic")
	if !ok || got.Headers["anthropic-beta"] != "bar" {
		t.Fatalf("GetNamed(anthropic) = %+v ok=%v, want headers anthropic-beta=bar", got, ok)
	}
}

func TestNamedProviderConfig_IsEmptyCountsHeaders(t *testing.T) {
	headersOnly := NamedProviderConfig{
		ProviderConfig: ProviderConfig{Headers: map[string]string{"x-a": "v"}},
	}
	if headersOnly.IsEmpty() {
		t.Fatal("IsEmpty() = true for a provider carrying headers, want false (MarshalJSON would drop it)")
	}
	if (NamedProviderConfig{}).IsEmpty() != true {
		t.Fatal("IsEmpty() = false for the zero value, want true")
	}
	if (NamedProviderConfig{ProviderConfig: ProviderConfig{Headers: map[string]string{}}}).IsEmpty() != true {
		t.Fatal("IsEmpty() = false for an empty headers map, want true")
	}
}

func TestProvidersConfig_UnmarshalRejectsInvalidCustomHeaders(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantErr  string // full message; "" = only check the substrings below
		contains []string
	}{
		{
			name:     "invalid header name",
			json:     `{"providers":{"llmproxy":{"api_key":"k","headers":{"bad name":"v"}}}}`,
			wantErr:  `provider "llmproxy": custom header "bad name": invalid name: must be an RFC 7230 token`,
			contains: []string{"llmproxy", "bad name"},
		},
		{
			name:     "CRLF in the value",
			json:     `{"providers":{"llmproxy":{"api_key":"k","headers":{"x-a":"a\r\nb"}}}}`,
			contains: []string{"llmproxy", "x-a", "control character"},
		},
		{
			name:     "forbidden Host header",
			json:     `{"providers":{"llmproxy":{"api_key":"k","headers":{"Host":"evil.example"}}}}`,
			contains: []string{"llmproxy", "Host", "cannot be overridden"},
		},
		{
			name:     "unknown placeholder",
			json:     `{"providers":{"llmproxy":{"api_key":"k","headers":{"x-a":"{{sesssion}}"}}}}`,
			contains: []string{"llmproxy", "x-a", "unknown placeholder"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			err := json.Unmarshal([]byte(tt.json), cfg)
			if err == nil {
				t.Fatalf("unmarshal %s = nil error, want rejection", tt.json)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want it to mention %q", err, want)
				}
			}
			if tt.wantErr != "" && err.Error() != tt.wantErr {
				t.Fatalf("error = %q, want exactly %q", err, tt.wantErr)
			}
		})
	}
}

func TestProvidersConfig_UnmarshalEmptyOrAbsentHeaders(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"empty map", `{"providers":{"llmproxy":{"api_key":"k","headers":{}}}}`},
		{"absent", `{"providers":{"llmproxy":{"api_key":"k"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			if err := json.Unmarshal([]byte(tt.json), cfg); err != nil {
				t.Fatalf("unmarshal %s: %v, want no error", tt.json, err)
			}
		})
	}
}

// TestProvidersConfig_EnvPlaceholderHeadersLoad pins the load half of the
// MAJOR-1 fix: {{ENV_NAME}} / {{ENV_NAME:default}} is the documented way to
// keep a secret out of config.json (pkg/config expandEnvVars), so it must be a
// legal token in a header value.
//
// Both paths are asserted because they differ in what the validator sees:
//   - LoadConfig expands the placeholder BEFORE parsing, so the runtime config
//     holds the secret and the validator never sees the token;
//   - the editable document keeps the RAW token (GET /api/v1/config must echo
//     it back, or the secret leaks to the browser), and every save feeds that
//     raw value through this parser via EditableDocument.ToConfig. That second
//     door is the one that 422'd: rejecting the token there made even an
//     unrelated edit (agents.defaults.model) impossible to save.
func TestProvidersConfig_EnvPlaceholderHeadersLoad(t *testing.T) {
	t.Setenv("LELE_TEST_GW_TOKEN", "s3cr3t")

	path := filepath.Join(t.TempDir(), "config.json")
	onDisk := `{"providers":{"llmproxy":{"type":"openai","api_key":"k",` +
		`"api_base":"https://proxy.example/v1","headers":{` +
		`"x-gw-token":"{{ENV_LELE_TEST_GW_TOKEN}}",` +
		`"x-gw-team":"{{ENV_LELE_TEST_UNSET_VAR:platform}}"}}}}`
	if err := os.WriteFile(path, []byte(onDisk), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig rejected an env placeholder header: %v", err)
	}
	named, ok := cfg.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("runtime config lost named provider llmproxy")
	}
	if got := named.Headers["x-gw-token"]; got != "s3cr3t" {
		t.Fatalf("x-gw-token = %q, want the expanded env value", got)
	}
	if got := named.Headers["x-gw-team"]; got != "platform" {
		t.Fatalf("x-gw-team = %q, want the default of an unset env var", got)
	}

	// The RAW token through this parser — exactly what a document save does.
	// It must be accepted AND left unexpanded: substitution is LoadConfig's
	// job, and the document round-trip has to preserve what the user wrote.
	raw := DefaultConfig()
	data := `{"providers":{"llmproxy":{"api_key":"k","headers":{` +
		`"x-gw-token":"{{ENV_LELE_TEST_GW_TOKEN}}",` +
		`"x-gw-empty-default":"{{ENV_LELE_TEST_GW_TOKEN:}}",` +
		`"x-gw-session":"Bearer {{ENV_LELE_TEST_GW_TOKEN}}/{{session}}"}}}}`
	if err := json.Unmarshal([]byte(data), raw); err != nil {
		t.Fatalf("unmarshal the raw document value: %v", err)
	}
	again, ok := raw.Providers.GetNamed("llmproxy")
	if !ok {
		t.Fatal("raw unmarshal lost named provider llmproxy")
	}
	for header, want := range map[string]string{
		"x-gw-token":         "{{ENV_LELE_TEST_GW_TOKEN}}",
		"x-gw-empty-default": "{{ENV_LELE_TEST_GW_TOKEN:}}",
		"x-gw-session":       "Bearer {{ENV_LELE_TEST_GW_TOKEN}}/{{session}}",
	} {
		if got := again.Headers[header]; got != want {
			t.Fatalf("%s = %q, want the raw token preserved (no expansion here)", header, got)
		}
	}
}

// The env grammar is a whitelist, not a wildcard: anything shaped like a token
// but outside {{session}} / {{ENV_NAME}} / {{ENV_NAME:default}} stays rejected,
// so a typo cannot ride all the way to the wire as literal text.
func TestProvidersConfig_UnmarshalRejectsMalformedEnvPlaceholder(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"empty name", "{{ENV_}}"},
		{"not the ENV_ prefix", "{{ENV-1}}"},
		{"leading digit in the name", "{{ENV_1FOO}}"},
		{"missing braces", "ENV_FOO"}, // legal literal, see below
	}
	for _, tt := range tests {
		if tt.name == "missing braces" {
			// A single brace pair is literal text: it must LOAD.
			cfg := DefaultConfig()
			data := `{"providers":{"llmproxy":{"api_key":"k","headers":{"x-a":"` + tt.value + `"}}}}`
			if err := json.Unmarshal([]byte(data), cfg); err != nil {
				t.Fatalf("unmarshal %q = %v, want it accepted as a literal value", tt.value, err)
			}
			if got := cfg.Providers.Named["llmproxy"].Headers["x-a"]; got != tt.value {
				t.Fatalf("x-a = %q, want %q", got, tt.value)
			}
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			data := `{"providers":{"llmproxy":{"api_key":"k","headers":{"x-a":"` + tt.value + `"}}}}`
			err := json.Unmarshal([]byte(data), cfg)
			if err == nil {
				t.Fatalf("unmarshal %q = nil error, want rejection", tt.value)
			}
			if !strings.Contains(err.Error(), "unknown placeholder") {
				t.Fatalf("error = %q, want it to mention unknown placeholder", err)
			}
		})
	}
}
