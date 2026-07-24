package agent

import (
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestGroupManager_Initialized(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "group-wiring-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())

	if al.GroupManager() == nil {
		t.Fatal("GroupManager should be initialized after NewAgentLoop")
	}
}

func TestGroupManager_SetStoreDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "group-wiring-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	gm := al.GroupManager()
	if gm == nil {
		t.Fatal("GroupManager should not be nil")
	}

	// SetStoreDir should not panic
	gm.SetStoreDir(tmpDir)
}
