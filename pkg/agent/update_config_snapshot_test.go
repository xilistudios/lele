package agent

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newConfigSwapLoop(t *testing.T, cfg *config.Config) *AgentLoop {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "cfg-swap-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	cfg.Agents.Defaults.Workspace = tmpDir
	return NewAgentLoop(cfg, bus.NewMessageBus())
}

func testCfgWithModel(model string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Model: model},
		},
	}
}

func TestUpdateConfigSnapshot_SwapsConfigPointer(t *testing.T) {
	al := newConfigSwapLoop(t, testCfgWithModel("model-A"))

	if got := al.cfg().Agents.Defaults.Model; got != "model-A" {
		t.Fatalf("initial model = %q, want %q", got, "model-A")
	}
	if got := al.GetProvidable().GetConfigSnapshot().Agents.Defaults.Model; got != "model-A" {
		t.Fatalf("GetConfigSnapshot initial model = %q, want %q", got, "model-A")
	}

	cfgB := testCfgWithModel("model-B")
	al.UpdateConfigSnapshot(cfgB)

	if got := al.cfg().Agents.Defaults.Model; got != "model-B" {
		t.Errorf("after swap: al.cfg() model = %q, want %q", got, "model-B")
	}
	if got := al.GetProvidable().GetConfigSnapshot().Agents.Defaults.Model; got != "model-B" {
		t.Errorf("after swap: GetConfigSnapshot model = %q, want %q", got, "model-B")
	}
	if al.cfg() != cfgB {
		t.Error("al.cfg() should return the exact pointer stored by UpdateConfigSnapshot")
	}
}

func TestUpdateConfigSnapshot_NilIsNoOp(t *testing.T) {
	al := newConfigSwapLoop(t, testCfgWithModel("model-A"))

	al.UpdateConfigSnapshot(nil)

	if got := al.cfg().Agents.Defaults.Model; got != "model-A" {
		t.Errorf("after nil swap: model = %q, want %q (nil must be ignored)", got, "model-A")
	}
}
