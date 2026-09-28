package session

// Lock-scope + durability regression tests for the excluded-message eviction
// path (T9).
//
// EvictExcludedMessages is what every compaction calls
// (evict_excluded_from_memory): it drops the excluded prefix from
// session.Messages and persists the new boundary. It used to run its SQLite
// round trip — UpsertSession (fold) or UpdateFirstInMemorySeq (targeted) —
// while holding sm.mu, so every compaction blocked GetHistoryView for the
// duration of the write; that is the "freeze after compacting" symptom this
// change removes.
//
// The tests below pin the three-phase contract (collect under the lock → I/O
// with the lock released → relock + guards), mirroring persist_lock_test.go
// for saveFullUnlocked:
//
//   - TestEvictExcludedDoesNotHoldLockDuringIO: no repository call happens
//     under sm.mu (a blocking fake parks the write and a read-lock probe
//     decides it deterministically), a TUI frame is served while the write is
//     parked, and a lock watchdog bounds the longest reader wait — for BOTH
//     persistence shapes the eviction can take.
//   - TestEvictExcludedPublishesViewUnderLock: the phase-1 mutation already
//     published a fresh snapshot while the I/O is parked (the copy-on-write
//     invariant of view.go) and the served view is exactly the resident suffix.
//   - TestEvictExcludedHealsConcurrentMutation: a mutation landing in the
//     unlocked window is never lost — the epoch guard forces the next Save to
//     rebuild the history from memory — and memory == SQLite afterwards.
//   - TestEvictExcludedConcurrentRacersStayConsistent: racing writers (append +
//     chunk) against a compacting goroutine; -race clean, no marker lost,
//     final memory/SQLite consistent.
//   - TestEvictExcludedPersistsSameBoundary: parity of the persisted boundary
//     (FirstInMemorySeq), the in-memory boundary/totals and the resident suffix
//     with the pre-T9 implementation, plus a cold-load round trip.
//   - TestEvictExcludedBoundaryWriteFailureIsNonFatal: a failing boundary write
//     is logged, the in-memory eviction still succeeds, and the folded summary
//     is retried by the next Save (metaDirty contract).

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// evictionFoldRoles is the alternating turn shape the compaction fixture uses.
// ExcludeOldMessagesFromContext pins index 0 (and the recent human turns), so
// the evicted region contains a preserved, non-excluded hole: the fold path
// runs and the boundary is persisted together with the rewritten summary
// (UpsertSession) instead of the targeted UpdateFirstInMemorySeq.
var evictionFoldRoles = []string{"user", "assistant", "user", "assistant", "user", "assistant"}

// seedPreEvictionSession builds the state EvictExcludedMessages documents as
// its precondition: a persisted session whose excluded prefix is ready to be
// dropped from memory (ExcludeOldMessagesFromContext + Save). It returns the
// eviction length and the resident size the eviction must report.
func seedPreEvictionSession(t *testing.T, sm *SessionManager, key string, roles []string, keep int) (wantEvicted, wantResident int) {
	t.Helper()
	sm.GetOrCreate(key)
	for i, role := range roles {
		sm.AddFullMessage(key, providers.Message{Role: role, Content: fmt.Sprintf("turn-%d", i)})
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	sm.ExcludeOldMessagesFromContext(key, keep)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
	return len(roles) - keep, keep
}

// seedTargetedEvictionSession builds a session whose evicted front contains no
// preserved hole: every message in it is excluded, so nothing is folded into
// the summary and the eviction takes the targeted UpdateFirstInMemorySeq path
// (the shape a cold load / direct call produces: excludeBoundary == 0, so the
// contiguity fallback decides the region).
func seedTargetedEvictionSession(t *testing.T, sm *SessionManager, key string) (wantEvicted, wantResident int) {
	t.Helper()
	const total, excluded = 8, 4
	sm.GetOrCreate(key)
	for i := 0; i < total; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: fmt.Sprintf("turn-%d", i)})
	}
	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session %q not resident", key)
	}
	for i := 0; i < excluded; i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	session.excludeBoundary = 0
	session.lastPersistedSeq = -1 // persist the new flags with a full rewrite
	session.bumpEpoch()
	sm.mu.Unlock()
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save with excluded prefix: %v", err)
	}
	return excluded, total - excluded
}

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// Which boundary statement the eviction issued (recorded by the fake below).
const (
	boundaryMethodNone int32 = iota
	boundaryMethodTargeted
	boundaryMethodUpsert
)

