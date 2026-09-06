package session

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// openStoreAt opens (or creates) a SQLite store at an explicit path so a test
// can close it and reopen it again — the graceful-shutdown scenario SaveAll
// exists for. Cleanup closes the store but tolerates an already-closed handle.
func openStoreAt(t *testing.T, dbPath string) *store.Store {
	t.Helper()
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store %q: %v", dbPath, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// mustParseMsg splits a "role:content" fixture into its two halves.
func mustParseMsg(t *testing.T, fixture string) (role, content string) {
	t.Helper()
	parts := strings.SplitN(fixture, ":", 2)
	if len(parts) != 2 {
		t.Fatalf("bad message fixture %q, want \"role:content\"", fixture)
	}
	return parts[0], parts[1]
}

// saveAllWithin runs fn (a SaveAll call) and fails the test if it does not
// return within limit. A deadlock inside SaveAll would otherwise hang CI
// forever; this turns it into a readable test failure.
func saveAllWithin(t *testing.T, sm *SessionManager, limit time.Duration) (int, int) {
	t.Helper()

	type result struct {
		saved  int
		failed int
	}
	done := make(chan result, 1)
	go func() {
		saved, failed := sm.SaveAll()
		done <- result{saved: saved, failed: failed}
	}()

	select {
	case r := <-done:
		return r.saved, r.failed
	case <-time.After(limit):
		t.Fatalf("SaveAll did not return within %v — deadlock (lock held across disk I/O?)", limit)
		return 0, 0
	}
}

// residentKeysOf returns the session keys currently held in memory.
func residentKeysOf(sm *SessionManager) map[string]bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	res := make(map[string]bool, len(sm.sessions))
	for key := range sm.sessions {
		res[key] = true
	}
	return res
}

func TestSaveAllPersistsResidentSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "saveall.db")
	s := openStoreAt(t, dbPath)

	sm := NewSessionManager()
	sm.SetStore(s)

	// Three sessions, never individually saved: everything lives in RAM only.
	wants := map[string][]string{
		"test:alpha": {"user:call", "assistant:hi", "user:bye"},
		"test:beta":  {"user:hello", "assistant:world"},
		"test:gamma": {"user:only"},
	}
	keys := make([]string, 0, len(wants))
	for key := range wants {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		for _, fixture := range wants[key] {
			role, content := mustParseMsg(t, fixture)
			sm.AddMessage(key, role, content)
		}
	}

	saved, failed := saveAllWithin(t, sm, 30*time.Second)
	if saved != 3 {
		t.Errorf("SaveAll saved = %d, want 3", saved)
	}
	if failed != 0 {
		t.Errorf("SaveAll failed = %d, want 0", failed)
	}

	// Simulate process exit: close the DB and reopen it from scratch.
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	s2 := openStoreAt(t, dbPath)

	sm2 := NewSessionManager()
	sm2.SetStore(s2)
	for _, key := range keys {
		history := sm2.GetHistory(key)
		if len(history) != len(wants[key]) {
			t.Fatalf("session %q: reloaded %d messages, want %d", key, len(history), len(wants[key]))
		}
		for i, fixture := range wants[key] {
			wantRole, wantContent := mustParseMsg(t, fixture)
			if history[i].Role != wantRole || history[i].Content != wantContent {
				t.Errorf("session %q msg %d = (%q, %q), want (%q, %q)",
					key, i, history[i].Role, history[i].Content, wantRole, wantContent)
			}
		}
	}
}

func TestSaveAllNoStoreIsSafe(t *testing.T) {
	sm := NewSessionManager() // deliberately no SetStore

	// In-memory mutations still work without a store.
	sm.AddMessage("nostore:1", "user", "hello")
	sm.AddMessage("nostore:2", "user", "world")

	saved, failed := saveAllWithin(t, sm, 10*time.Second)
	if saved != 0 || failed != 0 {
		t.Errorf("SaveAll with no store = (%d, %d), want (0, 0)", saved, failed)
	}
	if n := len(residentKeysOf(sm)); n != 2 {
		t.Errorf("sessions still resident = %d, want 2 (SaveAll must not drop state)", n)
	}
}

func TestSaveAllEmptyManager(t *testing.T) {
	s := openStoreAt(t, filepath.Join(t.TempDir(), "empty.db"))
	sm := NewSessionManager()
	sm.SetStore(s)

	saved, failed := saveAllWithin(t, sm, 10*time.Second)
	if saved != 0 || failed != 0 {
		t.Errorf("SaveAll on empty manager = (%d, %d), want (0, 0)", saved, failed)
	}
}

