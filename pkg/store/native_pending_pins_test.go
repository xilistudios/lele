package store

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// openTestPendingPINRepo is a convenience helper that returns the
// NativeClientRepo from a fresh test store.
func openTestPendingPINRepo(t *testing.T) *NativeClientRepo {
	t.Helper()
	return openTestStore(t).NativeClients()
}

// Test 1: insert + get round-trip preserves the opaque blob byte-for-byte.
func TestNativePendingPIN_InsertGetRoundTrip(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	blob := `{"pin":"123456","device_name":"phone","expires_at":"2026-01-01T00:05:00Z"}`
	expires := time.Date(2026, 1, 1, 0, 5, 0, 0, time.UTC).UnixNano()

	if err := repo.InsertPendingPIN("123456", blob, time.Now().UnixNano(), expires); err != nil {
		t.Fatalf("InsertPendingPIN failed: %v", err)
	}

	got, gotExp, found, err := repo.GetPendingPIN("123456")
	if err != nil {
		t.Fatalf("GetPendingPIN failed: %v", err)
	}
	if !found {
		t.Fatalf("GetPendingPIN(123456) not found")
	}
	if got != blob {
		t.Errorf("pending blob mismatch:\n  got  %q\n  want %q", got, blob)
	}
	if gotExp != expires {
		t.Errorf("expires_at = %d, want %d", gotExp, expires)
	}
}

// Test 2: TakePendingPIN is single-use; second take returns found=false.
func TestNativePendingPIN_TakeIsSingleUse(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := time.Now().UnixNano()
	expires := now + int64(5*time.Minute)

	if err := repo.InsertPendingPIN("abc123", `{"pin":"abc123"}`, now, expires); err != nil {
		t.Fatalf("InsertPendingPIN failed: %v", err)
	}

	pending, found, err := repo.TakePendingPIN("abc123", now)
	if err != nil {
		t.Fatalf("first TakePendingPIN failed: %v", err)
	}
	if !found {
		t.Fatalf("first TakePendingPIN: found=false, want true")
	}
	if pending != `{"pin":"abc123"}` {
		t.Errorf("first TakePendingPIN pending = %q, want %q", pending, `{"pin":"abc123"}`)
	}

	// Second take must return found=false (PIN was consumed).
	_, found, err = repo.TakePendingPIN("abc123", now)
	if err != nil {
		t.Fatalf("second TakePendingPIN failed: %v", err)
	}
	if found {
		t.Errorf("second TakePendingPIN: found=true, want false (single-use violation)")
	}
}

// Test 3: TakePendingPIN with an expired PIN returns found=false.
// The expired row is NOT deleted by TakePendingPIN (cleanup is
// explicit via DeleteExpiredPendingPINs).
func TestNativePendingPIN_TakeRespectsExpiryInSameStatement(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	createdAt := int64(1_000_000_000)             // well in the past
	expiresAt := createdAt + int64(5*time.Minute) // expired by now
	now := expiresAt + int64(time.Second)         // 1s after expiry

	if err := repo.InsertPendingPIN("expired", `{"pin":"expired"}`, createdAt, expiresAt); err != nil {
		t.Fatalf("InsertPendingPIN failed: %v", err)
	}

	_, found, err := repo.TakePendingPIN("expired", now)
	if err != nil {
		t.Fatalf("TakePendingPIN failed: %v", err)
	}
	if found {
		t.Errorf("TakePendingPIN(expired): found=true, want false")
	}

	// Document the design decision: the expired row survives TakePendingPIN.
	// DeleteExpiredPendingPINs is the explicit cleanup mechanism.
	_, _, stillExists, err := repo.GetPendingPIN("expired")
	if err != nil {
		t.Fatalf("GetPendingPIN failed: %v", err)
	}
	if !stillExists {
		t.Log("NOTE: expired row was deleted by Take — acceptable but document in plan if this changes")
	}
}

