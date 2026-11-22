package session

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

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

func TestAddFullMessage_RegistersSessionMeta(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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

func TestEvictSession_SubagentStaysListedAndReloadable(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := cronSpawnSessionKey()
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "maintenance run"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "all clean"})
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if !sm.EvictSession(key) {
		t.Fatalf("EvictSession(%q) returned false", key)
	}

	listed := findSessionByKey(sm.ListSessions(), key)
	if listed == nil {
		t.Fatal("evicted subagent session disappeared from ListSessions (the WebUI bug)")
	}
	if listed.Messages != nil {
		t.Errorf("listed session should be metadata-only (no messages loaded)")
	}

	hist := sm.GetHistory(key)
	if len(hist) != 2 {
		t.Fatalf("GetHistory after eviction = %d messages, want 2 (session must reload from SQLite)", len(hist))
	}
	if hist[0].Content != "maintenance run" || hist[1].Content != "all clean" {
		t.Errorf("unexpected history after reload: %+v", hist)
	}
}

func TestEvictSession_KeepsSessionMetaMirrorsStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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
	sm2.SetSessionRepo(s.Sessions())
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

func TestGetOrCreate_SetsUpdatedOnCreation(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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