// evictionBlockingRepo parks the eviction boundary write until the test
// releases it, and records whether sm.mu looked held at that moment.
//
// It reuses the ioGate handshake of persist_lock_test.go (T10): entering the
// gate probes sm.mu with TryRLock, which fails deterministically while a writer
// holds it, so an in-lock repository call is detected without racing a timer.
//
// Which of the two metadata statements is parked is configurable: the fake
// wraps the real repository, so parking UpsertSession would also park the
// writes of any concurrent Save (they share that statement), and the
// concurrency tests must be able to write to SQLite during the parked window.
type evictionBlockingRepo struct {
	sessionRepo
	gate        *ioGate
	blockSeq    bool // park UpdateFirstInMemorySeq
	blockUpsert bool // park UpsertSession
	method      atomic.Int32
}

func (b *evictionBlockingRepo) UpdateFirstInMemorySeq(sessionKey string, seq int) error {
	b.method.Store(boundaryMethodTargeted)
	if b.blockSeq {
		b.gate.enter()
	}
	return b.sessionRepo.UpdateFirstInMemorySeq(sessionKey, seq)
}

func (b *evictionBlockingRepo) UpsertSession(meta store.SessionMeta) error {
	b.method.Store(boundaryMethodUpsert)
	if b.blockUpsert {
		b.gate.enter()
	}
	return b.sessionRepo.UpsertSession(meta)
}

// useBlockingEvictionRepo swaps repoFor for the eviction-blocking fake until
// the test finishes.
func useBlockingEvictionRepo(t *testing.T, sm *SessionManager, gate *ioGate, blockSeq, blockUpsert bool) *evictionBlockingRepo {
	t.Helper()
	fake := &evictionBlockingRepo{
		sessionRepo: sm.sessionRepo,
		gate:        gate,
		blockSeq:    blockSeq,
		blockUpsert: blockUpsert,
	}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return fake }
	t.Cleanup(func() { repoFor = prev })
	return fake
}

// evictAsync runs EvictExcludedMessages in its own goroutine and reports the
// evicted count.
func evictAsync(sm *SessionManager, key string) <-chan int {
	done := make(chan int, 1)
	go func() { done <- sm.EvictExcludedMessages(key) }()
	return done
}

// awaitGate waits for the parked boundary write. Reaching the repository is
// itself part of the contract: if the collect phase were still holding sm.mu,
// this call would never arrive.
func awaitGate(t *testing.T, gate *ioGate, done <-chan int) {
	t.Helper()
	select {
	case <-gate.entered:
	case n := <-done:
		t.Fatalf("EvictExcludedMessages returned %d before touching the repository", n)
	case <-time.After(15 * time.Second):
		t.Fatal("EvictExcludedMessages never reached the repository: it is stuck (holding sm.mu?) before its first I/O")
	}
}

// awaitEviction collects the parked eviction after the gate was released.
func awaitEviction(t *testing.T, done <-chan int) int {
	t.Helper()
	select {
	case n := <-done:
		return n
	case <-time.After(30 * time.Second):
		t.Fatal("EvictExcludedMessages did not finish after the repository was released")
		return 0
	}
}

// ---------------------------------------------------------------------------
// (a) the regression test: the boundary write does not run under sm.mu
// ---------------------------------------------------------------------------

