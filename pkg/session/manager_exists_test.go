package session

import (
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/store"
)

// TestSessionExists verifies SessionExists across layers (in-memory, metadata, and SQLite).
func TestSessionExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	sm := NewSessionManager()
	sm.SetStore(s)

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

	// Save so it exists in DB, then evict.
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
	// DB entry remains. SessionExists must still report true so the
	// subagent manager does not reuse this ID.
	if !sm.SessionExists(subKey) {
		t.Errorf("SessionExists(%q) = false after EvictSession, want true (DB-only)", subKey)
	}

	// A fresh manager (simulating a restart) must also see it.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	if !sm2.SessionExists(subKey) {
		t.Errorf("SessionExists(%q) = false in fresh manager, want true", subKey)
	}

	// Deleting from the database makes it disappear entirely.
	if err := s.Sessions().DeleteSession(subKey); err != nil {
		t.Fatalf("failed to delete session from db: %v", err)
	}
	sm3 := NewSessionManager()
	sm3.SetStore(s)
	if sm3.SessionExists(subKey) {
		t.Errorf("SessionExists(%q) = true after DB deletion and fresh load, want false", subKey)
	}
}
