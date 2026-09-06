package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// mustEnqueue enqueues a row and fails the test on error.
func mustEnqueue(t *testing.T, repo *SpoolRepo, direction, sessionKey, msgID, payload string) int64 {
	t.Helper()

	id, err := repo.Enqueue(direction, "telegram", "chat-1", sessionKey, msgID, payload)
	if err != nil {
		t.Fatalf("Enqueue(%s, %q, %q) failed: %v", direction, sessionKey, msgID, err)
	}
	return id
}

// spoolIDs maps a claim result onto its ids, keeping assertions readable.
func spoolIDs(items []SpoolItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// testNow is the reference instant used by every time-controlled assertion.
// It is deliberately far in the past relative to the wall clock so rows
// written by MarkProcessed (which uses time.Now) never collide with it.
func testNow() time.Time {
	return time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
}

// insertLedger writes a processed_messages row directly so tests control
// processed_at, which the public MarkProcessed always stamps with now. It
// uses the repository's own layout so the rows look exactly like the ones
// production writes.
func insertLedger(t *testing.T, s *Store, channel, msgID string, at time.Time) {
	t.Helper()

	_, err := s.DB().Exec(
		`INSERT INTO processed_messages(channel, msg_id, processed_at) VALUES(?, ?, ?)`,
		channel, msgID, at.UTC().Format(spoolTimeLayout),
	)
	if err != nil {
		t.Fatalf("insert ledger %s/%q failed: %v", channel, msgID, err)
	}
}

// spoolPath returns a database path inside a temp dir, for the tests that
// need to close a store and reopen the same file.
func spoolPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "spool.db")
}

// openStoreAtPath opens (or reopens) the store at path. Unlike openTestStore
// it takes the path, so a test can simulate a gateway restart: close the
// store, then open a new one over the same file. sql.DB.Close is idempotent,
// so the cleanup below tolerates a manual Close earlier in the test.
func openStoreAtPath(t *testing.T, path string) *Store {
	t.Helper()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	})
	return s
}

// mustClose closes a store and fails the test on error. It marks the store
// as closed so a test cannot accidentally keep using a dead handle.
func mustClose(t *testing.T, s *Store) {
	t.Helper()

	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
}

// countSpoolRows returns the total number of rows in the spool table,
// pending and claimed together.
func countSpoolRows(t *testing.T, s *Store) int {
	t.Helper()

	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM spool`).Scan(&n); err != nil {
		t.Fatalf("count spool rows failed: %v", err)
	}
	return n
}

// claimedBy returns the distinct non-empty claimed_by values in the table.
func claimedBy(t *testing.T, s *Store) []string {
	t.Helper()

	rows, err := s.DB().Query(`SELECT DISTINCT claimed_by FROM spool WHERE claimed_by <> ''`)
	if err != nil {
		t.Fatalf("read claimed_by failed: %v", err)
	}
	defer rows.Close()

	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatalf("scan claimed_by failed: %v", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read claimed_by: %v", err)
	}
	return owners
}

// mustStats calls Stats and fails the test on error.
func mustStats(t *testing.T, repo *SpoolRepo) SpoolStats {
	t.Helper()

	stats, err := repo.Stats()
	if err != nil {
		t.Fatalf("Stats() failed: %v", err)
	}
	return stats
}

// ──────────────────────────────────────────────────────────────────────────────
// Queue behaviour
// ──────────────────────────────────────────────────────────────────────────────

func TestSpoolEnqueueClaimComplete(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	// Enqueue returns strictly increasing ids.
	var ids []int64
	for i := range 4 {
		id := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m-1", `{"n":`+string(rune('0'+i))+`}`)
		if i > 0 && id <= ids[0] {
			t.Fatalf("Enqueue id #%d = %d, want > %d", i, id, ids[0])
		}
		ids = append(ids, id)
	}

	// FIFO: the first batch holds the two oldest ids.
	batch, err := repo.ClaimBatch(SpoolInbound, 2, "gw-1", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if got, want := spoolIDs(batch), ids[:2]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimBatch() ids = %v, want %v", got, want)
	}

	// All columns round-trip, attempts starts at zero.
	first := batch[0]
	if first.Direction != SpoolInbound || first.Channel != "telegram" ||
		first.ChatID != "chat-1" || first.SessionKey != "telegram:main" || first.MsgID != "m-1" {
		t.Errorf("ClaimBatch() row fields mismatch: %+v", first)
	}
	if first.Payload == "" {
		t.Errorf("ClaimBatch() payload is empty")
	}
	if first.Attempts != 0 {
		t.Errorf("ClaimBatch() attempts = %d, want 0", first.Attempts)
	}
	if first.CreatedAt.IsZero() {
		t.Errorf("ClaimBatch() created_at is zero")
	}

	// Claiming removes rows from the pending set but keeps them in the queue.
	if got, want := mustPending(t, s), 2; got != want {
		t.Fatalf("pending after claim = %d, want %d", got, want)
	}

	// Complete deletes the delivered rows for good.
	if err := repo.Complete(ids[:2]); err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	rest, err := repo.ClaimBatch(SpoolInbound, 10, "gw-1", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() after complete failed: %v", err)
	}
	if got, want := spoolIDs(rest), ids[2:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimBatch() after complete = %v, want %v", got, want)
	}

	// The completed ids are gone: they are not returned by any later claim
	// and are physically deleted from the table.
	if err := repo.Complete(ids[:1]); err != nil {
		t.Fatalf("Complete(unknown id) failed: %v", err)
	}
	var remaining int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM spool`).Scan(&remaining); err != nil {
		t.Fatalf("count spool rows failed: %v", err)
	}
	if remaining != 2 {
		t.Errorf("spool rows after complete = %d, want 2", remaining)
	}
}