func TestEvictExcludedDoesNotHoldLockDuringIO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lock-scope timing test in -short mode")
	}

	cases := []struct {
		name            string
		seed            func(t *testing.T, sm *SessionManager, key string) (int, int)
		blockSeq        bool
		blockUpsert     bool
		wantMethod      int32
		wantEvicted     int
		wantResidentCnt int
	}{
		{
			// The fold rewrites the whole metadata row (summary included), so it
			// is the heavier — and more realistic — of the two writes.
			name: "fold persists the full metadata (UpsertSession)",
			seed: func(t *testing.T, sm *SessionManager, key string) (int, int) {
				return seedPreEvictionSession(t, sm, key, evictionFoldRoles, 2)
			},
			blockUpsert:     true,
			wantMethod:      boundaryMethodUpsert,
			wantEvicted:     4,
			wantResidentCnt: 2,
		},
		{
			name:            "no fold uses the targeted boundary write",
			seed:            seedTargetedEvictionSession,
			blockSeq:        true,
			wantMethod:      boundaryMethodTargeted,
			wantEvicted:     4,
			wantResidentCnt: 4,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			sm := NewSessionManager()
			sm.SetSessionRepo(s.Sessions())
			key := "test:evict-lock-scope"

			wantEvicted, wantResident := tc.seed(t, sm, key)
			if wantEvicted != tc.wantEvicted || wantResident != tc.wantResidentCnt {
				t.Fatalf("fixture drifted: evicted/resident = %d/%d, want %d/%d",
					wantEvicted, wantResident, tc.wantEvicted, tc.wantResidentCnt)
			}

			gate := newIOGate(&sm.mu)
			defer gate.unlock()
			fake := useBlockingEvictionRepo(t, sm, gate, tc.blockSeq, tc.blockUpsert)

			watchdog := startLockWatchdog(&sm.mu)

			done := evictAsync(sm, key)
			awaitGate(t, gate, done)

			// Deterministic half: the repository call is parked right now, so a
			// concurrent read lock must be available immediately. Reintroducing
			// the I/O under sm.mu fails here (TryRLock never succeeds while the
			// writer holds the lock).
			if gate.muHeld.Load() {
				t.Fatal("repository I/O ran while sm.mu was held: the write lock must cover collection only")
			}
			if got := fake.method.Load(); got != tc.wantMethod {
				t.Fatalf("eviction issued boundary method %d, want %d (parity: fold → UpsertSession, no fold → UpdateFirstInMemorySeq)", got, tc.wantMethod)
			}

			// The user-visible property: a TUI frame (GetHistoryView) is served
			// while the eviction is parked in the middle of its write, and it
			// already reflects the trimmed slice.
			viewDone := make(chan int, 1)
			go func() { viewDone <- len(sm.GetHistoryView(key)) }()
			select {
			case n := <-viewDone:
				if n != wantResident {
					t.Fatalf("GetHistoryView returned %d messages, want %d (the evicted prefix must already be gone)", n, wantResident)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("GetHistoryView blocked while an eviction was doing I/O: the lock is held across the I/O")
			}

			// Park long enough that the timing half below has a signal to
			// compare against: with the write inside the lock, a reader waits
			// the whole parked window.
			time.Sleep(400 * time.Millisecond)

			gate.unlock()
			if n := awaitEviction(t, done); n != wantEvicted {
				t.Fatalf("evicted %d messages, want %d", n, wantEvicted)
			}

			const maxLockWait = 100 * time.Millisecond
			if observed := watchdog.stopAndWait(); observed > maxLockWait {
				t.Fatalf("a reader waited %v for sm.mu during the eviction (limit %v): the lock is held across the I/O", observed, maxLockWait)
			}

			// The boundary really landed in SQLite and the resident slice is the
			// kept suffix.
			firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
			if firstInMemorySeq != wantEvicted {
				t.Errorf("firstInMemorySeq = %d, want %d", firstInMemorySeq, wantEvicted)
			}
			if len(resident) != wantResident {
				t.Fatalf("resident messages = %d, want %d", len(resident), wantResident)
			}
			meta, err := s.Sessions().GetSessionMeta(key)
			if err != nil || meta == nil {
				t.Fatalf("GetSessionMeta: %v (meta=%v)", err, meta)
			}
			if meta.FirstInMemorySeq != wantEvicted {
				t.Errorf("persisted FirstInMemorySeq = %d, want %d", meta.FirstInMemorySeq, wantEvicted)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// (d) the copy-on-write snapshot invariant
// ---------------------------------------------------------------------------

// TestEvictExcludedPublishesViewUnderLock pins the invariant view.go rests on:
// publishViewLocked() must run under sm.mu immediately after the slice swap —
// BEFORE the boundary I/O. Moving it after the I/O (or into the free phase-2
// helper) would leave the published view stale for the whole round trip and
// push an O(n) rebuild onto the first reader of every TUI frame.
//
// The observation point is the parked window: phase 1 is over and phase 3 has
// not run, so the snapshot must already belong to the trimmed slice.
func TestEvictExcludedPublishesViewUnderLock(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-view"

	wantEvicted, wantResident := seedPreEvictionSession(t, sm, key, evictionFoldRoles, 2)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	useBlockingEvictionRepo(t, sm, gate, false, true)

	done := evictAsync(sm, key)
	awaitGate(t, gate, done)

	firstInMemorySeq, resident, epoch, snapshotEpoch := liveSnapshot(t, sm, key)
	if epoch != snapshotEpoch {
		t.Fatalf("published view is stale while the boundary write is in flight (snapshot epoch %d, saveEpoch %d): publishViewLocked must run under sm.mu right after the slice swap",
			snapshotEpoch, epoch)
	}
	if firstInMemorySeq != wantEvicted || len(resident) != wantResident {
		t.Fatalf("post-eviction state: firstInMemorySeq=%d resident=%d, want %d/%d",
			firstInMemorySeq, len(resident), wantEvicted, wantResident)
	}

	// The served view is exactly the resident suffix: no excluded prefix, no
	// extras. Read it while the eviction is still parked.
	view := sm.GetHistoryView(key)
	if len(view) != wantResident {
		t.Fatalf("view len = %d, want %d (the evicted prefix must be gone)", len(view), wantResident)
	}
	for i := range view {
		if view[i].Content != resident[i].Content || view[i].ExcludeFromContext != resident[i].ExcludeFromContext {
			t.Errorf("view[%d] = {%q excluded=%v}, want resident {%q excluded=%v}",
				i, view[i].Content, view[i].ExcludeFromContext, resident[i].Content, resident[i].ExcludeFromContext)
		}
		if view[i].ExcludeFromContext {
			t.Errorf("view[%d] exposes an excluded message: %q", i, view[i].Content)
		}
	}
	// The non-vacuous half: the evicted messages really left the view.
	for _, msg := range view {
		if strings.Contains(msg.Content, "turn-0") {
			t.Errorf("view still holds an evicted message: %q", msg.Content)
		}
	}

	// The view handed out is the published snapshot, and it must not be touched
	// by the phase-3 bookkeeping that runs after the gate is released.
	heldCopy := cloneMessages(view)

	gate.unlock()
	if n := awaitEviction(t, done); n != wantEvicted {
		t.Fatalf("evicted %d messages, want %d", n, wantEvicted)
	}
	for i := range heldCopy {
		if view[i].Content != heldCopy[i].Content || view[i].ExcludeFromContext != heldCopy[i].ExcludeFromContext {
			t.Errorf("view[%d] mutated after the eviction returned: %+v (was %+v)", i, view[i], heldCopy[i])
		}
	}
	if snap := snapshotOf(t, sm, key); snap == nil || len(snap.view) != wantResident {
		t.Fatalf("published snapshot after the eviction: %+v, want %d resident messages", snap, wantResident)
	}
}

// ---------------------------------------------------------------------------
// (b) concurrency
// ---------------------------------------------------------------------------

// concurrentMutationAppend is the marker the deterministic window test appends
// while the eviction's boundary write is in flight.
const concurrentMutationAppend = "appended-during-eviction-io"

// TestEvictExcludedHealsConcurrentMutation proves that a mutation landing in the
// unlocked window is never lost, and that the eviction does not claim it was
// persisted.
//
// The eviction only mutates memory (the slice swap) and writes a non-destructive
// boundary statement (UPDATE / idempotent UPSERT), so it is safe to release the
// lock around it — provided the post-I/O bookkeeping is guarded. The guards are
// the same as saveFullUnlocked's: an epoch mismatch forces the next Save to
// rebuild the whole history from memory (lastPersistedSeq == -1), which heals
// any ordering damage between the boundary write and the concurrent save.
func TestEvictExcludedHealsConcurrentMutation(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-concurrent"

	wantEvicted, _ := seedTargetedEvictionSession(t, sm, key)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	// Only the eviction's boundary write is parked: the concurrent mutation
	// below still has to be able to reach SQLite.
	useBlockingEvictionRepo(t, sm, gate, true, false)

	done := evictAsync(sm, key)
	awaitGate(t, gate, done)
	if gate.muHeld.Load() {
		t.Fatal("repository I/O ran while sm.mu was held")
	}

	// Mutations in the window where the lock is released: an append and a
	// streaming chunk.
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: concurrentMutationAppend})
	sm.AppendAssistantChunk(key, "chunk-during-eviction-io")

	gate.unlock()
	if n := awaitEviction(t, done); n != wantEvicted {
		t.Fatalf("evicted %d messages, want %d", n, wantEvicted)
	}

	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
	if firstInMemorySeq != wantEvicted {
		t.Errorf("firstInMemorySeq = %d, want %d", firstInMemorySeq, wantEvicted)
	}
	// The eviction kept its own mutation (the prefix trim) and the concurrent
	// ones: nothing was clobbered by the phase-3 bookkeeping.
	wantResident := 4 + 2 // targeted fixture tail + appended user + streaming message
	if len(resident) != wantResident {
		t.Fatalf("resident messages = %d, want %d (the concurrent mutations must not be lost): %+v",
			len(resident), wantResident, contentsOf(resident))
	}
	if got := resident[4].Content; got != concurrentMutationAppend {
		t.Errorf("resident[4] = %q, want %q", got, concurrentMutationAppend)
	}
	if got := resident[5].Content; !strings.Contains(got, "chunk-during-eviction-io") {
		t.Errorf("resident[5] = %q, want it to carry the streaming chunk", got)
	}

	// Epoch guard: the eviction must NOT claim the concurrent mutation was
	// persisted — it forces a healing full rewrite instead.
	sm.mu.RLock()
	healed := sm.sessions[key].lastPersistedSeq
	sm.mu.RUnlock()
	if healed != -1 {
		t.Errorf("lastPersistedSeq = %d after a concurrent mutation, want -1 (the eviction must force a healing full rewrite)", healed)
	}

	// Behavioural half: the next Save persists exactly what is resident,
	// including the mutation that raced the eviction.
	if err := sm.Save(key); err != nil {
		t.Fatalf("healing Save: %v", err)
	}
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	if len(rows) != firstInMemorySeq+len(resident) {
		t.Fatalf("persisted %d rows, want %d (evicted prefix %d + resident %d)",
			len(rows), firstInMemorySeq+len(resident), firstInMemorySeq, len(resident))
	}
	for i, msg := range resident {
		row := rows[firstInMemorySeq+i]
		wantJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", i, mErr)
		}
		if row.Seq != firstInMemorySeq+i || row.JSON != string(wantJSON) || row.Excluded != msg.ExcludeFromContext {
			t.Errorf("row %d lost the concurrent mutation:\n got %+v\nwant seq=%d json=%s excluded=%v",
				firstInMemorySeq+i, row, firstInMemorySeq+i, string(wantJSON), msg.ExcludeFromContext)
		}
	}
}

// TestEvictExcludedConcurrentRacersStayConsistent races appending writers
// (append + streaming chunk + their own Saves) against a goroutine that keeps
// compacting: exclude → Save → evict. It is the -race companion of the
// deterministic window test above: it must report no data race, must not lose
// any written marker entirely, and must end with memory and SQLite in
// agreement.
func TestEvictExcludedConcurrentRacersStayConsistent(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-racers"

	seedTargetedEvictionSession(t, sm, key)

	const (
		writers   = 4
		perWriter = 6
	)

	var (
		markersMu sync.Mutex
		markers   []string
	)
	var writersWG sync.WaitGroup

	stop := make(chan struct{})
	compactorDone := make(chan struct{})
	go func() {
		defer close(compactorDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			// The documented precondition: the excluded flags are persisted
			// before the eviction drops them from memory.
			sm.ExcludeOldMessagesFromContext(key, 2)
			if err := sm.Save(key); err != nil {
				t.Errorf("compactor Save: %v", err)
				return
			}
			sm.EvictExcludedMessages(key)
		}
	}()

	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < perWriter; i++ {
				marker := fmt.Sprintf("w%d-%d-marker", w, i)
				markersMu.Lock()
				markers = append(markers, marker)
				markersMu.Unlock()
				if (w+i)%2 == 0 {
					sm.AddFullMessage(key, providers.Message{Role: "user", Content: marker})
				} else {
					sm.AppendAssistantChunk(key, marker)
				}
				if err := sm.Save(key); err != nil {
					t.Errorf("writer %d Save: %v", w, err)
					return
				}
			}
		}(w)
	}

	writersWG.Wait()
	close(stop)
	<-compactorDone

	// Flush whatever bookkeeping the racing compactor left pending, then assert
	// the two sources of truth agree. forceFullRewrite only pins the path; the
	// content is what is under test.
	forceFullRewrite(sm, key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("final Save: %v", err)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)

	// Seq contiguity: the persisted history is one dense block.
	for i, row := range rows {
		if row.Seq != i {
			t.Fatalf("row %d has seq %d: the persisted seqs must stay contiguous", i, row.Seq)
		}
	}
	if len(rows) < len(resident) {
		t.Fatalf("persisted %d rows for %d resident messages", len(rows), len(resident))
	}
	// The resident block is the tail of the persisted history, byte for byte.
	for i, msg := range resident {
		row := rows[len(rows)-len(resident)+i]
		wantJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", i, mErr)
		}
		if row.JSON != string(wantJSON) || row.Excluded != msg.ExcludeFromContext {
			t.Errorf("row %d/%d differs from memory:\n got %+v\nwant %s",
				len(rows)-len(resident)+i, len(rows), row, string(wantJSON))
		}
	}
	if firstInMemorySeq > len(rows) {
		t.Errorf("firstInMemorySeq = %d but only %d rows are persisted", firstInMemorySeq, len(rows))
	}

	// No marker may vanish entirely. A marker can legitimately end up in the
	// session summary (a fold of a compacted prefix) instead of in a message
	// row, but it must be somewhere.
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta: %v (meta=%v)", err, meta)
	}
	bodies := make([]string, 0, len(rows))
	for _, row := range rows {
		bodies = append(bodies, row.JSON)
	}
	for _, msg := range resident {
		bodies = append(bodies, msg.Content)
	}
	haystack := strings.Join(bodies, "\n") + "\n" + meta.Summary
	markersMu.Lock()
	all := append([]string(nil), markers...)
	markersMu.Unlock()
	for _, marker := range all {
		if !strings.Contains(haystack, marker) {
			t.Errorf("marker %q was written but is nowhere in memory, SQLite or the summary: a concurrent mutation was lost", marker)
		}
	}
}

