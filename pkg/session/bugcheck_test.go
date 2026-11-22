package session

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestSQLite_GapAware_FullRewriteThenAppend_PersistsSubsequentAppend(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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

	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 6 {
		t.Fatalf("evicted %d, want 6", evicted)
	}

	sess := sm.sessions[key]
	t.Logf("post-evict: firstInMemorySeq=%d evictedTotal=%d lastPersistedSeq=%d len(Messages)=%d",
		sess.firstInMemorySeq, sess.evictedTotal, sess.lastPersistedSeq, len(sess.Messages))

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

	sm.AddMessage(key, "user", "next-turn")
	if err := sm.Save(key); err != nil {
		t.Fatalf("append Save: %v", err)
	}

	rows2, _ := s.Sessions().LoadMessagesWithSeq(key)
	t.Logf("SQLite rows after append: %d", len(rows2))

	lastRow := rows2[len(rows2)-1]
	var lastMsg providers.Message
	json.Unmarshal([]byte(lastRow.JSON), &lastMsg)
	if lastMsg.Content != "next-turn" {
		t.Errorf("last persisted row content = %q, want %q (append was LOST)", lastMsg.Content, "next-turn")
	}

	total := sm.GetTotalMessageCount(key)
	t.Logf("GetTotalMessageCount=%d, SQLite rows=%d, in-memory=%d, evictedTotal=%d",
		total, len(rows2), len(sess.Messages), sess.evictedTotal)
	if total != len(rows2) {
		t.Errorf("GetTotalMessageCount=%d but SQLite has %d rows (counter desync)", total, len(rows2))
	}
}
