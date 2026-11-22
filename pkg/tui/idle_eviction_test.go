package tui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

func TestTUI_IdleEviction_KeepsChatAndReloadsMessages(t *testing.T) {
	m := newTestModel(t)
	s, err := store.Open(filepath.Join(t.TempDir(), "tui-idle-evict.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	m.sessionMgr.SetSessionRepo(s.Sessions())

	const key = "tui:chat:idle-visible"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "Persisted question about eviction?")
	m.sessionMgr.AddMessage(key, "assistant", "Yes — this reply lives in SQLite.")
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	renderLazyModel(t, m)

	out := m.View()
	if !strings.Contains(out, "Persisted question about eviction?") {
		t.Fatalf("expected resident history in view, got:\n%s", out)
	}

	if !m.sessionMgr.EvictSession(key) {
		t.Fatal("EvictSession returned false")
	}
	m.sessionMgr.SetEvictionTTL(time.Minute)

	m.reloadSessions()
	renderLazyModel(t, m)

	found := false
	for _, listed := range m.visibleSessions {
		if listed.Key == key {
			found = true
			if listed.Name == "" {
				t.Error("visibleSessions entry has empty Name after eviction")
			}
			break
		}
	}
	if !found {
		t.Fatalf("chat %q missing from visibleSessions after idle eviction; list=%v", key, sessionListKeys(m.visibleSessions))
	}

	out = m.View()
	if !strings.Contains(out, "Persisted question about eviction?") {
		t.Fatalf("history not reloaded from SQLite after eviction, view:\n%s", out)
	}
}

func sessionListKeys(sessions []*session.Session) []string {
	keys := make([]string, 0, len(sessions))
	for _, s := range sessions {
		keys = append(keys, s.Key)
	}
	return keys
}
