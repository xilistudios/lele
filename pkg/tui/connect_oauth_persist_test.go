package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func connectOAuthSeedModels() map[string]config.ProviderModelConfig {
	return map[string]config.ProviderModelConfig{
		"gemini-2.5-pro": {Model: "gemini-2.5-pro", ContextWindow: 1048576, MaxTokens: 8192},
	}
}

const connectOAuthSeedProxy = "http://proxy.test:8080"

func connectOAuthSeedProvider(t *testing.T, m *Model, key string) string {
	t.Helper()
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("test model has no providers config")
	}
	named := config.NamedProviderConfig{
		Type:           key,
		ProviderConfig: config.ProviderConfig{APIKey: "sk-stale-" + key, Proxy: connectOAuthSeedProxy},
		Models:         connectOAuthSeedModels(),
	}
	if m.cfg.Providers.Named == nil {
		m.cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	m.cfg.Providers.Named[key] = named

	typed := config.ProviderConfig{APIKey: named.APIKey, Proxy: connectOAuthSeedProxy}
	switch key {
	case "anthropic":
		m.cfg.Providers.Anthropic = typed
	case "gemini":
		m.cfg.Providers.Gemini = typed
	case "openai":
		m.cfg.Providers.OpenAI.ProviderConfig = typed
	default:
		t.Fatalf("no typed copy to seed for provider %q", key)
	}
	return named.Type
}

func connectOAuthComplete(t *testing.T, key string) (*Model, *fakeConnectOAuthFlow, string) {
	t.Helper()
	m, flow := connectOAuthEnterPending(t, key)
	configType := connectOAuthSeedProvider(t, m, key)
	flow.status = connectOAuthStatusDone
	flow.flowDone = true
	m.Update(connectOAuthPollMsg{seq: m.connectOAuthSeq})
	return m, flow, configType
}

func connectOAuthRequireSuccess(t *testing.T, m *Model, key string) {
	t.Helper()
	if m.connectOAuthState != connectOAuthIdle || m.modalMode != ModalAddProvider {
		t.Fatalf("OAuth login for %q did not reach the wizard: state=%d mode=%v err=%q",
			key, m.connectOAuthState, m.modalMode, m.connectOAuthError)
	}
	if cred := authGetCredential(t, key); cred == nil || cred.AccessToken != "at-secret" {
		t.Fatalf("credential for %q not persisted: %+v", key, cred)
	}
}

func assertConnectOAuthNamed(t *testing.T, m *Model, key, configType, wantAPIKey string) {
	t.Helper()
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("providers config disappeared")
	}
	entry, ok := m.cfg.Providers.Named[key]
	if !ok {
		t.Fatalf("named provider %q was not persisted", key)
	}
	if entry.Type != configType {
		t.Errorf("Named[%q].Type = %q, want %q", key, entry.Type, configType)
	}
	if entry.AuthMethod != "oauth" {
		t.Errorf("Named[%q].AuthMethod = %q, want oauth", key, entry.AuthMethod)
	}
	if entry.APIKey != wantAPIKey {
		t.Errorf("Named[%q].APIKey = %q, want %q", key, entry.APIKey, wantAPIKey)
	}
	if len(entry.Models) != 1 {
		t.Fatalf("Named[%q].Models = %v, want the pre-existing model map (data loss)", key, entry.Models)
	}
	if mc, ok := entry.Models["gemini-2.5-pro"]; !ok || mc.Model != "gemini-2.5-pro" {
		t.Errorf("Named[%q].Models[\"gemini-2.5-pro\"] = %+v, want it preserved", key, mc)
	}
	if entry.Proxy != connectOAuthSeedProxy {
		t.Errorf("Named[%q].Proxy = %q, want %q (data loss)", key, entry.Proxy, connectOAuthSeedProxy)
	}
}

func assertConnectOAuthTyped(t *testing.T, m *Model, key, wantAPIKey, wantProxy string) {
	t.Helper()
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("providers config disappeared")
	}
	var got config.ProviderConfig
	switch key {
	case "anthropic":
		got = m.cfg.Providers.Anthropic
	case "gemini":
		got = m.cfg.Providers.Gemini
	case "openai":
		got = m.cfg.Providers.OpenAI.ProviderConfig
	default:
		t.Fatalf("no typed copy for provider %q", key)
	}
	if got.AuthMethod != "oauth" {
		t.Errorf("typed %s.AuthMethod = %q, want oauth (transport selection reads this copy)", key, got.AuthMethod)
	}
	if got.APIKey != wantAPIKey {
		t.Errorf("typed %s.APIKey = %q, want %q (the two copies must agree)", key, got.APIKey, wantAPIKey)
	}
	if got.Proxy != wantProxy {
		t.Errorf("typed %s.Proxy = %q, want %q (data loss)", key, got.Proxy, wantProxy)
	}
}

