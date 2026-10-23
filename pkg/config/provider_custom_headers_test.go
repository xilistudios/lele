package config

import (
	"encoding/json"
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
