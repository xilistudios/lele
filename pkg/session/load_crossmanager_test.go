package session

// Cross-manager session visibility.
//
// Each agent owns its own SessionManager sharing one SQLite store. The
// sessionMeta index is bootstrapped once per manager (loadOnce), so a
// manager whose metadata was loaded BEFORE another manager created a
// session will silently return zero/empty for that session's data.
// loadSessionFromDisk must fall back to a store lookup when the key
// is absent from the local metadata index.

import (
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

func newCrossManagerTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestCrossManager_LoadSessionFromDisk verifies that a SessionManager whose
// metadata index was bootstrapped BEFORE a session was persisted by a
// different manager can still load that session's tokens and messages from
// disk. Without the fallback in loadSessionFromDisk, GetTokenCounts returns
// (0, 0) and GetHistoryView returns an empty slice.
func TestCrossManager_LoadSessionFromDisk(t *testing.T) {
	s := newCrossManagerTestStore(t)

	childKey := "agent:main:subagent-child-1"

	// Manager B: bootstrap its metadata index BEFORE any session exists.
	// This forces B's sessionMeta to be empty for childKey.
	managerB := NewSessionManager()
	managerB.SetStore(s)
	// Touch any API that triggers ensureLoaded to populate the index.
	_ = managerB.ListSessions() // loadOnce fires here

	// Manager A: create the session, add tokens, a message, and persist.
	managerA := NewSessionManager()
	managerA.SetStore(s)

	managerA.AddTokenCounts(childKey, 1200, 40)
	managerA.AddMessage(childKey, "user", "hello from child")
	if err := managerA.Save(childKey); err != nil {
		t.Fatalf("managerA.Save(%q) failed: %v", childKey, err)
	}

	// --- Bug surface: managerB reads through a stale metadata index ---

	// GetTokenCounts must return the values persisted by A.
	in, out := managerB.GetTokenCounts(childKey)
	if in != 1200 {
		t.Errorf("GetTokenCounts input = %d, want 1200 (cross-manager metadata fallback)", in)
	}
	if out != 40 {
		t.Errorf("GetTokenCounts output = %d, want 40 (cross-manager metadata fallback)", out)
	}

	// GetHistoryView must see the message written by A.
	view := managerB.GetHistoryView(childKey)
	if len(view) != 1 {
		t.Fatalf("GetHistoryView message count = %d, want 1 (cross-manager metadata fallback)", len(view))
	}
	if view[0].Content != "hello from child" {
		t.Errorf("GetHistoryView[0].Content = %q, want %q", view[0].Content, "hello from child")
	}

	// GetName / GetMode should also work after the fallback registered metadata.
	name := managerB.GetName(childKey)
	if name == "" {
		// Name is auto-generated from the first user message content, so it
		// should be non-empty after load.
		t.Errorf("GetName(%q) = %q, want non-empty", childKey, name)
	}
}

// TestCrossManager_NonExistentKey_NoPhantomSession ensures that querying a
// key that does not exist in the store returns zero/empty without creating a
// phantom metadata entry. This is the negative control for the cross-manager
// fallback.
func TestCrossManager_NonExistentKey_NoPhantomSession(t *testing.T) {
	s := newCrossManagerTestStore(t)

	sm := NewSessionManager()
	sm.SetStore(s)
	// Bootstrap the metadata index.
	_ = sm.ListSessions()

	ghostKey := "does-not-exist-anywhere"

	// Must return zero without panicking.
	in, out := sm.GetTokenCounts(ghostKey)
	if in != 0 || out != 0 {
		t.Errorf("GetTokenCounts(%q) = (%d, %d), want (0, 0)", ghostKey, in, out)
	}

	view := sm.GetHistoryView(ghostKey)
	if len(view) != 0 {
		t.Errorf("GetHistoryView(%q) returned %d messages, want 0", ghostKey, len(view))
	}

	// The phantom key must NOT appear in the metadata index.
	sm.mu.RLock()
	_, inMeta := sm.sessionMeta[ghostKey]
	sm.mu.RUnlock()
	if inMeta {
		t.Errorf("phantom key %q appeared in sessionMeta after nonexistent lookup", ghostKey)
	}
}

// TestCrossManager_FallbackRegistersMetadata verifies that after the
// cross-manager fallback fires, the metadata is registered in sessionMeta so
// subsequent ListSessions / GetName calls see it without another store round
// trip.
func TestCrossManager_FallbackRegistersMetadata(t *testing.T) {
	s := newCrossManagerTestStore(t)

	childKey := "agent:main:subagent-register-check"

	// Manager A: create and persist.
	managerA := NewSessionManager()
	managerA.SetStore(s)
	managerA.AddTokenCounts(childKey, 500, 20)
	managerA.AddFullMessage(childKey, providers.Message{Role: "user", Content: "register test"})
	if err := managerA.Save(childKey); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Manager B: bootstrap, then trigger the fallback.
	managerB := NewSessionManager()
	managerB.SetStore(s)
	_ = managerB.ListSessions() // loadOnce

	// Trigger the fallback by reading token counts.
	in, out := managerB.GetTokenCounts(childKey)
	if in != 500 || out != 20 {
		t.Fatalf("GetTokenCounts = (%d, %d), want (500, 20)", in, out)
	}

	// Now the metadata should be registered in sessionMeta.
	managerB.mu.RLock()
	meta, ok := managerB.sessionMeta[childKey]
	managerB.mu.RUnlock()
	if !ok {
		t.Fatal("metadata not registered in sessionMeta after fallback")
	}
	if meta.Key != childKey {
		t.Errorf("registered meta key = %q, want %q", meta.Key, childKey)
	}
}
