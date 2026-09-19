// Regression tests: Manager.ReloadConfig must preserve the post-construction
// seams (NativeClientRepo and InboundSpooler) across channel reconstruction.
// initChannels builds a fresh NativeChannel whose AuthManager.repo is nil and
// whose channels have no spooler, so without re-application a config reload
// silently un-wires both.

package channels

import (
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// newTestManagerForReload creates a minimal Manager with a native channel
// enabled. Returns the manager and a cleanup function.
func newTestManagerForReload(t *testing.T) *Manager {
	t.Helper()

	cfg := &config.Config{}
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.LeleDir = t.TempDir()

	msgBus := bus.NewMessageBus()
	m, err := NewManager(cfg, msgBus, nil, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return m
}

// openTestNativeRepo opens a real NativeClientRepo backed by an in-memory
// SQLite database in a temp directory.
func openTestNativeRepo(t *testing.T) *store.NativeClientRepo {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q) error = %v", dbPath, err)
	}
	t.Cleanup(func() { s.Close() })
	return s.NativeClients()
}

// getNativeAuth extracts the AuthManager from the current native channel
// in the manager. Fails the test if the native channel is missing or has
// the wrong type.
func getNativeAuth(t *testing.T, m *Manager) *AuthManager {
	t.Helper()

	ch, ok := m.GetChannel("native")
	if !ok {
		t.Fatal("native channel not found in manager")
	}
	nc, ok := ch.(*NativeChannel)
	if !ok {
		t.Fatalf("native channel has type %T, want *NativeChannel", ch)
	}
	return nc.auth
}

// TestReloadConfig_PreservesNativeClientRepo verifies the H1 fix:
// SetNativeClientStore persists the repo on the Manager, and ReloadConfig
// re-injects it into the new NativeChannel after initChannels.
//
// Before the fix the new AuthManager.repo was nil after every reload,
// causing the channel to fall back to the JSON file and silently drop
// every SQLite-only client.
func TestReloadConfig_PreservesNativeClientRepo(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	// Wire the repo (simulates gateway startup).
	m.SetNativeClientStore(repo)

	// Verify it took effect on the current native channel.
	auth := getNativeAuth(t, m)
	if auth.repo != repo {
		t.Fatal("SetNativeClientStore: repo not wired into current native channel")
	}

	// Reload with a fresh config (simulates config file change or API save).
	newCfg := &config.Config{}
	newCfg.Channels.Native.Enabled = true
	newCfg.Channels.Native.LeleDir = t.TempDir()

	if err := m.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig() error = %v", err)
	}

	// The new native channel must have the SAME repo, not nil.
	newAuth := getNativeAuth(t, m)
	if newAuth.repo != repo {
		t.Fatal("H1: NativeClientRepo lost after ReloadConfig — " +
			"new AuthManager.repo is nil; WebUI clients would be logged out")
	}
}

// TestReloadConfig_NoRepoIsNoop verifies that ReloadConfig succeeds
// when SetNativeClientStore was never called (repo is nil). The native
// channel simply runs without SQLite persistence, as it did before the
// feature existed.
func TestReloadConfig_NoRepoIsNoop(t *testing.T) {
	m := newTestManagerForReload(t)

	newCfg := &config.Config{}
	newCfg.Channels.Native.Enabled = true
	newCfg.Channels.Native.LeleDir = t.TempDir()

	if err := m.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig() with no repo set should succeed, got error = %v", err)
	}

	auth := getNativeAuth(t, m)
	if auth.repo != nil {
		t.Fatal("expected nil repo when SetNativeClientStore was never called")
	}
}

// TestReloadConfig_NilConfigRejected verifies the nil-config guard.
func TestReloadConfig_NilConfigRejected(t *testing.T) {
	m := newTestManagerForReload(t)

	if err := m.ReloadConfig(nil); err == nil {
		t.Fatal("ReloadConfig(nil) should return an error")
	}
}

