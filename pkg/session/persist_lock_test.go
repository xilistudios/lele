package session

// Lock-scope regression tests for the full-rewrite persistence path (T10).
//
// saveFullUnlocked is the path that runs right after every compaction
// (firstInMemorySeq > 0) and inside the LRU-eviction cascades. It used to do
// the evicted-prefix SQLite read, a json.Unmarshal per evicted row and a
// json.Marshal per resident message while holding sm.mu, which froze every
// concurrent GetHistoryView (a TUI frame) for the whole duration.
//
// The tests below pin the new contract:
//   - TestSaveFullDoesNotHoldLockDuringIO: no I/O and no marshalling happens
//     under sm.mu (a blocking fake repository parks the save mid-flight and a
//     lock watchdog measures the longest read-lock wait any reader suffered).
//   - TestSaveFullConcurrentMutationsNoLostUpdate: concurrent appends during
//     overlapping full rewrites are never lost (epoch guard), -race clean.
//   - TestSaveFullPersistedBytesParity: the rows written are byte-identical to
//     the ones the pre-T10 implementation wrote (same seqs, roles, JSON and
//     excluded flags), plus a round-trip check.

import (
	"database/sql"
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
// Test doubles / helpers
// ---------------------------------------------------------------------------

// blockingRepo is a sessionRepo that parks the full-save I/O until the test
// releases it, and records whether sm.mu looked held when the call was made.
// The embedded interface delegates everything to the real repository.
type blockingRepo struct {
	sessionRepo
	gate *ioGate
}

func (b *blockingRepo) LoadMessagesFullBeforeSeq(sessionKey string, beforeSeq int) ([]store.MessageRowFull, error) {
	b.gate.enter()
	return b.sessionRepo.LoadMessagesFullBeforeSeq(sessionKey, beforeSeq)
}

func (b *blockingRepo) UpsertSession(meta store.SessionMeta) error {
	b.gate.enter()
	return b.sessionRepo.UpsertSession(meta)
}

func (b *blockingRepo) ReplaceMessages(sessionKey string, messages []store.MessageRow) error {
	b.gate.enter()
	return b.sessionRepo.ReplaceMessages(sessionKey, messages)
}

func (b *blockingRepo) PruneExcluded(sessionKey string, keepCount int) (int, error) {
	b.gate.enter()
	return b.sessionRepo.PruneExcluded(sessionKey, keepCount)
}

// ioGate is the parking handshake between the fake repository and the test.
type ioGate struct {
	entered  chan struct{} // closed when the first repository call arrives
	release  chan struct{} // closed by the test to let calls through
	firstOne sync.Once
	releaseO sync.Once
	mu       *sync.RWMutex // the manager lock under test
	muHeld   atomic.Bool   // a repository call ran while sm.mu was held
}

func newIOGate(mu *sync.RWMutex) *ioGate {
	return &ioGate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		mu:      mu,
	}
}

// enter records the arrival, checks that sm.mu is NOT held at this moment and
// waits for the test to release it. A write lock held by the calling goroutine
// makes TryRLock fail forever, so an in-lock repository call is detected
// deterministically instead of by timing.
func (g *ioGate) enter() {
	// Probe BEFORE signalling: the test reads muHeld as soon as `entered` is
	// closed, so the flag has to be decided first (the channel close gives the
	// happens-before edge). The probe is instant when the lock is free.
	if !lockIsFreeWithin(g.mu, 250*time.Millisecond) {
		g.muHeld.Store(true)
	}
	g.firstOne.Do(func() { close(g.entered) })
	<-g.release
}

// unlock lets every parked (and future) repository call through.
func (g *ioGate) unlock() { g.releaseO.Do(func() { close(g.release) }) }

