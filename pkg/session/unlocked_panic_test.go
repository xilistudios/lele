package session

// R3.3 — "unlock, run, relock" must be panic-safe.
//
// Every save path releases sm.mu around its I/O and re-acquires it afterwards.
// Three of them run a free function in that window (the SQLite round trip plus
// the JSON encoding):
//
//   - saveFullUnlocked → writeFullUnlocked (persist.go),
//   - saveExcludedRangeUnlocked → writeExcludedRangeUnlocked (persist.go),
//   - EvictExcludedMessages → persistEvictionBoundary (eviction.go),
//
// and three write straight to the repository there:
//
//   - saveMetaOnlyUnlocked → UpsertSession (persist.go),
//   - saveIncrementalUnlocked → UpsertSession + InsertMessages/UpdateMessages,
//   - saveDeleteLastUnlocked → UpsertSession + DeleteMessagesFrom (persist.go).
//
// Written inline as `sm.mu.Unlock(); fn(); sm.mu.Lock()` a panic inside fn — a
// driver throwing on a broken database, a nil map in the payload builder —
// unwinds with the lock already released, so the caller's deferred Unlock trips
// sync's "unlock of unlocked mutex" (an unrecoverable throw, not a panic: it
// kills the process) and masks the original failure even when the panic is
// recovered upstream. The shared unlocked() helper re-acquires the lock while the
// panic unwinds instead.
//
// All six go through the same unlocked() helper. The test asserts the three
// observable consequences: the panic that reaches the caller is the repository's
// own (nothing was thrown while unwinding), the mutex is free afterwards, and the
// manager still works.
//
// Note on the mutation check: reverting one site to the inline form cannot be
// caught gracefully — sync throws on the caller's deferred Unlock, so the test
// binary dies with "fatal error: sync: unlock of unlocked mutex" instead of
// reporting a failure. That is exactly the bug, and it was verified by running
// this test with the helper reverted at one site (the process aborts on the
// subtest that drives it).

import (
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// panicSentinel is the value the fake repository panics with; it carries the
// method name so a subtest can assert the panic came from the expected I/O.
type panicSentinel struct{ method string }

// panickingRepo makes the listed repository methods panic while it is armed.
// Disarmed, it delegates everything to the real repository, so the same manager
// can be used again after the recovery.
type panickingRepo struct {
	sessionRepo
	mu    sync.Mutex
	armed map[string]bool
}

func (r *panickingRepo) arm(methods ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.armed == nil {
		r.armed = make(map[string]bool, len(methods))
	}
	for _, method := range methods {
		r.armed[method] = true
	}
}

func (r *panickingRepo) disarm() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.armed = nil
}

func (r *panickingRepo) check(method string) {
	r.mu.Lock()
	armed := r.armed[method]
	r.mu.Unlock()
	if armed {
		panic(panicSentinel{method: method})
	}
}

func (r *panickingRepo) UpsertSession(meta store.SessionMeta) error {
	r.check("UpsertSession")
	return r.sessionRepo.UpsertSession(meta)
}

func (r *panickingRepo) ReplaceMessages(key string, rows []store.MessageRow) error {
	r.check("ReplaceMessages")
	return r.sessionRepo.ReplaceMessages(key, rows)
}

func (r *panickingRepo) UpdateMessagesExcludedWithJSON(key string, rows []store.MessageRow) error {
	r.check("UpdateMessagesExcludedWithJSON")
	return r.sessionRepo.UpdateMessagesExcludedWithJSON(key, rows)
}

func (r *panickingRepo) UpdateFirstInMemorySeq(key string, seq int) error {
	r.check("UpdateFirstInMemorySeq")
	return r.sessionRepo.UpdateFirstInMemorySeq(key, seq)
}

func (r *panickingRepo) InsertMessages(key string, messages []store.MessageRow) error {
	r.check("InsertMessages")
	return r.sessionRepo.InsertMessages(key, messages)
}

func (r *panickingRepo) DeleteMessagesFrom(key string, fromSeq int) error {
	r.check("DeleteMessagesFrom")
	return r.sessionRepo.DeleteMessagesFrom(key, fromSeq)
}

// recoveredPanic runs fn and returns the value recovered from it (nil when fn
// returned normally).
func recoveredPanic(fn func()) (recovered interface{}) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

