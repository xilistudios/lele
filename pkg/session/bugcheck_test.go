package session

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"encoding/json"
)

// TestSQLite_GapAware_FullRewriteThenAppend_PersistsSubsequentAppend verifies
// that after a full rewrite with re-materialized evicted rows, a subsequent
// append is still persisted. Regression test for the bookkeeping bug where a
// full rewrite reset firstInMemorySeq/evictedTotal to 0 while the evicted rows
// were still absent from the in-memory slice, breaking the
// `seq = firstInMemorySeq + sliceIndex` invariant and silently dropping the
// next incremental append.
func TestSQLite_GapAware_FullRewriteThenAppend_PersistsSubsequentAppend(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:bugcheck"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	// Exclude first 7, save, evict → gap: firstInMemorySeq=7, evictedTotal=7.
	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 7 {
		t.Fatalf("evicted %d, want 7", evicted)
	}

	sess := sm.sessions[key]
	t.Logf("post-evict: firstInMemorySeq=%d evictedTotal=%d lastPersistedSeq=%d len(Messages)=%d",
		sess.firstInMemorySeq, sess.evictedTotal, sess.lastPersistedSeq, len(sess.Messages))

	// Simulate ensureSummaryMaterialized: SetHistory forces full rewrite.
	updated := append([]providers.Message{}, sess.Messages...)
	updated = append(updated, providers.Message{Role: "user", Content: "summary-msg"})
	sm.SetHistory(key, updated)
	if err := sm.Save(key); err != nil {
		t.Fatalf("full-rewrite Save: %v", err)
	}

	t.Logf("post-rewrite: firstInMemorySeq=%d evictedTotal=%d lastPersistedSeq=%d len(Messages)=%d",
		sess.firstInMemorySeq, sess.evictedTotal, sess.lastPersistedSeq, len(sess.Messages))

	rows, _ := s.Sessions().LoadMessagesWithSeq(key)
	t.Logf("SQLite rows after rewrite: %d", len(rows))

	// Now append a new user message (the next turn).
	sm.AddMessage(key, "user", "next-turn")
	if err := sm.Save(key); err != nil {
		t.Fatalf("append Save: %v", err)
	}

	rows2, _ := s.Sessions().LoadMessagesWithSeq(key)
	t.Logf("SQLite rows after append: %d", len(rows2))

	// The appended message must be persisted. Check the last row.
	lastRow := rows2[len(rows2)-1]
	var lastMsg providers.Message
	json.Unmarshal([]byte(lastRow.JSON), &lastMsg)
	if lastMsg.Content != "next-turn" {
		t.Errorf("last persisted row content = %q, want %q (append was LOST)", lastMsg.Content, "next-turn")
	}

	// Also verify total count is consistent.
	total := sm.GetTotalMessageCount(key)
	t.Logf("GetTotalMessageCount=%d, SQLite rows=%d, in-memory=%d, evictedTotal=%d",
		total, len(rows2), len(sess.Messages), sess.evictedTotal)
	if total != len(rows2) {
		t.Errorf("GetTotalMessageCount=%d but SQLite has %d rows (counter desync)", total, len(rows2))
	}
}