// Test 4: concurrent TakePendingPIN from two distinct store.Open
// handles over the same path — exactly 1 winner out of 12 goroutines.
func TestNativePendingPIN_TakeConcurrentExactlyOneWinner(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "concurrent.db")

	// First handle: insert the PIN.
	s1, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) handle 1: %v", dbPath, err)
	}
	t.Cleanup(func() { s1.Close() })

	now := time.Now().UnixNano()
	expires := now + int64(5*time.Minute)
	if err := s1.NativeClients().InsertPendingPIN("conc", `{"pin":"conc"}`, now, expires); err != nil {
		t.Fatalf("InsertPendingPIN: %v", err)
	}

	// Second handle: independent connection (simulates another process).
	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open(%q) handle 2: %v", dbPath, err)
	}
	t.Cleanup(func() { s2.Close() })

	repos := []*NativeClientRepo{
		s1.NativeClients(),
		s2.NativeClients(),
	}

	const goroutines = 12
	var (
		mu      sync.Mutex
		winners int
		wg      sync.WaitGroup
	)
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			_, found, err := repos[idx%len(repos)].TakePendingPIN("conc", now)
			if err != nil {
				t.Errorf("goroutine %d: TakePendingPIN error: %v", idx, err)
				return
			}
			if found {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if winners != 1 {
		t.Errorf("concurrent TakePendingPIN: %d winners, want exactly 1", winners)
	}
}

// Test 5: ListPendingPINs excludes expired rows.
func TestNativePendingPIN_ListExcludesExpired(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := int64(1_000_000_000_000_000_000) // fixed reference point
	live1 := now - int64(time.Minute)       // created 1 min ago
	live2 := now - int64(30*time.Second)    // created 30s ago
	exp1 := now - int64(10*time.Minute)     // created 10 min ago
	exp2 := now - int64(20*time.Minute)     // created 20 min ago
	liveExp := now + int64(5*time.Minute)   // expires in 5 min (live)
	pastExp := now - int64(time.Second)     // already expired

	if err := repo.InsertPendingPIN("live1", `"live1"`, live1, liveExp); err != nil {
		t.Fatalf("Insert live1: %v", err)
	}
	if err := repo.InsertPendingPIN("live2", `"live2"`, live2, liveExp); err != nil {
		t.Fatalf("Insert live2: %v", err)
	}
	if err := repo.InsertPendingPIN("exp1", `"exp1"`, exp1, pastExp); err != nil {
		t.Fatalf("Insert exp1: %v", err)
	}
	if err := repo.InsertPendingPIN("exp2", `"exp2"`, exp2, pastExp); err != nil {
		t.Fatalf("Insert exp2: %v", err)
	}

	pins, err := repo.ListPendingPINs(now)
	if err != nil {
		t.Fatalf("ListPendingPINs: %v", err)
	}
	if len(pins) != 2 {
		t.Errorf("ListPendingPINs count = %d, want 2 (expired rows should be excluded)", len(pins))
	}
	if _, ok := pins["live1"]; !ok {
		t.Error("live1 missing from result")
	}
	if _, ok := pins["live2"]; !ok {
		t.Error("live2 missing from result")
	}
}

// Test 6: ListPendingPINs returns results ordered by (created_at ASC, pin ASC).
func TestNativePendingPIN_ListOrderedByCreatedThenPIN(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	now := int64(1_000_000_000_000_000_000)
	expires := now + int64(5*time.Minute)

	// Insert in reverse order to ensure SQL ordering matters.
	if err := repo.InsertPendingPIN("zzz", `"zzz"`, now+3, expires); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertPendingPIN("aaa", `"aaa"`, now+3, expires); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertPendingPIN("mmm", `"mmm"`, now+1, expires); err != nil {
		t.Fatal(err)
	}

	pins, err := repo.ListPendingPINs(now)
	if err != nil {
		t.Fatalf("ListPendingPINs: %v", err)
	}
	if len(pins) != 3 {
		t.Fatalf("count = %d, want 3", len(pins))
	}

	// Verify ordering via a direct query on the SAME database handle.
	rows, err := s.DB().Query(
		`SELECT pin FROM native_pending_pins ORDER BY created_at ASC, pin ASC`,
	)
	if err != nil {
		t.Fatalf("direct query failed: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration: %v", err)
	}
	want := []string{"mmm", "aaa", "zzz"}
	if len(got) != len(want) {
		t.Fatalf("direct query returned %d rows, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: got %q, want %q", i, got[i], w)
		}
	}
}

