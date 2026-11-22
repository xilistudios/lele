package session

import (
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestGetHistoryView_ResidentReadDoesNotTouchLRU(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "tui:chat:lru-read"
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

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

func TestGetHistoryView_ColdReadStillTouches(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "tui:chat:lru-cold"
	sm.AddMessage(key, "user", "hello")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())

	view := sm2.GetHistoryView(key)
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

func TestGetHistoryView_ReadersRunDuringStreamFlush(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "tui:chat:readers-during-flush"
	sm.AddMessage(key, "user", "start")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sm.AppendAssistantChunk(key, "chunk ")
			}
		}()
	}
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

	if h := sm.GetHistoryView(key); len(h) != 2 {
		t.Fatalf("final history len = %d, want 2 (user + streaming assistant)", len(h))
	}
	_ = providers.Message{}
}
