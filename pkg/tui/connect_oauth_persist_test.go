package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// Persistence suite for the native /connect OAuth login (the success
// transition of ModalConnectOAuth: connectOAuthFinishDone →
// connectOAuthPersistProvider). It reuses the fake engine seam of the
// connect_oauth_test.go helpers (startConnectOAuthFlow), so no browser,
// listener, network or credential store is involved.
//
// The invariants pinned here are about the TWO config copies of the same
// provider — Providers.Named[key] (the copy MarshalJSON persists) and the
// typed field Providers.<Name> (the copy load paths and factory.go's first
// pre-check still read) — plus the file they are written to:
//
//  1. requiresEmptyAPIKey (today: gemini) must clear the API key in BOTH
//     copies, otherwise they disagree and the typed pre-check in
//     pkg/providers/factory.go fails on the stale key.
//  2. openai (Codex) and anthropic (Claude subscription) select their
//     transport from auth_method ALONE, so their developer API key MUST
//     survive in both copies (clearing it is data loss).
//  3. Pre-existing models/proxy are preserved in both copies.
//  4. The JSON on disk is coherent: no "providers": null, both copies in the
//     same state.

// ── pre-seed helpers ──────────────────────────────────────────────────

// connectOAuthSeedModels is the pre-existing model map of a provider the
// user configured BEFORE the OAuth login (the "already usable" shape).
// Losing it on login is the data-loss bug the assertions pin. Models live
// ONLY on the Named copy — config.ProviderConfig (the typed copy) has no
// Models field, so the mirror carries key/proxy only.
func connectOAuthSeedModels() map[string]config.ProviderModelConfig {
	return map[string]config.ProviderModelConfig{
		"gemini-2.5-pro": {Model: "gemini-2.5-pro", ContextWindow: 1048576, MaxTokens: 8192},
	}
}

// connectOAuthSeedProxy is the pre-existing proxy of the seeded provider.
const connectOAuthSeedProxy = "http://proxy.test:8080"

// connectOAuthSeedProvider writes BOTH copies of an existing API-key
// provider (Named + typed), i.e. the state a developer reaches by running
// /connect with an API key first and then re-logging in with OAuth.
//
// The typed copy is only seeded for the providers that HAVE one
// (anthropic/gemini: config.ProviderConfig; openai: config.OpenAIProviderConfig
// embedding it); google-antigravity has none. Returns the config type used.
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

// connectOAuthComplete drives a picked OAuth row to a COMPLETED flow through
// the real Update path: the poll notices status "done" and runs the one-shot
// success transition (credential store + connectOAuthPersistProvider + wizard
// hand-off). It returns the model, the fake flow and the config type of the
// seeded provider.
func connectOAuthComplete(t *testing.T, key string) (*Model, *fakeConnectOAuthFlow, string) {
	t.Helper()
	m, flow := connectOAuthEnterPending(t, key)
	configType := connectOAuthSeedProvider(t, m, key)
	flow.status = connectOAuthStatusDone
	flow.flowDone = true
	m.Update(connectOAuthPollMsg{seq: m.connectOAuthSeq})
	return m, flow, configType
}

// connectOAuthRequireSuccess is the shared precondition of every assertion
// below: without a completed transition nothing has been persisted, so a
// green test here would say nothing about the code under test.
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

// assertConnectOAuthNamed pins the persisted copy of an existing provider:
// type, auth method, API key (cleared or kept per the transport rule) and
// the pre-existing models/proxy (losing them is data loss).
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

// assertConnectOAuthTyped pins the typed mirror of an existing provider: it
// must carry the same auth method AND the same API key decision as the named
// copy (the two copies are one provider and must not disagree). wantProxy is
// the seeded proxy, "" for a provider that had no typed copy seeded.
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
	// Models exist only on the Named copy; the typed one must at least keep
	// the proxy (its only other seeded field).
	if got.Proxy != wantProxy {
		t.Errorf("typed %s.Proxy = %q, want %q (data loss)", key, got.Proxy, wantProxy)
	}
}

// assertConnectOAuthOnDisk reloads config.json from LELE_CONFIG_DIR and pins
// that BOTH copies come back in the same state as in memory. It also checks
// the raw file for the known "providers": null regression of
// ProvidersConfig.MarshalJSON (a null section makes LoadConfig nil the whole
// Providers config, silently dropping every provider on the next load).
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
	// Both helpers assert on the RELOADED config, so the in-memory state and
	// the serialized state cannot drift apart unnoticed.
	m := &Model{cfg: reloaded}
	assertConnectOAuthNamed(t, m, key, configType, wantAPIKey)
	assertConnectOAuthTyped(t, m, key, wantAPIKey, connectOAuthSeedProxy)
}

