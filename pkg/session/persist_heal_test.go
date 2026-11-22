package session

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

type racingExcludedRangeRepo struct {
	sessionRepo
	gate     *ioGate
	onUpdate func()

	once   sync.Once
	mu     sync.Mutex
	prefix []int
	repl   []int
}

func (r *racingExcludedRangeRepo) UpdateMessagesExcludedWithJSON(key string, rows []store.MessageRow) error {
	r.once.Do(func() {
		r.onUpdate()
		r.gate.enter()
	})
	return r.sessionRepo.UpdateMessagesExcludedWithJSON(key, rows)
}

func (r *racingExcludedRangeRepo) LoadMessagesFullBeforeSeq(key string, beforeSeq int) ([]store.MessageRowFull, error) {
	r.mu.Lock()
	r.prefix = append(r.prefix, beforeSeq)
	r.mu.Unlock()
	return r.sessionRepo.LoadMessagesFullBeforeSeq(key, beforeSeq)
}

func (r *racingExcludedRangeRepo) ReplaceMessages(key string, rows []store.MessageRow) error {
	r.mu.Lock()
	r.repl = append(r.repl, len(rows))
	r.mu.Unlock()
	return r.sessionRepo.ReplaceMessages(key, rows)
}

func (r *racingExcludedRangeRepo) rewriteCalls() (prefix []int, repl []int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int(nil), r.prefix...), append([]int(nil), r.repl...)
}

func seedHealRaceFixture(t *testing.T, sm *SessionManager, key string) (firstInMemorySeq, resident int) {
	t.Helper()

	sm.GetOrCreate(key)
	for i := 0; i < 12; i++ {
		role, content := "user", fmt.Sprintf("question %d", i)
		if i%2 == 1 {
			role, content = "assistant", fmt.Sprintf("answer %d", i)
		}
		sm.AddFullMessage(key, providers.Message{Role: role, Content: content})
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 6)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 6 {
		t.Fatalf("first compaction evicted %d messages, want 6", evicted)
	}

	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "partial streamed answer", Streaming: true})
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after the streaming append: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	firstInMemorySeq, residentMsgs, _, _ := liveSnapshot(t, sm, key)
	return firstInMemorySeq, len(residentMsgs)
}

