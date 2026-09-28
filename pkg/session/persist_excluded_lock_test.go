package session

// Lock-scope, byte-parity and concurrency regression tests for the
// excluded-range persistence path (T11).
//
// saveExcludedRangeUnlocked is the path a compaction triggers right after
// ExcludeOldMessagesFromContext flags a range of messages: the next Save
// re-marshals every message of that range and rewrites its excluded column and
// JSON. It used to do the json.Marshal of the whole range while holding sm.mu —
// the third and last in-lock encode after T9 (eviction) and T10 (full
// rewrite) — so compacting a long chat froze every concurrent GetHistoryView
// (a TUI frame) for the whole encode.
//
// The tests below pin the same three-phase contract the sibling paths already
// have (collect under the lock → marshal + I/O with the lock released → relock
// + guards):
//
//   - TestSaveExcludedRangeDoesNotHoldLockDuringIO: neither the SQLite write
//     nor the marshalling runs under sm.mu (a blocking fake parks the UPDATE,
//     a TryRLock probe decides that deterministically and a lock watchdog
//     bounds the longest reader wait across the whole save), and a TUI frame is
//     served while the write is parked.
//   - TestSaveExcludedRangePersistedBytesParity: the rows this path writes are
//     byte-identical to the ones the pre-T11 implementation wrote (same seqs,
//     JSON and excluded flags, holes included), the rows outside the range are
//     not touched at all, and a cold load restores the same content/flags.
//   - TestSaveExcludedRangeHealsConcurrentMutation: a mutation landing in the
//     unlocked window is never lost — the epoch guard forces the next Save to
//     rebuild the history from memory and keeps the range pending for a retry.
//   - TestSaveExcludedRangeAppendDuringWindowIsNotLost: the same with an append
//     travelling through saveUnlocked's fall-through routing; memory and SQLite
//     end up in agreement.
//   - TestSaveExcludedRangeConcurrentRacersStayConsistent: racing writers
//     (append + chunk + Save) against a compacting goroutine; -race clean, no
//     marker lost, final memory/SQLite consistent.
//   - TestSaveExcludedRangeOutsideResidentWindowWritesNoRows: the "evacuated
//     range" case — a stale range reaching past the resident slice is clamped
//     exactly like the old inline loop, with no row rewritten and no read back
//     from SQLite.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// excludedRangeBlockingRepo parks the excluded-range UPDATE until the test
// releases it and records whether sm.mu looked held when the call arrived.
//
// It reuses the ioGate handshake of persist_lock_test.go (T10): entering the
// gate probes sm.mu with TryRLock, which fails deterministically while a writer
// holds it, so in-lock I/O is detected without racing a timer. Only the
// targeted UPDATE is parked — UpsertSession is deliberately left alone so the
// concurrency tests can still write metadata (and their own messages) while
// this one statement is stuck.
type excludedRangeBlockingRepo struct {
	sessionRepo
	gate *ioGate
}

func (b *excludedRangeBlockingRepo) UpdateMessagesExcludedWithJSON(sessionKey string, messages []store.MessageRow) error {
	b.gate.enter()
	return b.sessionRepo.UpdateMessagesExcludedWithJSON(sessionKey, messages)
}