func TestSpoolClaimIsExclusive(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	for i := range 3 {
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"i":`+string(rune('0'+i))+`}`)
	}

	first, err := repo.ClaimBatch(SpoolInbound, 3, "gw-a", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-a) failed: %v", err)
	}
	if len(first) != 3 {
		t.Fatalf("ClaimBatch(gw-a) returned %d items, want 3", len(first))
	}

	// A second instance sees nothing: the rows belong to gw-a.
	second, err := repo.ClaimBatch(SpoolInbound, 3, "gw-b", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-b) failed: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("ClaimBatch(gw-b) returned %d items, want 0", len(second))
	}

	// Not even the original owner may re-claim them.
	again, err := repo.ClaimBatch(SpoolInbound, 3, "gw-a", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-a, retry) failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("re-claim by owner returned %d items, want 0", len(again))
	}

	// Once completed, the next row the same instance claims is a fresh one.
	if err := repo.Complete(spoolIDs(first)); err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	newID := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"fresh":true}`)
	got, err := repo.ClaimBatch(SpoolInbound, 5, "gw-b", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-b) after complete failed: %v", err)
	}
	if len(got) != 1 || got[0].ID != newID {
		t.Errorf("ClaimBatch(gw-b) = %v, want single row %d", spoolIDs(got), newID)
	}
}

func TestSpoolReleaseClaims(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	for i := range 2 {
		mustEnqueue(t, repo, SpoolOutbound, "discord:x", "m", `{"i":`+string(rune('0'+i))+`}`)
	}
	mustEnqueue(t, repo, SpoolInbound, "discord:x", "m", `{"inbound":true}`)

	held, err := repo.ClaimBatch(SpoolOutbound, 5, "gw-a", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("ClaimBatch() = %d items, want 2", len(held))
	}

	// Releasing only touches the given instance: the inbound row was never
	// claimed and stays pending.
	n, err := repo.ReleaseClaims("gw-a")
	if err != nil {
		t.Fatalf("ReleaseClaims(gw-a) failed: %v", err)
	}
	if n != 2 {
		t.Errorf("ReleaseClaims(gw-a) = %d, want 2", n)
	}
	if n, err := repo.ReleaseClaims("gw-nobody"); err != nil || n != 0 {
		t.Errorf("ReleaseClaims(unknown) = (%d, %v), want (0, nil)", n, err)
	}

	// Anyone can pick them up again, and claims were cleared.
	reclaimed, err := repo.ClaimBatch(SpoolOutbound, 5, "gw-b", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-b) failed: %v", err)
	}
	if got, want := spoolIDs(reclaimed), spoolIDs(held); !reflect.DeepEqual(got, want) {
		t.Fatalf("reclaimed ids = %v, want %v", got, want)
	}
	var claimedAt string
	if err := s.DB().QueryRow(
		`SELECT claimed_at FROM spool WHERE claimed_by = ?`, "gw-b",
	).Scan(&claimedAt); err != nil {
		t.Fatalf("read claimed_at failed: %v", err)
	}
	if claimedAt == "" {
		t.Errorf("claimed_at empty after re-claim")
	}
}

func TestSpoolReclaimStale(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	staleID := mustEnqueue(t, repo, SpoolInbound, "telegram:stale", "m", `{"stale":true}`)
	freshID := mustEnqueue(t, repo, SpoolInbound, "telegram:fresh", "m", `{"fresh":true}`)

	now := testNow()

	// Two crashed workers, one recent claim, one 60s old.
	if _, err := repo.ClaimBatch(SpoolInbound, 1, "dead-a", now.Add(-60*time.Second)); err != nil {
		t.Fatalf("claim stale row failed: %v", err)
	}
	if _, err := repo.ClaimBatch(SpoolInbound, 1, "alive-b", now.Add(-5*time.Second)); err != nil {
		t.Fatalf("claim fresh row failed: %v", err)
	}

	n, err := repo.ReclaimStale(30*time.Second, now)
	if err != nil {
		t.Fatalf("ReclaimStale() failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReclaimStale(30s) = %d rows, want 1", n)
	}

	// Only the stale row came back, and it keeps its session key.
	pending, err := repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession() failed: %v", err)
	}
	if !reflect.DeepEqual(pending, map[string]int{"telegram:stale": 1}) {
		t.Fatalf("pending after reclaim = %v, want {telegram:stale:1}", pending)
	}

	items, err := repo.ClaimBatch(SpoolInbound, 5, "gw-c", now)
	if err != nil {
		t.Fatalf("ClaimBatch(gw-c) failed: %v", err)
	}
	if len(items) != 1 || items[0].ID != staleID {
		t.Fatalf("reclaimed rows = %v, want [%d]", spoolIDs(items), staleID)
	}

	// Nothing else crossed the 30s line: gw-c holds the reclaimed row and
	// alive-b still holds the fresh one.
	if n, err := repo.ReclaimStale(30*time.Second, now); err != nil || n != 0 {
		t.Errorf("ReclaimStale() again = (%d, %v), want (0, nil)", n, err)
	}

	// A 10ms cutoff frees alive-b's row (claimed 5s ago) but not the row
	// gw-c claimed at `now` itself, which is 0ms old.
	if n, err := repo.ReclaimStale(10*time.Millisecond, now); err != nil || n != 1 {
		t.Fatalf("ReclaimStale(10ms) = (%d, %v), want (1, nil)", n, err)
	}
	relaxed, err := repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession() failed: %v", err)
	}
	if !reflect.DeepEqual(relaxed, map[string]int{"telegram:fresh": 1}) {
		t.Errorf("pending after 10ms reclaim = %v, want {telegram:fresh:1}", relaxed)
	}

	// gw-c keeps its claim: it is still the only owner of the stale row.
	held, err := repo.ClaimBatch(SpoolInbound, 5, "gw-d", now)
	if err != nil {
		t.Fatalf("ClaimBatch(gw-d) failed: %v", err)
	}
	if len(held) != 1 || held[0].ID != freshID {
		t.Errorf("ClaimBatch(gw-d) = %v, want single row %d", spoolIDs(held), freshID)
	}
}

