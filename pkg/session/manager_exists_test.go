package session

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSessionExists verifies SessionExists across all three layers:
// in-memory, metadata-only, and disk-only (the state left behind by
// EvictSession for subagent sessions).
func TestSessionExists(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	if sm.SessionExists("") {
		t.Error("SessionExists(\"\") should be false")
	}
	if sm.SessionExists("telegram:999") {
		t.Error("SessionExists should be false for a key that was never created")
	}

	// In-memory session.
	key := "telegram:123"
	sm.GetOrCreate(key)
	if !sm.SessionExists(key) {
		t.Errorf("SessionExists(%q) = false, want true (in-memory)", key)
	}

	// Save so the file exists on disk, then evict. For subagent-style keys
	// EvictSession also drops the metadata entry, leaving only the file.
	subKey := "cli:direct:subagent-1"
	sm.GetOrCreate(subKey)
	sm.AddMessage(subKey, "user", "hello")
	if err := sm.Save(subKey); err != nil {
		t.Fatalf("Save(%q) failed: %v", subKey, err)
	}

	if !sm.EvictSession(subKey) {
		t.Fatalf("EvictSession(%q) returned false, want true", subKey)
	}

	// After eviction the session is gone from memory and metadata, but the
	// file remains on disk. SessionExists must still report true so the
	// subagent manager does not reuse this ID.
	if !sm.SessionExists(subKey) {
		t.Errorf("SessionExists(%q) = false after EvictSession, want true (disk-only)", subKey)
	}

	// A fresh manager (simulating a restart) must also see it.
	sm2 := NewSessionManager(tmpDir)
	if !sm2.SessionExists(subKey) {
		t.Errorf("SessionExists(%q) = false in fresh manager, want true", subKey)
	}

	// Deleting the file makes it disappear entirely — but only for a manager
	// that rescans from scratch (simulating a restart). A manager that already
	// loaded the metadata keeps it until restart, which is harmless: it just
	// makes ID claiming skip one extra number.
	filename := filepath.Join(tmpDir, sanitizeFilename(subKey)+".json")
	if err := os.Remove(filename); err != nil {
		t.Fatalf("failed to remove %s: %v", filename, err)
	}
	sm3 := NewSessionManager(tmpDir)
	if sm3.SessionExists(subKey) {
		t.Errorf("SessionExists(%q) = true after file deletion and fresh load, want false", subKey)
	}
}