func TestSaveAllConcurrentWithAppend(t *testing.T) {
	s := openStoreAt(t, filepath.Join(t.TempDir(), "concurrent.db"))
	sm := NewSessionManager()
	sm.SetStore(s)

	keys := []string{"conc:0", "conc:1", "conc:2", "conc:3"}
	for _, key := range keys {
		sm.AddMessage(key, "user", "seed")
	}

	const rounds = 15
	appenders := len(keys)
	const perAppender = 40

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var appendMu sync.Mutex
	appended := map[string]int{}

	for a := 0; a < appenders; a++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			key := keys[idx]
			for i := 0; i < perAppender; i++ {
				select {
				case <-stop:
					return
				default:
				}
				sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: fmt.Sprintf("m%d", i)})
				appendMu.Lock()
				appended[key]++
				appendMu.Unlock()
			}
		}(a)
	}

	// Hammer SaveAll while the appenders run. Before the snapshot-then-release
	// design this deadlocked: saveUnlocked releases sm.mu for I/O and
	// re-acquires it, which can never happen if the caller holds it.
	for r := 0; r < rounds; r++ {
		saved, failed := saveAllWithin(t, sm, 30*time.Second)
		if failed != 0 {
			t.Fatalf("SaveAll round %d: failed = %d, want 0", r, failed)
		}
		if saved == 0 {
			t.Fatalf("SaveAll round %d: saved = 0, want > 0 (sessions are resident)", r)
		}
	}

	close(stop)
	wg.Wait()

	// Final flush with the writers quiesced, then verify nothing was lost:
	// every appended message must be readable from SQLite.
	if _, failed := saveAllWithin(t, sm, 30*time.Second); failed != 0 {
		t.Fatalf("final SaveAll: failed != 0")
	}
	repo := s.Sessions()
	for _, key := range keys {
		got, err := repo.MessageCount(key)
		if err != nil {
			t.Fatalf("MessageCount(%q): %v", key, err)
		}
		want := 1 + appended[key] // seed + appended
		if got != want {
			t.Errorf("session %q persisted %d messages, want %d", key, got, want)
		}
	}
}

func TestSaveAllSkipsEvicted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "evicted.db")
	s := openStoreAt(t, dbPath)

	sm := NewSessionManager()
	sm.SetStore(s)
	// One resident session max, so creating the third session forces the LRU
	// path to evict the first one (and persist it on the way out).
	sm.SetMaxInMemory(1)
	sm.SetEvictionTTL(0)

	for _, key := range []string{"evict:a", "evict:b", "evict:c"} {
		sm.AddMessage(key, "user", "hello from "+key)
		sm.AddMessage(key, "assistant", "hi")
	}
	if n := sm.CleanupIdleSessions(); n != 0 {
		t.Logf("CleanupIdleSessions evicted %d sessions", n)
	}

	// The LRU path must have dropped exactly the oldest session.
	resident := residentKeysOf(sm)
	if resident["evict:a"] {
		t.Fatalf("fixture broken: evict:a is still resident, nothing was evicted (resident: %v)", resident)
	}
	if len(resident) != 2 || !resident["evict:b"] || !resident["evict:c"] {
		t.Fatalf("fixture broken: resident sessions = %v, want {evict:b evict:c}", resident)
	}

	saved, failed := saveAllWithin(t, sm, 30*time.Second)
	if failed != 0 {
		t.Errorf("SaveAll failed = %d, want 0", failed)
	}
	if saved != 2 {
		t.Errorf("SaveAll saved = %d, want 2 (only the resident sessions)", saved)
	}

	// SaveAll must not resurrect the evicted session into memory.
	if residentKeysOf(sm)["evict:a"] {

		t.Error("SaveAll resurrected evicted session evict:a in memory")
	}

	// ...but its data is durable and readable straight from the store.
	count, err := s.Sessions().MessageCount("evict:a")
	if err != nil {
		t.Fatalf("MessageCount(evict:a): %v", err)
	}
	if count != 2 {
		t.Errorf("evicted session has %d persisted messages, want 2", count)
	}

	// A fresh manager over the same DB (i.e. after a restart) sees it too.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	if history := sm2.GetHistory("evict:a"); len(history) != 2 {
		t.Errorf("fresh manager reloaded %d messages for evict:a, want 2", len(history))
	}
}
