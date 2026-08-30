package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// loopConfig returns the config pointer the agent loop currently reads from
// (the same value its goroutines see via cfgPtr.Load()).
func loopConfig(t *testing.T, m *Model) *config.Config {
	t.Helper()
	if m.agentLoop == nil {
		t.Fatal("agent loop is nil")
	}
	cfg := m.agentLoop.GetProvidable().GetConfigSnapshot()
	if cfg == nil {
		t.Fatal("loop config snapshot is nil")
	}
	return cfg
}

// TestSaveConfigToDisk_PublishesPrivateSnapshotToLoop verifies the C1 fix
// end to end: a successful save persists to the (temp-dir) config file AND
// publishes a private deep copy to the agent loop, so later mutations of
// m.cfg.Providers.Named by the TUI are invisible to the loop.
func TestSaveConfigToDisk_PublishesPrivateSnapshotToLoop(t *testing.T) {
	m := newTestModel(t) // LELE_CONFIG_DIR points at a temp dir — no real config touched

	if err := m.addProvider("alphaprov", "openai", "sk-key-one", "https://api.example/v1"); err != nil {
		t.Fatalf("addProvider: %v", err)
	}

	// The provider must be visible through the loop's snapshot...
	loopCfg := loopConfig(t, m)
	if p, ok := loopCfg.Providers.GetNamed("alphaprov"); !ok || p.APIKey != "sk-key-one" {
		t.Fatalf("loop snapshot missing saved provider (ok=%v)", ok)
	}
	// ...and the loop must NOT hold the pointer the TUI mutates.
	if loopCfg == m.cfg {
		t.Fatal("loop config pointer is the same as m.cfg after saveConfigToDisk (shared-pointer race)")
	}

	// Mutate the TUI's copy without saving: the loop's snapshot must keep the
	// old map contents.
	m.cfg.Providers.Named["betaprov"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "sk-mutation-only"},
	}
	alphaprov, _ := m.cfg.Providers.GetNamed("alphaprov")
	alphaprov.APIKey = "sk-changed-in-place"
	m.cfg.Providers.Named["alphaprov"] = alphaprov

	loopCfg = loopConfig(t, m)
	if _, exists := loopCfg.Providers.Named["betaprov"]; exists {
		t.Error("loop snapshot sees unsaved TUI mutation (map added after publish)")
	}
	if p, ok := loopCfg.Providers.GetNamed("alphaprov"); !ok || p.APIKey != "sk-key-one" {
		t.Errorf("loop snapshot provider mutated: ok=%v", ok)
	}
}

// TestSaveConfigToDisk_FailureDoesNotPublish verifies that a failed save does
// not publish a new snapshot: the loop keeps the last known-good config.
func TestSaveConfigToDisk_FailureDoesNotPublish(t *testing.T) {
	m := newTestModel(t)

	if err := m.addProvider("goodprov", "openai", "sk-good", ""); err != nil {
		t.Fatalf("addProvider: %v", err)
	}
	before := loopConfig(t, m)

	// Break the config path: point LELE_CONFIG_DIR at a regular file so
	// SaveConfig's MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	t.Setenv("LELE_CONFIG_DIR", blocker)

	m.cfg.Providers.Named["neverpublished"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "sk-bad"},
	}
	if err := m.saveConfigToDisk(); err == nil {
		t.Fatal("expected saveConfigToDisk to fail with LELE_CONFIG_DIR pointing at a file")
	}

	after := loopConfig(t, m)
	if after != before {
		t.Error("loop config pointer changed after a failed save (must keep last known-good)")
	}
	if _, exists := after.Providers.Named["neverpublished"]; exists {
		t.Error("failed save must not publish mutated contents to the loop")
	}
}

// TestUpdateConfigSnapshot_LoopIsolatedFromTUIMutations exercises the
// publishing mechanism directly (per fix plan T1): after
// UpdateConfigSnapshot(m.cfg.Snapshot()), mutating m.cfg.Providers.Named
// leaves the loop's snapshot with the OLD contents and the two pointers
// differ.
func TestUpdateConfigSnapshot_LoopIsolatedFromTUIMutations(t *testing.T) {
	m := newTestModel(t)

	m.cfg.Providers.Named["snapme"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "sk-before"},
	}
	m.agentLoop.UpdateConfigSnapshot(m.cfg.Snapshot())

	loopCfg := loopConfig(t, m)
	if loopCfg == m.cfg {
		t.Fatal("loop holds the same pointer the TUI mutates")
	}
	if p, ok := loopCfg.Providers.GetNamed("snapme"); !ok || p.APIKey != "sk-before" {
		t.Fatalf("snapshot missing pre-publish provider (ok=%v)", ok)
	}

	// Mutate the TUI's live map after publishing.
	m.cfg.Providers.Named["snapme"] = config.NamedProviderConfig{
		Type:           "openai",
		ProviderConfig: config.ProviderConfig{APIKey: "sk-after"},
	}
	delete(m.cfg.Providers.Named, "ollama")

	loopCfg = loopConfig(t, m)
	if p := loopCfg.Providers.Named["snapme"]; p.APIKey != "sk-before" {
		t.Errorf("loop snapshot mutated through shared map: APIKey=%q, want %q", p.APIKey, "sk-before")
	}
	if _, ok := loopCfg.Providers.Named["ollama"]; !ok {
		t.Error("delete on m.cfg affected the loop snapshot (not a deep copy)")
	}
}

// TestApplyAgentReload_PassesSnapshot verifies settings_agents.go's reload
// path hands the loop a private copy, not the TUI's mutable pointer.
func TestApplyAgentReload_PassesSnapshot(t *testing.T) {
	m := newTestModel(t)

	m.cfg.Agents.List = []config.AgentConfig{{ID: "solo", Name: "Solo"}}
	m.applyAgentReload() // ReloadRegistry(m.cfg.Snapshot())

	loopCfg := loopConfig(t, m)
	if loopCfg == m.cfg {
		t.Fatal("applyAgentReload shared m.cfg pointer with the loop")
	}

	// Mutate the agents list in the TUI copy; loop snapshot must be unchanged.
	m.cfg.Agents.List[0].Name = "Renamed"
	loopCfg = loopConfig(t, m)
	if len(loopCfg.Agents.List) != 1 || loopCfg.Agents.List[0].Name != "Solo" {
		t.Errorf("loop agents list mutated through shared slice: %+v", loopCfg.Agents.List)
	}
}