// TestUnlockedKeepsManagerUsableAfterPanic is the panic-safety regression test:
// every unlock/relock site panics inside its I/O, and the manager must survive it
// (the panic reaches the caller unchanged and every later operation works).
func TestUnlockedKeepsManagerUsableAfterPanic(t *testing.T) {
	t.Run("saveFullUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		// Route the next Save down the full-rewrite path and make its I/O throw.
		forceFullRewrite(sm, key)
		repo.arm("ReplaceMessages")

		assertRecoveredThenUsable(t, sm, repo, key, "ReplaceMessages")
	})

	t.Run("saveExcludedRangeUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		// A pending excluded range makes the next Save take the targeted UPDATE.
		sm.ExcludeOldMessagesFromContext(key, 2)
		sm.mu.RLock()
		pending := sm.sessions[key].excludedRange
		sm.mu.RUnlock()
		if pending == [2]int{} {
			t.Fatal("fixture: no pending excluded range")
		}
		repo.arm("UpdateMessagesExcludedWithJSON")

		assertRecoveredThenUsable(t, sm, repo, key, "UpdateMessagesExcludedWithJSON")
	})

	t.Run("saveMetaOnlyUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		// A metadata-only mutation persists through saveMetaOnlyUnlocked, which
		// does nothing but UpsertSession with the lock released.
		repo.arm("UpsertSession")

		recovered := recoveredPanic(func() { _ = sm.SetName(key, "renamed-during-the-panic") })
		checkRecovered(t, recovered)
		if p, ok := recovered.(panicSentinel); ok && p.method != "UpsertSession" {
			t.Fatalf("recovered the panic of %s, want UpsertSession", p.method)
		}
		checkMutexFreeAndUsable(t, sm, repo, key)
	})

	t.Run("saveIncrementalUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		// An appended message routes the next Save to the incremental path,
		// whose I/O window holds three consecutive calls: the panic is armed on
		// the second, i.e. after the metadata write already went out.
		sm.AddFullMessage(key, providers.Message{Role: "user", Content: "appended before the panic"})
		repo.arm("InsertMessages")

		assertRecoveredThenUsable(t, sm, repo, key, "InsertMessages")
	})

	t.Run("saveDeleteLastUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		// A removed last message routes the next Save to the watermark delete.
		if !sm.RemoveLastMessage(key) {
			t.Fatal("fixture: RemoveLastMessage reported nothing to remove")
		}
		repo.arm("DeleteMessagesFrom")

		assertRecoveredThenUsable(t, sm, repo, key, "DeleteMessagesFrom")
	})

	t.Run("evictExcludedMessages", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		// The boundary write of the eviction is the unlock/relock site. The fold
		// picks UpsertSession (a non-excluded message sits inside the evicted
		// region), the plain path picks UpdateFirstInMemorySeq: arm both so the
		// panic comes from persistEvictionBoundary either way.
		sm.ExcludeOldMessagesFromContext(key, 2)
		repo.arm("UpsertSession", "UpdateFirstInMemorySeq")

		recovered := recoveredPanic(func() { sm.EvictExcludedMessages(key) })
		checkRecovered(t, recovered)
		checkMutexFreeAndUsable(t, sm, repo, key)
	})
}

// newPanicHarness builds a store-backed manager with a resident 6-message chat
// and the panicking wrapper installed.
func newPanicHarness(t *testing.T) (*SessionManager, *panickingRepo, string) {
	t.Helper()
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:unlocked-panic"

	sm.GetOrCreate(key)
	for i := 0; i < 3; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "user", Content: "panic question"})
		sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "panic answer"})
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	repo := &panickingRepo{sessionRepo: sm.sessionRepo}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return repo }
	t.Cleanup(func() { repoFor = prev })
	return sm, repo, key
}

// assertRecoveredThenUsable runs sm.Save (the operation of the arm-ed site),
// asserts the recovered panic and then that the manager still works.
func assertRecoveredThenUsable(t *testing.T, sm *SessionManager, repo *panickingRepo, key, method string) {
	t.Helper()
	recovered := recoveredPanic(func() {
		if err := sm.Save(key); err != nil {
			t.Errorf("Save returned %v instead of panicking", err)
		}
	})
	checkRecovered(t, recovered)
	if p, ok := recovered.(panicSentinel); ok && p.method != method {
		t.Fatalf("recovered the panic of %s, want %s", p.method, method)
	}
	checkMutexFreeAndUsable(t, sm, repo, key)
}

// checkRecovered asserts that the panic of the panicking repository reached the
// caller: if the lock had leaked, sync's unrecoverable throw would have killed
// the process (or, for a lock left held, the deferred Unlock would never have
// returned), so anything else here is a different failure.
func checkRecovered(t *testing.T, recovered interface{}) {
	t.Helper()
	if recovered == nil {
		t.Fatal("no panic escaped: the panicking repository was never reached")
	}
	sentinel, ok := recovered.(panicSentinel)
	if !ok {
		t.Fatalf("recovered %v (%T), want the repository's panicSentinel", recovered, recovered)
	}
	t.Logf("panic recovered from repository method %s", sentinel.method)
}

// checkMutexFreeAndUsable asserts the manager survived the panic: sm.mu is free
// and the following operations still work (including the very path that panicked,
// now that the repository behaves).
func checkMutexFreeAndUsable(t *testing.T, sm *SessionManager, repo *panickingRepo, key string) {
	t.Helper()
	if !lockIsFreeWithin(&sm.mu, time.Second) {
		t.Fatal("sm.mu is still held after the panic")
	}
	repo.disarm()

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after the panic: %v", err)
	}
	if got := sm.GetHistoryView(key); len(got) == 0 {
		t.Fatal("GetHistoryView returned an empty history after the panic")
	}
	sm.AddMessage(key, "user", "after-panic")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save with a new message after the panic: %v", err)
	}
	if !lockIsFreeWithin(&sm.mu, time.Second) {
		t.Fatal("sm.mu is still held after the post-panic operations")
	}
}