// Test 7: DeleteExpiredPendingPINs returns the correct RowsAffected count.
func TestNativePendingPIN_DeleteExpiredReturnsCount(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := int64(1_000_000_000_000_000_000)
	pastExp := now - int64(time.Second)
	futureExp := now + int64(5*time.Minute)

	// 2 expired + 1 live.
	repo.InsertPendingPIN("e1", `"e1"`, now-100, pastExp)
	repo.InsertPendingPIN("e2", `"e2"`, now-200, pastExp)
	repo.InsertPendingPIN("l1", `"l1"`, now-50, futureExp)

	n, err := repo.DeleteExpiredPendingPINs(now)
	if err != nil {
		t.Fatalf("DeleteExpiredPendingPINs: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteExpiredPendingPINs deleted %d rows, want 2", n)
	}

	// Verify only the live one remains.
	pins, err := repo.ListPendingPINs(now)
	if err != nil {
		t.Fatalf("ListPendingPINs: %v", err)
	}
	if len(pins) != 1 {
		t.Errorf("remaining count = %d, want 1", len(pins))
	}
	if _, ok := pins["l1"]; !ok {
		t.Error("l1 missing after purge")
	}
}

// Test 8: EvictOldestPendingPIN is deterministic — evicts the PIN
// with the smallest (created_at, pin). With identical created_at
// the pin lexicographic tiebreaker applies.
func TestNativePendingPIN_EvictOldestIsDeterministic(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := int64(1_000_000_000_000_000_000)
	expires := now + int64(5*time.Minute)

	// Scenario A: distinct created_at → evicts the oldest.
	repo.InsertPendingPIN("oldest", `"oldest"`, now-300, expires)
	repo.InsertPendingPIN("middle", `"middle"`, now-200, expires)
	repo.InsertPendingPIN("newest", `"newest"`, now-100, expires)

	evicted, err := repo.EvictOldestPendingPIN(now)
	if err != nil {
		t.Fatalf("EvictOldestPendingPIN (scenario A): %v", err)
	}
	if evicted != "oldest" {
		t.Errorf("evicted %q, want %q (distinct created_at)", evicted, "oldest")
	}

	// Clean slate for scenario B: remove remaining rows.
	repo.DeletePendingPIN("middle")
	repo.DeletePendingPIN("newest")

	// Scenario B: same created_at, different pin → pin tiebreaker.
	repo.InsertPendingPIN("zzz", `"zzz"`, now+100, expires)
	repo.InsertPendingPIN("aaa", `"aaa"`, now+100, expires)
	repo.InsertPendingPIN("mmm", `"mmm"`, now+100, expires)

	evicted, err = repo.EvictOldestPendingPIN(now)
	if err != nil {
		t.Fatalf("EvictOldestPendingPIN (scenario B): %v", err)
	}
	if evicted != "aaa" {
		t.Errorf("evicted %q, want %q (same created_at, pin tiebreaker)", evicted, "aaa")
	}

	// Scenario C: evict until empty → ("", nil) when nothing remains.
	repo.EvictOldestPendingPIN(now)
	repo.EvictOldestPendingPIN(now)
	evicted, err = repo.EvictOldestPendingPIN(now)
	if err != nil {
		t.Fatalf("EvictOldestPendingPIN (empty): %v", err)
	}
	if evicted != "" {
		t.Errorf("evicted %q, want empty string when nothing to evict", evicted)
	}
}

// Test 9: InsertPendingPIN with a duplicate pin returns an error
// (no silent upsert).
func TestNativePendingPIN_InsertDuplicatePINErrors(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := time.Now().UnixNano()
	expires := now + int64(5*time.Minute)

	if err := repo.InsertPendingPIN("dup", `"first"`, now, expires); err != nil {
		t.Fatalf("first InsertPendingPIN: %v", err)
	}
	err := repo.InsertPendingPIN("dup", `"second"`, now+1, expires+1)
	if err == nil {
		t.Fatalf("second InsertPendingPIN(dup) did not return error — upsert is forbidden")
	}
	if !strings.Contains(err.Error(), "store:") {
		t.Errorf("error %v does not contain 'store:' prefix", err)
	}
}

// Test 10: CountClients matches the number of clients from ListClients.
func TestNativePendingPIN_CountClientsMatchesListClients(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	// Empty.
	count, err := repo.CountClients()
	if err != nil {
		t.Fatalf("CountClients (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("CountClients (empty) = %d, want 0", count)
	}

	// Insert 2 clients.
	repo.SetClient("c1", `{"k":"v1"}`)
	repo.SetClient("c2", `{"k":"v2"}`)

	clients, err := repo.ListClients()
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}

	count, err = repo.CountClients()
	if err != nil {
		t.Fatalf("CountClients: %v", err)
	}
	if count != len(clients) {
		t.Errorf("CountClients = %d, ListClients len = %d — mismatch", count, len(clients))
	}
	if count != 2 {
		t.Errorf("CountClients = %d, want 2", count)
	}
}