func TestSpoolDirectionValidation(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	if _, err := repo.Enqueue("sideways", "telegram", "chat", "sess", "m", "{}"); !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("Enqueue(sideways) error = %v, want ErrUnknownDirection", err)
	}
	if _, err := repo.ClaimBatch("inbound ", 1, "gw", testNow()); !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("ClaimBatch error = %v, want ErrUnknownDirection", err)
	}
	if _, err := repo.PendingBySession("OUTBOUND"); !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("PendingBySession error = %v, want ErrUnknownDirection", err)
	}

	// Nothing was written while rejecting bad directions.
	if n, err := repo.PendingBySession(SpoolInbound); err != nil || len(n) != 0 {
		t.Errorf("pending after rejected enqueues = (%v, %v), want (empty, nil)", n, err)
	}

	// The CHECK constraint backs up the Go-side validation for any other
	// writer that bypasses the repository.
	if _, err := s.DB().Exec(
		`INSERT INTO spool(direction, payload, created_at) VALUES('lateral', '{}', ?)`,
		testNow().Format(time.RFC3339Nano),
	); err == nil {
		t.Errorf("CHECK constraint accepted direction 'lateral'")
	}
}

func TestSpoolCompleteEmptyIsNoop(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	kept := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"keep":true}`)

	if err := repo.Complete(nil); err != nil {
		t.Errorf("Complete(nil) failed: %v", err)
	}
	if err := repo.Complete([]int64{}); err != nil {
		t.Errorf("Complete(empty) failed: %v", err)
	}

	if _, err := repo.ClaimBatch(SpoolInbound, 5, "gw", testNow()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if n, err := repo.ReleaseClaims("gw"); err != nil || n != 1 {
		t.Fatalf("ReleaseClaims() = (%d, %v), want (1, nil)", n, err)
	}

	items, err := repo.ClaimBatch(SpoolInbound, 5, "gw", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if len(items) != 1 || items[0].ID != kept {
		t.Errorf("rows after no-op completes = %v, want [%d]", spoolIDs(items), kept)
	}
}

func TestSpoolClaimBatchNonPositiveLimit(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"a":1}`)

	for _, limit := range []int{0, -1} {
		items, err := repo.ClaimBatch(SpoolInbound, limit, "gw", testNow())
		if err != nil {
			t.Fatalf("ClaimBatch(limit=%d) failed: %v", limit, err)
		}
		if items == nil {
			t.Errorf("ClaimBatch(limit=%d) returned nil, want empty slice", limit)
		}
		if len(items) != 0 {
			t.Errorf("ClaimBatch(limit=%d) returned %d items, want 0", limit, len(items))
		}
	}

	// The row is untouched and still pending.
	if n, err := repo.PendingBySession(SpoolInbound); err != nil || n["telegram:main"] != 1 {
		t.Errorf("pending after zero-limit claims = (%v, %v), want {telegram:main:1}", n, err)
	}
}