// contentsOf renders the resident messages for failure output.
func contentsOf(msgs []providers.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("%s:%q", m.Role, m.Content))
	}
	return out
}

// ---------------------------------------------------------------------------
// (c) parity with the pre-T9 implementation, plus a cold-load round trip
// ---------------------------------------------------------------------------

// TestEvictExcludedPersistsSameBoundary pins the observable state the eviction
// leaves behind, so the T9 split cannot change any of it:
//   - the in-memory boundary (firstInMemorySeq) and the evicted total;
//   - the resident slice: exactly the tail of the pre-eviction history, with
//     `seq = firstInMemorySeq + sliceIndex` and no excluded message left;
//   - the persisted boundary (FirstInMemorySeq) and the folded summary;
//   - a cold load (fresh manager, same store) rebuilding the same state.
func TestEvictExcludedPersistsSameBoundary(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-parity"

	wantEvicted, wantResident := seedPreEvictionSession(t, sm, key, evictionFoldRoles, 2)

	rowsBefore, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq before eviction: %v", err)
	}
	if len(rowsBefore) != len(evictionFoldRoles) {
		t.Fatalf("persisted %d rows before eviction, want %d", len(rowsBefore), len(evictionFoldRoles))
	}

	if got := sm.EvictExcludedMessages(key); got != wantEvicted {
		t.Fatalf("evicted %d messages, want %d", got, wantEvicted)
	}

	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
	if firstInMemorySeq != wantEvicted {
		t.Errorf("in-memory firstInMemorySeq = %d, want %d", firstInMemorySeq, wantEvicted)
	}
	if len(resident) != wantResident {
		t.Fatalf("resident messages = %d, want %d", len(resident), wantResident)
	}
	for i, msg := range resident {
		if want := fmt.Sprintf("turn-%d", wantEvicted+i); msg.Content != want {
			t.Errorf("resident[%d].Content = %q, want %q (the resident slice must be the tail at the boundary)", i, msg.Content, want)
		}
		if msg.ExcludeFromContext {
			t.Errorf("resident[%d] is excluded: the kept tail must stay in context", i)
		}
	}

	sm.mu.RLock()
	evictedTotal := sm.sessions[key].evictedTotal
	sm.mu.RUnlock()
	if evictedTotal != wantEvicted {
		t.Errorf("in-memory evictedTotal = %d, want %d", evictedTotal, wantEvicted)
	}

	// Persisted parity: same boundary, and (fold path) the same summary carrying
	// the pinned hole that left memory.
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta: %v (meta=%v)", err, meta)
	}
	if meta.FirstInMemorySeq != wantEvicted {
		t.Errorf("persisted FirstInMemorySeq = %d, want %d", meta.FirstInMemorySeq, wantEvicted)
	}
	if !strings.Contains(meta.Summary, "turn-0") {
		t.Errorf("persisted summary lost the folded hole: %q", meta.Summary)
	}

	// Cold-load round trip: a fresh manager must rebuild the identical state.
	rowsAfter, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq after eviction: %v", err)
	}
	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	sm2.GetOrCreate(key)

	coldBoundary, coldResident, _, _ := liveSnapshot(t, sm2, key)
	if coldBoundary != wantEvicted {
		t.Errorf("cold-load firstInMemorySeq = %d, want %d", coldBoundary, wantEvicted)
	}
	if len(coldResident) != wantResident {
		t.Fatalf("cold-load resident messages = %d, want %d", len(coldResident), wantResident)
	}
	for i := range coldResident {
		if coldResident[i].Content != resident[i].Content ||
			coldResident[i].Role != resident[i].Role ||
			coldResident[i].ExcludeFromContext != resident[i].ExcludeFromContext {
			t.Errorf("cold-load resident[%d] = {%s %q excluded=%v}, want {%s %q excluded=%v}",
				i, coldResident[i].Role, coldResident[i].Content, coldResident[i].ExcludeFromContext,
				resident[i].Role, resident[i].Content, resident[i].ExcludeFromContext)
		}
	}
	sm2.mu.RLock()
	coldTotal := sm2.sessions[key].evictedTotal
	sm2.mu.RUnlock()
	if wantTotal := len(rowsAfter) - wantResident; coldTotal != wantTotal {
		t.Errorf("cold-load evictedTotal = %d, want %d (persisted rows %d - resident %d)",
			coldTotal, wantTotal, len(rowsAfter), wantResident)
	}
}

