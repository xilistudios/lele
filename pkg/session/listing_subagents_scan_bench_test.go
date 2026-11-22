package session

import (
	"fmt"
	"testing"
	"time"
)

func TestListSubagentSessionMeta_PrefixScanIsExact(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	parent := "native:prefilter"
	wantKey := recordSubagent(t, sm, parent, "subagent-1", "answer")
	otherKey := recordSubagent(t, sm, "native:prefilter-other", "subagent-2", "answer")
	sm.AddMessage("native:plain-chat", "user", "hello")
	if err := sm.Save("native:plain-chat"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sm.GetOrCreate("native:name-trap")
	if err := sm.SetName("native:name-trap", "parent:subagent-9"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if err := sm.Save("native:name-trap"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rows := sm.ListSubagentSessionMeta([]string{parent})
	if len(rows) != 1 {
		t.Fatalf("listing = %d rows, want 1 (%+v)", len(rows), rows)
	}
	if rows[0].Key != wantKey {
		t.Errorf("Key = %q, want %q", rows[0].Key, wantKey)
	}
	for _, row := range rows {
		if row.Key == otherKey {
			t.Errorf("another parent's subagent leaked: %+v", row)
		}
		if row.Key == "native:plain-chat" || row.Key == "native:name-trap" {
			t.Errorf("a non-subagent session leaked: %+v", row)
		}
	}

	if !sm.EvictSession(wantKey) {
		t.Fatal("EvictSession = false")
	}
	if rows := sm.ListSubagentSessionMeta([]string{parent}); len(rows) != 1 || rows[0].Key != wantKey {
		t.Errorf("cold listing = %+v, want only %q", rows, wantKey)
	}
}

func TestListSubagentSessionMeta_NoSubagentPrefixReturnsEarly(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	recordSubagent(t, sm, "native:early", "subagent-1", "answer")

	if rows := sm.ListSubagentSessionMeta([]string{"native:not-a-parent"}); len(rows) != 0 {
		t.Errorf("listing = %+v, want none", rows)
	}
}

func BenchmarkListSubagentSessionMeta(b *testing.B) {
	sm := NewSessionManager()
	now := time.Now()
	sm.mu.Lock()
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("agent:main:telegram:direct:%d", i)
		sm.sessions[key] = &Session{Key: key, Created: now, Updated: now}
		sm.sessionMeta[key] = &sessionMetadata{Key: key, Created: now, Updated: now}
	}
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("telegram:%d:subagent-%d", 12345, i+1)
		sm.sessions[key] = &Session{Key: key, Created: now, Updated: now, SubagentStatus: "completed"}
		sm.sessionMeta[key] = &sessionMetadata{Key: key, Created: now, Updated: now, SubagentStatus: "completed"}
	}
	sm.mu.Unlock()
	sm.loadOnce.Do(func() {})

	prefixes := []string{
		"agent:main:telegram:direct:12345",
		"agent", "agent:main", "agent:main:telegram", "agent:main:telegram:direct",
		"main:telegram:direct:12345", "telegram:direct:12345", "direct:12345",
		"telegram:12345",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows := sm.ListSubagentSessionMeta(prefixes)
		if len(rows) != 50 {
			b.Fatalf("rows = %d, want 50", len(rows))
		}
	}
}