func assertConnectOAuthOnDisk(t *testing.T, key, configType, wantAPIKey string) {
	t.Helper()
	path := filepath.Join(os.Getenv("LELE_CONFIG_DIR"), "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(raw), `"providers": null`) {
		t.Fatalf("config.json carries a null providers section (every provider would be dropped on load):\n%s", raw)
	}
	reloaded, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", path, err)
	}
	if reloaded.Providers == nil {
		t.Fatalf("reloaded providers = nil, want a section (file: %s)", raw)
	}
	m := &Model{cfg: reloaded}
	assertConnectOAuthNamed(t, m, key, configType, wantAPIKey)
	assertConnectOAuthTyped(t, m, key, wantAPIKey, connectOAuthSeedProxy)
}

func TestConnectOAuthPersist_GeminiClearsAPIKeyInBothCopies(t *testing.T) {
	m, _, configType := connectOAuthComplete(t, "gemini")
	connectOAuthRequireSuccess(t, m, "gemini")

	assertConnectOAuthNamed(t, m, "gemini", configType, "")
	assertConnectOAuthTyped(t, m, "gemini", "", connectOAuthSeedProxy)
	if m.cfg.Providers.Named["gemini"].APIKey != m.cfg.Providers.Gemini.APIKey {
		t.Errorf("copies disagree: Named=%q typed=%q",
			m.cfg.Providers.Named["gemini"].APIKey, m.cfg.Providers.Gemini.APIKey)
	}
}

func TestConnectOAuthPersist_AnthropicKeepsAPIKey(t *testing.T) {
	m, _, configType := connectOAuthComplete(t, "anthropic")
	connectOAuthRequireSuccess(t, m, "anthropic")

	assertConnectOAuthNamed(t, m, "anthropic", configType, "sk-stale-anthropic")
	assertConnectOAuthTyped(t, m, "anthropic", "sk-stale-anthropic", connectOAuthSeedProxy)
}

func TestConnectOAuthPersist_OpenAIKeepsAPIKey(t *testing.T) {
	m, _, configType := connectOAuthComplete(t, "openai")
	connectOAuthRequireSuccess(t, m, "openai")

	assertConnectOAuthNamed(t, m, "openai", configType, "sk-stale-openai")
	assertConnectOAuthTyped(t, m, "openai", "sk-stale-openai", connectOAuthSeedProxy)
}

func TestConnectOAuthPersist_RoundTripToDisk(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		wantAPIKey string
	}{
		{name: "gemini key cleared", key: "gemini", wantAPIKey: ""},
		{name: "anthropic key kept", key: "anthropic", wantAPIKey: "sk-stale-anthropic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _, configType := connectOAuthComplete(t, tc.key)
			connectOAuthRequireSuccess(t, m, tc.key)
			assertConnectOAuthOnDisk(t, tc.key, configType, tc.wantAPIKey)
		})
	}
}

func TestConnectOAuthPersist_FreshOpenAIEntryInventsNothing(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "openai")
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("test model has no providers config")
	}
	if prev, ok := m.cfg.Providers.Named["openai"]; ok && !prev.IsEmpty() {
		t.Fatalf("precondition: openai already configured: %+v", prev)
	}
	delete(m.cfg.Providers.Named, "openai")

	flow := m.connectOAuthFlow.(*fakeConnectOAuthFlow)
	flow.status = connectOAuthStatusDone
	flow.flowDone = true
	m.Update(connectOAuthPollMsg{seq: m.connectOAuthSeq})
	connectOAuthRequireSuccess(t, m, "openai")

	entry, ok := m.cfg.Providers.Named["openai"]
	if !ok {
		t.Fatal("the fresh openai entry was not persisted")
	}
	if entry.Type != "openai" {
		t.Errorf("Type = %q, want openai", entry.Type)
	}
	if entry.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want oauth", entry.AuthMethod)
	}
	if entry.APIKey != "" {
		t.Errorf("APIKey = %q, want empty for a fresh OAuth entry", entry.APIKey)
	}
	if len(entry.Models) != 0 {
		t.Errorf("Models = %v, want no invented models", entry.Models)
	}
	assertConnectOAuthTyped(t, m, "openai", "", "")
}

func TestConnectOAuthCatalogRequiresEmptyAPIKeyOnlyForGemini(t *testing.T) {
	for typ, meta := range connectOAuthCatalog {
		want := typ == "gemini"
		if meta.requiresEmptyAPIKey != want {
			t.Errorf("catalog[%q].requiresEmptyAPIKey = %v, want %v (only gemini needs a keyless entry)",
				typ, meta.requiresEmptyAPIKey, want)
		}
	}
	if len(connectOAuthCatalog) != 3 {
		t.Errorf("connectOAuthCatalog has %d entries, want 3 (openai, anthropic, gemini)", len(connectOAuthCatalog))
	}
}
