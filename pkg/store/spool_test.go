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

func mustEnqueue(t *testing.T, repo *SpoolRepo, direction, sessionKey, msgID, payload string) int64 {
	t.Helper()

	id, err := repo.Enqueue(direction, "telegram", "chat-1", sessionKey, msgID, payload)
	if err != nil {
		t.Fatalf("Enqueue(%s, %q, %q) failed: %v", direction, sessionKey, msgID, err)
	}
	return id
}

func spoolIDs(items []SpoolItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

func testNow() time.Time {
	return time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
}

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

func spoolPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "spool.db")
}

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

func mustClose(t *testing.T, s *Store) {
	t.Helper()

	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
}

func countSpoolRows(t *testing.T, s *Store) int {
	t.Helper()

	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM spool`).Scan(&n); err != nil {
		t.Fatalf("count spool rows failed: %v", err)
	}
	return n
}

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

func mustStats(t *testing.T, repo *SpoolRepo) SpoolStats {
	t.Helper()

	stats, err := repo.Stats()
	if err != nil {
		t.Fatalf("Stats() failed: %v", err)
	}
	return stats
}

func TestSpoolEnqueueClaimComplete(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	var ids []int64
	for i := range 4 {
		id := mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m-1", `{"n":`+string(rune('0'+i))+`}`)
		if i > 0 && id <= ids[0] {
			t.Fatalf("Enqueue id #%d = %d, want > %d", i, id, ids[0])
		}
		ids = append(ids, id)
	}

	batch, err := repo.ClaimBatch(SpoolInbound, 2, "gw-1", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if got, want := spoolIDs(batch), ids[:2]; !reflect.DeepEqual(got, want) {
		t.Fatalf("ClaimBatch() ids = %v, want %v", got, want)
	}

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

	if got, want := mustPending(t, s), 2; got != want {
		t.Fatalf("pending after claim = %d, want %d", got, want)
	}

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

	second, err := repo.ClaimBatch(SpoolInbound, 3, "gw-b", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-b) failed: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("ClaimBatch(gw-b) returned %d items, want 0", len(second))
	}

	again, err := repo.ClaimBatch(SpoolInbound, 3, "gw-a", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(gw-a, retry) failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("re-claim by owner returned %d items, want 0", len(again))
	}

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

func TestSpoolEnqueueClaimedIsInvisibleToClaimBatch(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	if _, err := repo.EnqueueClaimed(SpoolInbound, "telegram", "chat-1", "sess", "m1", "payload", "instA", time.Now()); err != nil {
		t.Fatalf("EnqueueClaimed() failed: %v", err)
	}

	got, err := repo.ClaimBatch(SpoolInbound, 10, "instB", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(instB) failed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ClaimBatch(instB) = %v, want no rows: the row belongs to instA", spoolIDs(got))
	}
	checkStats(t, repo, SpoolStats{ClaimedInbound: 1})
	if owners := claimedBy(t, s); !reflect.DeepEqual(owners, []string{"instA"}) {
		t.Errorf("claimed_by = %v, want [instA]", owners)
	}

	if _, err := repo.EnqueueClaimed("sideways", "telegram", "chat-1", "sess", "m", "p", "instA", testNow()); !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("EnqueueClaimed(sideways) error = %v, want ErrUnknownDirection", err)
	}
	if n, err := repo.PendingBySession(SpoolInbound); err != nil || len(n) != 0 {
		t.Errorf("pending after a rejected enqueue = (%v, %v), want (empty, nil)", n, err)
	}
}

func TestSpoolReleaseIDsScopedToInstance(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	id, err := repo.EnqueueClaimed(SpoolInbound, "telegram", "chat-1", "sess", "m1", "payload", "instA", testNow())
	if err != nil {
		t.Fatalf("EnqueueClaimed() failed: %v", err)
	}

	if n, err := repo.ReleaseIDs([]int64{id}, "instB"); err != nil || n != 0 {
		t.Fatalf("ReleaseIDs(%d, instB) = (%d, %v), want (0, nil)", id, n, err)
	}
	if got, err := repo.ClaimBatch(SpoolInbound, 10, "instB", testNow()); err != nil || len(got) != 0 {
		t.Errorf("ClaimBatch(instB) = (%v, %v), want nothing: the row is still held by instA", spoolIDs(got), err)
	}
	if owners := claimedBy(t, s); !reflect.DeepEqual(owners, []string{"instA"}) {
		t.Fatalf("claimed_by = %v, want still [instA] after a foreign release", owners)
	}

	if n, err := repo.ReleaseIDs([]int64{id}, "instA"); err != nil || n != 1 {
		t.Fatalf("ReleaseIDs(%d, instA) = (%d, %v), want (1, nil)", id, n, err)
	}
	reclaimed, err := repo.ClaimBatch(SpoolInbound, 10, "instC", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(instC) failed: %v", err)
	}
	if got, want := spoolIDs(reclaimed), []int64{id}; !reflect.DeepEqual(got, want) {
		t.Errorf("ClaimBatch(instC) = %v, want %v", got, want)
	}
	if st := mustStats(t, repo); st.PendingInbound != 0 || st.ClaimedInbound != 1 {
		t.Errorf("stats after the re-claim = %+v, want 0 pending / 1 claimed by instC", st)
	}
}

func TestSpoolReleaseIDsEmptyIsNoop(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	held, err := repo.EnqueueClaimed(SpoolInbound, "telegram", "chat-1", "sess", "m1", "p", "instA", testNow())
	if err != nil {
		t.Fatalf("EnqueueClaimed() failed: %v", err)
	}

	if n, err := repo.ReleaseIDs(nil, "instA"); err != nil || n != 0 {
		t.Errorf("ReleaseIDs(nil) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := repo.ReleaseIDs([]int64{}, "instA"); err != nil || n != 0 {
		t.Errorf("ReleaseIDs(empty) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := repo.ReleaseIDs([]int64{held, 1 << 40}, "nobody"); err != nil || n != 0 {
		t.Errorf("ReleaseIDs(unknown owner) = (%d, %v), want (0, nil)", n, err)
	}
	checkStats(t, repo, SpoolStats{ClaimedInbound: 1})
}

func TestSpoolReclaimStale(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	staleID := mustEnqueue(t, repo, SpoolInbound, "telegram:stale", "m", `{"stale":true}`)
	freshID := mustEnqueue(t, repo, SpoolInbound, "telegram:fresh", "m", `{"fresh":true}`)

	now := testNow()

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

	if n, err := repo.ReclaimStale(30*time.Second, now); err != nil || n != 0 {
		t.Errorf("ReclaimStale() again = (%d, %v), want (0, nil)", n, err)
	}

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

	if n, err := repo.PendingBySession(SpoolInbound); err != nil || len(n) != 0 {
		t.Errorf("pending after rejected enqueues = (%v, %v), want (empty, nil)", n, err)
	}

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

	if n, err := repo.PendingBySession(SpoolInbound); err != nil || n["telegram:main"] != 1 {
		t.Errorf("pending after zero-limit claims = (%v, %v), want {telegram:main:1}", n, err)
	}
}

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

	if err := repo.IncAttempt(9999); err != nil {
		t.Errorf("IncAttempt(missing) failed: %v", err)
	}
}

func TestProcessedLedger(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	if got, err := repo.WasProcessed("telegram", "42"); err != nil || got {
		t.Fatalf("WasProcessed(telegram, 42) = (%v, %v), want (false, nil)", got, err)
	}

	if err := repo.MarkProcessed("telegram", "42"); err != nil {
		t.Fatalf("MarkProcessed() failed: %v", err)
	}
	if got, err := repo.WasProcessed("telegram", "42"); err != nil || !got {
		t.Fatalf("WasProcessed() after mark = (%v, %v), want (true, nil)", got, err)
	}

	if err := repo.MarkProcessed("telegram", "42"); err != nil {
		t.Errorf("second MarkProcessed() failed: %v", err)
	}
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

func checkStats(t *testing.T, repo *SpoolRepo, want SpoolStats) {
	t.Helper()

	if got := mustStats(t, repo); got != want {
		t.Fatalf("Stats() = %+v, want %+v", got, want)
	}
}

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

	checkStats(t, repo, SpoolStats{})

	for i := range 3 {
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "in", `{"i":`+string(rune('0'+i))+`}`)
	}
	for i := range 2 {
		mustEnqueue(t, repo, SpoolOutbound, "telegram:main", "out", `{"i":`+string(rune('0'+i))+`}`)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 3, PendingOutbound: 2})
	checkStatsMatchesTable(t, s, repo)

	if _, err := repo.ClaimBatch(SpoolInbound, 2, "gw-1", testNow()); err != nil {
		t.Fatalf("ClaimBatch(inbound) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, PendingOutbound: 2, ClaimedInbound: 2})

	if _, err := repo.ClaimBatch(SpoolOutbound, 2, "gw-1", testNow()); err != nil {
		t.Fatalf("ClaimBatch(outbound) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, ClaimedInbound: 2, ClaimedOutbound: 2})
	checkStatsMatchesTable(t, s, repo)

	for _, msgID := range []string{"m-1", "m-2", "m-1", ""} {
		if err := repo.MarkProcessed("telegram", msgID); err != nil {
			t.Fatalf("MarkProcessed(telegram, %q) failed: %v", msgID, err)
		}
	}
	if err := repo.MarkProcessed("discord", "m-1"); err != nil {
		t.Fatalf("MarkProcessed(discord, m-1) failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, ClaimedInbound: 2, ClaimedOutbound: 2, ProcessedCount: 3})

	claimedIn := mustClaimedIDs(t, s, SpoolInbound)
	if len(claimedIn) != 2 {
		t.Fatalf("claimed inbound ids = %v, want 2 rows", claimedIn)
	}
	if err := repo.Complete(claimedIn); err != nil {
		t.Fatalf("Complete() failed: %v", err)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, ClaimedOutbound: 2, ProcessedCount: 3})

	n, err := repo.ReleaseClaims("gw-1")
	if err != nil {
		t.Fatalf("ReleaseClaims(gw-1) failed: %v", err)
	}
	if n != 2 {
		t.Fatalf("ReleaseClaims(gw-1) = %d, want 2", n)
	}
	checkStats(t, repo, SpoolStats{PendingInbound: 1, PendingOutbound: 2, ProcessedCount: 3})
	checkStatsMatchesTable(t, s, repo)

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

	got, err := repo.PendingBySession(SpoolInbound)
	if err != nil {
		t.Fatalf("PendingBySession(inbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:a": 2, "telegram:b": 1, "telegram:c": 1, "": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PendingBySession(inbound) = %v, want %v", got, want)
	}

	out, err := repo.PendingBySession(SpoolOutbound)
	if err != nil {
		t.Fatalf("PendingBySession(outbound) failed: %v", err)
	}
	if want := map[string]int{"telegram:a": 2}; !reflect.DeepEqual(out, want) {
		t.Fatalf("PendingBySession(outbound) = %v, want %v", out, want)
	}

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

	bad, err := repo.PendingBySession("in")
	if !errors.Is(err, ErrUnknownDirection) {
		t.Errorf("PendingBySession(\"in\") error = %v, want ErrUnknownDirection", err)
	}
	if bad != nil {
		t.Errorf("PendingBySession(\"in\") = %v, want nil map on error", bad)
	}
}

func TestSpoolSurvivesReopen(t *testing.T) {
	path := spoolPath(t)

	s := openStoreAtPath(t, path)
	repo := s.Spool()

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

	mustClose(t, s)

	s2 := openStoreAtPath(t, path)
	repo2 := s2.Spool()

	if got, want := countSpoolRows(t, s2), 4; got != want {
		t.Fatalf("spool rows after reopen = %d, want %d", got, want)
	}
	if got, want := mustStats(t, repo2), (SpoolStats{PendingInbound: 1, ClaimedInbound: 2, ClaimedOutbound: 1}); got != want {
		t.Fatalf("Stats() after reopen = %+v, want %+v", got, want)
	}

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

func TestSpoolClaimBatchLimitExceedsRows(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

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

	got, err := repo.ClaimBatch(SpoolInbound, 1<<20, "gw", testNow())
	if err != nil {
		t.Fatalf("ClaimBatch(huge limit) failed: %v", err)
	}
	if !reflect.DeepEqual(spoolIDs(got), ids) {
		t.Fatalf("ClaimBatch(huge limit) = %v, want %v", spoolIDs(got), ids)
	}

	again, err := repo.ClaimBatch(SpoolInbound, 1<<20, "gw", testNow())
	if err != nil {
		t.Fatalf("second ClaimBatch(huge limit) failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second ClaimBatch(huge limit) = %v, want nothing", spoolIDs(again))
	}

	if err := repo.Complete([]int64{999998, 999999}); err != nil {
		t.Errorf("Complete(unknown ids) failed: %v", err)
	}
	if err := repo.Complete([]int64{ids[0], 999998}); err != nil {
		t.Fatalf("Complete(mixed ids) failed: %v", err)
	}
	if got, want := countSpoolRows(t, s), 1; got != want {
		t.Fatalf("spool rows after mixed Complete = %d, want %d", got, want)
	}
	if err := repo.Complete([]int64{ids[1], ids[1]}); err != nil {
		t.Errorf("Complete(repeated id) failed: %v", err)
	}
	if got, want := countSpoolRows(t, s), 0; got != want {
		t.Fatalf("spool rows after repeated Complete = %d, want %d", got, want)
	}
}

func TestSpoolTimestampsSortChronologically(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	base := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	offsets := []time.Duration{
		0,
		time.Nanosecond,
		250 * time.Millisecond,
		500 * time.Millisecond,
	}
	for i := range offsets {
		mustEnqueue(t, repo, SpoolInbound, "telegram:main", "m", `{"i":`+string(rune('0'+i))+`}`)
	}

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

	sweeps := []struct {
		cutoff time.Duration
		want   int
	}{
		{0, 0},
		{time.Nanosecond, 1},
		{250 * time.Millisecond, 2},
		{500 * time.Millisecond, 3},
		{time.Second, 4},
	}
	ky := time.FixedZone("Asia/Seoul", 9*60*60)
	for _, sw := range sweeps {
		resetClaims()
		n, err := repo.ReclaimStale(0, base.Add(sw.cutoff).In(ky))
		if err != nil {
			t.Fatalf("ReclaimStale(cutoff %v) failed: %v", sw.cutoff, err)
		}
		if n != sw.want {
			t.Fatalf("ReclaimStale(cutoff %v) = %d rows, want %d", sw.cutoff, n, sw.want)
		}
	}

	resetClaims()
	if n, err := repo.ReclaimStale(0, base.Add(time.Hour).UTC()); err != nil || n != len(offsets) {
		t.Fatalf("final ReclaimStale() = (%d, %v), want (%d, nil)", n, err, len(offsets))
	}
	if got, want := mustStats(t, repo), (SpoolStats{PendingInbound: len(offsets)}); got != want {
		t.Fatalf("Stats() after full sweep = %+v, want %+v", got, want)
	}

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

func TestSpoolReclaimStaleNonUTCNow(t *testing.T) {
	s := openTestStore(t)
	repo := s.Spool()

	for i, zone := range []*time.Location{
		time.FixedZone("Asia/Seoul", 9*60*60),
		time.FixedZone("America/Bogota", -5*60*60),
		time.Local, //nolint:gosmopolitan // deliberately the host's local zone: this test proves the store normalises ANY zone, including the machine's own, to UTC
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

		if n, err := repo.ReclaimStale(time.Hour, claimedAt.Add(time.Minute).In(zone)); err != nil || n != 0 {
			t.Fatalf("ReclaimStale(zone %v, fresh) = (%d, %v), want (0, nil)", zone, n, err)
		}
		n, err := repo.ReclaimStale(time.Hour, claimedAt.Add(2*time.Hour).In(zone))
		if err != nil || n != 1 {
			t.Fatalf("ReclaimStale(zone %v, stale) = (%d, %v), want (1, nil)", zone, n, err)
		}

		if err := repo.Complete([]int64{id}); err != nil {
			t.Fatalf("Complete(%d) failed: %v", id, err)
		}
	}

	if got, want := mustStats(t, repo), (SpoolStats{}); got != want {
		t.Fatalf("Stats() after draining = %+v, want %+v", got, want)
	}
}