// mustPending returns the total number of unclaimed inbound rows.
func mustPending(t *testing.T, s *Store) int {
	t.Helper()

	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM spool WHERE direction = 'inbound' AND claimed_by = ''`,
	).Scan(&n); err != nil {
		t.Fatalf("count pending rows failed: %v", err)
	}
	return n
}

// ──────────────────────────────────────────────────────────────────────────────
// Attempts
// ──────────────────────────────────────────────────────────────────────────────

func TestSpoolIncAttempt(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	id := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"a":1}`)

	for want := 1; want <= 3; want++ {
		if err := repo.IncAttempt(id); err != nil {
			t.Fatalf("IncAttempt(%d) failed: %v", id, err)
		}
		var got int
		if err := s.DB().QueryRow(`SELECT attempts FROM spool WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatalf("read attempts failed: %v", err)
		}
		if got != want {
			t.Fatalf("attempts after %d increments = %d, want %d", want, got, want)
		}
	}

	// The next claim reports the accumulated attempts so the caller can
	// decide to park the message.
	if _, err := repo.ClaimBatch(SpoolInbound, 1, "gw", testNow()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if n, err := repo.ReleaseClaims("gw"); err != nil || n != 1 {
		t.Fatalf("ReleaseClaims(gw) = (%d, %v), want (1, nil)", n, err)
	}
	items, err := repo.ClaimBatch(SpoolInbound, 1, "gw2", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("re-claimed %d items, want 1", len(items))
	}
	if items[0].Attempts != 3 {
		t.Fatalf("claimed attempts = %d, want 3", items[0].Attempts)
	}

	// A missing id is not an error (the row may have been completed).
	if err := repo.IncAttempt(9999); err != nil {
		t.Errorf("IncAttempt(missing) failed: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Dedupe ledger
// ──────────────────────────────────────────────────────────────────────────────

func TestProcessedLedger(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	// Unknown pair.
	if got, err := repo.WasProcessed("telegram", "42"); err != nil || got {
		t.Fatalf("WasProcessed(telegram, 42) = (%v, %v), want (false, nil)", got, err)
	}

	if err := repo.MarkProcessed("telegram", "42"); err != nil {
		t.Fatalf("MarkProcessed() failed: %v", err)
	}
	if got, err := repo.WasProcessed("telegram", "42"); err != nil || !got {
		t.Fatalf("WasProcessed() after mark = (%v, %v), want (true, nil)", got, err)
	}

	// Idempotent: marking twice keeps one row and is not an error.
	if err := repo.MarkProcessed("telegram", "42"); err != nil {
		t.Errorf("second MarkProcessed() failed: %v", err)
	}
	// The same msg id on another channel is a different message.
	if err := repo.MarkProcessed("discord", "42"); err != nil {
		t.Errorf("MarkProcessed(discord) failed: %v", err)
	}
	var rows int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM processed_messages`).Scan(&rows); err != nil {
		t.Fatalf("count ledger rows failed: %v", err)
	}
	if rows != 2 {
		t.Errorf("ledger rows = %d, want 2", rows)
	}
	if got, err := repo.WasProcessed("discord", "42"); err != nil || !got {
		t.Errorf("WasProcessed(discord, 42) = (%v, %v), want (true, nil)", got, err)
	}
	if got, err := repo.WasProcessed("telegram", "43"); err != nil || got {
		t.Errorf("WasProcessed(telegram, 43) = (%v, %v), want (false, nil)", got, err)
	}

	// No dedupe key: both methods short-circuit without touching the DB.
	if got, err := repo.WasProcessed("telegram", ""); err != nil || got {
		t.Errorf("WasProcessed(telegram, \"\") = (%v, %v), want (false, nil)", got, err)
	}
	if err := repo.MarkProcessed("telegram", ""); err != nil {
		t.Errorf("MarkProcessed(telegram, \"\") failed: %v", err)
	}
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM processed_messages WHERE msg_id = ''`,
	).Scan(&rows); err != nil {
		t.Fatalf("count empty msg_id rows failed: %v", err)
	}
	if rows != 0 {
		t.Errorf("ledger holds %d empty msg_id rows, want 0", rows)
	}
}

func TestPruneProcessed(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	now := testNow()
	insertLedger(t, s, "telegram", "old-1", now.Add(-3*time.Hour))
	insertLedger(t, s, "telegram", "old-2", now.Add(-2*time.Hour))
	insertLedger(t, s, "telegram", "edge", now.Add(-time.Hour))
	insertLedger(t, s, "telegram", "fresh", now.Add(-time.Minute))

	n, err := repo.PruneProcessed(2*time.Hour, now)
	if err != nil {
		t.Fatalf("PruneProcessed(2h) failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneProcessed(2h) = %d rows, want 1", n)
	}

	// The boundary row is *exactly* 2h old, so the cutoff has not passed it
	// yet: PruneProcessed removes strictly-older rows only. With the trimmed
	// RFC3339Nano layout this comparison was a coin flip (a whole-second
	// "...:00Z" sorts after a fractional "...:00.5Z"), which is why the
	// spool stores fixed-width timestamps.
	if got, err := repo.WasProcessed("telegram", "edge"); err != nil || !got {
		t.Fatalf("edge row pruned too early: (%v, %v)", got, err)
	}
	if got, err := repo.WasProcessed("telegram", "old-2"); err != nil || !got {
		t.Fatalf("boundary old-2 row pruned too early: (%v, %v)", got, err)
	}
	if got, err := repo.WasProcessed("telegram", "old-1"); err != nil || got {
		t.Fatalf("old-1 row survived the prune: (%v, %v)", got, err)
	}

	n, err = repo.PruneProcessed(90*time.Minute, now)
	if err != nil {
		t.Fatalf("PruneProcessed(90m) failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("PruneProcessed(90m) = %d rows, want 1", n)
	}

	// The 90m cutoff sits at 10:30, past old-2 (10:00) but not past edge
	// (11:00), so exactly the boundary row goes now.
	if got, err := repo.WasProcessed("telegram", "old-2"); err != nil || got {
		t.Errorf("old-2 survived the 90m prune: (%v, %v)", got, err)
	}
	if got, err := repo.WasProcessed("telegram", "edge"); err != nil || !got {
		t.Errorf("edge row pruned too early by the 90m cutoff: (%v, %v)", got, err)
	}
	if n, err := repo.PruneProcessed(time.Second, now); err != nil || n != 2 {
		t.Fatalf("PruneProcessed(1s) = (%d, %v), want (2, nil)", n, err)
	}
	if n, err := repo.PruneProcessed(time.Second, now); err != nil || n != 0 {
		t.Errorf("second PruneProcessed() = (%d, %v), want (0, nil)", n, err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Observability: Stats / PendingBySession
// ──────────────────────────────────────────────────────────────────────────────

// checkStats asserts the *exact* queue depth reported by Stats. Every field
// is compared on purpose: Stats is the only window the restart feature has
// onto the queue, so a counter that quietly drifts would mislead operators
// even though no call ever returned an error.
func checkStats(t *testing.T, repo *SpoolRepo, want SpoolStats) {
	t.Helper()

	if got := mustStats(t, repo); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

// checkStatsMatchesTable cross-checks Stats against the table using an
// invariant the implementation cannot satisfy by copying its own query: the
// four depth counters must add up to the number of rows, and the ledger
// counter to the number of processed_messages rows.
func checkStatsMatchesTable(t *testing.T, s *Store, repo *SpoolRepo) {
	t.Helper()

	stats := mustStats(t, repo)
	total := stats.PendingInbound + stats.PendingOutbound + stats.ClaimedInbound + stats.ClaimedOutbound
	if rows := countSpoolRows(t, s); total != rows {
		t.Fatalf("Stats() depths add up to %d, but spool holds %d rows", total, rows)
	}

	var ledger int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM processed_messages`).Scan(&ledger); err != nil {
		t.Fatalf("count ledger rows failed: %v", err)
	}
	if stats.ProcessedCount != ledger {
		t.Fatalf("Stats().ProcessedCount = %d, but ledger holds %d rows", stats.ProcessedCount, ledger)
	}
}

