package auth

import (
	"reflect"
	"testing"
)

// TestOAuthProviderNamesSortedAndComplete pins the registry contents: the CLI
// derives its help/error text from this list, so a missing or renamed entry is a
// user-visible regression.
func TestOAuthProviderNamesSortedAndComplete(t *testing.T) {
	got := OAuthProviderNames()
	want := []string{"anthropic", "google-antigravity", "openai"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OAuthProviderNames() = %v, want %v", got, want)
	}

	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("OAuthProviderNames() is not sorted ascending: %v", got)
		}
	}
}

// TestOAuthProviderNamesFreshSlicePerCall guards the "slice nueva en cada
// llamada" contract: a caller mutating the returned slice must not affect the
// registry or another caller.
func TestOAuthProviderNamesFreshSlicePerCall(t *testing.T) {
	first := OAuthProviderNames()
	if len(first) == 0 {
		t.Fatal("OAuthProviderNames() returned an empty list; the test would be vacuous")
	}
	canonical := first[0]

	first[0] = "pwned"

	second := OAuthProviderNames()
	if !reflect.DeepEqual(second, []string{"anthropic", "google-antigravity", "openai"}) {
		t.Errorf("OAuthProviderNames() after a caller mutation = %v, want the canonical list [anthropic google-antigravity openai]", second)
	}
	if second[0] != canonical {
		t.Errorf("OAuthProviderNames() slices share state: second call [0] = %q, want %q", second[0], canonical)
	}

	// The second slice must be independent too (no cached slice handed out twice).
	second[0] = "pwned again"
	if third := OAuthProviderNames(); third[0] != canonical {
		t.Errorf("OAuthProviderNames() reused a previously returned slice: third call [0] = %q, want %q", third[0], canonical)
	}
}

func TestOAuthProviderConfigByName(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantOK      bool
		wantCfgName string
	}{
		{name: "canonical openai", input: "openai", wantOK: true, wantCfgName: "openai"},
		{name: "canonical google-antigravity", input: "google-antigravity", wantOK: true, wantCfgName: "google-antigravity"},
		{name: "alias codex -> openai", input: "codex", wantOK: true, wantCfgName: "openai"},
		{name: "alias antigravity -> google-antigravity", input: "antigravity", wantOK: true, wantCfgName: "google-antigravity"},
		{name: "uppercase", input: "OPENAI", wantOK: true, wantCfgName: "openai"},
		{name: "mixed case alias", input: "Codex", wantOK: true, wantCfgName: "openai"},
		{name: "surrounding whitespace", input: "  google-antigravity\t", wantOK: true, wantCfgName: "google-antigravity"},
		{name: "whitespace around alias", input: " antigravity ", wantOK: true, wantCfgName: "google-antigravity"},
		{name: "unknown provider", input: "grok", wantOK: false},
		{name: "alias claude -> anthropic", input: " claude ", wantOK: true, wantCfgName: "anthropic"},
		{name: "empty", input: "", wantOK: false},
		{name: "whitespace only", input: "   ", wantOK: false},
		{name: "partial name is not a prefix match", input: "open", wantOK: false},
		{name: "canonical anthropic", input: "anthropic", wantOK: true, wantCfgName: "anthropic"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, ok := OAuthProviderConfigByName(tc.input)

			if ok != tc.wantOK {
				t.Fatalf("OAuthProviderConfigByName(%q) ok = %v, want %v", tc.input, ok, tc.wantOK)
			}

			if !tc.wantOK {
				if !reflect.DeepEqual(cfg, OAuthProviderConfig{}) {
					t.Errorf("OAuthProviderConfigByName(%q) returned a non-zero config on failure: %+v", tc.input, cfg)
				}
				return
			}

			if cfg.Name != tc.wantCfgName {
				t.Errorf("OAuthProviderConfigByName(%q).Name = %q, want %q", tc.input, cfg.Name, tc.wantCfgName)
			}
		})
	}
}

// TestClaudeAliasResolvesToAnthropicPreset pins the `claude` alias contract:
// the alias selects the Claude OAuth flow, but the canonical registry and
// credential-store key stays `anthropic`. It must therefore resolve to the
// verbatim ClaudeOAuthConfig() preset (Name == "anthropic", not "claude"), which
// is what pkg/providers and the credential store look up.
func TestClaudeAliasResolvesToAnthropicPreset(t *testing.T) {
	cfg, ok := OAuthProviderConfigByName("claude")
	if !ok {
		t.Fatal("OAuthProviderConfigByName(\"claude\") ok = false, want true")
	}
	if cfg.Name != "anthropic" {
		t.Errorf("OAuthProviderConfigByName(\"claude\").Name = %q, want %q", cfg.Name, "anthropic")
	}
	if want := ClaudeOAuthConfig(); !reflect.DeepEqual(cfg, want) {
		t.Errorf("OAuthProviderConfigByName(\"claude\") = %+v, want the ClaudeOAuthConfig() preset %+v", cfg, want)
	}
}

