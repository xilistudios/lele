package main

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

// authStoreForTest loads the auth store through the public auth package API,
// which maps to whichever backend is currently active (legacy JSON when
// authUseStore(nil) is called in a temp LELE_CONFIG_DIR).
func authStoreForTest() (*auth.AuthStore, error) {
	return auth.LoadStore()
}

// testConfigWithProviders returns a default test config that also has a
// non-empty providers section. config.LoadConfig disables the Providers
// struct when the saved JSON has no "providers" key, so tests that reload the
// config (e.g. authLoginPasteToken) need providers present to avoid a nil
// dereference.
func testConfigWithProviders(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = make(map[string]config.NamedProviderConfig)
	}
	// Add a named provider with a model so the custom ProvidersConfig
	// MarshalJSON emits a non-empty object ("has APIKey or models"). The
	// top-level struct fields are omitted when empty, so the providers key
	// would otherwise serialize to null and reload to a nil pointer.
	cfg.Providers.Named["testprov"] = config.NamedProviderConfig{
		Type: "openai",
		ProviderConfig: config.ProviderConfig{
			APIBase: "https://api.openai.com/v1",
			APIKey:  "test-key",
		},
		Models: map[string]config.ProviderModelConfig{
			"default": {Model: "gpt-4o"},
		},
	}
	return cfg
}

// TestAuthLoginPasteToken_Success exercises the success path of
// authLoginPasteToken: the token is read from stdin, the credential is stored,
// the config auth method is updated, and a confirmation is printed.
func TestAuthLoginPasteToken_Success(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg := testConfigWithProviders(t)
	// Provide a valid config so loadConfig succeeds in authLoginPasteToken.
	saveConfigAt(t, dir, cfg)
	authUseStore(nil) // reset to legacy JSON backend isolated in LELE_CONFIG_DIR

	p := newStdinPipe(t)
	p.feed("sk-test-token-12345\n")
	p.close()

	out := runCmd(func() { authLoginPasteToken("openai") })
	if !strings.Contains(out, "Token saved for openai") {
		t.Errorf("expected token saved message, got: %s", out)
	}

	// The credential should now be persisted in the legacy JSON store inside
	// the temp config dir.
	st, err := authStoreForTest()
	if err != nil {
		t.Fatalf("authStoreForTest: %v", err)
	}
	cred, ok := st.Credentials["openai"]
	if !ok {
		t.Fatalf("expected openai credential to be stored")
	}
	if cred.AccessToken != "sk-test-token-12345" {
		t.Errorf("access token = %q, want sk-test-token-12345", cred.AccessToken)
	}
	if cred.AuthMethod != "token" {
		t.Errorf("auth method = %q, want token", cred.AuthMethod)
	}
}

// TestAuthLoginPasteToken_Anthropic checks the anthropic branch updates the
// right config provider field.
func TestAuthLoginPasteToken_Anthropic(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg := testConfigWithProviders(t)
	saveConfigAt(t, dir, cfg)
	authUseStore(nil)

	p := newStdinPipe(t)
	p.feed("anthropic-token\n")
	p.close()

	out := runCmd(func() { authLoginPasteToken("anthropic") })
	if !strings.Contains(out, "Token saved for anthropic") {
		t.Errorf("expected token saved message, got: %s", out)
	}

	// The credential should be stored with the anthropic provider.
	st, err := authStoreForTest()
	if err != nil {
		t.Fatalf("authStoreForTest: %v", err)
	}
	cred, ok := st.Credentials["anthropic"]
	if !ok {
		t.Fatalf("expected anthropic credential to be stored")
	}
	if cred.AccessToken != "anthropic-token" {
		t.Errorf("access token = %q, want anthropic-token", cred.AccessToken)
	}
}
