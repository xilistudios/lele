package session

// R3.4 — a corrupted evicted row must be skipped for real.
//
// writeFullUnlocked re-materializes the evicted prefix from SQLite before a
// ReplaceMessages rewrite so the DELETE-all does not destroy rows that were
// evicted from memory. A row whose JSON does not unmarshal is skipped
// (pre-existing behaviour), but the slice used to be pre-allocated to
// evicted+len(messages) and written by index, so `continue` left a zero
// store.MessageRow{} in the row's slot: Seq 0, empty role, empty JSON. That
// placeholder is then written as a real row —
//
//   - at index 0 it replaces the genuine seq-0 row with an empty one;
//   - at any other index it collides with the genuine seq-0 row on
//     UNIQUE(session_key, seq) and fails the whole rewrite with an error.
//
// The fix builds the slice with append, so the corrupted row is a hole in the
// seq sequence (its Seq keeps the absolute index, exactly like every other
// evicted row) and nothing else changes.

import (
	"encoding/json"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestWriteFullUnlockedSkipsCorruptedEvictedRow drives a full rewrite over a
// session whose evicted prefix holds one corrupted row in the middle, and pins
// that the rewrite succeeds and exports no phantom zero row.
//
// Red-check: restoring the pre-allocated slice with `rows[i] = …` (and the bare
// `continue`) in writeFullUnlocked makes this test fail — ReplaceMessages trips
// the UNIQUE(session_key, seq) constraint on the phantom row and Save returns
// "save session … to sqlite: …".
func TestWriteFullUnlockedSkipsCorruptedEvictedRow(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:corrupt-evicted-row"

	// 6 messages, fully persisted, then the prefix evicted from memory: the
	// evicted rows 0..3 (keep 2) stay in SQLite and are what a rewrite reads back.
	if evicted := seedPostCompactionSession(t, sm, key); evicted != 4 {
		t.Fatalf("fixture evicted %d messages, want 4", evicted)
	}
	before := readSessionMessageRows(t, s.DB(), key)
	if len(before) != 6 {
		t.Fatalf("precondition: %d persisted rows, want 6", len(before))
	}

	// Corrupt the middle row of the evicted prefix (seq 1 of 0..3).
	const corruptSeq = 1
	if _, err := s.DB().Exec(
		`UPDATE session_messages SET message = ? WHERE session_key = ? AND seq = ?`,
		"{not valid json", key, corruptSeq); err != nil {
		t.Fatalf("corrupt row %d: %v", corruptSeq, err)
	}

	// Force the next Save down the full-rewrite path (the state compaction and
	// TruncateHistory leave behind).
	forceFullRewrite(sm, key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("full rewrite over a corrupted evicted row failed: %v", err)
	}

	rows := readSessionMessageRows(t, s.DB(), key)
	if len(rows) != len(before)-1 {
		t.Fatalf("persisted %d rows, want %d: the corrupted row must be dropped, not replaced by a placeholder",
			len(rows), len(before)-1)
	}
	// The surviving evicted rows keep their absolute seqs: the corruption is a
	// hole, not a shift that would break seq = firstInMemorySeq + index.
	wantSeqs := []int{0, 2, 3, 4, 5}
	for i, row := range rows {
		if row.Seq != wantSeqs[i] {
			t.Fatalf("row %d has seq %d, want %d (seqs %v)", i, row.Seq, wantSeqs[i], wantSeqs)
		}
		if row.Role == "" || row.JSON == "" {
			t.Errorf("row seq %d is a placeholder: %+v", row.Seq, row)
		}
		var msg providers.Message
		if err := json.Unmarshal([]byte(row.JSON), &msg); err != nil {
			t.Errorf("row seq %d does not hold a message: %v", row.Seq, err)
		}
	}
	// The rows that were not corrupted come through untouched, byte for byte.
	for _, want := range before {
		if want.Seq == corruptSeq {
			continue
		}
		got, ok := readPersistedRow(t, s.DB(), key, want.Seq)
		if !ok {
			t.Fatalf("row seq %d disappeared from the rewrite", want.Seq)
		}
		if got != want {
			t.Errorf("row seq %d changed:\n got %+v\nwant %+v", want.Seq, got, want)
		}
	}
}
