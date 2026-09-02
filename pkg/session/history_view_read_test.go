package session

import (
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// GetHistoryView on a resident session must NOT touch the LRU access time.
//
// Rationale: the TUI render loop calls GetHistoryView on every frame while an
// agent streams. If reads touched accessTimes, (a) they would need the
// exclusive write lock (map mutation), serializing every frame behind
// in-flight stream flushes, and (b) a purely-passive reader would pin cold
// sessions in memory forever. Reads now take RLock and skip the touch; an
// idle session read-only can be evicted and simply reloads from disk on the
// next call — cheap and correct.
func TestGetHistoryView_ResidentReadDoesNotTouchLRU(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "tui:chat:lru-read"
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Force the session to look stale in the LRU map, then read it.
	stale := time.Now().Add(-time.Hour)
	sm.mu.Lock()
	sm.accessTimes[key] = stale
	sm.mu.Unlock()

	view := sm.GetHistoryView(key)
	if len(view) != 2 {
		t.Fatalf("GetHistoryView len = %d, want 2", len(view))
	}

	sm.mu.RLock()
	got := sm.accessTimes[key]
	sm.mu.RUnlock()
	if !got.Equal(stale) {
		t.Errorf("resident read mutated accessTimes: got %v, want unchanged %v", got, stale)
	}
}

// Cold reads must still populate the session and refresh access time (the
// disk-load path keeps its original write-lock semantics).
func TestGetHistoryView_ColdReadStillTouches(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "tui:chat:lru-cold"
	sm.AddMessage(key, "user", "hello")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Fresh manager over the same store: session is cold (metadata only),
	// exactly like a TUI process reopening a stored chat.
	sm2 := NewSessionManager()
	sm2.SetStore(s)

	view := sm2.GetHistoryView(key) // cold path → loadSessionFromDisk
	if len(view) != 1 {
		t.Fatalf("cold GetHistoryView len = %d, want 1", len(view))
	}
	sm2.mu.RLock()
	_, resident := sm2.sessions[key]
	_, touched := sm2.accessTimes[key]
	sm2.mu.RUnlock()
	if !resident {
		t.Error("cold read did not make session resident")
	}
	if !touched {
		t.Error("cold read did not record access time (loadSessionFromDisk should)")
	}
}

// Concurrent readers must not serialize behind each other while a writer
// holds the lock only briefly. Uses a slow store-backed flush window: readers
// that needed the write lock (old behavior) would queue behind the flush.
func TestGetHistoryView_ReadersRunDuringStreamFlush(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "tui:chat:readers-during-flush"
	sm.AddMessage(key, "user", "start")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var wg sync.WaitGroup
	// Keep a streaming message alive so writers hold sm.mu across the flush.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sm.AppendAssistantChunk(key, "chunk ")
			}
		}()
	}
	// Readers must complete promptly even while writes/flushes are in flight.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for j := 0; j < 100; j++ {
			sm.GetHistoryView(key)
		}
	}()

	readerDone := make(chan struct{})
	go func() { wg.Wait(); close(readerDone) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("GetHistoryView readers stalled behind streaming writers")
	}
	<-readerDone

	// Final state sanity: history intact.
	if h := sm.GetHistoryView(key); len(h) != 2 {
		t.Fatalf("final history len = %d, want 2 (user + streaming assistant)", len(h))
	}
	_ = providers.Message{}
}