// mustClaimedIDs returns the ids currently held by any instance for one
// direction, so a test can complete rows it does not own.
func mustClaimedIDs(t *testing.T, s *Store, direction string) []int64 {
	t.Helper()

	rows, err := s.DB().Query(
		`SELECT id FROM spool WHERE direction = ? AND claimed_by <> '' ORDER BY id`,
		direction,
	)
	if err != nil {
		t.Fatalf("read claimed ids failed: %v", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan claimed id failed: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read claimed ids: %v", err)
	}
	return ids
}

func TestSpoolStats(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	// A fresh store reports zeros rather than an error: the gateway polls
	// Stats before any message has ever been spooled.
	checkStats(t, repo, SpoolStats{})

	// N inbound + M outbound, everything pending.
	for i := range 3 {
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "in", `{"i":`+string(rune('0'+i))+`}`)
	}
	for i := range 2 {
		mustEnqueue(t, repo, SpoolOutbound, "telegram:main", "out", `{"i":`+string(rune('0'+i))+`}`)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 3, PendingOutbound: 2})
	checkStatsMatchesTable(t, s, repo)

	// A claim moves rows from pending to claimed inside its own direction
	// only: the outbound counters must not budge.
	if _, err := repo.ClaimBatch(SpoolInbound, 2, "gw-1", testNow()); err != nil {
		t.Fatalf("ClaimBatch(inbound) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, PendingOutbound: 2, ClaimedInbound: 2})

	if _, err := repo.ClaimBatch(SpoolOutbound, 2, "gw-1", testNow()); err != nil {
		t.Fatalf("ClaimBatch(outbound) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, ClaimedInbound: 2, ClaimedOutbound: 2})
	checkStatsMatchesTable(t, s, repo)

	// The dedupe ledger is counted independently of the queue depth: three
	// distinct pairs, one repeat (idempotent) and one keyless write (no-op).
	for _, msgID := range []string{"m-1", "m-2", "m-1", ""} {
		if err := repo.MarkProcessed("telegram", msgID); err != nil {
			t.Fatalf("MarkProcessed(telegram, %q) failed: %v", msgID, err)
		}
	}
	if err := repo.MarkProcessed("discord", "m-1"); err != nil {
		t.Fatalf("MarkProcessed(discord, m-1) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, ClaimedInbound: 2, ClaimedOutbound: 2, ProcessedCount: 3})

	// Completing delivered rows takes them out of the claimed set for good.
	claimedIn := mustClaimedIDs(t, s, SpoolInbound)
	if len(claimedIn) != 2 {
		t.Fatalf("claimed inbound ids = %v, want 2 rows", claimedIn)
	}
	if err := repo.Complete(claimedIn); err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, ClaimedOutbound: 2, ProcessedCount: 3})

	// A graceful release moves claimed rows back to pending, so the total
	// depth stays the same while the split changes.
	n, err := repo.ReleaseClaims("gw-1")
	if err != nil {
		t.Fatalf("ReleaseClaims(gw-1) failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("ReleaseClaims(gw-1) = %d, want 2", n)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, PendingOutbound: 2, ProcessedCount: 3})
	checkStatsMatchesTable(t, s, repo)

	// Draining the queue returns the depth counters to zero while the ledger
	// keeps its size: dedupe has to outlive the rows it protects.
	left, err := repo.ClaimBatch(SpoolInbound, 10, "gw-2", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(inbound) failed: %v", err)
	}
	right, err := repo.ClaimBatch(SpoolOutbound, 10, "gw-2", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(outbound) failed: %v", err)
	}
	if err := repo.Complete(append(spoolIDs(left), spoolIDs(right)...)); err != nil {
		t.Fatalf("Complete(all) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{ProcessedCount: 3})
	checkStatsMatchesTable(t, s, repo)
}

func TestSpoolPendingBySession(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	// Empty table: an empty map, never nil. The resume layer ranges over the
	// result right after a restart, and the documented contract promises a
	// map, so a nil here would be a (silent) lie rather than a crash.
	empty, err := repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) on empty table failed: %v", err)
	}
	if empty == nil {
		t.Fatalf("PendingBySession(inbound) on empty table returned nil, want empty map")
	}
	if len(empty) != 0 {
		t.Fatalf("PendingBySession(inbound) on empty table = %v, want empty map", empty)
	}

	// Three sessions of different depth, two rows with no session key at all,
	// plus outbound rows that must stay out of the inbound answer.
	for range 2 {
		mustEnqueue(t, repo, SpoolInbound, "telegram:a", "m", `{"s":"a"}`)
	}
	mustEnqueue(t, repo, SpoolInbound, "telegram:b", "m", `{"s":"b"}`)
	for range 2 {
		mustEnqueue(t, repo, SpoolInbound, "", "m", `{"s":"no-key"}`)
	}
	mustEnqueue(t, repo, SpoolInbound, "telegram:c", "m", `{"s":"c"}`)
	for range 2 {
		mustEnqueue(t, repo, SpoolOutbound, "telegram:a", "m", `{"out":true}`)
	}

	// Per-session counts, keyless rows grouped under "".
	got, err := repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:a": 2, "telegram:b": 1, "telegram:c": 1, "": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingBySession(inbound) = %v, want %v", got, want)
	}

	// The direction filter is strict: the same session is reported apart.
	out, err := repo.PendingBySession(SpoolOutbound)
	if err != nil {
		t.Fatalf("PendingBySession(outbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:a": 2}; !reflect.DeepEqual(out, want) {
		t.Fatalf("PendingBySession(outbound) = %v, want %v", out, want)
	}

	// Claimed rows are not pending. The two oldest inbound rows are both
	// "telegram:a", so that session disappears from the map entirely instead
	// of showing a misleading zero.
	held, err := repo.ClaimBatch(SpoolInbound, 2, "gw-1", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	got, err = repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:b": 1, "telegram:c": 1, "": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending after claim = %v, want %v", got, want)
	}
	if _, ok := got["telegram:a"]; ok {
		t.Errorf("pending map still counts fully claimed session telegram:a: %v", got)
	}

	// Releasing hands the rows back and the session reappears with its count.
	if n, err := repo.ReleaseClaims("gw-1"); err != nil || n != 2 {
		t.Fatalf("ReleaseClaims(gw-1) = (%d, %v), want (2, nil)", n, err)
	}
	got, err = repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:a": 2, "telegram:b": 1, "telegram:c": 1, "": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending after release = %v, want %v", got, want)
	}

	// Completing them removes the session for good: a completed row is gone
	// from the table, so it is neither pending nor claimed.
	if err := repo.Complete(spoolIDs(held)); err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	got, err = repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:b": 1, "telegram:c": 1, "": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending after complete = %v, want %v", got, want)
	}

	// A second claim takes the keyless rows and telegram:b, leaving only
	// telegram:c pending - a session with one row behaves like any other.
	second, err := repo.ClaimBatch(SpoolInbound, 3, "gw-2", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if len(second) != 3 {
		t.Fatalf("ClaimBatch(gw-2) returned %d items, want 3", len(second))
	}
	got, err = repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:c": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending after second claim = %v, want %v", got, want)
	}

	// Unknown direction: sentinel error and a nil map.
	bad, err := repo.PendingBySession("in")
	if !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("PendingBySession(\"in\") error = %v, want ErrUnknownDirection", err)
	}
	if bad != nil {
		t.Errorf("PendingBySession(\"in\") = %v, want nil map on error", bad)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Crash recovery: the reason the spool exists
// ──────────────────────────────────────────────────────────────────────────────

func TestSpoolSurvivesReopen(t *testing.T) {
	path := spoolPath(t)

	s := openStoreAtPath(t, path)
	repo := s.Spool()

	// Two inbound rows and one outbound row go in flight with the gateway
	// that is about to die; one inbound row is left pending on purpose.
	in1 := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m-1", `{"i":1}`)
	in2 := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m-2", `{"i":2}`)
	out1 := mustEnqueue(t, repo, SpoolOutbound, "telegram:main", "m-3", `{"i":3}`)
	pending := mustEnqueue(t, repo, SpoolInbound, "telegram:other", "m-4", `{"i":4}`)

	claimedIn, err := repo.ClaimBatch(SpoolInbound, 2, "gw-old", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(inbound) failed: %v", err)
	}
	if got, want := spoolIDs(claimedIn), []int64{in1, in2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimBatch(inbound) = %v, want %v", got, want)
	}
	claimedOut, err := repo.ClaimBatch(SpoolOutbound, 5, "gw-old", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(outbound) failed: %v", err)
	}
	if got, want := spoolIDs(claimedOut), []int64{out1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimBatch(outbound) = %v, want %v", got, want)
	}

	// Hard stop: no ReleaseClaims, no graceful drain. This is a crash.
	mustClose(t, s)

	// A restart: a brand new Store over the same file.
	s2 := openStoreAtPath(t, path)
	repo2 := s2.Spool()

	// (a) Nothing was lost: every unfinished row survived the restart.
	if got, want := countSpoolRows(t, s2), 4; got != want {
		t.Fatalf("spool rows after reopen = %d, want %d", got, want)
	}
	if got, want := mustStats(t, repo2), (SpoolStats{PendingInbound: 1, ClaimedInbound: 2, ClaimedOutbound: 1}); got != want {
		t.Fatalf("Stats() after reopen = %+v, want %+v", got, want)
	}

	// (b) They are still owned by the instance that died, claim timestamps
	// included - that is exactly what lets ReclaimStale find them later.
	if got, want := claimedBy(t, s2), []string{"gw-old"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claimed_by after reopen = %v, want %v", got, want)
	}
	var stamped int
	if err := s2.DB().QueryRow(
		`SELECT COUNT(*) FROM spool WHERE claimed_by = 'gw-old' AND claimed_at <> ''`,
	).Scan(&stamped); err != nil {
		t.Fatalf("count stamped claims failed: %v", err)
	}
	if stamped != 3 {
		t.Fatalf("%d orphaned rows kept their claimed_at, want 3", stamped)
	}

	// Until they are reclaimed, a new instance may only pick up what nobody
	// owns: the orphaned work stays invisible, so nothing is delivered twice
	// by accident.
	if items, err := repo2.ClaimBatch(SpoolInbound, 5, "gw-new", testNow().Add(time.Hour)); err != nil {
		t.Fatalf("ClaimBatch(inbound) after reopen failed: %v", err)
	} else if len(items) != 1 || items[0].ID != pending {
		t.Fatalf("ClaimBatch(inbound) after reopen = %v, want only pending row %d", spoolIDs(items), pending)
	}
	if items, err := repo2.ClaimBatch(SpoolOutbound, 5, "gw-new", testNow().Add(time.Hour)); err != nil {
		t.Fatalf("ClaimBatch(outbound) after reopen failed: %v", err)
	} else if len(items) != 0 {
		t.Fatalf("ClaimBatch(outbound) after reopen = %v, want nothing (row is orphaned)", spoolIDs(items))
	}

	// (c) The recovery sweep. The orphans were claimed at testNow(), so a
	// sweep two hours later with a one-hour window puts the cutoff exactly on
	// gw-new's claim: the comparison is strict, so gw-new keeps its row while
	// everything the dead instance held comes back.
	n, err := repo2.ReclaimStale(time.Hour, testNow().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ReclaimStale(1h) failed: %v", err)
	}
	if n != 3 {
		t.Fatalf("ReclaimStale(1h) = %d rows, want 3", n)
	}
	if got, want := mustStats(t, repo2), (SpoolStats{PendingInbound: 2, PendingOutbound: 1, ClaimedInbound: 1}); got != want {
		t.Fatalf("Stats() after reclaim = %+v, want %+v", got, want)
	}
	if got, want := claimedBy(t, s2), []string{"gw-new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claimed_by after reclaim = %v, want %v (the live claim must survive)", got, want)
	}

	// The recovered rows are claimable again, with their contents intact.
	recovered, err := repo2.ClaimBatch(SpoolInbound, 5, "gw-new-2", testNow().Add(2*time.Hour))
	if err != nil {
		t.Fatalf("ClaimBatch(inbound) after reclaim failed: %v", err)
	}
	if got, want := spoolIDs(recovered), []int64{in1, in2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered inbound rows = %v, want %v", got, want)
	}
	if recovered[0].Payload != `{"i":1}` || recovered[0].SessionKey != "telegram:main" {
		t.Errorf("recovered row lost its contents: %+v", recovered[0])
	}

	// (d) And the cycle closes: the successor completes the work it inherited,
	// including the row it had already claimed before the sweep, so the queue
	// empties exactly as it would have without the crash.
	if err := repo2.Complete(spoolIDs(recovered)); err != nil {
		t.Fatalf("Complete() after reclaim failed: %v", err)
	}
	if n, err := repo2.ReclaimStale(time.Hour, testNow().Add(3*time.Hour)); err != nil || n != 1 {
		t.Fatalf("ReclaimStale(gw-new) = (%d, %v), want (1, nil)", n, err)
	}
	orphanIn, err := repo2.ClaimBatch(SpoolInbound, 5, "gw-new-3", testNow().Add(3*time.Hour))
	if err != nil {
		t.Fatalf("ClaimBatch(inbound) failed: %v", err)
	}
	if len(orphanIn) != 1 || orphanIn[0].ID != pending {
		t.Fatalf("late inbound rows = %v, want [%d]", spoolIDs(orphanIn), pending)
	}
	orphanOut, err := repo2.ClaimBatch(SpoolOutbound, 5, "gw-new-3", testNow().Add(3*time.Hour))
	if err != nil {
		t.Fatalf("ClaimBatch(outbound) after reclaim failed: %v", err)
	}
	if len(orphanOut) != 1 || orphanOut[0].ID != out1 {
		t.Fatalf("recovered outbound rows = %v, want [%d]", spoolIDs(orphanOut), out1)
	}
	if err := repo2.Complete(append(spoolIDs(orphanIn), spoolIDs(orphanOut)...)); err != nil {
		t.Fatalf("Complete(remaining) failed: %v", err)
	}
	checkStats(t, repo2, SpoolStats{})
	if got, want := countSpoolRows(t, s2), 0; got != want {
		t.Fatalf("spool rows after full drain = %d, want %d", got, want)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Cheap invariants that callers rely on
// ──────────────────────────────────────────────────────────────────────────────

// TestSpoolClaimBatchLimitExceedsRows checks the "take everything" case:
// asking for more rows than exist is neither an error nor a failure.
func TestSpoolClaimBatchLimitExceedsRows(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	// Nothing pending at all: an empty slice, not nil, no error.
	items, err := repo.ClaimBatch(SpoolInbound, 1000, "gw", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(empty table) failed: %v", err)
	}
	if items == nil {
		t.Fatalf("ClaimBatch(empty table) returned nil, want empty slice")
	}
	if len(items) != 0 {
		t.Fatalf("ClaimBatch(empty table) = %v, want nothing", spoolIDs(items))
	}

	ids := []int64{
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m-1", `{"i":1}`),
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m-2", `{"i":2}`),
	}

	// A limit far above the row count returns exactly the two rows, in order.
	got, err := repo.ClaimBatch(SpoolInbound, 1<<20, "gw", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(huge limit) failed: %v", err)
	}
	if !reflect.DeepEqual(spoolIDs(got), ids) {
		t.Fatalf("ClaimBatch(huge limit) = %v, want %v", spoolIDs(got), ids)
	}

	// And it claims nothing extra: a second pass finds an empty queue.
	again, err := repo.ClaimBatch(SpoolInbound, 1<<20, "gw", testNow())
	if err != nil {
		t.Fatalf("second ClaimBatch(huge limit) failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second ClaimBatch(huge limit) = %v, want nothing", spoolIDs(again))
	}

	// Complete with ids that never existed (or were already deleted) is not
	// an error: a caller replaying ids it no longer owns must not break.
	if err := repo.Complete([]int64{999998, 999999}); err != nil {
		t.Errorf("Complete(unknown ids) failed: %v", err)
	}
	// A mix of live and unknown ids deletes what exists and ignores the rest.
	if err := repo.Complete([]int64{ids[0], 999998}); err != nil {
		t.Fatalf("Complete(mixed ids) failed: %v", err)
	}
	if got, want := countSpoolRows(t, s), 1; got != want {
		t.Fatalf("spool rows after mixed Complete = %d, want %d", got, want)
	}
	// Repeating an id is harmless too.
	if err := repo.Complete([]int64{ids[1], ids[1]}); err != nil {
		t.Errorf("Complete(repeated id) failed: %v", err)
	}
	if got, want := countSpoolRows(t, s), 0; got != want {
		t.Fatalf("spool rows after repeated Complete = %d, want %d", got, want)
	}
}

// TestSpoolTimestampsSortChronologically guards the invariant the SQL-side
// comparisons bet on: claimed_at is TEXT compared byte-wise, so rows stamped
// with different sub-second offsets - including an exact whole second - must
// order as strings the same way they order as times.
//
// This is a regression test. With time.RFC3339Nano, which trims trailing
// zeros, a whole second is stored as "...12:00:00Z" while a fractional one is
// "...12:00:00.5Z": byte-wise the whole second sorts *after* the fractional
// one ('Z' = 0x5A > '.' = 0x2E), so a single sweep could both resurrect a
// live claim and leave a genuinely stale row orphaned forever.
func TestSpoolTimestampsSortChronologically(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	base := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	offsets := []time.Duration{
		0, // exact whole second: the width that used to be trimmed away
		time.Nanosecond,
		250 * time.Millisecond,
		500 * time.Millisecond,
	}
	for i := range offsets {
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"i":`+string(rune('0'+i))+`}`)
	}

	// resetClaims puts every row back under a dedicated owner stamped at its
	// own offset. ClaimBatch hands out rows in id order, so row i always
	// receives offsets[i].
	resetClaims := func() {
		t.Helper()
		for i := range offsets {
			if _, err := repo.ReleaseClaims("dead-" + strconv.Itoa(i)); err != nil {
				t.Fatalf("ReleaseClaims(dead-%d) failed: %v", i, err)
			}
		}
		for i, off := range offsets {
			if _, err := repo.ClaimBatch(SpoolInbound, 1, "dead-"+strconv.Itoa(i), base.Add(off)); err != nil {
				t.Fatalf("claim row %d at offset %v failed: %v", i, off, err)
			}
		}
	}

	// Sweeps run with an ascending cutoff and a zero window: a row is stale
	// when its claim is strictly older than the cutoff. Each round starts
	// from the same state, so the expected count is simply the number of
	// offsets below the cutoff.
	sweeps := []struct {
		cutoff time.Duration
		want   int
	}{
		{0, 0},                      // nothing is strictly older than base
		{time.Nanosecond, 1},        // the whole-second claim
		{250 * time.Millisecond, 2}, // + the 1ns claim
		{500 * time.Millisecond, 3}, // + the .25s claim
		{time.Second, 4},            // + the .5s claim
	}
	ky := time.FixedZone("Asia/Seoul", 9*60*60)
	for _, sw := range sweeps {
		resetClaims()
		// The cutoff is expressed in a +09:00 zone: the instant, not the
		// zone, must decide the outcome.
		n, err := repo.ReclaimStale(0, base.Add(sw.cutoff).In(ky))
		if err != nil {
			t.Fatalf("ReclaimStale(cutoff %v) failed: %v", sw.cutoff, err)
		}
		if n != sw.want {
			t.Fatalf("ReclaimStale(cutoff %v) = %d rows, want %d", sw.cutoff, n, sw.want)
		}
	}

	// After the last sweep everything is pending again.
	resetClaims()
	if n, err := repo.ReclaimStale(0, base.Add(time.Hour).UTC()); err != nil || n != len(offsets) {
		t.Fatalf("final ReclaimStale() = (%d, %v), want (%d, nil)", n, err, len(offsets))
	}
	if got, want := mustStats(t, repo), (SpoolStats{PendingInbound: len(offsets)}); got != want {
		t.Fatalf("Stats() after full sweep = %+v, want %+v", got, want)
	}

	// processed_at is pruned by the same byte-wise comparison, so the ledger
	// has to keep the same property. Pruning is destructive, so each round
	// rebuilds the same four timestamps from scratch.
	resetLedger := func() {
		t.Helper()
		if _, err := s.DB().Exec(`DELETE FROM processed_messages`); err != nil {
			t.Fatalf("clear ledger failed: %v", err)
		}
		for i, off := range offsets {
			insertLedger(t, s, "telegram", "m-"+strconv.Itoa(i), base.Add(off))
		}
	}
	for _, sw := range sweeps {
		resetLedger()
		n, err := repo.PruneProcessed(0, base.Add(sw.cutoff).In(ky))
		if err != nil {
			t.Fatalf("PruneProcessed(cutoff %v) failed: %v", sw.cutoff, err)
		}
		if n != sw.want {
			t.Fatalf("PruneProcessed(cutoff %v) = %d rows, want %d", sw.cutoff, n, sw.want)
		}
	}
	resetLedger()
	if _, err := repo.PruneProcessed(0, base.Add(time.Hour).UTC()); err != nil {
		t.Fatalf("final PruneProcessed() failed: %v", err)
	}
	if got := mustStats(t, repo); got.ProcessedCount != 0 {
		t.Fatalf("ledger after full prune = %d rows, want 0", got.ProcessedCount)
	}
}

// TestSpoolReclaimStaleNonUTCNow is the narrow version of the same promise,
// phrased the way the gateway uses it: claim with a local-time now, sweep
// with a local-time now, and the row still comes back.
func TestSpoolReclaimStaleNonUTCNow(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	for i, zone := range []*time.Location{
		time.FixedZone("Asia/Seoul", 9*60*60),
		time.FixedZone("America/Bogota", -5*60*60),
		time.Local,
	} {
		id := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"z":`+string(rune('0'+i))+`}`)
		claimedAt := testNow()

		if _, err := repo.ClaimBatch(SpoolInbound, 1, "dead", claimedAt.In(zone)); err != nil {
			t.Fatalf("ClaimBatch(zone %v) failed: %v", zone, err)
		}
		var stored string
		if err := s.DB().QueryRow(`SELECT claimed_at FROM spool WHERE id = ?`, id).Scan(&stored); err != nil {
			t.Fatalf("read claimed_at failed: %v", err)
		}
		if !strings.HasSuffix(stored, "Z") {
			t.Fatalf("claimed_at %q was not normalised to UTC", stored)
		}
		if parsed, err := time.Parse(time.RFC3339Nano, stored); err != nil {
			t.Fatalf("claimed_at %q does not parse: %v", stored, err)
		} else if !parsed.Equal(claimedAt) {
			t.Fatalf("claimed_at %q is not the instant %v", stored, claimedAt)
		}

		// Too recent to be stale, then stale an hour later - both sweeps
		// asked in the same non-UTC zone.
		if n, err := repo.ReclaimStale(time.Hour, claimedAt.Add(time.Minute).In(zone)); err != nil || n != 0 {
			t.Fatalf("ReclaimStale(zone %v, fresh) = (%d, %v), want (0, nil)", zone, n, err)
		}
		n, err := repo.ReclaimStale(time.Hour, claimedAt.Add(2*time.Hour).In(zone))
		if err != nil || n != 1 {
			t.Fatalf("ReclaimStale(zone %v, stale) = (%d, %v), want (1, nil)", zone, n, err)
		}

		// Drain it so the next round starts from an empty queue and its
		// single-row claim cannot land on this row instead.
		if err := repo.Complete([]int64{id}); err != nil {
			t.Fatalf("Complete(%d) failed: %v", id, err)
		}
	}

	if got, want := mustStats(t, repo), (SpoolStats{}); got != want {
		t.Fatalf("Stats() after draining = %+v, want %+v", got, want)
	}
}