// ---------------------------------------------------------------------------
// error path: the boundary write is best-effort
// ---------------------------------------------------------------------------

// failingBoundaryRepo fails every metadata write, so the test can pin the
// "logged, not fatal" contract of the eviction's persistence phase.
type failingBoundaryRepo struct {
	sessionRepo
	err error
}

func (b *failingBoundaryRepo) UpdateFirstInMemorySeq(string, int) error { return b.err }
func (b *failingBoundaryRepo) UpsertSession(store.SessionMeta) error    { return b.err }

// TestEvictExcludedBoundaryWriteFailureIsNonFatal: a failed boundary write must
// not fail the eviction (the in-memory eviction is the operation; the write only
// makes the boundary durable), and when the fold rewrote the summary the retry
// must be armed via metaDirty so the next Save persists it — the pre-T9
// semantics, unchanged by the phase split.
func TestEvictExcludedBoundaryWriteFailureIsNonFatal(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-write-failure"

	wantEvicted, wantResident := seedPreEvictionSession(t, sm, key, evictionFoldRoles, 2)

	boundaryErr := fmt.Errorf("boom: sqlite is unavailable")
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo {
		return &failingBoundaryRepo{sessionRepo: sm.sessionRepo, err: boundaryErr}
	}

	if got := sm.EvictExcludedMessages(key); got != wantEvicted {
		repoFor = prev
		t.Fatalf("evicted %d messages, want %d: a failed boundary write must not fail the eviction", got, wantEvicted)
	}
	repoFor = prev

	_, resident, _, _ := liveSnapshot(t, sm, key)
	if len(resident) != wantResident {
		t.Fatalf("resident messages = %d, want %d: the failed write must not undo the in-memory eviction", len(resident), wantResident)
	}

	// The boundary was NOT persisted, but the retry is armed.
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta != nil && meta.FirstInMemorySeq == wantEvicted {
		t.Error("boundary was persisted even though every metadata write failed")
	}
	sm.mu.RLock()
	metaDirty := sm.sessions[key].metaDirty
	sm.mu.RUnlock()
	if !metaDirty {
		t.Error("metaDirty is not set after a failed folded-summary write: the next Save would silently drop the folded text")
	}

	// The retry: a Save with a working repository persists the folded summary
	// and the boundary.
	if err := sm.Save(key); err != nil {
		t.Fatalf("retry Save: %v", err)
	}
	meta, err = s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta after retry: %v (meta=%v)", err, meta)
	}
	if meta.FirstInMemorySeq != wantEvicted {
		t.Errorf("persisted FirstInMemorySeq after retry = %d, want %d", meta.FirstInMemorySeq, wantEvicted)
	}
	if !strings.Contains(meta.Summary, "turn-0") {
		t.Errorf("the folded summary was not persisted by the retry: %q", meta.Summary)
	}
}
