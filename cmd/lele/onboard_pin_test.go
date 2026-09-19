package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// defaultTestConfig returns a minimal Config with native channel enabled.
func defaultTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.MaxClients = 5
	cfg.Channels.Native.PinExpiryMinutes = 5
	return cfg
}

// TestNewClientAuthManager_DBOpens tests the happy path: when the DB at
// leleDir/lele.db is openable, the helper returns an AuthManager with a live
// store (repo != nil) and a cleanup that closes the DB.
//
// Anti-false-green (E7): we verify the store is genuinely wired by writing a
// client through a "server" store handle BEFORE creating the helper, then
// checking the helper's AuthManager can see it (proving SetStore was called
// and loadStore reloaded from the same DB). This mirrors the real flow: the
// server has clients in SQLite, and the CLI's AuthManager must see them via
// SetStore. We do NOT use GeneratePIN because PIN persistence through the
// repo is added in T3.3; using it would create a test that only passes after
// T3.3, defeating the purpose of testing the wiring in isolation.
func TestNewClientAuthManager_DBOpens(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := defaultTestConfig()
	dbPath := filepath.Join(tmpDir, "lele.db")

	// Phase 1: Write a client via a "server" store handle (simulates a
	// previously-running gateway that has paired clients in SQLite).
	s1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open (server): %v", err)
	}
	if err := s1.NativeClients().SetClient("server-client", `{"client_id":"server-client","device_name":"server-phone","token":"tok123"}`); err != nil {
		t.Fatalf("SetClient via server: %v", err)
	}
	s1.Close()

	// Phase 2: Create the helper (simulates CLI or onboard). It must open
	// the same DB, call SetStore, and loadStore — which should pick up the
	// client written in phase 1.
	authMgr, cleanup, err := newClientAuthManager(cfg, tmpDir)
	if err != nil {
		t.Fatalf("newClientAuthManager returned error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil")
	}
	defer cleanup()

	// Verify the helper's AuthManager sees the server's client.
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

// TestNewClientAuthManager_DBOpens_CleanupSafe verifies that the cleanup
// function returned by the helper is safe to call and actually releases the
// database resources.
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
	// Must not panic.
	cleanup()
	// Calling twice must also be safe (idempotent).
	cleanup()
}

// TestNewClientAuthManager_DBNotOpenable tests the fallback path: when the DB
// cannot be opened (invalid path), the helper still returns a usable
// AuthManager without a store (repo == nil), err == nil, and a non-nil
// cleanup. This is the branch exercised on mips64 or when the DB file is
// corrupted.
func TestNewClientAuthManager_DBNotOpenable(t *testing.T) {
	cfg := defaultTestConfig()

	// Use a regular file as leleDir — store.Open will fail because the DB
	// path (leleDir/lele.db) would be inside a non-directory.
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmpFile, []byte("plain file"), 0644); err != nil {
		t.Fatalf("creating temp file: %v", err)
	}

	authMgr, cleanup, err := newClientAuthManager(cfg, tmpFile)
	if err != nil {
		t.Fatalf("newClientAuthManager should not return error on DB failure, got: %v", err)
	}
	if authMgr == nil {
		t.Fatal("authMgr must not be nil even when DB is not openable")
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil even when store was not opened")
	}

	// Must not panic.
	cleanup()
}

// TestNewClientAuthManager_CleanupNoStore verifies that cleanup() is safe to
// call when the store was never opened (fallback path). This is the "always
// non-nil, always safe" contract. Calling cleanup twice must also be safe.
func TestNewClientAuthManager_CleanupNoStore(t *testing.T) {
	cfg := defaultTestConfig()

	// Path that doesn't exist — store.Open will fail.
	_, cleanup, err := newClientAuthManager(cfg, filepath.Join(t.TempDir(), "nonexistent", "deep", "path"))
	if err != nil {
		t.Fatalf("expected nil error on fallback, got: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil")
	}
	// Must not panic.
	cleanup()
	// Calling twice must also be safe.
	cleanup()
}
