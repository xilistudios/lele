package channels

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// Issue #328: with LELE_CONFIG_DIR set, the gateway opened its SQLite store at
// $LELE_CONFIG_DIR/lele.db but read native_clients.json from $HOME/.lele,
// because the default of channels.native.lele_dir was computed from $HOME.
// NewNativeChannel now resolves the field, so one process keeps a single data
// directory.
func TestNewNativeChannel_ResolvesLeleDirFromEnv(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", envDir)

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Port = 0
	// Left at the default on purpose: this is the case the issue is about.
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

	// The auth manager's store path and the resolved dir must agree — that
	// divergence was the bug.
	if got := native.auth.storePath; got != envDir+"/native_clients.json" {
		t.Errorf("auth storePath = %q, want %q: the auth store split from the "+
			"SQLite store, which is exactly #328", got, envDir+"/native_clients.json")
	}

	// Everything that reads n.cfg.LeleDir (uploads, attachment staging, the
	// skills dirs) has to see the resolved value too, not the empty default:
	// filepath.Join("", "tmp", "uploads") would silently root uploads in the
	// process working directory.
	if got := native.cfg.LeleDir; got != envDir {
		t.Errorf("native.cfg.LeleDir = %q, want %q", got, envDir)
	}
	if got := native.leleDir; got != envDir {
		t.Errorf("native.leleDir = %q, want %q", got, envDir)
	}
}

// The resolution must stay local to the channel. Writing it back into the
// shared *config.Config would put an environment-derived path into the struct
// that SaveConfig marshals, freezing it into config.json — the failure mode
// that makes the empty default necessary in the first place.
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

// An explicit lele_dir must outrank LELE_CONFIG_DIR: deployments that set the
// field deliberately keep working, and the env var only fills the gap.
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
