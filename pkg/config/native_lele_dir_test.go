package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// Issue #328: channels.native.lele_dir used to default to getDefaultLeleDir(),
// which is derived from $HOME and ignores LELE_CONFIG_DIR. The gateway opened
// its SQLite store at $LELE_CONFIG_DIR/lele.db (config.GetLeleDir) while the
// native channel read native_clients.json from $HOME/.lele, splitting one
// process's authentication store from the rest of its data.
//
// The obvious fix — default the field to GetLeleDir() — is not safe here, and
// these tests pin down why the field is left empty instead:
//
// SaveConfig marshals the whole struct, and lele_dir is only `omitempty`. Any
// non-empty default is therefore WRITTEN to config.json the next time
// `lele auth ...` or onboarding saves. A value in the file beats the code
// default on load, so a single run with LELE_CONFIG_DIR set would freeze that
// scratch path into the user's config and relocate the auth store, uploads,
// attachment staging and the skills dirs for every later run that has no such
// env var. The default must stay unresolved for LELE_CONFIG_DIR to be honoured
// per run; NewNativeChannel resolves it.

func TestDefaultConfig_NativeLeleDirIsNotBakedIn(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", envDir)

	// GetLeleDir itself must honour the env var — that is the whole point of
	// the resolution the channel performs.
	if got := GetLeleDir(); got != envDir {
		t.Fatalf("GetLeleDir() = %q, want %q", got, envDir)
	}

	// The default must NOT capture it.
	if got := DefaultConfig().Channels.Native.LeleDir; got != "" {
		t.Errorf("DefaultConfig().Channels.Native.LeleDir = %q, want %q: a "+
			"path baked into the default gets persisted by SaveConfig and then "+
			"beats the code default on every later load", got, "")
	}
}

// TestSaveConfig_LeavesUnsetNativeLeleDirUnpersisted is the freeze guard: the
// round trip must not materialise a lele_dir key.
func TestSaveConfig_LeavesUnsetNativeLeleDirUnpersisted(t *testing.T) {
	envDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", envDir)

	path := filepath.Join(t.TempDir(), "config.json")
	cfg := DefaultConfig()
	cfg.Providers = nil // unrelated to this test; keeps the file small

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if bytes.Contains(data, []byte(`"lele_dir"`)) {
		t.Fatalf("SaveConfig persisted lele_dir. The saved value is %q, resolved "+
			"from the environment of THIS process; the next run without "+
			"LELE_CONFIG_DIR would load it and move the auth store, uploads and "+
			"skills dirs into a path from the previous environment.\nsaved: %s",
			envDir, data)
	}

	// And reloading elsewhere must not inherit this run's directory.
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := reloaded.Channels.Native.LeleDir; got != "" {
		t.Errorf("reloaded Channels.Native.LeleDir = %q, want %q (unresolved)", got, "")
	}
}

// TestNativeLeleDir_ExplicitValueStillWins covers the other half: leaving the
// default empty must not break installations that set the field on purpose.
func TestNativeLeleDir_ExplicitValueStillWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// An explicit lele_dir survives a save/load round trip.
	cfg := DefaultConfig()
	cfg.Providers = nil
	cfg.Channels.Native.LeleDir = "/var/lib/lele"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.Channels.Native.LeleDir != "/var/lib/lele" {
		t.Fatalf("explicit lele_dir = %q, want %q", got.Channels.Native.LeleDir, "/var/lib/lele")
	}

	// The env var must not override an explicit configuration.
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	if err := SaveConfig(path, got); err != nil {
		t.Fatalf("SaveConfig (second): %v", err)
	}
	again, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig (second): %v", err)
	}
	if again.Channels.Native.LeleDir != "/var/lib/lele" {
		t.Errorf("lele_dir after a run with LELE_CONFIG_DIR set = %q, want %q: "+
			"an explicit setting must outrank the environment",
			again.Channels.Native.LeleDir, "/var/lib/lele")
	}
}
