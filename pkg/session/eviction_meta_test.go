package session

import (
	"testing"
	"time"
)

// Idle LRU/TTL eviction must leave a listing shell that still names the chat
// and sorts by its real Updated time. ListSessions serves non-resident keys
// from sessionMeta; without syncing that index before delete, the TUI lost
// every chat that had been idle long enough for CleanupIdleSessions while
// SQLite still held the full history.
func TestCleanupIdleSessions_KeepsNameAndUpdatedInMeta(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "tui:chat:idle-meta"
	sm.GetOrCreate(key)
	_ = sm.SetMode(key, "agent")
	sm.AddMessage(key, "user", "How do I rotate API keys?")
	sm.AddMessage(key, "assistant", "Use the /keys command.")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Force the session to look idle beyond the TTL, then run cleanup.
	sm.SetEvictionTTL(time.Minute)
	stale := time.Now().Add(-time.Hour)
	sm.mu.Lock()
	sm.accessTimes[key] = stale
	sm.mu.Unlock()

	if n := sm.CleanupIdleSessions(); n != 1 {
		t.Fatalf("CleanupIdleSessions = %d, want 1", n)
	}

	// Session must be non-resident.
	sm.mu.RLock()
	_, resident := sm.sessions[key]
	sm.mu.RUnlock()
	if resident {
		t.Fatal("session still resident after idle cleanup")
	}

	// Listing shell must keep the generated name and updated timestamp.
	found := false
	for _, listed := range sm.ListSessions() {
		if listed.Key != key {
			continue
		}
		found = true
		if listed.Name == "" {
			t.Error("ListSessions Name empty after idle eviction — TUI would show a blank chat")
		}
		if listed.Updated.IsZero() || !listed.Updated.After(stale) {
			t.Errorf("ListSessions Updated = %v, want post-message timestamp", listed.Updated)
		}
		if len(listed.Messages) != 0 {
			t.Errorf("ListSessions Messages = %d, want 0 (metadata-only shell)", len(listed.Messages))
		}
	}
	if !found {
		t.Fatal("session missing from ListSessions after idle eviction")
	}

	// SQLite still has every message and cold GetHistoryView reloads them.
	counts := sm.AllTotalMessageCounts()
	if counts[key] != 2 {
		t.Fatalf("AllTotalMessageCounts[%q] = %d, want 2", key, counts[key])
	}
	hist := sm.GetHistoryView(key)
	if len(hist) != 2 {
		t.Fatalf("GetHistoryView after idle eviction = %d messages, want 2", len(hist))
	}
	if hist[0].Content != "How do I rotate API keys?" {
		t.Errorf("reloaded first message = %q", hist[0].Content)
	}

	// Cold load must also refresh the listing index so a second eviction
	// cannot resurrect an empty shell.
	sm.SetEvictionTTL(time.Minute)
	sm.mu.Lock()
	sm.accessTimes[key] = time.Now().Add(-time.Hour)
	sm.mu.Unlock()
	if n := sm.CleanupIdleSessions(); n != 1 {
		t.Fatalf("second CleanupIdleSessions = %d, want 1", n)
	}
	for _, listed := range sm.ListSessions() {
		if listed.Key == key && listed.Name == "" {
			t.Fatal("Name lost again after evict → cold load → evict")
		}
	}
}

// EvictSession is the explicit (non-idle) eviction path used by tests and
// callers that force a drop; it must leave the same listing shell.
func TestEvictSession_SyncsMetaName(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "tui:chat:evict-meta"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "named chat body")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !sm.EvictSession(key) {
		t.Fatal("EvictSession returned false")
	}

	for _, listed := range sm.ListSessions() {
		if listed.Key == key && listed.Name == "" {
			t.Fatal("EvictSession left sessionMeta.Name empty")
		}
	}
	if hist := sm.GetHistoryView(key); len(hist) != 1 {
		t.Fatalf("GetHistoryView after EvictSession = %d, want 1", len(hist))
	}
}
