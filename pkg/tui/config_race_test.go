package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

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

func TestSaveConfigToDisk_PublishesPrivateSnapshotToLoop(t *testing.T) {
	m := newTestModel(t)

	if err := m.addProvider("alphaprov", "openai", "sk-key-one", "https://api.example/v1"); err != nil {
		t.Fatalf("addProvider: %v", err)
	}

	loopCfg := loopConfig(t, m)
	if p, ok := loopCfg.Providers.GetNamed("alphaprov"); !ok || p.APIKey != "sk-key-one" {
		t.Fatalf("loop snapshot missing saved provider (ok=%v)", ok)
	}
	if loopCfg == m.cfg {
		t.Fatal("loop config pointer is the same as m.cfg after saveConfigToDisk (shared-pointer race)")
	}

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

func TestSaveConfigToDisk_FailureDoesNotPublish(t *testing.T) {
	m := newTestModel(t)

	if err := m.addProvider("goodprov", "openai", "sk-good", ""); err != nil {
		t.Fatalf("addProvider: %v", err)
	}
	before := loopConfig(t, m)

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

func TestApplyAgentReload_PassesSnapshot(t *testing.T) {
	m := newTestModel(t)

	m.cfg.Agents.List = []config.AgentConfig{{ID: "solo", Name: "Solo"}}
	m.applyAgentReload()

	loopCfg := loopConfig(t, m)
	if loopCfg == m.cfg {
		t.Fatal("applyAgentReload shared m.cfg pointer with the loop")
	}

	m.cfg.Agents.List[0].Name = "Renamed"
	loopCfg = loopConfig(t, m)
	if len(loopCfg.Agents.List) != 1 || loopCfg.Agents.List[0].Name != "Solo" {
		t.Errorf("loop agents list mutated through shared slice: %+v", loopCfg.Agents.List)
	}
}
