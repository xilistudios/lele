package session

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

type ioGate struct {
	entered  chan struct{}
	release  chan struct{}
	firstOne sync.Once
	releaseO sync.Once
	mu       *sync.RWMutex
	muHeld   atomic.Bool
}

func newIOGate(mu *sync.RWMutex) *ioGate {
	return &ioGate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		mu:      mu,
	}
}

func (g *ioGate) enter() {
	if !lockIsFreeWithin(g.mu, 250*time.Millisecond) {
		g.muHeld.Store(true)
	}
	g.firstOne.Do(func() { close(g.entered) })
	<-g.release
}

func (g *ioGate) unlock() { g.releaseO.Do(func() { close(g.release) }) }

func useBlockingRepo(t *testing.T, sm *SessionManager, gate *ioGate) {
	t.Helper()
	fake := &blockingRepo{sessionRepo: sm.sessionRepo, gate: gate}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return fake }
	t.Cleanup(func() { repoFor = prev })
}

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

type lockWaitWatchdog struct {
	maxWait atomic.Int64
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

var deadlockGuard = 60 * time.Second

func withDeadlockGuard(t *testing.T, guard time.Duration) {
	t.Helper()
	previous := deadlockGuard
	deadlockGuard = guard
	t.Cleanup(func() { deadlockGuard = previous })
}

func guardFailure(stage string) string {
	return fmt.Sprintf("deadlock guard fired (not a timing assertion) after %v: %s", deadlockGuard, stage)
}

func awaitIORepositoryHit(gate *ioGate, saveErr <-chan error, stage string) string {
	select {
	case <-gate.entered:
		return ""
	case err := <-saveErr:
		return fmt.Sprintf("Save returned before touching the repository: %v", err)
	case <-time.After(deadlockGuard):
		return guardFailure(stage)
	}
}

func awaitSaveResult(saveErr <-chan error, stage string) string {
	select {
	case err := <-saveErr:
		if err != nil {
			return fmt.Sprintf("Save failed: %v", err)
		}
		return ""
	case <-time.After(deadlockGuard):
		return guardFailure(stage)
	}
}

func awaitHistoryView(sm *SessionManager, key string, want int, stage string) string {
	viewDone := make(chan int, 1)
	go func() { viewDone <- len(sm.GetHistoryView(key)) }()
	select {
	case n := <-viewDone:
		if n != want {
			return fmt.Sprintf("GetHistoryView returned %d messages, want %d", n, want)
		}
		return ""
	case <-time.After(deadlockGuard):
		return guardFailure(stage)
	}
}

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

func forceFullRewrite(sm *SessionManager, key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[key]; ok {
		session.lastPersistedSeq = -1
	}
}

func seedPostCompactionSession(t *testing.T, sm *SessionManager, key string) int {
	t.Helper()
	sm.GetOrCreate(key)
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

func TestSaveFullDoesNotHoldLockDuringIO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the lock-scope IO-volume test in -short mode")
	}
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:full-lock-scope"

	evicted := seedPostCompactionSession(t, sm, key)

	fat := make([]providers.Message, 300)
	blob := strings.Repeat("x", 256*1024)
	for i := range fat {
		fat[i] = providers.Message{Role: "assistant", Content: blob, ToolCallID: fmt.Sprintf("call-%d", i)}
	}
	sm.SetHistory(key, fat)

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

	saveErr := make(chan error, 1)
	go func() { saveErr <- sm.Save(key) }()

	if failure := awaitIORepositoryHit(gate, saveErr, "Save never reached the repository: it is stuck before its first I/O"); failure != "" {
		t.Fatal(failure)
	}

	if gate.muHeld.Load() {
		t.Fatal("repository I/O ran while sm.mu was held: the write lock must cover collection only")
	}

	if failure := awaitHistoryView(sm, key, len(fat), "GetHistoryView did not return while the full save was doing I/O: the lock is held across the I/O"); failure != "" {
		t.Fatal(failure)
	}

	gate.unlock()
	if failure := awaitSaveResult(saveErr, "Save did not finish after the repository was released"); failure != "" {
		t.Fatal(failure)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq: %v", err)
	}
	if len(rows) != evicted+len(fat) {
		t.Fatalf("persisted %d rows, want %d (evicted prefix %d + resident %d)", len(rows), evicted+len(fat), evicted, len(fat))
	}
}

func TestDeadlockGuardFailsInsteadOfHanging(t *testing.T) {
	withDeadlockGuard(t, 50*time.Millisecond)

	stuckSave := make(chan error)
	failure := awaitSaveResult(stuckSave, "Save did not finish after the repository was released")

	const wantLabel = "deadlock guard fired (not a timing assertion)"
	if !strings.HasPrefix(failure, wantLabel) {
		t.Fatalf("stuck Save was reported as %q, want a failure starting with %q", failure, wantLabel)
	}
	if !strings.Contains(failure, "Save did not finish after the repository was released") {
		t.Errorf("guard failure %q does not name the stuck stage", failure)
	}
}

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

func storageComparable(m providers.Message) providers.Message {
	m.ContentParts = nil
	m.Media = nil
	return m
}

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
