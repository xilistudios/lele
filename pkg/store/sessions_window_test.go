package store

import (
	"fmt"
	"path/filepath"
	"testing"
)

func newWindowTestRepo(t *testing.T) *SessionRepo {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	repo := s.Sessions()
	if err := repo.UpsertSession(SessionMeta{Key: "win:s1"}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := repo.InsertMessage("win:s1", i, "user", fmt.Sprintf(`{"role":"user","content":"m%d"}`, i), i < 7); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	return repo
}

func TestLoadMessagesBeforeLimited(t *testing.T) {
	repo := newWindowTestRepo(t)

	rows, err := repo.LoadMessagesBeforeLimited("win:s1", 7, 5)
	if err != nil {
		t.Fatalf("LoadMessagesBeforeLimited: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
	// Newest first (DESC).
	if rows[0].Seq != 6 || rows[4].Seq != 2 {
		t.Errorf("expected seqs 6..2 DESC, got %d..%d", rows[0].Seq, rows[4].Seq)
	}

	// Limit larger than the region returns everything below beforeSeq.
	rows, err = repo.LoadMessagesBeforeLimited("win:s1", 7, 100)
	if err != nil {
		t.Fatalf("LoadMessagesBeforeLimited full: %v", err)
	}
	if len(rows) != 7 || rows[len(rows)-1].Seq != 0 {
		t.Errorf("expected 7 rows ending at seq 0, got %d rows ending at %d", len(rows), rows[len(rows)-1].Seq)
	}

	// Empty region.
	rows, err = repo.LoadMessagesBeforeLimited("win:s1", 0, 5)
	if err != nil {
		t.Fatalf("LoadMessagesBeforeLimited empty: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows below seq 0, got %d", len(rows))
	}
}

func TestLoadMessagesBetweenLimited(t *testing.T) {
	repo := newWindowTestRepo(t)

	// Bounded: 3 < seq < 7 → rows 4,5,6 ASC.
	rows, err := repo.LoadMessagesBetweenLimited("win:s1", 3, 7, 10)
	if err != nil {
		t.Fatalf("LoadMessagesBetweenLimited: %v", err)
	}
	if len(rows) != 3 || rows[0].Seq != 4 || rows[2].Seq != 6 {
		t.Fatalf("expected seqs 4..6, got %+v", rows)
	}

	// Unbounded above: seq > 8 → rows 9.
	rows, err = repo.LoadMessagesBetweenLimited("win:s1", 8, -1, 10)
	if err != nil {
		t.Fatalf("LoadMessagesBetweenLimited unbounded: %v", err)
	}
	if len(rows) != 1 || rows[0].Seq != 9 {
		t.Fatalf("expected seq 9, got %+v", rows)
	}

	// Limit respected and applied to the OLDEST side (ASC).
	rows, err = repo.LoadMessagesBetweenLimited("win:s1", 0, 7, 2)
	if err != nil {
		t.Fatalf("LoadMessagesBetweenLimited limit: %v", err)
	}
	if len(rows) != 2 || rows[0].Seq != 1 || rows[1].Seq != 2 {
		t.Fatalf("expected seqs 1,2, got %+v", rows)
	}
}

func TestCountMessagesBefore(t *testing.T) {
	repo := newWindowTestRepo(t)

	n, err := repo.CountMessagesBefore("win:s1", 7)
	if err != nil {
		t.Fatalf("CountMessagesBefore: %v", err)
	}
	if n != 7 {
		t.Errorf("CountMessagesBefore(7) = %d, want 7", n)
	}
	n, err = repo.CountMessagesBefore("win:s1", 0)
	if err != nil {
		t.Fatalf("CountMessagesBefore(0): %v", err)
	}
	if n != 0 {
		t.Errorf("CountMessagesBefore(0) = %d, want 0", n)
	}
	n, err = repo.CountMessagesBefore("win:s1", -1)
	if err != nil {
		t.Fatalf("CountMessagesBefore(-1): %v", err)
	}
	if n != 10 {
		t.Errorf("CountMessagesBefore(all) = %d, want 10", n)
	}
}
func TestLoadMessagesFullBeforeSeq(t *testing.T) {
	repo := newWindowTestRepo(t)

	// Mixed excluded flags in the fixture (seq < 7 excluded, 7..9 not): the
	// reader must surface the persisted flag per row, in seq ASC order.
	rows, err := repo.LoadMessagesFullBeforeSeq("win:s1", 7)
	if err != nil {
		t.Fatalf("LoadMessagesFullBeforeSeq: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("expected 7 rows, got %d", len(rows))
	}
	for i, row := range rows {
		if row.Seq != i {
			t.Errorf("row %d seq = %d, want %d", i, row.Seq, i)
		}
		if !row.Excluded {
			t.Errorf("row %d excluded = false, want true (fixture excludes seq<7)", i)
		}
		wantJSON := fmt.Sprintf(`{"role":"user","content":"m%d"}`, i)
		if row.JSON != wantJSON {
			t.Errorf("row %d JSON = %q, want %q", i, row.JSON, wantJSON)
		}
	}

	// Non-excluded rows below the boundary must NOT be reported as excluded
	// (the exact bug class saveFullUnlocked used to hardcode).
	if err := repo.InsertMessage("win:s1", 11, "user", `{"role":"user","content":"m11"}`, false); err != nil {
		t.Fatalf("insert seq 11 (not excluded): %v", err)
	}
	rows, err = repo.LoadMessagesFullBeforeSeq("win:s1", 12)
	if err != nil {
		t.Fatalf("LoadMessagesFullBeforeSeq with mixed flags: %v", err)
	}
	if len(rows) != 11 {
		t.Fatalf("expected 11 rows, got %d", len(rows))
	}
	last := rows[len(rows)-1]
	if last.Seq != 11 || last.Excluded {
		t.Errorf("last row = seq %d excluded %v, want seq 11 excluded false", last.Seq, last.Excluded)
	}
	if !rows[0].Excluded {
		t.Errorf("row 0 excluded = false, want true (fixture excludes seq 0..6)")
	}

	// Empty region.
	rows, err = repo.LoadMessagesFullBeforeSeq("win:s1", 0)
	if err != nil {
		t.Fatalf("LoadMessagesFullBeforeSeq empty: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected no rows below seq 0, got %d", len(rows))
	}
}
