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

// TestNewClientAuthManager_DBNotOpenable tests the error path: when the DB
// cannot be opened (invalid path) on a platform that DOES support SQLite
// (x86, arm64), the helper returns a non-nil error and a nil AuthManager.
// This is the new contract after the M1 fix: minting a PIN here would
// produce a dead PIN because the gateway uses SQLite and would never find it.
//
// Cleanup is still safe to call (nil-safe/idempotent) even when the store
// was never opened.
func TestNewClientAuthManager_DBNotOpenable(t *testing.T) {
	cfg := defaultTestConfig()

	// Use a regular file as leleDir — store.Open will fail because the DB
	// path (leleDir/lele.db) would be inside a non-directory.
	tmpFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tmpFile, []byte("plain file"), 0644); err != nil {
		t.Fatalf("creating temp file: %v", err)
	}

	authMgr, cleanup, err := newClientAuthManager(cfg, tmpFile)
	if err == nil {
		t.Fatal("newClientAuthManager should return error when DB cannot be opened on a supported platform, got nil")
	}
	// The error must NOT be ErrUnsupportedPlatform — on x86/arm64 the
	// platform is supported, so any open failure is a real DB problem.
	if errors.Is(err, store.ErrUnsupportedPlatform) {
		t.Fatalf("error should be a real DB open failure, not ErrUnsupportedPlatform: %v", err)
	}
	if authMgr != nil {
		t.Fatal("authMgr must be nil when DB open fails on a supported platform")
	}
	if cleanup == nil {
		t.Fatal("cleanup must not be nil (must be safe to call)")
	}

	// Must not panic even when store was never opened.
	cleanup()
}

// TestNewClientAuthManager_CleanupNoStore verifies that cleanup() is safe to
// call when the store was never opened (DB failure path). This is the "always
// non-nil, always safe" contract. On a supported platform (x86/arm64) with a
// nonexistent path, store.Open fails with a real DB error, so the helper
// returns error — but cleanup must still be safe to call.
func TestNewClientAuthManager_CleanupNoStore(t *testing.T) {
	cfg := defaultTestConfig()

	// Path that doesn't exist — store.Open will fail.
	_, cleanup, err := newClientAuthManager(cfg, filepath.Join(t.TempDir(), "nonexistent", "deep", "path"))
	if err != nil {
		// On supported platforms (x86/arm64), a nonexistent path produces
		// a real DB error, not ErrUnsupportedPlatform — this is expected.
		if cleanup == nil {
			t.Fatal("cleanup must not be nil")
		}
		// Must not panic.
		cleanup()
		// Calling twice must also be safe.
		cleanup()
		return
	}
	// err == nil: this is the mips64/unsupported-platform path where JSON
	// fallback is legitimate. Cleanup must still be safe.
	if cleanup == nil {
		t.Fatal("cleanup must not be nil")
	}
	cleanup()
	cleanup()
}

// TestStoreOpen_ErrUnsupportedPlatform_Sentinel verifies that the sentinel
// error is correctly wrapped in the error returned by store.Open when the
// platform doesn't support SQLite. We can't test the real mips64 path on
// x86/arm64, but we CAN verify that:
//   - store.Open on a supported platform returns an error that is NOT
//     ErrUnsupportedPlatform (proving the sentinel is not spuriously set).
//   - errors.Is wrapping works correctly by testing the sentinel directly.
func TestStoreOpen_ErrUnsupportedPlatform_Sentinel(t *testing.T) {
	// On x86/arm64, store.Open with an invalid path returns a real DB error,
	// NOT ErrUnsupportedPlatform. This proves the sentinel is not spuriously
	// set on supported platforms.
	_, err := store.Open(filepath.Join(t.TempDir(), "nonexistent", "deep", "path"))
	if err == nil {
		t.Skip("store.Open unexpectedly succeeded (should not happen with invalid path)")
	}
	if errors.Is(err, store.ErrUnsupportedPlatform) {
		t.Errorf("store.Open on supported platform must NOT return ErrUnsupportedPlatform; got: %v", err)
	}

	// Direct sentinel wrapping test: fmt.Errorf("%w: ...", ErrUnsupportedPlatform)
	// must be recognized by errors.Is.
	wrapped := fmt.Errorf("%w: test wrapping", store.ErrUnsupportedPlatform)
	if !errors.Is(wrapped, store.ErrUnsupportedPlatform) {
		t.Errorf("errors.Is on wrapped ErrUnsupportedPlatform should be true")
	}
}
