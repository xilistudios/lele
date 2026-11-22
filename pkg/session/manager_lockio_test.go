package session

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

func newTestStoreLockIO(t *testing.T) *store.Store {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveDoesNotHoldLockDuringIO(t *testing.T) {
	s := newTestStoreLockIO(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:lockio"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	big := strings.Repeat("x", 64*1024)
	for i := 0; i < 40; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: big})
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := sm.Save(key); err != nil {
				t.Errorf("Save failed: %v", err)
				return
			}
		}
	}()

	maxWait := time.Duration(0)
	var mu sync.Mutex
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				start := time.Now()
				sm.mu.RLock()
				wait := time.Since(start)
				_ = len(sm.sessions)
				sm.mu.RUnlock()
				mu.Lock()
				if wait > maxWait {
					maxWait = wait
				}
				mu.Unlock()
			}
		}()
	}

	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()

	const limit = 200 * time.Millisecond
	if maxWait > limit {
		t.Errorf("read lock blocked for %v while saving (limit %v) — lock held during disk I/O?", maxWait, limit)
	}
	fmt.Printf("max reader wait during concurrent saves: %v\n", maxWait)
}

func TestConcurrentSavesNeverLoseNewestData(t *testing.T) {
	s := newTestStoreLockIO(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:ordering"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "base"})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sm.AddFullMessage(key, providers.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("msg-%d", n),
			})
			if err := sm.Save(key); err != nil {
				t.Errorf("Save failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if err := sm.Save(key); err != nil {
		t.Fatalf("final Save failed: %v", err)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	sm2.ensureLoaded()
	sm2.mu.Lock()
	session, ok := sm2.loadSessionFromDisk(key)
	sm2.mu.Unlock()
	if !ok {
		t.Fatal("could not reload session from disk — file may be corrupted")
	}
	if len(session.Messages) == 0 {
		t.Fatal("reloaded session has no messages")
	}
	if session.Messages[0].Content != "base" {
		t.Errorf("first message = %q, want %q", session.Messages[0].Content, "base")
	}
}
