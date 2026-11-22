package channels

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

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

func TestReloadConfig_PreservesNativeClientRepo(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	m.SetNativeClientStore(repo)

	auth := getNativeAuth(t, m)
	if auth.repo != repo {
		t.Fatal("SetNativeClientStore: repo not wired into current native channel")
	}

	newCfg := &config.Config{}
	newCfg.Channels.Native.Enabled = true
	newCfg.Channels.Native.LeleDir = t.TempDir()

	if err := m.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig() error = %v", err)
	}

	newAuth := getNativeAuth(t, m)
	if newAuth.repo != repo {
		t.Fatal("H1: NativeClientRepo lost after ReloadConfig — " +
			"new AuthManager.repo is nil; WebUI clients would be logged out")
	}
}

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

func TestReloadConfig_NilConfigRejected(t *testing.T) {
	m := newTestManagerForReload(t)

	if err := m.ReloadConfig(nil); err == nil {
		t.Fatal("ReloadConfig(nil) should return an error")
	}
}

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

func TestSetNativeClientStore_PersistsReference(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	m.SetNativeClientStore(repo)

	if m.nativeClientRepo != repo {
		t.Fatal("SetNativeClientStore did not persist repo on Manager.nativeClientRepo")
	}
}

func TestSetNativeClientStore_NilClearsRepo(t *testing.T) {
	m := newTestManagerForReload(t)
	repo := openTestNativeRepo(t)

	m.SetNativeClientStore(repo)
	m.SetNativeClientStore(nil)

	if m.nativeClientRepo != nil {
		t.Fatal("SetNativeClientStore(nil) did not clear Manager.nativeClientRepo")
	}
}

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

func TestSetInboundSpooler_PersistsReference(t *testing.T) {
	m := newTestManagerForReload(t)
	spooler := newFakeSpooler(bus.NewMessageBus())

	m.SetInboundSpooler(spooler)

	if m.inboundSpooler != InboundSpooler(spooler) {
		t.Fatal("SetInboundSpooler did not persist the spooler on Manager.inboundSpooler")
	}
}

func TestReloadConfig_PreservesNativeStoreUnavailable(t *testing.T) {
	m := newTestManagerForReload(t)
	storeErr := realStoreOpenError(t)

	m.SetNativeStoreUnavailable(storeErr)

	auth := getNativeAuth(t, m)
	if reason, degraded := auth.StoreUnavailable(); !degraded || reason != storeErr.Error() {
		t.Fatalf("SetNativeStoreUnavailable: current native channel degraded=%v reason=%q, want the store error", degraded, reason)
	}
	if m.nativeStoreUnavailable == nil {
		t.Fatal("SetNativeStoreUnavailable did not persist the error on the Manager")
	}

	newCfg := &config.Config{}
	newCfg.Channels.Native.Enabled = true
	newCfg.Channels.Native.LeleDir = t.TempDir()

	if err := m.ReloadConfig(newCfg); err != nil {
		t.Fatalf("ReloadConfig() error = %v", err)
	}

	newAuth := getNativeAuth(t, m)
	if reason, degraded := newAuth.StoreUnavailable(); !degraded || reason != storeErr.Error() {
		t.Fatalf("degraded mark lost after ReloadConfig: degraded=%v reason=%q", degraded, reason)
	}
	if _, _, _, err := newAuth.PairWithPIN("123456", "some-device"); !errors.Is(err, ErrPairingUnavailable) {
		t.Fatalf("post-reload PairWithPIN err = %v, want ErrPairingUnavailable", err)
	}
}