// useBlockingExcludedRangeRepo swaps repoFor for the fake until the test ends.
func useBlockingExcludedRangeRepo(t *testing.T, sm *SessionManager, gate *ioGate) {
	t.Helper()
	fake := &excludedRangeBlockingRepo{sessionRepo: sm.sessionRepo, gate: gate}
	prev := repoFor
	repoFor = func(*SessionManager) sessionRepo { return fake }
	t.Cleanup(func() { repoFor = prev })
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// excludedRangeFixture is an 11-turn history that exercises every payload shape
// the storage encoding has to keep byte-identical (unicode, escapes, tool
// calls, content parts, attachments) plus a tool_use/tool_result pair the
// exclusion boundary must not split.
//
// With keepCount=3 the compaction that follows pins index 0 and the last two
// human user turns (indices 7 and 9), so the persisted range is [1, 8) and its
// last row is a non-excluded HOLE — exactly the shape saveExcludedRangeUnlocked
// writes row by row (each row's actual ExcludeFromContext value, not a
// hardcoded flag).
func excludedRangeFixture() []providers.Message {
	return []providers.Message{
		{Role: "user", Content: "quotes \" backslash \\ newline \n tab \t <tag> & ünïcode"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "second question"},
		{
			Role:    "assistant",
			Content: "",
			ContentParts: []providers.ContentPart{
				{Type: "text", Text: "look"},
				{Type: "image_url", ImageURL: &providers.ImageURL{URL: "data:image/png;base64,AAAA", Detail: "high"}},
			},
			ToolCalls: []providers.ToolCall{{ID: "call-1", Type: "function", Function: &providers.FunctionCall{Name: "read_file", Arguments: `{"path":"/tmp/a"}`}}},
		},
		{Role: "tool", ToolCallID: "call-1", Content: "file contents"},
		{Role: "assistant", Content: "tool result digested"},
		{Role: "user", Content: "third question"},
		{
			Role:           "user",
			Content:        "expanded prompt",
			DisplayContent: "/cmd args",
			Command:        &providers.CommandApplied{Name: "cmd", Args: "args", Source: "user"},
			Attachments:    []providers.MessageAttachment{{Name: "a.txt", Path: "/tmp/a.txt", MIMEType: "text/plain", Kind: "file", Caption: "cap"}},
			Media:          []string{"/tmp/a.txt"},
		},
		{Role: "assistant", Content: "reasoned answer", ReasoningContent: "because"},
		{Role: "user", Content: "final question"},
		{Role: "assistant", Content: "final answer"},
	}
}

// seedExcludedRangeSession builds the state saveExcludedRangeUnlocked exists
// for: a fully persisted session whose next Save has ONLY a pending excluded
// range to write (lastPersistedSeq != -1, nothing appended or modified) after
// ExcludeOldMessagesFromContext flagged it. Returns the range and the resident
// count.
func seedExcludedRangeSession(t *testing.T, sm *SessionManager, key string, msgs []providers.Message, keep int) (from, to, resident int) {
	t.Helper()
	sm.GetOrCreate(key)
	for _, msg := range msgs {
		sm.AddFullMessage(key, msg)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	sm.ExcludeOldMessagesFromContext(key, keep)

	sm.mu.RLock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.RUnlock()
		t.Fatalf("session %q is not resident", key)
	}
	from, to = session.excludedRange[0], session.excludedRange[1]
	resident = len(session.Messages)
	lastPersisted := session.lastPersistedSeq
	dirty := session.msgsAppended != 0 || session.modifiedFrom != 0
	sm.mu.RUnlock()

	if to <= from {
		t.Fatalf("fixture: excludedRange = [%d, %d), want a non-empty range", from, to)
	}
	if lastPersisted < 0 {
		t.Fatalf("fixture: lastPersistedSeq = %d, want a value >= 0 (otherwise saveUnlocked picks the full rewrite)", lastPersisted)
	}
	if dirty {
		t.Fatalf("fixture: messages are dirty (appended/modified), so saveUnlocked would chain another path")
	}
	if resident != len(msgs) {
		t.Fatalf("fixture: %d resident messages, want %d", resident, len(msgs))
	}
	return from, to, resident
}

// readPersistedRow reads one persisted row's raw columns.
func readPersistedRow(t *testing.T, db *sql.DB, key string, seq int) (store.MessageRow, bool) {
	t.Helper()
	var row store.MessageRow
	var excluded int
	err := db.QueryRow(
		`SELECT seq, role, message, excluded FROM session_messages WHERE session_key = ? AND seq = ?`, key, seq).
		Scan(&row.Seq, &row.Role, &row.JSON, &excluded)
	if errors.Is(err, sql.ErrNoRows) {
		return store.MessageRow{}, false
	}
	if err != nil {
		t.Fatalf("read persisted row seq %d: %v", seq, err)
	}
	row.Excluded = excluded != 0
	return row, true
}

// countPersistedRows counts the rows of a session without materializing them
// (the lock-scope fixture is ~80 MB).
func countPersistedRows(t *testing.T, db *sql.DB, key string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM session_messages WHERE session_key = ?`, key).Scan(&n); err != nil {
		t.Fatalf("count persisted rows: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// (a) the regression test: no I/O and no marshalling under sm.mu
// ---------------------------------------------------------------------------

func TestSaveExcludedRangeDoesNotHoldLockDuringIO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping lock-scope timing test in -short mode")
	}
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-lock-scope"

	// Compaction-sized payload: ~80 MB of JSON, which at the measured ~630 MB/s
	// marshalling throughput is ~125 ms of lock hold if the encode ever moves
	// back inside sm.mu (that is the mutation the watchdog below catches).
	const total = 202
	blob := strings.Repeat("x", 400*1024)
	msgs := make([]providers.Message, total)
	for i := range msgs {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		msgs[i] = providers.Message{Role: role, Content: blob}
	}

	// keepCount=2 → excludeUpTo = 200; index 0 and the human user turns 198/200
	// are pinned, so the range is [1, 200) with index 198 persisted as a hole.
	from, to, resident := seedExcludedRangeSession(t, sm, key, msgs, 2)
	if from != 1 || to != 200 {
		t.Fatalf("fixture drifted: excludedRange = [%d, %d), want [1, 200)", from, to)
	}
	if resident != total {
		t.Fatalf("resident messages = %d, want %d", resident, total)
	}

	// The collect phase copies only the message headers of the window (199
	// messages × 192 B ≈ 38 KB), so an in-lock marshal of ~80 MB outgrows it by
	// three orders of magnitude: the watchdog below is calibrated on the JSON
	// encoding, not on the copy.
	_, residentMsgs, _, _ := liveSnapshot(t, sm, key)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	useBlockingExcludedRangeRepo(t, sm, gate)

	watchdog := startLockWatchdog(&sm.mu)

	saveErr := make(chan error, 1)
	go func() { saveErr <- sm.Save(key) }()

	// Reaching the repository is itself part of the contract: if the collect
	// phase still held sm.mu, this call would never arrive.
	select {
	case <-gate.entered:
	case err := <-saveErr:
		t.Fatalf("Save returned before touching the repository: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("Save never reached the repository: it is stuck (holding sm.mu?) before its first I/O")
	}

	// Deterministic half: the UPDATE is parked right now, so a concurrent read
	// lock must be available immediately. Reintroducing the I/O under sm.mu
	// fails here (TryRLock never succeeds while the writer holds the lock).
	if gate.muHeld.Load() {
		t.Fatal("repository I/O ran while sm.mu was held: the write lock must cover collection only")
	}

	// The user-visible property: a TUI frame is served while the save is parked
	// in the middle of its write, and it still shows the full history (the
	// excluded prefix stays resident — exclusion is a context flag, not an
	// eviction).
	viewDone := make(chan int, 1)
	go func() { viewDone <- len(sm.GetHistoryView(key)) }()
	select {
	case n := <-viewDone:
		if n != resident {
			t.Fatalf("GetHistoryView returned %d messages, want %d", n, resident)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetHistoryView blocked while an excluded-range save was doing I/O: the lock is held across the I/O")
	}

	// Park long enough that an in-lock marshal would show up in the watchdog:
	// without the split the reader is blocked for the whole encode (~125 ms).
	time.Sleep(400 * time.Millisecond)

	gate.unlock()
	select {
	case err := <-saveErr:
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Save did not finish after the repository was released")
	}

	// Timing half: the watchdog covered the whole save, marshalling included.
	// With the encode under the lock its longest wait is ~125 ms; outside the
	// lock it is scheduling noise. maxLockWait sits between the two with margin
	// on both sides.
	const maxLockWait = 60 * time.Millisecond
	if observed := watchdog.stopAndWait(); observed > maxLockWait {
		t.Fatalf("a reader waited %v for sm.mu during an excluded-range save (limit %v): the lock is held across the collect/marshal phase",
			observed, maxLockWait)
	}

	// The write really landed: same row count, and the range carries the new
	// flags — including the non-excluded hole at 198.
	if got := countPersistedRows(t, s.DB(), key); got != resident {
		t.Fatalf("persisted %d rows, want %d", got, resident)
	}
	for _, seq := range []int{1, 100} {
		row, ok := readPersistedRow(t, s.DB(), key, seq)
		if !ok {
			t.Fatalf("row seq %d is missing after the save", seq)
		}
		if !row.Excluded {
			t.Errorf("row seq %d excluded = false, want true", seq)
		}
		wantJSON, mErr := json.Marshal(residentMsgs[seq])
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", seq, mErr)
		}
		if row.JSON != string(wantJSON) {
			t.Errorf("row seq %d JSON does not match the resident message", seq)
		}
	}
	hole, ok := readPersistedRow(t, s.DB(), key, 198)
	if !ok {
		t.Fatal("row seq 198 (the preserved hole) is missing after the save")
	}
	if hole.Excluded {
		t.Error("row seq 198 excluded = true: a pinned message inside the range must be persisted un-excluded")
	}
}

// ---------------------------------------------------------------------------
// (b) byte parity with the pre-T11 implementation + cold-load round trip
// ---------------------------------------------------------------------------

// TestSaveExcludedRangePersistedBytesParity pins the exact bytes this path
// leaves in SQLite. The expected rows are rebuilt the way the pre-T11
// implementation built them inline (walk [from, to), re-marshal the resident
// message, address it by seq = firstInMemorySeq + index), and the comparison is
// column by column against a direct SELECT. Rows outside the range must not be
// touched at all, so a change in json output, in seq mapping or in the excluded
// flag fails here.
func TestSaveExcludedRangePersistedBytesParity(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-parity"

	rich := excludedRangeFixture()
	from, to, resident := seedExcludedRangeSession(t, sm, key, rich, 3)
	if from != 1 || to != 8 {
		t.Fatalf("fixture drifted: excludedRange = [%d, %d), want [1, 8)", from, to)
	}
	if resident != len(rich) {
		t.Fatalf("resident messages = %d, want %d", resident, len(rich))
	}

	firstInMemorySeq, residentMsgs, _, _ := liveSnapshot(t, sm, key)
	if firstInMemorySeq != 0 {
		t.Fatalf("fixture: firstInMemorySeq = %d, want 0 (no eviction in this test)", firstInMemorySeq)
	}
	before := readSessionMessageRows(t, s.DB(), key)
	if len(before) != len(rich) {
		t.Fatalf("persisted %d rows before the save, want %d", len(before), len(rich))
	}

	// The pre-T11 expectation, rebuilt inline.
	want := make([]store.MessageRow, 0, to-from)
	for i := from; i < to && i < len(residentMsgs); i++ {
		msg := residentMsgs[i]
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			t.Fatalf("marshal resident %d: %v", i, mErr)
		}
		want = append(want, store.MessageRow{
			Seq:      firstInMemorySeq + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}
	if len(want) != to-from {
		t.Fatalf("expected %d rows, built %d", to-from, len(want))
	}
	wantExcluded := 0
	for _, row := range want {
		if row.Excluded {
			wantExcluded++
		}
	}
	if wantExcluded != 6 { // 1..6 excluded; index 7 is the pinned hole
		t.Fatalf("fixture drifted: %d excluded rows in the range, want 6", wantExcluded)
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got := readSessionMessageRows(t, s.DB(), key)
	if len(got) != len(before) {
		t.Fatalf("persisted %d rows after the save, want %d (this path must not add or drop rows)", len(got), len(before))
	}

	wantBySeq := make(map[int]store.MessageRow, len(want))
	for _, row := range want {
		wantBySeq[row.Seq] = row
	}
	flagChanges := 0
	for i, row := range got {
		if row.Seq != i {
			t.Fatalf("row %d has seq %d: the persisted seqs must stay contiguous", i, row.Seq)
		}
		w, inRange := wantBySeq[row.Seq]
		if !inRange {
			if row != before[i] {
				t.Errorf("row seq %d is outside the excluded range but was rewritten:\n got %+v\nwas %+v", row.Seq, row, before[i])
			}
			continue
		}
		if row.JSON != w.JSON {
			t.Errorf("row seq %d JSON differs from the pre-T11 output:\n got %s\nwant %s", row.Seq, row.JSON, w.JSON)
		}
		if row.Excluded != w.Excluded {
			t.Errorf("row seq %d excluded = %v, want %v", row.Seq, row.Excluded, w.Excluded)
		}
		// The UPDATE does not write the role column (pre-existing behaviour),
		// so the role must simply survive untouched.
		if row.Role != before[i].Role {
			t.Errorf("row seq %d role = %q, want %q (this statement must not rewrite roles)", row.Seq, row.Role, before[i].Role)
		}
		if row.Excluded != before[i].Excluded {
			flagChanges++
		}
	}
	// Non-vacuous half: the excluded column really was rewritten by this path,
	// and only for the messages the compaction newly excluded (the pinned hole
	// inside the range keeps its value).
	if flagChanges != wantExcluded {
		t.Errorf("%d rows had their excluded flag rewritten, want %d", flagChanges, wantExcluded)
	}

	// Round-trip: a cold load (fresh manager, same store) restores the same
	// content and flags. ContentParts/Media are dropped by the documented
	// storage encoding (see T10's storageComparable), so they are excluded from
	// the comparison — the byte parity above still covers their stored form.
	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	sm2.GetOrCreate(key)
	coldSeq, cold, _, _ := liveSnapshot(t, sm2, key)
	if coldSeq != firstInMemorySeq {
		t.Errorf("cold-load firstInMemorySeq = %d, want %d", coldSeq, firstInMemorySeq)
	}
	if len(cold) != len(residentMsgs) {
		t.Fatalf("cold-load resident messages = %d, want %d", len(cold), len(residentMsgs))
	}
	for i := range residentMsgs {
		if cold[i].ExcludeFromContext != residentMsgs[i].ExcludeFromContext {
			t.Errorf("cold-load message %d excluded = %v, want %v (the persisted flag must survive the round trip)",
				i, cold[i].ExcludeFromContext, residentMsgs[i].ExcludeFromContext)
		}
		wantJSON, _ := json.Marshal(storageComparable(residentMsgs[i]))
		coldJSON, _ := json.Marshal(storageComparable(cold[i]))
		if string(wantJSON) != string(coldJSON) {
			t.Errorf("round-trip mismatch at %d:\n got %s\nwant %s", i, coldJSON, wantJSON)
		}
	}
}

// ---------------------------------------------------------------------------
// (c) concurrency: the unlocked window never loses a mutation
// ---------------------------------------------------------------------------

// concurrentExcludedSummary is the metadata mutation the healing test lands
// while the excluded-range write is parked.
const concurrentExcludedSummary = "summary-during-excluded-save"

// TestSaveExcludedRangeHealsConcurrentMutation proves that a mutation landing
// in the unlocked window is never lost and that the save does not claim it was
// persisted.
//
// A metadata-only mutation (SetSummary) is used on purpose: it bumps the epoch
// without marking any message dirty, so it isolates the epoch guard of this
// path — nothing else in saveUnlocked's fall-through can overwrite the healing
// flag (lastPersistedSeq == -1) that the guard sets.
func TestSaveExcludedRangeHealsConcurrentMutation(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-concurrent"

	rich := excludedRangeFixture()
	from, to, _ := seedExcludedRangeSession(t, sm, key, rich, 3)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	useBlockingExcludedRangeRepo(t, sm, gate)

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
		t.Fatal("repository I/O ran while sm.mu was held")
	}

	// The mutation, in the window where sm.mu is released.
	sm.SetSummary(key, concurrentExcludedSummary)

	gate.unlock()
	select {
	case err := <-saveErr:
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Save did not finish after the repository was released")
	}

	// Epoch guard: the save must not claim it persisted a state that no longer
	// exists. saveExcludedRangeUnlocked signals the uncertainty with
	// lastPersistedSeq = -1, and saveUnlocked re-verifies that flag right after
	// the excluded-range step (mirroring the delete-last branch, R3.1) → the
	// healing full rewrite runs in THIS Save call instead of being deferred to
	// the next one. Nothing is left pending: the range is not kept for a retry
	// because the rewrite just persisted it (ReplaceMessages writes every
	// resident message with its excluded flag).
	sm.mu.RLock()
	session := sm.sessions[key]
	healed := session.lastPersistedSeq
	pending := session.excludedRange
	residentNow := len(session.Messages)
	sm.mu.RUnlock()
	if healed != residentNow-1 {
		t.Errorf("lastPersistedSeq = %d after a concurrent mutation, want %d (the save must reconcile by rebuilding the history from memory, not leave the heal pending)", healed, residentNow-1)
	}
	if pending != [2]int{} {
		t.Errorf("excludedRange = %v, want the zero range: the range [%d %d] whose UPDATE lost the epoch was persisted by the healing full rewrite in this same Save",
			pending, from, to)
	}

	// The concurrent metadata mutation itself is durable, carried by that same
	// rewrite (its phase-1 snapshot was taken after SetSummary). The Save below
	// is a no-op and must leave the agreement intact: every row matches the
	// resident message byte for byte, flags included.
	if err := sm.Save(key); err != nil {
		t.Fatalf("healing Save: %v", err)
	}
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta: %v (meta=%v)", err, meta)
	}
	if meta.Summary != concurrentExcludedSummary {
		t.Errorf("persisted summary = %q, want %q (the concurrent mutation was lost)", meta.Summary, concurrentExcludedSummary)
	}

	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
	rows := readSessionMessageRows(t, s.DB(), key)
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
			t.Errorf("row seq %d lost the healing rewrite:\n got %+v\nwant seq=%d excluded=%v json=%s",
				firstInMemorySeq+i, row, firstInMemorySeq+i, msg.ExcludeFromContext, string(wantJSON))
		}
	}
}

// concurrentExcludedAppend is the message the append test writes while the
// excluded-range UPDATE is parked.
const concurrentExcludedAppend = "appended-during-excluded-save"

// TestSaveExcludedRangeAppendDuringWindowIsNotLost covers the other half of the
// window: an append. It rides saveUnlocked's routing after the excluded-range
// save in the same Save call — which, since R3.1 re-verifies the heal flag right
// after that step, is the healing full rewrite rather than the incremental
// patch — so the assertion is end-to-end: nothing may be lost and memory/SQLite
// must agree, rather than on the guard's internal flags.
func TestSaveExcludedRangeAppendDuringWindowIsNotLost(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-append-window"

	rich := excludedRangeFixture()
	from, to, _ := seedExcludedRangeSession(t, sm, key, rich, 3)

	gate := newIOGate(&sm.mu)
	defer gate.unlock()
	useBlockingExcludedRangeRepo(t, sm, gate)

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
		t.Fatal("repository I/O ran while sm.mu was held")
	}

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: concurrentExcludedAppend})

	gate.unlock()
	select {
	case err := <-saveErr:
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Save did not finish after the repository was released")
	}

	// The range was not claimed as persisted by the UPDATE that lost the epoch,
	// so the re-verification answers with a full rewrite in this same Save: the
	// range is already persisted (not left pending) and the concurrent append
	// went out with it.
	sm.mu.RLock()
	pending := sm.sessions[key].excludedRange
	sm.mu.RUnlock()
	if pending != [2]int{} {
		t.Errorf("excludedRange = %v, want the zero range: the range [%d %d] whose UPDATE lost the epoch was persisted by the healing rewrite of this same Save",
			pending, from, to)
	}

	// A following Save must leave memory and SQLite in exact agreement, the
	// concurrent append included.
	if err := sm.Save(key); err != nil {
		t.Fatalf("follow-up Save: %v", err)
	}
	sm.mu.RLock()
	pending = sm.sessions[key].excludedRange
	sm.mu.RUnlock()
	if pending != [2]int{} {
		t.Errorf("excludedRange = %v after a clean Save, want the zero range", pending)
	}

	firstInMemorySeq, resident, _, _ := liveSnapshot(t, sm, key)
	if len(resident) != len(rich)+1 {
		t.Fatalf("resident messages = %d, want %d (the concurrent append must not be lost)", len(resident), len(rich)+1)
	}
	if resident[len(rich)].Content != concurrentExcludedAppend {
		t.Errorf("resident[%d] = %q, want the append %q", len(rich), resident[len(rich)].Content, concurrentExcludedAppend)
	}
	rows := readSessionMessageRows(t, s.DB(), key)
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
			t.Errorf("row seq %d differs from memory:\n got %+v\nwant seq=%d excluded=%v json=%s",
				firstInMemorySeq+i, row, firstInMemorySeq+i, msg.ExcludeFromContext, string(wantJSON))
		}
	}
}

// TestSaveExcludedRangeConcurrentRacersStayConsistent races appending writers
// (append + streaming chunk + their own Saves) against a goroutine that keeps
// compacting (exclude + Save). It is the -race companion of the deterministic
// window tests above: it must report no data race, must not lose any written
// marker, and must end with memory and SQLite in agreement.
func TestSaveExcludedRangeConcurrentRacersStayConsistent(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-racers"

	seedExcludedRangeSession(t, sm, key, excludedRangeFixture(), 3)

	const (
		writers   = 4
		perWriter = 5
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
			// The documented compaction sequence: flag the range, persist it,
			// repeat as the chat grows.
			sm.ExcludeOldMessagesFromContext(key, 3)
			if err := sm.Save(key); err != nil {
				t.Errorf("compactor Save: %v", err)
				return
			}
		}
	}()

	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			for i := 0; i < perWriter; i++ {
				marker := fmt.Sprintf("w%d-%d-excluded-marker", w, i)
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
			t.Errorf("row %d/%d differs from memory:\n got %+v\nwant excluded=%v json=%s",
				len(rows)-len(resident)+i, len(rows), row, msg.ExcludeFromContext, string(wantJSON))
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
	bodies := make([]string, 0, len(rows)+len(resident))
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

// ---------------------------------------------------------------------------
// (d) the "evacuated range" decision
// ---------------------------------------------------------------------------

// TestSaveExcludedRangeOutsideResidentWindowWritesNoRows pins the decision
// taken for the "evacuated range" case: excludedRange is an index window into
// session.Messages, so when a (stale) range reaches past the resident slice the
// window is clamped exactly like the pre-T11 loop's `i < len(session.Messages)`
// bound. The missing prefix is deliberately NOT read back from SQLite: that
// would change *what* this path writes instead of preserving its behaviour
// (metadata is still upserted, so the save is not a no-op).
func TestSaveExcludedRangeOutsideResidentWindowWritesNoRows(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:excluded-evacuated-range"

	rich := excludedRangeFixture()
	sm.GetOrCreate(key)
	for _, msg := range rich {
		sm.AddFullMessage(key, msg)
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	before := readSessionMessageRows(t, s.DB(), key)
	if len(before) != len(rich) {
		t.Fatalf("persisted %d rows before, want %d", len(before), len(rich))
	}

	// Simulate what a concurrent eviction leaves behind: the in-memory slice was
	// trimmed to its last two messages (persisted seqs 4 and 5) while a range
	// computed against the longer slice is still pending.
	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session %q is not resident", key)
	}
	session.Messages = session.Messages[len(session.Messages)-2:]
	session.firstInMemorySeq = len(rich) - 2
	session.evictedTotal = len(rich) - 2
	session.bumpEpoch()
	session.publishViewLocked()
	session.excludedRange = [2]int{len(rich) - 2, len(rich) + 3} // entirely past the resident window
	sm.mu.Unlock()

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	after := readSessionMessageRows(t, s.DB(), key)
	if len(after) != len(before) {
		t.Fatalf("persisted %d rows after, want %d (this path must not touch the row set)", len(after), len(before))
	}
	for i := range after {
		if after[i] != before[i] {
			t.Errorf("row seq %d was rewritten by a range that no longer maps to resident messages:\n got %+v\nwas %+v",
				after[i].Seq, after[i], before[i])
		}
	}

	// The bookkeeping still ran: the range was consumed and the metadata upsert
	// (idempotent) happened.
	sm.mu.RLock()
	pending := sm.sessions[key].excludedRange
	sm.mu.RUnlock()
	if pending != [2]int{} {
		t.Errorf("excludedRange = %v after the save, want the zero range", pending)
	}
	if meta, err := s.Sessions().GetSessionMeta(key); err != nil || meta == nil {
		t.Fatalf("GetSessionMeta: %v (meta=%v)", err, meta)
	}
}
