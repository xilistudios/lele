package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

func defaultTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.MaxClients = 5
	cfg.Channels.Native.PinExpiryMinutes = 5
	return cfg
}

func TestNewClientAuthManager_DBOpens(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := defaultTestConfig()
	dbPath := filepath.Join(tmpDir, "lele.db")

	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open (server): %v", err)
	}
	if err := s1.NativeClients().SetClient("server-client", `{"client_id":"server-client","device_name":"server-phone","token":"tok123"}`); err != nil {
		t.Fatalf("SetClient via server: %v", err)
	}
	s1.Close()

	authMgr, cleanup, err := newClientAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("newClientAuthManager returned error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil")
	}
	defer cleanup()

	clients := authMgr.ListClients()
	found := false
	for _, c := range clients {
		if c.ClientID == "server-client" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("helper's AuthManager does not see client 'server-client'; got %d clients", len(clients))
	}
}

func TestNewClientAuthManager_DBOpens_CleanupSafe(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := defaultTestConfig()

	_, cleanup, err := newClientAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("newClientAuthManager returned error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil")
	}
	cleanup()
	cleanup()
}

func TestNewClientAuthManager_DBNotOpenable(t *testing.T) {
	cfg := defaultTestConfig()

	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmpFile, []byte("plain file"), 0644); err != nil {
		t.Fatalf("creating temp file: %v", err)
	}

	authMgr, cleanup, err := newClientAuthManager(cfg, tmpFile)
	if err == nil {
		t.Fatal("newClientAuthManager should return error when DB cannot be opened on a supported platform, got nil")
	}
	if errors.Is(err, store.ErrUnsupportedPlatform) {
		t.Fatalf("error should be a real DB open failure, not ErrUnsupportedPlatform: %v", err)
	}
	if authMgr != nil {
		t.Fatal("authMgr must be nil when DB open fails on a supported platform")
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil (must be safe to call)")
	}

	cleanup()
}

func TestNewClientAuthManager_CleanupNoStore(t *testing.T) {
	cfg := defaultTestConfig()

	_, cleanup, err := newClientAuthManager(cfg, filepath.Join(t.TempDir(), "nonexistent", "deep", "path"))
	if err != nil {
		if cleanup == nil {
			t.Fatal("cleanup must not be nil")
		}
		cleanup()
		cleanup()
		return
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil")
	}
	cleanup()
	cleanup()
}

func TestStoreOpen_ErrUnsupportedPlatform_Sentinel(t *testing.T) {
	_, err := store.Open(filepath.Join(t.TempDir(), "nonexistent", "deep", "path"))
	if err == nil {
		t.Skip("store.Open unexpectedly succeeded (should not happen with invalid path)")
	}
	if errors.Is(err, store.ErrUnsupportedPlatform) {
		t.Errorf("store.Open on supported platform must NOT return ErrUnsupportedPlatform; got: %v", err)
	}

	wrapped := fmt.Errorf("%w: test wrapping", store.ErrUnsupportedPlatform)
	if !errors.Is(wrapped, store.ErrUnsupportedPlatform) {
		t.Errorf("errors.Is on wrapped ErrUnsupportedPlatform should be true")
	}
}

func TestNewClientAuthManager_CreatesMissingLeleDir(t *testing.T) {
	root := t.TempDir()
	leleDir := filepath.Join(root, "no-existe-todavia")
	cfg := &config.Config{}
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.PinExpiryMinutes = 5
	cfg.Channels.Native.MaxClients = 5

	authMgr, cleanup, err := newClientAuthManager(cfg, leleDir)
	if err != nil {
		t.Fatalf("newClientAuthManager on a fresh install: %v", err)
	}
	defer cleanup()

	if _, statErr := os.Stat(leleDir); statErr != nil {
		t.Fatalf("lele dir should have been created: %v", statErr)
	}

	pending, err := authMgr.GeneratePIN("fresh-install")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}

	s, err := store.Open(filepath.Join(leleDir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	if _, _, found, err := s.NativeClients().GetPendingPIN(pending.PIN); err != nil {
		t.Fatalf("GetPendingPIN: %v", err)
	} else if !found {
		t.Errorf("PIN %s minted on a fresh install is not in SQLite", pending.PIN)
	}
}