// ── 1. gemini: requiresEmptyAPIKey clears BOTH copies ─────────────────

// TestConnectOAuthPersist_GeminiClearsAPIKeyInBothCopies pins the
// requiresEmptyAPIKey contract for gemini: the OAuth login must leave
// Providers.Named["gemini"] AND Providers.Gemini keyless (a stale key in the
// typed field would fail factory.go's first-evaluated pre-check
// `Gemini.AuthMethod == "oauth" && Gemini.APIKey == ""` and push the
// transport onto the Named-entry path), while auth_method becomes "oauth"
// and the pre-existing models/proxy survive in both copies.
func TestConnectOAuthPersist_GeminiClearsAPIKeyInBothCopies(t *testing.T) {
	m, _, configType := connectOAuthComplete(t, "gemini")
	connectOAuthRequireSuccess(t, m, "gemini")

	assertConnectOAuthNamed(t, m, "gemini", configType, "")
	assertConnectOAuthTyped(t, m, "gemini", "", connectOAuthSeedProxy)
	// The invariant itself, spelled out: the two copies of one provider must
	// not disagree about the key.
	if m.cfg.Providers.Named["gemini"].APIKey != m.cfg.Providers.Gemini.APIKey {
		t.Errorf("copies disagree: Named=%q typed=%q",
			m.cfg.Providers.Named["gemini"].APIKey, m.cfg.Providers.Gemini.APIKey)
	}
}

// ── 2/3. anthropic + openai: auth_method alone, the key is kept ───────

// TestConnectOAuthPersist_AnthropicKeepsAPIKey is the data-loss guard for
// the Claude subscription transport: factory.go selects
// providerTypeClaudeAuth from auth_method ALONE, so the developer API key in
// BOTH copies must survive an OAuth login (models/proxy too).
func TestConnectOAuthPersist_AnthropicKeepsAPIKey(t *testing.T) {
	m, _, configType := connectOAuthComplete(t, "anthropic")
	connectOAuthRequireSuccess(t, m, "anthropic")

	assertConnectOAuthNamed(t, m, "anthropic", configType, "sk-stale-anthropic")
	assertConnectOAuthTyped(t, m, "anthropic", "sk-stale-anthropic", connectOAuthSeedProxy)
}

// TestConnectOAuthPersist_OpenAIKeepsAPIKey is the same data-loss guard for
// the Codex transport (auth_method alone at factory.go:208/:472).
func TestConnectOAuthPersist_OpenAIKeepsAPIKey(t *testing.T) {
	m, _, configType := connectOAuthComplete(t, "openai")
	connectOAuthRequireSuccess(t, m, "openai")

	assertConnectOAuthNamed(t, m, "openai", configType, "sk-stale-openai")
	assertConnectOAuthTyped(t, m, "openai", "sk-stale-openai", connectOAuthSeedProxy)
}

// ── 4. round-trip to disk ─────────────────────────────────────────────

// TestConnectOAuthPersist_RoundTripToDisk proves the JSON written by
// connectOAuthPersistProvider reloads into BOTH copies in the same state
// (key cleared for gemini, kept for anthropic; auth_method oauth; models and
// proxy intact) and that the providers section is never serialized as null.
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

// ── 5. fresh entry: no invented data ──────────────────────────────────

// TestConnectOAuthPersist_FreshOpenAIEntryInventsNothing pins the
// anti-oracle behaviour for a provider the user had never configured: the
// entry must be created from the catalog metadata alone (type + auth
// method), with an empty API key and ZERO invented models — a preset model
// leaking in here would make an unconfigured provider look usable.
func TestConnectOAuthPersist_FreshOpenAIEntryInventsNothing(t *testing.T) {
	m, _ := connectOAuthEnterPending(t, "openai")
	if m.cfg == nil || m.cfg.Providers == nil {
		t.Fatal("test model has no providers config")
	}
	// Precondition: no openai entry at all (newTestModel seeds a pristine
	// config, so the placeholder may exist but must carry no user data).
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
	// The typed mirror must exist for the fresh entry too (openai is one of
	// the three providers with a typed copy).
	// No typed copy was seeded (fresh entry), so the mirror must exist but
	// carry no invented proxy/key beyond the auth method.
	assertConnectOAuthTyped(t, m, "openai", "", "")
}

// ── 6. catalog rule: the gemini-only API-key requirement ──────────────

// TestConnectOAuthCatalogRequiresEmptyAPIKeyOnlyForGemini pins the documented
// transport rule behind the whole file: only gemini needs a keyless entry,
// openai/anthropic must keep the developer key. Flipping the flag here would
// silently turn a data-loss bug into the default behaviour.
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