func TestSaveExcludedRangeEpochMismatchForcesFullRewrite(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-epoch-heal"
	removedSeq := 12

	firstInMemorySeq, resident := seedHealRaceFixture(t, sm, key)
	if firstInMemorySeq != 6 || resident != 7 {
		t.Fatalf("fixture drifted: firstInMemorySeq=%d resident=%d, want 6/7", firstInMemorySeq, resident)
	}

	sm.mu.Lock()
	sm.sessions[key].lastStreamFlush = time.Now()
	sm.mu.Unlock()
	sm.AppendAssistantChunk(key, " ...")
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "streamed answer, finalized"})

	prefixBefore := readSessionMessageRows(t, s.DB(), key)
	if len(prefixBefore) != 13 {
		t.Fatalf("precondition: %d persisted rows, want 13 (6 evicted + 6 resident + the streaming row)", len(prefixBefore))
	}
	evictedRowsBefore := prefixBefore[:firstInMemorySeq]
	_, residentBefore, _, _ := liveSnapshot(t, sm, key)
	if len(residentBefore) != 7 {
		t.Fatalf("precondition: %d resident messages, want 7", len(residentBefore))
	}

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	racing := &racingExcludedRangeRepo{
		sessionRepo: sm.sessionRepo,
		gate:        gate,
		onUpdate:    func() { sm.RemoveLastMessage(key) },
	}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return racing }
	t.Cleanup(func() { repoFor = prev })

	saveErr := make(chan error, 1)
	go func() { saveErr <- sm.Save(key) }()

	select {
	case <-gate.entered:
	case err := <-saveErr:
		t.Fatalf("Save returned before touching the repository: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("Save never reached the repository: it is stuck (holding sm.mu?) before its first I/O")
	}
	if gate.muHeld.Load() {
		t.Fatal("repository I/O ran while sm.mu was held: the write lock must cover collection only")
	}
	gate.unlock()

	select {
	case err := <-saveErr:
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Save did not finish after the repository was released")
	}

	prefixCalls, replCalls := racing.rewriteCalls()
	if len(prefixCalls) != 1 || prefixCalls[0] != firstInMemorySeq {
		t.Errorf("LoadMessagesFullBeforeSeq calls = %v, want one call with beforeSeq=%d: the evicted prefix was never re-materialized", prefixCalls, firstInMemorySeq)
	}
	if len(replCalls) != 1 || replCalls[0] != firstInMemorySeq+resident-1 {
		t.Errorf("ReplaceMessages calls = %v, want one call with %d rows (evicted prefix + residents): the epoch-mismatch heal was swallowed",
			replCalls, firstInMemorySeq+resident-1)
	}

	rows := readSessionMessageRows(t, s.DB(), key)
	if len(rows) != firstInMemorySeq+resident-1 {
		t.Fatalf("persisted %d rows, want %d: the row of the concurrently retracted message must be purged by the rewrite",
			len(rows), firstInMemorySeq+resident-1)
	}
	for i, row := range rows {
		if row.Seq != i {
			t.Fatalf("row %d has seq %d: the rewrite must write a contiguous 0..N-1 block", i, row.Seq)
		}
	}
	if _, ok := readPersistedRow(t, s.DB(), key, removedSeq); ok {
		t.Errorf("row seq %d survived: it belongs to the message the concurrent writer removed from memory", removedSeq)
	}

	_, residentNow, _, _ := liveSnapshot(t, sm, key)
	if len(residentNow) != resident-1 {
		t.Fatalf("resident messages = %d, want %d after the concurrent retraction", len(residentNow), resident-1)
	}
	for i, want := range evictedRowsBefore {
		if rows[i] != want {
			t.Errorf("evicted row seq %d changed during the rewrite:\n got %+v\nwant %+v", i, rows[i], want)
		}
	}
	for i, msg := range residentNow {
		row := rows[firstInMemorySeq+i]
		wantJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", i, mErr)
		}
		if row.JSON != string(wantJSON) || row.Excluded != msg.ExcludeFromContext {
			t.Errorf("resident row seq %d does not match memory:\n got %s (excluded=%v)\nwant %s (excluded=%v)",
				firstInMemorySeq+i, row.JSON, row.Excluded, string(wantJSON), msg.ExcludeFromContext)
		}
	}

	sm.mu.RLock()
	session := sm.sessions[key]
	got := struct {
		lastPersisted  int
		firstInMemory  int
		evictedTotal   int
		msgsAppended   int
		modifiedFrom   int
		excludedRange  [2]int
		lastMsgDeleted bool
		deleteFromSeq  int
		metaDirty      bool
	}{session.lastPersistedSeq, session.firstInMemorySeq, session.evictedTotal,
		session.msgsAppended, session.modifiedFrom, session.excludedRange,
		session.lastMsgDeleted, session.deleteFromSeq, session.metaDirty}
	sm.mu.RUnlock()
	if got.lastPersisted != len(residentNow)-1 || got.firstInMemory != firstInMemorySeq {
		t.Errorf("bookkeeping = %+v, want lastPersistedSeq=%d firstInMemorySeq=%d",
			got, len(residentNow)-1, firstInMemorySeq)
	}
	if got.evictedTotal != firstInMemorySeq {
		t.Errorf("evictedTotal = %d, want %d", got.evictedTotal, firstInMemorySeq)
	}
	if got.msgsAppended != 0 || got.modifiedFrom != 0 || got.excludedRange != [2]int{} ||
		got.lastMsgDeleted || got.deleteFromSeq != 0 || got.metaDirty {
		t.Errorf("dirty state survived the healing rewrite: %+v", got)
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("follow-up Save: %v", err)
	}
	prefixAfter, replAfter := racing.rewriteCalls()
	if len(prefixAfter) != len(prefixCalls) || len(replAfter) != len(replCalls) {
		t.Errorf("the follow-up Save rewrote history again (%v / %v, was %v / %v): the previous save left work pending",
			prefixAfter, replAfter, prefixCalls, replCalls)
	}
}
