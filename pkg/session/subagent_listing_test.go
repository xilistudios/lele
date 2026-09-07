package session

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// Reproduces the cron-spawn/subagent WebUI invisibility bug: a subagent
// session is written by the tool loop through AddFullMessage (the loop's
// SessionRecorder), saved, and then evicted when its task reaches a terminal
// state. The session must remain listed by ListSessions — the source for
// ListAllSessions, which the WebUI session-history endpoints merge in — and
// must stay reloadable, because its rows are persisted in SQLite and never
// deleted.
//
// Before the fix this failed twice over:
//   - AddFullMessage materialized the session in sm.sessions without a
//     sessionMeta entry, so once evicted it was in neither index;
//   - EvictSession additionally deleted the sessionMeta entry for any
//     ":subagent-" key, guaranteeing invisibility until restart (which
//     rebuilds metadata from SQLite via loadSessionMetadataFromSQLite).

func cronSpawnSessionKey() string {
	return "native:cron-dbcb45b8f62b875d:subagent-26"
}

func findSessionByKey(list []*Session, key string) *Session {
	for _, s := range list {
		if s.Key == key {
			return s
		}
	}
	return nil
}

// TestAddFullMessage_RegistersSessionMeta is the invariant test: every path
// that makes a session resident must also register it in sessionMeta, so the
// session survives eviction in the listing index and stays reloadable
// (loadSessionFromDisk is gated on sessionMeta).
func TestAddFullMessage_RegistersSessionMeta(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := cronSpawnSessionKey()
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "scheduled run"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "done"})

	sm.mu.RLock()
	_, resident := sm.sessions[key]
	_, meta := sm.sessionMeta[key]
	sm.mu.RUnlock()

	if !resident {
		t.Fatal("session should be resident after AddFullMessage")
	}
	if !meta {
		t.Fatal("session must be registered in sessionMeta when resident (invariant: resident ⇒ metadata)")
	}
}

// TestEvictSession_SubagentStaysListedAndReloadable covers the full cron-spawn
// lifecycle end-to-end at the manager level: create via the recorder path,
// persist, evict on task cleanup, then verify the session still appears in
// ListSessions and its history can be lazily reloaded from SQLite.
func TestEvictSession_SubagentStaysListedAndReloadable(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := cronSpawnSessionKey()
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "maintenance run"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "all clean"})
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if !sm.EvictSession(key) {
		t.Fatalf("EvictSession(%q) returned false", key)
	}

	// 1. Still listed (this is what the WebUI cron-spawn view reads).
	listed := findSessionByKey(sm.ListSessions(), key)
	if listed == nil {
		t.Fatal("evicted subagent session disappeared from ListSessions (the WebUI bug)")
	}
	if listed.Messages != nil {
		t.Errorf("listed session should be metadata-only (no messages loaded)")
	}

	// 2. Still reloadable: lazy load must find it from the metadata index.
	hist := sm.GetHistory(key)
	if len(hist) != 2 {
		t.Fatalf("GetHistory after eviction = %d messages, want 2 (session must reload from SQLite)", len(hist))
	}
	if hist[0].Content != "maintenance run" || hist[1].Content != "all clean" {
		t.Errorf("unexpected history after reload: %+v", hist)
	}
}

// TestEvictSession_KeepsSessionMetaMirrorsStore asserts sessionMeta is treated
// as a mirror of the persisted rows: eviction removes residency only. A fresh
// manager over the same store must see exactly the same listing.
func TestEvictSession_KeepsSessionMetaMirrorsStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	subKey := "native:cron-abc:subagent-1"
	chatKey := "native:telegram:42"
	for _, k := range []string{subKey, chatKey} {
		sm.AddFullMessage(k, providers.Message{Role: "user", Content: "hello"})
		if err := sm.Save(k); err != nil {
			t.Fatalf("Save(%q): %v", k, err)
		}
	}

	sm.EvictSession(subKey)
	sm.EvictSession(chatKey)

	sm2 := NewSessionManager()
	sm2.SetStore(s)
	after := map[string]bool{}
	for _, info := range sm2.ListSessions() {
		after[info.Key] = true
	}
	if !after[subKey] {
		t.Errorf("fresh manager must list subagent session persisted by old one")
	}
	if !after[chatKey] {
		t.Errorf("fresh manager must list chat session persisted by old one")
	}
}

// TestSetSummary_NonExistentDoesNotCreate guards against the delegation
// refactor turning read-only/return-early paths into accidental creators:
// SetSummary on an unknown session must stay a no-op.
func TestSetSummary_NonExistentDoesNotCreate(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSummary("ghost:session", "summary")

	sm.mu.RLock()
	_, resident := sm.sessions["ghost:session"]
	_, meta := sm.sessionMeta["ghost:session"]
	sm.mu.RUnlock()

	if resident || meta {
		t.Fatal("SetSummary must not create a non-existent session")
	}
}

// TestGetOrCreate_SetsUpdatedOnCreation documents the unification: the shared
// creation path stamps Updated at creation (previously only GetOrCreate did),
// so metadata-only listings have a valid sort key for every session.
func TestGetOrCreate_SetsUpdatedOnCreation(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:updated-on-create"
	before := time.Now().Add(-time.Second)
	sm.GetOrCreate(key)

	sm.mu.RLock()
	updated := sm.sessions[key].Updated
	metaUpdated := sm.sessionMeta[key].Updated
	sm.mu.RUnlock()

	if !updated.After(before) {
		t.Errorf("session.Updated = %v, want set at creation", updated)
	}
	if !metaUpdated.After(before) {
		t.Errorf("sessionMeta.Updated = %v, want set at creation", metaUpdated)
	}
}
