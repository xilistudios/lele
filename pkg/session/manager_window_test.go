package session

import (
	"fmt"
	"testing"
)

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

func setupEvictedSession(t *testing.T, key string, total, keepLast int) *SessionManager {
	t.Helper()
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())
	buildEvictedSession(t, sm, key, total, keepLast)
	return sm
}

func TestLoadMessagesWindow_ResidentNoEviction_ReturnsNil(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSessionRepo(newTestStore(t).Sessions())
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
	if len(w.Messages) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(w.Messages))
	}
	if w.FirstSeq != 1 || w.LastSeq != 5 {
		t.Errorf("expected seqs 1..5, got %d..%d", w.FirstSeq, w.LastSeq)
	}
	if want := "msg-01"; w.Messages[0].Content != want {
		t.Errorf("first message content = %q, want %q", w.Messages[0].Content, want)
	}
	if want := "msg-05"; w.Messages[4].Content != want {
		t.Errorf("last message content = %q, want %q", w.Messages[4].Content, want)
	}
	if !w.HasOlder {
		t.Error("expected HasOlder=true (seq 0 remains)")
	}
	if w.HasNewer {
		t.Error("expected HasNewer=false (seq 5 is adjacent to memory at 6)")
	}
	if w.EvictedCount != 6 {
		t.Errorf("EvictedCount = %d, want 6", w.EvictedCount)
	}
	if w.TotalCount != 10 {
		t.Errorf("TotalCount = %d, want 10", w.TotalCount)
	}
}

func TestLoadMessagesWindow_PagingBefore(t *testing.T) {
	key := "test:paging"
	sm := setupEvictedSession(t, key, 10, 3)

	w := sm.LoadMessagesWindow(key, -1, 0, 5)
	if w == nil || w.FirstSeq != 1 {
		t.Fatalf("bad first page: %+v", w)
	}
	w2 := sm.LoadMessagesWindow(key, w.FirstSeq, 0, 5)
	if w2 == nil {
		t.Fatal("expected second page, got nil")
	}
	if len(w2.Messages) != 1 || w2.FirstSeq != 0 || w2.LastSeq != 0 {
		t.Fatalf("expected seqs 0..0, got %+v", w2)
	}
	if w2.Messages[0].Content != "msg-00" {
		t.Errorf("wrong page content: %q", w2.Messages[0].Content)
	}
	if w2.HasOlder {
		t.Error("expected HasOlder=false at transcript start")
	}
	if !w2.HasNewer {
		t.Error("expected HasNewer=true (gap up to memory at seq 6)")
	}
	if w3 := sm.LoadMessagesWindow(key, w2.FirstSeq, 0, 5); w3 != nil {
		t.Fatalf("expected nil third page, got %+v", w3)
	}
}

func TestLoadMessagesWindow_FullyEvictedSession(t *testing.T) {
	key := "test:full-evict"
	sm := setupEvictedSession(t, key, 10, 3)

	sm.mu.Lock()
	delete(sm.sessions, key)
	delete(sm.accessTimes, key)
	sm.mu.Unlock()

	w := sm.LoadMessagesWindow(key, -1, 0, 5)
	if w == nil {
		t.Fatal("expected window for fully evicted session, got nil")
	}
	if len(w.Messages) != 5 || w.FirstSeq != 1 || w.LastSeq != 5 {
		t.Fatalf("expected seqs 1..5, got %+v", w)
	}
	if !w.HasOlder {
		t.Error("expected HasOlder=true")
	}
	if w.HasNewer {
		t.Error("expected HasNewer=false (seq 5 adjacent to boundary 6)")
	}
	if w.EvictedCount != 6 || w.TotalCount != 10 {
		t.Errorf("counts = %d/%d, want 6/10", w.EvictedCount, w.TotalCount)
	}

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
		t.Errorf("expected 2 pages for 6 evicted at limit 5, got %d", pages)
	}

	sm.mu.RLock()
	_, resident := sm.sessions[key]
	sm.mu.RUnlock()
	if resident {
		t.Error("LoadMessagesWindow must not load the session into memory")
	}

	hist := sm.GetHistoryView(key)
	if len(hist) != 4 {
		t.Fatalf("cold load must bring only the tail (4 messages), got %d", len(hist))
	}
}

func TestLoadMessagesWindow_AfterCursor(t *testing.T) {
	key := "test:after"
	sm := setupEvictedSession(t, key, 10, 3)

	w := sm.LoadMessagesWindow(key, -1, 3, 10)
	if w == nil {
		t.Fatal("expected window, got nil")
	}
	if len(w.Messages) != 2 || w.FirstSeq != 4 || w.LastSeq != 5 {
		t.Fatalf("expected seqs 4..5, got %+v", w)
	}
	if !w.HasOlder {
		t.Error("expected HasOlder=true")
	}
	if w.HasNewer {
		t.Error("expected HasNewer=false")
	}

	if w := sm.LoadMessagesWindow(key, -1, 6, 10); w != nil {
		t.Errorf("expected nil for after >= memFloor, got %+v", w)
	}
}

func TestLoadMessagesWindow_LimitClampAndValidation(t *testing.T) {
	key := "test:clamp"
	sm := setupEvictedSession(t, key, 10, 3)

	w := sm.LoadMessagesWindow(key, -1, 0, maxMessagesWindowLimit+100)
	if w == nil || len(w.Messages) != 6 {
		t.Fatalf("expected 6 messages, got %+v", w)
	}
	if w := sm.LoadMessagesWindow(key, -1, 0, 0); w != nil {
		t.Errorf("expected nil for limit=0, got %+v", w)
	}
	if w := sm.LoadMessagesWindow("test:ghost", -1, 0, 10); w != nil {
		t.Errorf("expected nil for unknown session, got %+v", w)
	}
	smNoStore := NewSessionManager()
	if w := smNoStore.LoadMessagesWindow(key, -1, 0, 10); w != nil {
		t.Errorf("expected nil without store, got %+v", w)
	}
}

func TestLoadMessagesWindow_ColdLoadKeepsBoundary(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:cold"
	buildEvictedSession(t, sm, key, 12, 4)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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

	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta.FirstInMemorySeq != 8 {
		t.Errorf("FirstInMemorySeq = %d, want 8", meta.FirstInMemorySeq)
	}
}