// TestOAuthProviderConfigByNameClonesExtraAuthParams is the map-leak test:
// OAuthProviderConfig embeds a map, so an accessor that returned the stored
// preset as-is would let any caller rewrite shared state
// (cfg.ExtraAuthParams["pwned"] = "1") and change the behavior of every other
// consumer.
func TestOAuthProviderConfigByNameClonesExtraAuthParams(t *testing.T) {
	for _, name := range OAuthProviderNames() {
		t.Run(name, func(t *testing.T) {
			cfg, ok := OAuthProviderConfigByName(name)
			if !ok {
				t.Fatalf("OAuthProviderConfigByName(%q) ok = false, want true", name)
			}
			if len(cfg.ExtraAuthParams) == 0 {
				t.Fatalf("preset %q has no ExtraAuthParams; this leak test would be vacuous", name)
			}

			want := make(map[string]string, len(cfg.ExtraAuthParams))
			for key, value := range cfg.ExtraAuthParams {
				want[key] = value
			}

			// Corrupt the returned copy on purpose: add an unknown key and wipe
			// the declared ones.
			cfg.ExtraAuthParams["pwned"] = "1"
			for key := range want {
				delete(cfg.ExtraAuthParams, key)
			}

			again, ok := OAuthProviderConfigByName(name)
			if !ok {
				t.Fatalf("second OAuthProviderConfigByName(%q) ok = false, want true", name)
			}
			if leaked, found := again.ExtraAuthParams["pwned"]; found {
				t.Errorf("ExtraAuthParams leaked into the shared preset %q: a second lookup returns pwned=%q; the accessor must clone the map per call", name, leaked)
			}
			if !reflect.DeepEqual(again.ExtraAuthParams, want) {
				t.Errorf("stored ExtraAuthParams of %q changed: got %v, want %v; the accessor must clone the map per call", name, again.ExtraAuthParams, want)
			}

			// The alias path must observe the same (intact) preset.
			alias := name
			switch name {
			case "anthropic":
				alias = "claude"
			case "openai":
				alias = "codex"
			case "google-antigravity":
				alias = "antigravity"
			}
			if alias != name {
				aliased, ok := OAuthProviderConfigByName(alias)
				if !ok {
					t.Fatalf("OAuthProviderConfigByName(%q) ok = false, want true", alias)
				}
				if !reflect.DeepEqual(aliased.ExtraAuthParams, want) {
					t.Errorf("alias %q sees ExtraAuthParams %v, want %v", alias, aliased.ExtraAuthParams, want)
				}
			}
		})
	}

	// The constructors themselves are stateless: a value obtained directly from
	// OpenAIOAuthConfig must carry the declared params, not the caller's edits.
	if got := OpenAIOAuthConfig().ExtraAuthParams; !reflect.DeepEqual(got, map[string]string{
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
	}) {
		t.Errorf("OpenAIOAuthConfig().ExtraAuthParams = %v, want the declared map", got)
	}
}

// TestOAuthProviderConfigByNameMatchesConstructor makes sure the registry hands
// out the real presets: the key fields must match the constructor's output
// byte for byte, so adding a provider to the registry cannot silently downgrade
// its configuration.
func TestOAuthProviderConfigByNameMatchesConstructor(t *testing.T) {
	presets := map[string]OAuthProviderConfig{
		"anthropic":          ClaudeOAuthConfig(),
		"openai":             OpenAIOAuthConfig(),
		"google-antigravity": GoogleAntigravityOAuthConfig(),
	}

	for name, want := range presets {
		t.Run(name, func(t *testing.T) {
			cfg, ok := OAuthProviderConfigByName(name)
			if !ok {
				t.Fatalf("OAuthProviderConfigByName(%q) ok = false, want true", name)
			}

			if !reflect.DeepEqual(cfg, want) {
				t.Errorf("OAuthProviderConfigByName(%q) = %+v, want the constructor output %+v", name, cfg, want)
			}

			// Key fields spelled out, so a failure is readable.
			if cfg.Name != want.Name {
				t.Errorf("Name = %q, want %q", cfg.Name, want.Name)
			}
			if cfg.Port != want.Port {
				t.Errorf("Port = %d, want %d", cfg.Port, want.Port)
			}
			if cfg.AuthorizeURL != want.AuthorizeURL {
				t.Errorf("AuthorizeURL = %q, want %q", cfg.AuthorizeURL, want.AuthorizeURL)
			}
			if cfg.Scopes != want.Scopes {
				t.Errorf("Scopes = %q, want %q", cfg.Scopes, want.Scopes)
			}
			if cfg.ClientID != want.ClientID {
				t.Errorf("ClientID = %q, want %q", cfg.ClientID, want.ClientID)
			}
		})
	}
}

// TestOAuthProviderPresetNameMatchesRegistryKey documents an invariant the CLI
// relies on: the registry key is the canonical provider name, and each preset
// advertises it in Name (the CLI stores credentials under that name). It also
// makes reflect.DeepEqual against the constructor meaningful.
func TestOAuthProviderPresetNameMatchesRegistryKey(t *testing.T) {
	for _, name := range OAuthProviderNames() {
		cfg, ok := OAuthProviderConfigByName(name)
		if !ok {
			t.Fatalf("OAuthProviderConfigByName(%q) ok = false, want true", name)
		}
		if cfg.Name != name {
			t.Errorf("registry key %q resolves to a preset with Name = %q; the key must be the canonical name", name, cfg.Name)
		}
	}
}
