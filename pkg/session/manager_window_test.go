package session

import (
	"fmt"
	"testing"
)

// buildEvictedSession creates a session with `total` messages, persists it,
// excludes all but `keepLast` messages from context, saves (persisting the
// flags) and evicts the excluded prefix from memory.
func buildEvictedSession(t *testing.T, sm *SessionManager, key string, total, keepLast int) {
	t.Helper()
	sm.GetOrCreate(key)
	for i := 0; i < total; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%02d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial save failed: %v", err)
	}
	sm.ExcludeOldMessagesFromContext(key, keepLast)
	if err := sm.Save(key); err != nil {
		t.Fatalf("save after exclude failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted == 0 {
		t.Fatal("expected messages to be evicted")
	}
}

// setupEvictedSession returns a manager with its own store holding one
// evicted session (see buildEvictedSession).
func setupEvictedSession(t *testing.T, key string, total, keepLast int) *SessionManager {
	t.Helper()
	sm := NewSessionManager()
	sm.SetStore(newTestStore(t))
	buildEvictedSession(t, sm, key, total, keepLast)
	return sm
}

func TestLoadMessagesWindow_ResidentNoEviction_ReturnsNil(t *testing.T) {
	sm := NewSessionManager()
	sm.SetStore(newTestStore(t))
	key := "test:novict"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	if err := sm.Save(key); err != nil {
		t.Fatalf("save: %v", err)
	}
	if w := sm.LoadMessagesWindow(key, -1, 0, 10); w != nil {
		t.Fatalf("expected nil window for session without eviction, got %+v", w)
	}
}

func TestLoadMessagesWindow_ResidentEvictedPrefix(t *testing.T) {
	key := "test:resident"
	sm := setupEvictedSession(t, key, 10, 3)

	w := sm.LoadMessagesWindow(key, -1, 0, 5)
	if w == nil {
		t.Fatal("expected window, got nil")
	}
	// Evicted region is seq [0,7). Newest 5: seq 2..6, chronological.
	if len(w.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(w.Messages))
	}
	if w.FirstSeq != 2 || w.LastSeq != 6 {
		t.Errorf("expected seqs 2..6, got %d..%d", w.FirstSeq, w.LastSeq)
	}
	if want := "msg-02"; w.Messages[0].Content != want {
		t.Errorf("first message content = %q, want %q", w.Messages[0].Content, want)
	}
	if want := "msg-06"; w.Messages[4].Content != want {
		t.Errorf("last message content = %q, want %q", w.Messages[4].Content, want)
	}
	if !w.HasOlder {
		t.Error("expected HasOlder=true (seq 0,1 remain)")
	}
	if w.HasNewer {
		t.Error("expected HasNewer=false (seq 6 is adjacent to memory at 7)")
	}
	if w.EvictedCount != 7 {
		t.Errorf("EvictedCount = %d, want 7", w.EvictedCount)
	}
	if w.TotalCount != 10 {
		t.Errorf("TotalCount = %d, want 10", w.TotalCount)
	}
}

func TestLoadMessagesWindow_PagingBefore(t *testing.T) {
	key := "test:paging"
	sm := setupEvictedSession(t, key, 10, 3)

	// First page: newest 5 of evicted region [0,7) → seq 2..6
	w := sm.LoadMessagesWindow(key, -1, 0, 5)
	if w == nil || w.FirstSeq != 2 {
		t.Fatalf("bad first page: %+v", w)
	}
	// Second page: before=2 → seq 0,1 chronological
	w2 := sm.LoadMessagesWindow(key, w.FirstSeq, 0, 5)
	if w2 == nil {
		t.Fatal("expected second page, got nil")
	}
	if len(w2.Messages) != 2 || w2.FirstSeq != 0 || w2.LastSeq != 1 {
		t.Fatalf("expected seqs 0..1, got %+v", w2)
	}
	if w2.Messages[0].Content != "msg-00" || w2.Messages[1].Content != "msg-01" {
		t.Errorf("wrong page contents: %q %q", w2.Messages[0].Content, w2.Messages[1].Content)
	}
	if w2.HasOlder {
		t.Error("expected HasOlder=false at transcript start")
	}
	if !w2.HasNewer {
		t.Error("expected HasNewer=true (gap up to memory at seq 7)")
	}
	// Third page: nothing older.
	if w3 := sm.LoadMessagesWindow(key, w2.FirstSeq, 0, 5); w3 != nil {
		t.Fatalf("expected nil third page, got %+v", w3)
	}
}