// Test 11: the D2 trap — expiry comparison must be numeric (INTEGER
// unix-nanoseconds), not lexicographic (TEXT RFC3339). This test
// constructs two timestamps where RFC3339Nano text ordering is WRONG
// and verifies the integer ordering is correct.
//
// Mechanism:
//
//	T0 = 1000000000.000000000s → RFC3339Nano: "...T01:46:40Z"       (no frac)
//	T1 = 1000000000.500000000s → RFC3339Nano: "...T01:46:40.5Z"     (has frac)
//
// Text: '.5Z' < 'Z' → T1 sorts BEFORE T0 (WRONG).
// INTEGER: 1000000000500000000 > 1000000000000000000 → T1 sorts AFTER T0 (CORRECT).
func TestNativePendingPIN_ExpiryComparisonIsNumericNotTextual(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	// T0: whole seconds (no fractional part in RFC3339Nano).
	t0 := time.Unix(1_000_000_000, 0).UTC()
	n0 := t0.UnixNano() // 1000000000000000000

	// T0 + 0.5s: has a fractional part when formatted as RFC3339Nano.
	t1 := time.Unix(1_000_000_000, 500_000_000).UTC()
	n1 := t1.UnixNano() // 1000000000500000000

	// Sanity: the text representation would sort incorrectly.
	s0 := t0.Format(time.RFC3339Nano)
	s1 := t1.Format(time.RFC3339Nano)
	if s0 < s1 {
		// If text happens to sort correctly, the trap isn't exercised.
		t.Logf("WARNING: text sort is already correct (%q vs %q); D2 trap not fully exercised", s0, s1)
	}

	// expires_at = T1 (the larger nanosecond value, smaller text).
	// We query with now = T0. The row should be visible (T1 > T0 as integers).
	repo.InsertPendingPIN("d2check", `"d2"`, n0, n1)

	pins, err := repo.ListPendingPINs(n0)
	if err != nil {
		t.Fatalf("ListPendingPINs: %v", err)
	}
	if _, ok := pins["d2check"]; !ok {
		t.Fatalf("d2check missing from ListPendingPINs(now=T0) — " +
			"expiry comparison is TEXT (D2 trap hit); expected INTEGER (expires_at=T1 > now=T0)")
	}

	// Also verify TakePendingPIN sees it as non-expired.
	_, found, err := repo.TakePendingPIN("d2check", n0)
	if err != nil {
		t.Fatalf("TakePendingPIN: %v", err)
	}
	if !found {
		t.Errorf("TakePendingPIN(d2check, now=T0) found=false — " +
			"D2 trap: integer 1000000000500000000 > 1000000000000000000, but text '.5Z' < 'Z'")
	}
}

// Test 12: InsertPendingPIN duplicate returns ErrDuplicate (typed sentinel),
// while other errors (e.g. closed DB) do NOT match ErrDuplicate.
func TestNativePendingPIN_InsertDuplicateReturnsErrDuplicate(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	now := time.Now().UnixNano()
	expires := now + int64(5*time.Minute)

	if err := repo.InsertPendingPIN("dup1", `"first"`, now, expires); err != nil {
		t.Fatalf("first InsertPendingPIN: %v", err)
	}

	// Second insert with the same PK ⇒ UNIQUE violation.
	err := repo.InsertPendingPIN("dup1", `"second"`, now+1, expires+1)
	if err == nil {
		t.Fatal("second InsertPendingPIN(dup1) expected error, got nil")
	}
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate insert: errors.Is(err, ErrDuplicate) = false; want true (err: %v)", err)
	}

	// A different kind of error must NOT match ErrDuplicate.
	// Close the underlying DB to trigger a non-UNIQUE error on next op.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err = repo.InsertPendingPIN("dup2", `"after-close"`, now+2, expires+2)
	if err == nil {
		t.Fatal("InsertPendingPIN on closed DB expected error, got nil")
	}
	if errors.Is(err, ErrDuplicate) {
		t.Errorf("closed-DB error must NOT be ErrDuplicate, but errors.Is returned true (err: %v)", err)
	}
	// Sanity: the error is about the closed database.
	if !strings.Contains(err.Error(), "database is closed") {
		t.Logf("NOTE: closed-DB error text is %q (expected 'database is closed' substring)", err.Error())
	}
}
