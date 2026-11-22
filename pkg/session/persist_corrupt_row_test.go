package session

import (
	"encoding/json"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestWriteFullUnlockedSkipsCorruptedEvictedRow(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	key := "test:corrupt-evicted-row"

	if evicted := seedPostCompactionSession(t, sm, key); evicted != 4 {
		t.Fatalf("fixture evicted %d messages, want 4", evicted)
	}
	before := readSessionMessageRows(t, s.DB(), key)
	if len(before) != 6 {
		t.Fatalf("precondition: %d persisted rows, want 6", len(before))
	}

	const corruptSeq = 1
	if _, err := s.DB().Exec(
		`UPDATE session_messages SET message = ? WHERE session_key = ? AND seq = ?`,
		"{not valid json", key, corruptSeq); err != nil {
		t.Fatalf("corrupt row %d: %v", corruptSeq, err)
	}

	forceFullRewrite(sm, key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("full rewrite over a corrupted evicted row failed: %v", err)
	}

	rows := readSessionMessageRows(t, s.DB(), key)
	if len(rows) != len(before)-1 {
		t.Fatalf("persisted %d rows, want %d: the corrupted row must be dropped, not replaced by a placeholder",
			len(rows), len(before)-1)
	}
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