func TestLoadMessagesWindow_FullyEvictedSession(t *testing.T) {
	key := "test:full-evict"
	sm := setupEvictedSession(t, key, 10, 3)

	// Simulate LRU eviction: drop the session object from memory (its rows
	// were persisted by saveForEviction; seq 7..9 stay in SQLite too).
	sm.mu.Lock()
	delete(sm.sessions, key)
	delete(sm.accessTimes, key)
	sm.mu.Unlock()

	// Non-resident: the window serves the persisted eviction prefix
	// (seq < FirstInMemorySeq=7). The tail 7..9 comes back via the normal
	// cold-load history path, so there is no overlap.
	w := sm.LoadMessagesWindow(key, -1, 0, 5)
	if w == nil {
		t.Fatal("expected window for fully evicted session, got nil")
	}
	if len(w.Messages) != 5 || w.FirstSeq != 2 || w.LastSeq != 6 {
		t.Fatalf("expected seqs 2..6, got %+v", w)
	}
	if !w.HasOlder {
		t.Error("expected HasOlder=true")
	}
	if w.HasNewer {
		t.Error("expected HasNewer=false (seq 6 adjacent to boundary 7)")
	}
	if w.EvictedCount != 7 || w.TotalCount != 10 {
		t.Errorf("counts = %d/%d, want 7/10", w.EvictedCount, w.TotalCount)
	}

	// Page all the way to the beginning.
	cur := w
	pages := 1
	for cur.HasOlder {
		next := sm.LoadMessagesWindow(key, cur.FirstSeq, 0, 5)
		if next == nil {
			t.Fatalf("unexpected nil page after %d pages", pages)
		}
		if next.LastSeq >= cur.FirstSeq {
			t.Fatalf("page overlap: %d >= %d", next.LastSeq, cur.FirstSeq)
		}
		cur = next
		pages++
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}
	}
	if cur.FirstSeq != 0 {
		t.Errorf("last page should reach seq 0, got %d", cur.FirstSeq)
	}
	if pages != 2 {
		t.Errorf("expected 2 pages for 7 evicted at limit 5, got %d", pages)
	}

	// Loading a window must NOT resurrect the session into memory.
	sm.mu.RLock()
	_, resident := sm.sessions[key]
	sm.mu.RUnlock()
	if resident {
		t.Error("LoadMessagesWindow must not load the session into memory")
	}

	// Cold-loading the session afterwards must yield exactly the tail, and
	// combined with the window the transcript is complete and gapless.
	hist := sm.GetHistoryView(key)
	if len(hist) != 3 {
		t.Fatalf("cold load must bring only the tail (3 messages), got %d", len(hist))
	}
}

func TestLoadMessagesWindow_AfterCursor(t *testing.T) {
	key := "test:after"
	sm := setupEvictedSession(t, key, 10, 3)

	// Gap fill after seq 3 within evicted region [0,7): rows 4,5,6.
	w := sm.LoadMessagesWindow(key, -1, 3, 10)
	if w == nil {
		t.Fatal("expected window, got nil")
	}
	if len(w.Messages) != 3 || w.FirstSeq != 4 || w.LastSeq != 6 {
		t.Fatalf("expected seqs 4..6, got %+v", w)
	}
	if !w.HasOlder {
		t.Error("expected HasOlder=true")
	}
	if w.HasNewer {
		t.Error("expected HasNewer=false")
	}

	// after >= memory floor returns nothing (the in-memory tail is served by
	// the regular history endpoints, not the evicted window).
	if w := sm.LoadMessagesWindow(key, -1, 7, 10); w != nil {
		t.Errorf("expected nil for after >= memFloor, got %+v", w)
	}
}

func TestLoadMessagesWindow_LimitClampAndValidation(t *testing.T) {
	key := "test:clamp"
	sm := setupEvictedSession(t, key, 10, 3)

	// limit larger than max is clamped; returns all 7 evicted rows.
	w := sm.LoadMessagesWindow(key, -1, 0, maxMessagesWindowLimit+100)
	if w == nil || len(w.Messages) != 7 {
		t.Fatalf("expected 7 messages, got %+v", w)
	}
	// limit <= 0 rejected.
	if w := sm.LoadMessagesWindow(key, -1, 0, 0); w != nil {
		t.Errorf("expected nil for limit=0, got %+v", w)
	}
	// Unknown session → nil.
	if w := sm.LoadMessagesWindow("test:ghost", -1, 0, 10); w != nil {
		t.Errorf("expected nil for unknown session, got %+v", w)
	}
	// No store → nil.
	smNoStore := NewSessionManager()
	if w := smNoStore.LoadMessagesWindow(key, -1, 0, 10); w != nil {
		t.Errorf("expected nil without store, got %+v", w)
	}
}

func TestLoadMessagesWindow_ColdLoadKeepsBoundary(t *testing.T) {
	// A session saved with an eviction boundary, then loaded cold by a NEW
	// manager, must expose exactly the evicted prefix through the window and
	// keep firstInMemorySeq intact (no RAM inflation).
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "test:cold"
	buildEvictedSession(t, sm, key, 12, 4)

	sm2 := NewSessionManager()
	sm2.SetStore(s)
	// Trigger cold load through the normal history path.
	hist := sm2.GetHistoryView(key)
	if len(hist) != 4 {
		t.Fatalf("cold load must keep only the in-memory tail: got %d messages", len(hist))
	}
	w := sm2.LoadMessagesWindow(key, -1, 0, 50)
	if w == nil {
		t.Fatal("expected window after cold load")
	}
	if len(w.Messages) != 8 {
		t.Fatalf("expected 8 evicted messages, got %d", len(w.Messages))
	}
	if w.Messages[0].Content != "msg-00" || w.Messages[7].Content != "msg-07" {
		t.Errorf("wrong contents: %q..%q", w.Messages[0].Content, w.Messages[7].Content)
	}
	if w.HasOlder || w.HasNewer {
		t.Errorf("full-range page should have no more out-of-memory rows: %+v", w)
	}

	// The store must still hold the boundary (window reads are read-only).
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta.FirstInMemorySeq != 8 {
		t.Errorf("FirstInMemorySeq = %d, want 8", meta.FirstInMemorySeq)
	}
}
