package session

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

var evictionFoldRoles = []string{"user", "assistant", "user", "assistant", "user", "assistant"}

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
	session.lastPersistedSeq = -1
	session.bumpEpoch()
	sm.mu.Unlock()
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save with excluded prefix: %v", err)
	}
	return excluded, total - excluded
}

const (
	boundaryMethodNone int32 = iota
	boundaryMethodTargeted
	boundaryMethodUpsert
)

type evictionBlockingRepo struct {
	sessionRepo
	gate        *ioGate
	blockSeq    bool
	blockUpsert bool
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

func evictAsync(sm *SessionManager, key string) <-chan int {
	done := make(chan int, 1)
	go func() { done <- sm.EvictExcludedMessages(key) }()
	return done
}

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

			if gate.muHeld.Load() {
				t.Fatal("repository I/O ran while sm.mu was held: the write lock must cover collection only")
			}
			if got := fake.method.Load(); got != tc.wantMethod {
				t.Fatalf("eviction issued boundary method %d, want %d (parity: fold → UpsertSession, no fold → UpdateFirstInMemorySeq)", got, tc.wantMethod)
			}

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

			time.Sleep(400 * time.Millisecond)

			gate.unlock()
			if n := awaitEviction(t, done); n != wantEvicted {
				t.Fatalf("evicted %d messages, want %d", n, wantEvicted)
			}

			const maxLockWait = 100 * time.Millisecond
			if observed := watchdog.stopAndWait(); observed > maxLockWait {
				t.Fatalf("a reader waited %v for sm.mu during the eviction (limit %v): the lock is held across the I/O", observed, maxLockWait)
			}

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
	for _, msg := range view {
		if strings.Contains(msg.Content, "turn-0") {
			t.Errorf("view still holds an evicted message: %q", msg.Content)
		}
	}

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

const concurrentMutationAppend = "appended-during-eviction-io"

func TestEvictExcludedHealsConcurrentMutation(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:evict-concurrent"

	wantEvicted, _ := seedTargetedEvictionSession(t, sm, key)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	useBlockingEvictionRepo(t, sm, gate, true, false)

	done := evictAsync(sm, key)
	awaitGate(t, gate, done)
	if gate.muHeld.Load() {
		t.Fatal("repository I/O ran while sm.mu was held")
	}

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
	wantResident := 4 + 2
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

	sm.mu.RLock()
	healed := sm.sessions[key].lastPersistedSeq
	sm.mu.RUnlock()
	if healed != -1 {
		t.Errorf("lastPersistedSeq = %d after a concurrent mutation, want -1 (the eviction must force a healing full rewrite)", healed)
	}

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

	forceFullRewrite(sm, key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("final Save: %v", err)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)

	for i, row := range rows {
		if row.Seq != i {
			t.Fatalf("row %d has seq %d: the persisted seqs must stay contiguous", i, row.Seq)
		}
	}
	if len(rows) < len(resident) {
		t.Fatalf("persisted %d rows for %d resident messages", len(rows), len(resident))
	}
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

func contentsOf(msgs []providers.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, fmt.Sprintf("%s:%q", m.Role, m.Content))
	}
	return out
}

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

type failingBoundaryRepo struct {
	sessionRepo
	err error
}

func (b *failingBoundaryRepo) UpdateFirstInMemorySeq(string, int) error { return b.err }
func (b *failingBoundaryRepo) UpsertSession(store.SessionMeta) error    { return b.err }

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
