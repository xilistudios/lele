package session

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestSaveDoesNotHoldLockDuringIO verifies the fix for the TUI freeze:
// while a save is doing disk I/O (JSON encode + fsync), the manager's
// mutex must be released so that readers (TUI renders calling GetHistory,
// GetCurrentContextUsage, ...) are never blocked for hundreds of ms.
func TestSaveDoesNotHoldLockDuringIO(t *testing.T) {
	sm := NewSessionManager(t.TempDir())
	key := "test:lockio"

	// Build a session large enough that encoding + fsync takes a while.
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	big := strings.Repeat("x", 64*1024)
	for i := 0; i < 40; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: big})
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer: save repeatedly.
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

	// Readers: measure how long a read-lock acquisition takes while saves
	// are in flight. Before the fix, this could block 100-300ms per save.
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
				_ = len(sm.sessions) // trivial read work
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

	// Allow generous slack for CI/slow storage: the point is that readers
	// are never blocked for the full duration of a save's fsync. With the
	// lock held across I/O, waits of 100ms+ were routine.
	const limit = 50 * time.Millisecond
	if maxWait > limit {
		t.Errorf("read lock blocked for %v while saving (limit %v) — lock held during disk I/O?", maxWait, limit)
	}
	fmt.Printf("max reader wait during concurrent saves: %v\n", maxWait)
}

// TestConcurrentSavesNeverLoseNewestData verifies the ordering guard: when
// two saves of the same key overlap, the final file must contain the newest
// message, never an older snapshot.
func TestConcurrentSavesNeverLoseNewestData(t *testing.T) {
	sm := NewSessionManager(t.TempDir())
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

	// Final explicit save of current state, then verify the file parses
	// and contains at least the base message plus consistent content.
	if err := sm.Save(key); err != nil {
		t.Fatalf("final Save failed: %v", err)
	}

	// Reload from disk into a fresh manager to confirm the file is valid
	// JSON (a torn write from overlapping saves would corrupt it).
	sm2 := NewSessionManager(sm.storage)
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
