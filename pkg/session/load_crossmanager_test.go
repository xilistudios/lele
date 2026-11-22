package session

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

func TestCrossManager_LoadSessionFromDisk(t *testing.T) {
	s := newCrossManagerTestStore(t)

	childKey := "agent:main:subagent-child-1"

	managerB := NewSessionManager()
	managerB.SetSessionRepo(s.Sessions())
	_ = managerB.ListSessions()

	managerA := NewSessionManager()
	managerA.SetSessionRepo(s.Sessions())

	managerA.AddTokenCounts(childKey, 1200, 40)
	managerA.AddMessage(childKey, "user", "hello from child")
	if err := managerA.Save(childKey); err != nil {
		t.Fatalf("managerA.Save(%q) failed: %v", childKey, err)
	}

	in, out := managerB.GetTokenCounts(childKey)
	if in != 1200 {
		t.Errorf("GetTokenCounts input = %d, want 1200 (cross-manager metadata fallback)", in)
	}
	if out != 40 {
		t.Errorf("GetTokenCounts output = %d, want 40 (cross-manager metadata fallback)", out)
	}

	view := managerB.GetHistoryView(childKey)
	if len(view) != 1 {
		t.Fatalf("GetHistoryView message count = %d, want 1 (cross-manager metadata fallback)", len(view))
	}
	if view[0].Content != "hello from child" {
		t.Errorf("GetHistoryView[0].Content = %q, want %q", view[0].Content, "hello from child")
	}

	name := managerB.GetName(childKey)
	if name == "" {
		t.Errorf("GetName(%q) = %q, want non-empty", childKey, name)
	}
}

func TestCrossManager_NonExistentKey_NoPhantomSession(t *testing.T) {
	s := newCrossManagerTestStore(t)

	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	_ = sm.ListSessions()

	ghostKey := "does-not-exist-anywhere"

	in, out := sm.GetTokenCounts(ghostKey)
	if in != 0 || out != 0 {
		t.Errorf("GetTokenCounts(%q) = (%d, %d), want (0, 0)", ghostKey, in, out)
	}

	view := sm.GetHistoryView(ghostKey)
	if len(view) != 0 {
		t.Errorf("GetHistoryView(%q) returned %d messages, want 0", ghostKey, len(view))
	}

	sm.mu.RLock()
	_, inMeta := sm.sessionMeta[ghostKey]
	sm.mu.RUnlock()
	if inMeta {
		t.Errorf("phantom key %q appeared in sessionMeta after nonexistent lookup", ghostKey)
	}
}

func TestCrossManager_FallbackRegistersMetadata(t *testing.T) {
	s := newCrossManagerTestStore(t)

	childKey := "agent:main:subagent-register-check"

	managerA := NewSessionManager()
	managerA.SetSessionRepo(s.Sessions())
	managerA.AddTokenCounts(childKey, 500, 20)
	managerA.AddFullMessage(childKey, providers.Message{Role: "user", Content: "register test"})
	if err := managerA.Save(childKey); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	managerB := NewSessionManager()
	managerB.SetSessionRepo(s.Sessions())
	_ = managerB.ListSessions()

	in, out := managerB.GetTokenCounts(childKey)
	if in != 500 || out != 20 {
		t.Fatalf("GetTokenCounts = (%d, %d), want (500, 20)", in, out)
	}

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