// TestReloadConfig_RepoSurvivesMultipleReloads verifies that the repo
// persists through several successive reloads — not just one.
func TestReloadConfig_RepoSurvivesMultipleReloads(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	m.SetNativeClientStore(repo)

	for i := 0; i < 3; i++ {
		newCfg := &config.Config{}
		newCfg.Channels.Native.Enabled = true
		newCfg.Channels.Native.LeleDir = t.TempDir()

		if err := m.ReloadConfig(newCfg); err != nil {
			t.Fatalf("ReloadConfig iteration %d: error = %v", i, err)
		}

		auth := getNativeAuth(t, m)
		if auth.repo != repo {
			t.Fatalf("H1: repo lost after reload iteration %d", i)
		}
	}
}

// TestSetNativeClientStore_PersistsReference verifies that
// SetNativeClientStore stores the repo on the Manager struct itself,
// not just on the current channel.
func TestSetNativeClientStore_PersistsReference(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	m.SetNativeClientStore(repo)

	// Directly inspect the manager field (same package access).
	if m.nativeClientRepo != repo {
		t.Fatal("SetNativeClientStore did not persist repo on Manager.nativeClientRepo")
	}
}

// TestSetNativeClientStore_NilClearsRepo verifies that passing nil
// clears the stored repo (analogous to disabling SQLite persistence).
func TestSetNativeClientStore_NilClearsRepo(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	m.SetNativeClientStore(repo)
	m.SetNativeClientStore(nil)

	if m.nativeClientRepo != nil {
		t.Fatal("SetNativeClientStore(nil) did not clear Manager.nativeClientRepo")
	}
}

// nativeSpoolerOf reads the inbound spooler currently wired into the manager's
// native channel, failing the test if the channel is missing or of the wrong
// type.
func nativeSpoolerOf(t *testing.T, m *Manager) InboundSpooler {
	t.Helper()

	ch, ok := m.GetChannel("native")
	if !ok {
		t.Fatal("native channel not found in manager")
	}
	nc, ok := ch.(*NativeChannel)
	if !ok {
		t.Fatalf("native channel has type %T, want *NativeChannel", nc)
	}
	if nc.base == nil {
		t.Fatal("native channel has no base channel")
	}
	return nc.base.InboundSpooler
}

// TestReloadConfig_PreservesInboundSpooler covers the sibling of the
// native-client seam: durable inbound is handed to channels after
// construction, so ReloadConfig must re-apply it to the channels it builds.
// Losing it would silently switch spooling off after the first config reload.
func TestReloadConfig_PreservesInboundSpooler(t *testing.T) {
	m := newTestManagerForReload(t)
	spooler := newFakeSpooler(bus.NewMessageBus())

	m.SetInboundSpooler(spooler)
	if got := nativeSpoolerOf(t, m); got != InboundSpooler(spooler) {
		t.Fatal("SetInboundSpooler: spooler not wired into the current native channel")
	}

	newCfg := &config.Config{}
	newCfg.Channels.Native.Enabled = true
	newCfg.Channels.Native.LeleDir = t.TempDir()

	if err := m.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig() error = %v", err)
	}

	if got := nativeSpoolerOf(t, m); got != InboundSpooler(spooler) {
		t.Fatal("inbound spooler lost after ReloadConfig - durable inbound would be silently off")
	}
}

// TestSetInboundSpooler_PersistsReference pins that the value is retained on
// the Manager, which is what makes the re-application above possible at all.
func TestSetInboundSpooler_PersistsReference(t *testing.T) {
	m := newTestManagerForReload(t)
	spooler := newFakeSpooler(bus.NewMessageBus())

	m.SetInboundSpooler(spooler)

	if m.inboundSpooler != InboundSpooler(spooler) {
		t.Fatal("SetInboundSpooler did not persist the spooler on Manager.inboundSpooler")
	}
}