// useBlockingRepo swaps repoFor for a blocking fake wrapping the real
// repository, and restores it when the test finishes.
func useBlockingRepo(t *testing.T, sm *SessionManager, gate *ioGate) {
	t.Helper()
	fake := &blockingRepo{sessionRepo: sm.sessionRepo, gate: gate}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return fake }
	t.Cleanup(func() { repoFor = prev })
}

// lockIsFreeWithin reports whether sm.mu can be read-locked before timeout.
func lockIsFreeWithin(mu *sync.RWMutex, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if mu.TryRLock() {
			mu.RUnlock()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// lockWaitWatchdog stands in for the TUI render loop: it hammers sm.mu with
// read locks and remembers the longest wait it ever had to endure. Any save
// path that holds sm.mu across real work shows up as a long wait here.
type lockWaitWatchdog struct {
	maxWait atomic.Int64 // nanoseconds
	stop    chan struct{}
	done    chan struct{}
}

func startLockWatchdog(mu *sync.RWMutex) *lockWaitWatchdog {
	w := &lockWaitWatchdog{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		for {
			select {
			case <-w.stop:
				return
			default:
			}
			start := time.Now()
			mu.RLock()
			wait := int64(time.Since(start))
			mu.RUnlock()
			if wait > w.maxWait.Load() {
				w.maxWait.Store(wait)
			}
		}
	}()
	return w
}

func (w *lockWaitWatchdog) stopAndWait() time.Duration {
	close(w.stop)
	<-w.done
	return time.Duration(w.maxWait.Load())
}

// liveSnapshot reads, under the read lock, the session's eviction boundary and
// a private copy of its resident messages. Everything it returns is owned by
// the caller, so it is race-free.
func liveSnapshot(t *testing.T, sm *SessionManager, key string) (firstInMemorySeq int, msgs []providers.Message, epoch, snapshotEpoch uint64) {
	t.Helper()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[key]
	if !ok {
		t.Fatalf("session %q is not resident", key)
	}
	msgs = make([]providers.Message, len(session.Messages))
	copy(msgs, session.Messages)
	epoch = session.saveEpoch
	if snap := session.viewSnapshot.Load(); snap != nil {
		snapshotEpoch = snap.epoch
	}
	return session.firstInMemorySeq, msgs, epoch, snapshotEpoch
}

// forceFullRewrite simulates the state compaction leaves behind: a resident
// session that must be rewritten from scratch on the next Save
// (lastPersistedSeq == -1 is what TruncateHistory/SetHistory set). A no-op for
// a non-resident session; callers assert residency where it matters.
func forceFullRewrite(sm *SessionManager, key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[key]; ok {
		session.lastPersistedSeq = -1
	}
}

// seedPostCompactionSession builds the fixture the reported freezes came from:
// a persisted conversation whose excluded prefix was evicted from memory
// (firstInMemorySeq > 0, the evicted rows still live in SQLite) and whose
// resident tail is still in RAM. Returns the number of evicted rows.
func seedPostCompactionSession(t *testing.T, sm *SessionManager, key string) int {
	t.Helper()
	sm.GetOrCreate(key)
	// index 0 is pinned by compaction, so build a few turns it can exclude.
	roles := []string{"user", "assistant", "user", "assistant", "user", "assistant"}
	for i, role := range roles {
		sm.AddFullMessage(key, providers.Message{Role: role, Content: fmt.Sprintf("turn-%d", i)})
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	const keepCount = 2
	sm.ExcludeOldMessagesFromContext(key, keepCount)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after excluding: %v", err)
	}
	evicted := sm.EvictExcludedMessages(key)
	if evicted != len(roles)-keepCount {
		t.Fatalf("evicted %d messages, want %d", evicted, len(roles)-keepCount)
	}
	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
	if firstInMemorySeq != evicted || len(resident) != keepCount {
		t.Fatalf("post-eviction state: firstInMemorySeq=%d resident=%d, want %d/%d",
			firstInMemorySeq, len(resident), evicted, keepCount)
	}
	return evicted
}

// ---------------------------------------------------------------------------
// (a) the regression test: no I/O, no marshalling under sm.mu
// ---------------------------------------------------------------------------

func TestSaveFullDoesNotHoldLockDuringIO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lock-scope timing test in -short mode")
	}
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:full-lock-scope"

	evicted := seedPostCompactionSession(t, sm, key)

	// Resident payload big enough that a marshal performed under sm.mu would
	// hold the lock for well over maxLockWait: measured throughput is ~630 MB/s
	// for these messages, so 78 MB of JSON is ~125 ms of lock hold.
	fat := make([]providers.Message, 300)
	blob := strings.Repeat("x", 256*1024)
	for i := range fat {
		fat[i] = providers.Message{Role: "assistant", Content: blob, ToolCallID: fmt.Sprintf("call-%d", i)}
	}
	sm.SetHistory(key, fat)

	// SetHistory publishes a fresh view snapshot, so the collect phase is a
	// pure map/struct read (no O(n) copy under the lock). Assert it, because
	// the measurement below depends on it.
	_, resident, epoch, snapshotEpoch := liveSnapshot(t, sm, key)
	if epoch != snapshotEpoch {
		t.Fatalf("view snapshot is stale (epoch %d vs saveEpoch %d): the collect phase would copy under the lock",
			snapshotEpoch, epoch)
	}
	if len(resident) != len(fat) {
		t.Fatalf("resident messages = %d, want %d", len(resident), len(fat))
	}
	forceFullRewrite(sm, key)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	useBlockingRepo(t, sm, gate)

	watchdog := startLockWatchdog(&sm.mu)

	saveErr := make(chan error, 1)
	go func() { saveErr <- sm.Save(key) }()

	// The save must reach the repository without holding sm.mu: if it does the
	// repository work under the lock it still gets here, but the gate flag
	// below will be set and the watchdog will have recorded a long wait.
	select {
	case <-gate.entered:
	case err := <-saveErr:
		t.Fatalf("Save returned before touching the repository: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("Save never reached the repository: it is stuck (holding sm.mu?) before its first I/O")
	}

	// Deterministic half: the repository call is parked right now, so a
	// concurrent read lock must be available immediately. Reintroducing I/O
	// under sm.mu fails here (TryRLock never succeeds while the writer holds
	// the lock).
	if gate.muHeld.Load() {
		t.Fatal("repository I/O ran while sm.mu was held: the write lock must cover collection only")
	}

	// The user-visible property: a TUI frame (GetHistoryView) is served while
	// the save is parked in the middle of its I/O.
	viewDone := make(chan int, 1)
	go func() { viewDone <- len(sm.GetHistoryView(key)) }()
	select {
	case n := <-viewDone:
		if n != len(fat) {
			t.Fatalf("GetHistoryView returned %d messages, want %d", n, len(fat))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetHistoryView blocked while a full save was doing I/O: the lock is held across the I/O")
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

	// Timing half: the watchdog covered the whole save, including the
	// marshalling of the resident messages. With the marshalling under the
	// lock its longest wait is ~125 ms; outside the lock it is scheduling
	// noise. maxLockWait sits between the two with margin on both sides.
	const maxLockWait = 60 * time.Millisecond
	observed := watchdog.stopAndWait()
	t.Logf("longest read-lock wait during the full save: %v (limit %v)", observed, maxLockWait)
	if observed > maxLockWait {
		t.Fatalf("a reader waited %v for sm.mu during a full save (limit %v): the lock is held across the collect/marshal phase", observed, maxLockWait)
	}

	// Sanity check: the rewrite really did persist the evicted prefix plus the
	// resident messages.
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	if len(rows) != evicted+len(fat) {
		t.Fatalf("persisted %d rows, want %d (evicted prefix %d + resident %d)", len(rows), evicted+len(fat), evicted, len(fat))
	}
}

// ---------------------------------------------------------------------------
// (b) concurrent mutations during full rewrites: no lost update
// ---------------------------------------------------------------------------

func TestSaveFullConcurrentMutationsNoLostUpdate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:full-concurrent"

	evicted := seedPostCompactionSession(t, sm, key)
	sm.SetHistory(key, []providers.Message{
		{Role: "user", Content: "concurrent base"},
		{Role: "assistant", Content: "ready"},
	})
	forceFullRewrite(sm, key)

	const (
		writers      = 8
		perWriter    = 20
		firstAppend  = "w%d-%d"
		expectedMsgs = writers * perWriter
	)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// Every round forces the destructive full-rewrite path, so the
				// overlapping writers race on ReplaceMessages as hard as the
				// post-compaction path does in production.
				forceFullRewrite(sm, key)
				sm.AddFullMessage(key, providers.Message{
					Role:    "assistant",
					Content: fmt.Sprintf(firstAppend, w, i),
				})
				if err := sm.Save(key); err != nil {
					t.Errorf("Save failed: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Final flush and the assertion: whatever is resident now must be on disk.
	forceFullRewrite(sm, key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("final Save: %v", err)
	}

	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
	if len(resident) != expectedMsgs+2 {
		t.Fatalf("resident messages = %d, want %d (no mutation may be dropped)", len(resident), expectedMsgs+2)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	if len(rows) != evicted+len(resident) {
		t.Fatalf("persisted %d rows, want %d (evicted prefix %d + resident %d)",
			len(rows), evicted+len(resident), evicted, len(resident))
	}
	if firstInMemorySeq != evicted {
		t.Fatalf("firstInMemorySeq = %d, want %d", firstInMemorySeq, evicted)
	}
	for i, msg := range resident {
		row := rows[evicted+i]
		wantJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", i, mErr)
		}
		if row.Seq != evicted+i {
			t.Fatalf("row %d has seq %d, want %d", evicted+i, row.Seq, evicted+i)
		}
		if row.JSON != string(wantJSON) {
			t.Fatalf("row %d lost its update: persisted %q, resident %q", evicted+i, row.JSON, string(wantJSON))
		}
		if row.Excluded != msg.ExcludeFromContext {
			t.Fatalf("row %d excluded = %v, want %v", evicted+i, row.Excluded, msg.ExcludeFromContext)
		}
	}
}

// ---------------------------------------------------------------------------
// (c) byte parity with the pre-T10 implementation
// ---------------------------------------------------------------------------

// TestSaveFullPersistedBytesParity pins the exact bytes a full rewrite leaves
// in SQLite. The expected rows are rebuilt the way the pre-T10 implementation
// built them inline: the evicted prefix is carried over verbatim from SQLite
// (role decoded from the stored JSON, excluded column preserved, seqs re-based
// from 0) and each resident message is re-marshalled. The comparison is
// column-by-column against a direct SELECT, so a change in json output,
// ordering, seq re-basing or the excluded flag fails here.
func TestSaveFullPersistedBytesParity(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:full-parity"

	evicted := seedPostCompactionSession(t, sm, key)

	rich := []providers.Message{
		{Role: "user", Content: "quotes \" backslash \\ newline \n tab \t <tag> & ünïcode"},
		{
			Role:    "assistant",
			Content: "",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "look"},
				{Type: "image_url", ImageURL: &providers.ImageURL{URL: "data:image/png;base64,AAAA", Detail: "high"}},
			},
			ToolCalls: []providers.ToolCall{{ID: "call-1", Type: "function", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/a"}`}}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "file contents", ExcludeFromContext: true},
		{
			Role:           "user",
			Content:        "expanded prompt",
			DisplayContent: "/cmd args",
			Command:        &providers.CommandApplied{Name: "cmd", Args: "args", Source: "user"},
			Attachments:    []providers.MessageAttachment{{Name: "a.txt", Path: "/tmp/a.txt", MIMEType: "text/plain", Kind: "file", Caption: "cap"}},
			Media:          []string{"/tmp/a.txt"},
			Streaming:      false,
		},
		{Role: "assistant", Content: "done", ReasoningContent: "because"},
	}
	sm.SetHistory(key, rich)

	// Expected prefix rows: exactly what the old implementation re-materialized.
	prefix, err := s.Sessions().LoadMessagesFullBeforeSeq(key, evicted)
	if err != nil {
		t.Fatalf("LoadMessagesFullBeforeSeq: %v", err)
	}
	if len(prefix) != evicted {
		t.Fatalf("prefix rows = %d, want %d", len(prefix), evicted)
	}
	want := make([]store.MessageRow, 0, evicted+len(rich))
	for i, row := range prefix {
		var evt providers.Message
		if uErr := json.Unmarshal([]byte(row.JSON), &evt); uErr != nil {
			t.Fatalf("unmarshal prefix row %d: %v", i, uErr)
		}
		want = append(want, store.MessageRow{Seq: i, Role: evt.Role, JSON: row.JSON, Excluded: row.Excluded})
	}
	for i, msg := range rich {
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", i, mErr)
		}
		want = append(want, store.MessageRow{
			Seq:      evicted + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	forceFullRewrite(sm, key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := readSessionMessageRows(t, s.DB(), key)
	if len(got) != len(want) {
		t.Fatalf("persisted %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d differs from the pre-T10 output:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}

	// Round-trip: the stored JSON must decode back to the resident messages.
	// The comparison zeroes ContentParts/Media first: json.Marshal on a VALUE
	// never reaches Message's pointer-receiver MarshalJSON (encoding/json only
	// uses it for addressable pointers), so those two fields are persisted
	// through their struct tags and UnmarshalJSON — deliberately, pre-existing
	// behaviour — does not read them back. The byte-parity check above still
	// covers their exact stored form.
	cold := make([]providers.Message, 0, len(rich))
	for i := range rich {
		var msg providers.Message
		if uErr := json.Unmarshal([]byte(got[evicted+i].JSON), &msg); uErr != nil {
			t.Fatalf("round-trip unmarshal row %d: %v", evicted+i, uErr)
		}
		cold = append(cold, msg)
	}
	if len(cold) != len(rich) {
		t.Fatalf("round-trip produced %d messages, want %d", len(cold), len(rich))
	}
	for i := range rich {
		msgJSON, _ := json.Marshal(storageComparable(rich[i]))
		coldJSON, _ := json.Marshal(storageComparable(cold[i]))
		if string(msgJSON) != string(coldJSON) {
			t.Errorf("round-trip mismatch at %d:\n got %s\nwant %s", i, coldJSON, msgJSON)
		}
	}
}

// storageComparable drops the fields the storage encoding does not round-trip
// (see the round-trip comment above).
func storageComparable(m providers.Message) providers.Message {
	m.ContentParts = nil
	m.Media = nil
	return m
}

// readSessionMessageRows reads the raw session_messages columns so the parity
// test can assert on all four of them (seq, role, message, excluded), which
// the repository API does not expose together.
func readSessionMessageRows(t *testing.T, db *sql.DB, key string) []store.MessageRow {
	t.Helper()
	rows, err := db.Query(
		`SELECT seq, role, message, excluded FROM session_messages WHERE session_key = ? ORDER BY seq ASC`, key)
	if err != nil {
		t.Fatalf("select session_messages: %v", err)
	}
	defer rows.Close()
	var out []store.MessageRow
	for rows.Next() {
		var row store.MessageRow
		var excluded int
		if err := rows.Scan(&row.Seq, &row.Role, &row.JSON, &excluded); err != nil {
			t.Fatalf("scan session_messages: %v", err)
		}
		row.Excluded = excluded != 0
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate session_messages: %v", err)
	}
	return out
}
