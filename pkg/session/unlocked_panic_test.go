package session

import (
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

type panicSentinel struct{ method string }

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

func recoveredPanic(fn func()) (recovered interface{}) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

func TestUnlockedKeepsManagerUsableAfterPanic(t *testing.T) {
	t.Run("saveFullUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		forceFullRewrite(sm, key)
		repo.arm("ReplaceMessages")

		assertRecoveredThenUsable(t, sm, repo, key, "ReplaceMessages")
	})

	t.Run("saveExcludedRangeUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

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

		sm.AddFullMessage(key, providers.Message{Role: "user", Content: "appended before the panic"})
		repo.arm("InsertMessages")

		assertRecoveredThenUsable(t, sm, repo, key, "InsertMessages")
	})

	t.Run("saveDeleteLastUnlocked", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		if !sm.RemoveLastMessage(key) {
			t.Fatal("fixture: RemoveLastMessage reported nothing to remove")
		}
		repo.arm("DeleteMessagesFrom")

		assertRecoveredThenUsable(t, sm, repo, key, "DeleteMessagesFrom")
	})

	t.Run("evictExcludedMessages", func(t *testing.T) {
		sm, repo, key := newPanicHarness(t)

		sm.ExcludeOldMessagesFromContext(key, 2)
		repo.arm("UpsertSession", "UpdateFirstInMemorySeq")

		recovered := recoveredPanic(func() { sm.EvictExcludedMessages(key) })
		checkRecovered(t, recovered)
		checkMutexFreeAndUsable(t, sm, repo, key)
	})
}

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
