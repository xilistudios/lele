package session

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestCompactSession_Basic verifies that CompactSession stores the summary,
// excludes all but the last keepCount messages, and increments the compaction
// counter.
func TestCompactSession_Basic(t *testing.T) {
	sm := NewSessionManager()
	key := "test:compact-basic"

	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddFullMessage(key, providers.Message{Role: role, Content: "message " + string(rune('a'+i))})
	}

	if err := sm.CompactSession(key, "summary of earlier work", 3, false); err != nil {
		t.Fatalf("CompactSession returned error: %v", err)
	}

	if got := sm.GetSummary(key); got != "summary of earlier work" {
		t.Errorf("summary = %q, want %q", got, "summary of earlier work")
	}

	session := sm.GetOrCreate(key)
	if len(session.Messages) != 10 {
		t.Fatalf("expected 10 messages retained in storage, got %d", len(session.Messages))
	}
	excluded := countExcluded(session.Messages)
	// Messages 1..6 are excluded (7 kept: index 0 is always preserved plus the
	// last keepCount=3).
	if excluded != 6 {
		t.Errorf("expected 6 excluded messages, got %d", excluded)
	}
	// First message must never be excluded.
	if session.Messages[0].ExcludeFromContext {
		t.Errorf("message[0] should never be excluded")
	}
	// Last keepCount messages must remain in context.
	for i := 7; i < 10; i++ {
		if session.Messages[i].ExcludeFromContext {
			t.Errorf("message[%d] should remain in context", i)
		}
	}
}

// TestCompactSession_KeepCountLargerThanMessages verifies the no-op case: when
// keepCount covers all messages, nothing is excluded and no error is returned.
func TestCompactSession_KeepCountLargerThanMessages(t *testing.T) {
	sm := NewSessionManager()
	key := "test:compact-large-keep"

	for i := 0; i < 4; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "user", Content: "m"})
	}

	if err := sm.CompactSession(key, "summary", 10, false); err != nil {
		t.Fatalf("CompactSession returned error: %v", err)
	}

	session := sm.GetOrCreate(key)
	if n := countExcluded(session.Messages); n != 0 {
		t.Errorf("expected 0 excluded messages, got %d", n)
	}
	if got := sm.GetSummary(key); got != "summary" {
		t.Errorf("summary = %q, want %q", got, "summary")
	}
}

// TestCompactSession_CompactionCount verifies the compaction counter is
// incremented exactly once per call.
func TestCompactSession_CompactionCount(t *testing.T) {
	sm := NewSessionManager()
	key := "test:compact-count"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "m1"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "m2"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "m3"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "m4"})

	before := sm.GetOrCreate(key).CompactionCount
	if err := sm.CompactSession(key, "s1", 2, false); err != nil {
		t.Fatalf("CompactSession returned error: %v", err)
	}
	after := sm.GetOrCreate(key).CompactionCount
	if after != before+1 {
		t.Errorf("compaction count = %d, want %d", after, before+1)
	}
}

// TestCompactSession_EvictWithoutStore verifies that evict=true is a safe no-op
// when there is no persistent store backing the manager (eviction is only
// allowed when SQLite is the source of truth).
func TestCompactSession_EvictWithoutStore(t *testing.T) {
	sm := NewSessionManager()
	key := "test:compact-evict"

	for i := 0; i < 8; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "user", Content: "m"})
	}

	if err := sm.CompactSession(key, "summary", 2, true); err != nil {
		t.Fatalf("CompactSession returned error: %v", err)
	}

	session := sm.GetOrCreate(key)
	// Without a store, eviction must not drop messages permanently.
	if len(session.Messages) != 8 {
		t.Errorf("expected 8 messages retained without store, got %d", len(session.Messages))
	}
}
