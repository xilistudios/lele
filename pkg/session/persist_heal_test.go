package session

// R3.1 — the excluded-range save must not swallow its own healing flag.
//
// saveExcludedRangeUnlocked and saveFullUnlocked share one contract: when the
// session is mutated while their I/O is in flight (the epoch guard), the rows
// they wrote may be interleaved with the concurrent save's writes in any order,
// so the DB state is uncertain and only a rewrite from the in-memory source of
// truth re-establishes it. Both signal that with `lastPersistedSeq = -1`, which
// is what makes saveUnlocked pick saveFullUnlocked.
//
// saveUnlocked re-verified the flag after the delete-last step (so a delete that
// lost its epoch still rebuilt the history) but NOT after the excluded-range
// step: the fall-through then reached saveIncrementalUnlocked, which resets
// lastPersistedSeq unconditionally once its own I/O succeeds — the heal was
// silently swallowed. The full rewrite is not decoration: it is what
// re-materializes the evicted prefix from SQLite (LoadMessagesFullBeforeSeq) and
// what deletes every row the in-memory slice no longer contains. The incremental
// patch only rewrites the resident rows, so a row that a concurrent writer
// removed from memory survives it — and nothing forces another Save (the session
// can be evicted as "already durable", and a cold load then resurrects the
// removed message).
//
// The race is driven deterministically: the store wrapper runs the concurrent
// mutation from inside UpdateMessagesExcludedWithJSON — the call the save path
// makes with sm.mu RELEASED — and only then parks, so the stale UPDATE lands
// right after the concurrent writer's state existed and the epoch guard has to
// fire.

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// racingExcludedRangeRepo runs onUpdate inside the excluded-range UPDATE (with
// sm.mu released: that is how the save path calls it), parks there until the
// test releases the gate, and records the calls that identify a full rewrite.
type racingExcludedRangeRepo struct {
	sessionRepo
	gate     *ioGate
	onUpdate func()

	once   sync.Once
	mu     sync.Mutex
	prefix []int // beforeSeq of every LoadMessagesFullBeforeSeq call
	repl   []int // len(rows) of every ReplaceMessages call
}

func (r *racingExcludedRangeRepo) UpdateMessagesExcludedWithJSON(key string, rows []store.MessageRow) error {
	r.once.Do(func() {
		r.onUpdate()   // the concurrent writer, in the unlocked window
		r.gate.enter() // park the stale write until the test releases it
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

// seedHealRaceFixture builds the state the heal is about: a persisted 12-message
// chat whose first 6 messages a first compaction evicted from memory
// (firstInMemorySeq = 6, their rows still live in SQLite), plus a second
// compaction's excluded range pending on the resident slice.
//
// It also appends a streaming assistant turn and persists it, so the "last
// message" the concurrent writer retracts below has a row in SQLite.
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

	// Home the chat the way a first compaction does: exclude + evict the prefix.
	sm.ExcludeOldMessagesFromContext(key, 6)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 6 {
		t.Fatalf("first compaction evicted %d messages, want 6", evicted)
	}

	// The in-flight streaming turn: appended and persisted, so it owns a row.
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "partial streamed answer", Streaming: true})
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after the streaming append: %v", err)
	}

	// Second compaction: flags the older resident messages (a pending excluded
	// range) — the step whose save chain is under test.
	sm.ExcludeOldMessagesFromContext(key, 2)

	firstInMemorySeq, residentMsgs, _, _ := liveSnapshot(t, sm, key)
	return firstInMemorySeq, len(residentMsgs)
}

// TestSaveExcludedRangeEpochMismatchForcesFullRewrite pins the mirrored
// re-verification: the Save that carries the compaction's excluded range AND a
// pending streaming finalization must, when a concurrent writer lands inside the
// excluded-range UPDATE, rebuild the whole history from memory in the same call
// — re-materializing the evicted prefix and dropping the row of the message the
// concurrent writer retracted.
//
// Red-check: deleting the `if session.lastPersistedSeq == -1 { return
// sm.saveFullUnlocked(key) }` re-verification in saveUnlocked makes this test
// fail on every assertion below (no LoadMessagesFullBeforeSeq/ReplaceMessages
// call, 13 persisted rows instead of 12).
func TestSaveExcludedRangeEpochMismatchForcesFullRewrite(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-epoch-heal"
	removedSeq := 12 // the appended streaming turn's row

	firstInMemorySeq, resident := seedHealRaceFixture(t, sm, key)
	if firstInMemorySeq != 6 || resident != 7 {
		t.Fatalf("fixture drifted: firstInMemorySeq=%d resident=%d, want 6/7", firstInMemorySeq, resident)
	}

	// The streaming turn is finalized in place, so this Save carries a pending
	// excluded range AND a pending in-place modification (the shape the routing
	// comment at saveIncrementalUnlocked's fall-through describes). The flush
	// throttle is armed so AppendAssistantChunk does not save on its own.
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
		// The concurrent writer: the agent layer retracting the assistant
		// message it just committed (llm_runner's plain-text-tool-call path).
		// It removes the message from memory only — its row has to be purged by
		// whoever owns the DB state — and it bumps the epoch, which is what
		// makes the excluded-range save lose its claim.
		onUpdate: func() { sm.RemoveLastMessage(key) },
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
	// The mutation is done (it ran before the park) and the stale UPDATE is
	// waiting: release it and let the epoch guard see the mismatch.
	gate.unlock()

	select {
	case err := <-saveErr:
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Save did not finish after the repository was released")
	}

	// (1) The heal ran in THIS save: the evicted prefix was re-materialized and
	// the whole row set was rewritten (ReplaceMessages is the full rewrite's
	// only writer).
	prefixCalls, replCalls := racing.rewriteCalls()
	if len(prefixCalls) != 1 || prefixCalls[0] != firstInMemorySeq {
		t.Errorf("LoadMessagesFullBeforeSeq calls = %v, want one call with beforeSeq=%d: the evicted prefix was never re-materialized", prefixCalls, firstInMemorySeq)
	}
	if len(replCalls) != 1 || replCalls[0] != firstInMemorySeq+resident-1 {
		t.Errorf("ReplaceMessages calls = %v, want one call with %d rows (evicted prefix + residents): the epoch-mismatch heal was swallowed",
			replCalls, firstInMemorySeq+resident-1)
	}

	// (2) …so the DB matches memory exactly: the retracted message's row is gone
	// (the incremental fall-through only rewrites resident rows, so it survives
	// there) and no phantom row appeared.
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

	// (3) "Filas reconstruidas": the evicted prefix is byte-identical and every
	// resident row matches the in-memory message (content and excluded flag).
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

	// (4) Bookkeeping: reconciled by the rewrite, every dirty flag cleared — the
	// session must be fully durable, not carrying a deferred heal.
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

	// (5) Nothing is deferred: the next Save is a clean no-op (no further
	// repository round trip at all). Without the heal it re-enters the
	// delete-last path with a stale watermark.
	if err := sm.Save(key); err != nil {
		t.Fatalf("follow-up Save: %v", err)
	}
	prefixAfter, replAfter := racing.rewriteCalls()
	if len(prefixAfter) != len(prefixCalls) || len(replAfter) != len(replCalls) {
		t.Errorf("the follow-up Save rewrote history again (%v / %v, was %v / %v): the previous save left work pending",
			prefixAfter, replAfter, prefixCalls, replCalls)
	}
}
