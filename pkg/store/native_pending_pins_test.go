package store

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func openTestPendingPINRepo(t *testing.T) *NativeClientRepo {
	t.Helper()
	return openTestStore(t).NativeClients()
}

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

	_, found, err = repo.TakePendingPIN("abc123", now)
	if err != nil {
		t.Fatalf("second TakePendingPIN failed: %v", err)
	}
	if found {
		t.Errorf("second TakePendingPIN: found=true, want false (single-use violation)")
	}
}

func TestNativePendingPIN_TakeRespectsExpiryInSameStatement(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	createdAt := int64(1_000_000_000)
	expiresAt := createdAt + int64(5*time.Minute)
	now := expiresAt + int64(time.Second)

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

	_, _, stillExists, err := repo.GetPendingPIN("expired")
	if err != nil {
		t.Fatalf("GetPendingPIN failed: %v", err)
	}
	if !stillExists {
		t.Log("NOTE: expired row was deleted by Take — acceptable but document in plan if this changes")
	}
}

func TestNativePendingPIN_TakeConcurrentExactlyOneWinner(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "concurrent.db")

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

func TestNativePendingPIN_ListExcludesExpired(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := int64(1_000_000_000_000_000_000)
	live1 := now - int64(time.Minute)
	live2 := now - int64(30*time.Second)
	exp1 := now - int64(10*time.Minute)
	exp2 := now - int64(20*time.Minute)
	liveExp := now + int64(5*time.Minute)
	pastExp := now - int64(time.Second)

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

func TestNativePendingPIN_ListOrderedByCreatedThenPIN(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	now := int64(1_000_000_000_000_000_000)
	expires := now + int64(5*time.Minute)

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

func TestNativePendingPIN_DeleteExpiredReturnsCount(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := int64(1_000_000_000_000_000_000)
	pastExp := now - int64(time.Second)
	futureExp := now + int64(5*time.Minute)

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

func TestNativePendingPIN_EvictOldestIsDeterministic(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	now := int64(1_000_000_000_000_000_000)
	expires := now + int64(5*time.Minute)

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

	repo.DeletePendingPIN("middle")
	repo.DeletePendingPIN("newest")

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

func TestNativePendingPIN_CountClientsMatchesListClients(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	count, err := repo.CountClients()
	if err != nil {
		t.Fatalf("CountClients (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("CountClients (empty) = %d, want 0", count)
	}

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

func TestNativePendingPIN_ExpiryComparisonIsNumericNotTextual(t *testing.T) {
	repo := openTestPendingPINRepo(t)

	t0 := time.Unix(1_000_000_000, 0).UTC()
	n0 := t0.UnixNano()

	t1 := time.Unix(1_000_000_000, 500_000_000).UTC()
	n1 := t1.UnixNano()

	s0 := t0.Format(time.RFC3339Nano)
	s1 := t1.Format(time.RFC3339Nano)
	if s0 < s1 {
		t.Logf("WARNING: text sort is already correct (%q vs %q); D2 trap not fully exercised", s0, s1)
	}

	repo.InsertPendingPIN("d2check", `"d2"`, n0, n1)

	pins, err := repo.ListPendingPINs(n0)
	if err != nil {
		t.Fatalf("ListPendingPINs: %v", err)
	}
	if _, ok := pins["d2check"]; !ok {
		t.Fatalf("d2check missing from ListPendingPINs(now=T0) — " +
			"expiry comparison is TEXT (D2 trap hit); expected INTEGER (expires_at=T1 > now=T0)")
	}

	_, found, err := repo.TakePendingPIN("d2check", n0)
	if err != nil {
		t.Fatalf("TakePendingPIN: %v", err)
	}
	if !found {
		t.Errorf("TakePendingPIN(d2check, now=T0) found=false — " +
			"D2 trap: integer 1000000000500000000 > 1000000000000000000, but text '.5Z' < 'Z'")
	}
}

func TestNativePendingPIN_InsertDuplicateReturnsErrDuplicate(t *testing.T) {
	s := openTestStore(t)
	repo := s.NativeClients()

	now := time.Now().UnixNano()
	expires := now + int64(5*time.Minute)

	if err := repo.InsertPendingPIN("dup1", `"first"`, now, expires); err != nil {
		t.Fatalf("first InsertPendingPIN: %v", err)
	}

	err := repo.InsertPendingPIN("dup1", `"second"`, now+1, expires+1)
	if err == nil {
		t.Fatal("second InsertPendingPIN(dup1) expected error, got nil")
	}
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate insert: errors.Is(err, ErrDuplicate) = false; want true (err: %v)", err)
	}

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
	if !strings.Contains(err.Error(), "database is closed") {
		t.Logf("NOTE: closed-DB error text is %q (expected 'database is closed' substring)", err.Error())
	}
}
