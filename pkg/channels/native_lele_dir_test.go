package channels

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestNewNativeChannel_ResolvesLeleDirFromEnv(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", envDir)

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Port = 0
	if cfg.Channels.Native.LeleDir != "" {
		t.Fatalf("test premise broken: default lele_dir = %q, want empty",
			cfg.Channels.Native.LeleDir)
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	native, err := NewNativeChannel(cfg, msgBus, newNativeTestAgentLoop(cfg), NewApprovalManager())
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	if got := native.auth.storePath; got != envDir+"/native_clients.json" {
		t.Errorf("auth storePath = %q, want %q: the auth store split from the "+
			"SQLite store, which is exactly #328", got, envDir+"/native_clients.json")
	}

	if got := native.cfg.LeleDir; got != envDir {
		t.Errorf("native.cfg.LeleDir = %q, want %q", got, envDir)
	}
	if got := native.leleDir; got != envDir {
		t.Errorf("native.leleDir = %q, want %q", got, envDir)
	}
}

func TestNewNativeChannel_DoesNotMutateSharedConfig(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", envDir)

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Port = 0

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	if _, err := NewNativeChannel(cfg, msgBus, newNativeTestAgentLoop(cfg), NewApprovalManager()); err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	if got := cfg.Channels.Native.LeleDir; got != "" {
		t.Errorf("shared config.Channels.Native.LeleDir = %q after building the "+
			"channel, want %q: SaveConfig would persist this and pin the next "+
			"run's data directory to %s", got, "", envDir)
	}
}

func TestNewNativeChannel_ExplicitLeleDirBeatsEnv(t *testing.T) {
	explicit := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.LeleDir = explicit

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	native, err := NewNativeChannel(cfg, msgBus, newNativeTestAgentLoop(cfg), NewApprovalManager())
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	if got := native.cfg.LeleDir; got != explicit {
		t.Errorf("native.cfg.LeleDir = %q, want %q", got, explicit)
	}
	if got := native.auth.storePath; got != explicit+"/native_clients.json" {
		t.Errorf("auth storePath = %q, want %q", got, explicit+"/native_clients.json")
	}
}
