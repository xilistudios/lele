package tui

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

func TestArchivedPrefix_LoadsNewestPage(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-a"

	evicted := seedEvictionSession(t, m, key, 12, 5)
	if evicted != 19 {
		t.Fatalf("expected 19 evicted, got %d", evicted)
	}

	m.currentKey = key
	changed := m.refreshArchivedHistory()
	if !changed {
		t.Fatal("expected refreshArchivedHistory to return true")
	}

	archived := m.archivedForCurrentSession()
	if len(archived) != 19 {
		t.Fatalf("expected 19 archived messages, got %d", len(archived))
	}

	if archived[0].Content != "Evict question 0?" {
		t.Fatalf("expected first archived message to be 'Evict question 0?', got %q", archived[0].Content)
	}

	if archived[18].Content != "Evict question 9?" {
		t.Fatalf("expected last archived message to be 'Evict question 9?', got %q", archived[18].Content)
	}

	if archived[1].Content != "Evict answer 0." {
		t.Fatalf("expected archived[1] to be 'Evict answer 0.', got %q", archived[1].Content)
	}
}

func TestArchivedPrefix_SessionSwitchIsolates(t *testing.T) {
	m := newEvictionTestModel(t)
	const keyA = "tui:chat:arch-b"
	const keyB = "tui:chat:arch-b2"

	seedEvictionSession(t, m, keyA, 12, 5)

	m.currentKey = keyA
	m.refreshArchivedHistory()

	if len(m.archivedForCurrentSession()) != 19 {
		t.Fatalf("expected 19 archived for A, got %d", len(m.archivedForCurrentSession()))
	}

	m.sessionMgr.GetOrCreate(keyB)
	_ = m.sessionMgr.SetMode(keyB, "agent")
	m.sessionMgr.AddMessage(keyB, "user", "hello")
	m.sessionMgr.AddMessage(keyB, "assistant", "hi")

	m.currentKey = keyB
	if got := m.archivedForCurrentSession(); got != nil {
		t.Fatalf("expected nil for switched session, got %d messages", len(got))
	}

	if m.refreshArchivedHistory() {
		t.Fatal("expected refreshArchivedHistory to return false for session without evictions")
	}
	if len(m.archivedForCurrentSession()) != 0 {
		t.Fatalf("expected 0 archived for B, got %d", len(m.archivedForCurrentSession()))
	}

	m.currentKey = keyA
	if !m.refreshArchivedHistory() {
		t.Fatal("expected refreshArchivedHistory to return true when switching back to A")
	}
	if len(m.archivedForCurrentSession()) != 19 {
		t.Fatalf("expected 19 archived after switching back to A, got %d", len(m.archivedForCurrentSession()))
	}
}

func TestArchivedPrefix_StaleAfterSecondCompaction(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-c"

	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	for i := 0; i < 20; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Evict question %d?", i))
		m.sessionMgr.AddMessage(key, "assistant", fmt.Sprintf("Evict answer %d.", i))
	}
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	m.sessionMgr.ExcludeOldMessagesFromContext(key, 20)
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("exclude Save: %v", err)
	}
	evicted1 := m.sessionMgr.EvictExcludedMessages(key)
	if evicted1 <= 0 {
		t.Fatalf("first evict = %d, want > 0", evicted1)
	}

	m.currentKey = key
	m.refreshArchivedHistory()

	total1 := m.sessionMgr.GetEvictedMessageCount(key)
	if len(m.archivedPrefix) == 0 {
		t.Fatal("expected non-empty archived prefix after first compaction")
	}

	residentHistory := m.sessionMgr.GetHistory(key)
	keep2 := 5
	if len(residentHistory) <= keep2 {
		keep2 = len(residentHistory) - 1
	}
	m.sessionMgr.ExcludeOldMessagesFromContext(key, keep2)
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("second exclude Save: %v", err)
	}
	m.sessionMgr.EvictExcludedMessages(key)

	total2 := m.sessionMgr.GetEvictedMessageCount(key)
	if total2 <= total1 {
		t.Fatalf("expected evicted count to increase: first=%d, second=%d", total1, total2)
	}

	changed := m.refreshArchivedHistory()
	if !changed {
		t.Fatal("expected refreshArchivedHistory to return true after second compaction")
	}

	if m.archivedTotal != total2 {
		t.Fatalf("expected archivedTotal=%d, got %d", total2, m.archivedTotal)
	}

	if len(m.archivedPrefix) == 0 {
		t.Fatal("expected non-empty archived prefix after second compaction refresh")
	}
}

func TestArchivedPrefix_NeverInflatesSessionMemory(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-d"

	seedEvictionSession(t, m, key, 12, 5)

	residentBefore := len(m.sessionMgr.GetHistory(key))
	if residentBefore != 5 {
		t.Fatalf("expected 5 resident messages before refresh, got %d", residentBefore)
	}

	m.currentKey = key
	m.refreshArchivedHistory()

	m.loadOlderArchivedPage()
	m.loadOlderArchivedPage()

	residentAfter := len(m.sessionMgr.GetHistory(key))
	if residentAfter != 5 {
		t.Fatalf("expected 5 resident messages after loading archived pages, got %d", residentAfter)
	}

	if got := m.sessionMgr.GetEvictedMessageCount(key); got != 19 {
		t.Fatalf("expected 19 evicted after archived load, got %d", got)
	}
}

func TestArchivedPrefix_DeepPagingAndCap(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-e"

	origCap := maxArchivedPrefixMessages
	maxArchivedPrefixMessages = 30
	t.Cleanup(func() { maxArchivedPrefixMessages = origCap })

	evicted := seedEvictionSession(t, m, key, 200, 20)
	if evicted != 380 {
		t.Fatalf("expected 380 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.maxRenderedMessages = 500
	m.refreshArchivedHistory()

	if len(m.archivedPrefix) > maxArchivedPrefixMessages {
		t.Fatalf("archivedPrefix %d exceeds cap %d", len(m.archivedPrefix), maxArchivedPrefixMessages)
	}

	for i := 0; i < 50; i++ {
		added := m.loadOlderArchivedPage()
		if len(m.archivedPrefix) > maxArchivedPrefixMessages {
			t.Fatalf("archivedPrefix %d exceeds cap %d after page load %d", len(m.archivedPrefix), maxArchivedPrefixMessages, i)
		}
		if added == 0 {
			break
		}
	}

	if len(m.archivedPrefix) > maxArchivedPrefixMessages {
		t.Fatalf("final archivedPrefix %d exceeds cap %d", len(m.archivedPrefix), maxArchivedPrefixMessages)
	}

	if got := m.loadOlderArchivedPage(); got != 0 {
		t.Fatalf("expected loadOlderArchivedPage to return 0 after exhaustion, got %d", got)
	}

	if len(m.archivedPrefix) < m.archivedTotal && m.archivedHasOlder {
		t.Logf("cap reached: archivedPrefix=%d, archivedTotal=%d, archivedHasOlder=true", len(m.archivedPrefix), m.archivedTotal)
	}
}

func TestArchivedPrefix_NoArchivedIsNoop(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-f"

	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hello")
	m.sessionMgr.AddMessage(key, "assistant", "hi there")

	m.currentKey = key

	if m.refreshArchivedHistory() {
		t.Fatal("expected refreshArchivedHistory to return false for no evictions")
	}
	if m.archivedHiddenCount() != 0 {
		t.Fatalf("expected archivedHiddenCount=0, got %d", m.archivedHiddenCount())
	}
	if got := m.loadOlderArchivedPage(); got != 0 {
		t.Fatalf("expected loadOlderArchivedPage=0, got %d", got)
	}
}

func TestArchivedPrefix_RespectsRenderWindow(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-h"

	evicted := seedEvictionSession(t, m, key, 200, 150)
	if evicted != 250 {
		t.Fatalf("expected 250 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.maxRenderedMessages = 200
	m.refreshArchivedHistory()

	if len(m.archivedPrefix) > 50 {
		t.Fatalf("expected archivedPrefix <= 50 (needed=200-150), got %d", len(m.archivedPrefix))
	}
	if len(m.archivedPrefix) == 0 {
		t.Fatal("expected non-empty archivedPrefix")
	}

	expectedHidden := evicted - len(m.archivedPrefix)
	if got := m.archivedHiddenCount(); got != expectedHidden {
		t.Fatalf("expected archivedHiddenCount=%d, got %d", expectedHidden, got)
	}
}

func TestArchivedPrefix_IdempotentRefresh(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-i"

	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key

	if !m.refreshArchivedHistory() {
		t.Fatal("expected first refresh to return true")
	}
	if m.refreshArchivedHistory() {
		t.Fatal("expected second refresh to return false (no change)")
	}
	if m.refreshArchivedHistory() {
		t.Fatal("expected third refresh to return false (no change)")
	}
}

func TestArchivedPrefix_EmptySession(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-j"

	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key

	if m.refreshArchivedHistory() {
		t.Fatal("expected refreshArchivedHistory to return false for empty session")
	}
	if m.archivedForCurrentSession() != nil {
		t.Fatal("expected nil archived prefix for empty session")
	}
	if m.archivedHiddenCount() != 0 {
		t.Fatalf("expected 0 hidden count, got %d", m.archivedHiddenCount())
	}
}

func TestArchivedPrefix_NoSessionManager(t *testing.T) {
	m := newTestModel(t)
	m.sessionMgr = nil
	m.currentKey = "some-key"

	if m.refreshArchivedHistory() {
		t.Fatal("expected false with nil sessionMgr")
	}
	if m.archivedForCurrentSession() != nil {
		t.Fatal("expected nil with nil sessionMgr")
	}
}

func TestArchivedPrefix_OverlapGuardTrimsTail(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:overlap"
	m.archivedKey = m.currentKey
	m.archivedOldestSeq = 20

	m.archivedPrefix = []providers.Message{
		{Role: "user", Content: "seq20"},
		{Role: "assistant", Content: "seq21"},
	}

	m.prependArchivedPage(&session.EvictedMessagesPage{
		Messages: []providers.Message{
			{Role: "user", Content: "seq18"},
			{Role: "assistant", Content: "seq19"},
			{Role: "user", Content: "seq20-dup"},
		},
		Seqs: []int{18, 19, 20},
	})

	if len(m.archivedPrefix) != 4 {
		t.Fatalf("expected 4 messages after trimming the overlapping tail, got %d: %v", len(m.archivedPrefix), contentsOf(m.archivedPrefix))
	}
	want := []string{"seq18", "seq19", "seq20", "seq21"}
	for i, w := range want {
		if m.archivedPrefix[i].Content != w {
			t.Fatalf("prefix[%d] = %q, want %q (full: %v)", i, m.archivedPrefix[i].Content, w, contentsOf(m.archivedPrefix))
		}
	}
	if m.archivedOldestSeq != 18 {
		t.Fatalf("archivedOldestSeq = %d, want 18", m.archivedOldestSeq)
	}
}

func TestArchivedPrefix_MismatchedSeqsDiscardsPage(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:mismatch"
	m.archivedKey = m.currentKey

	added := m.prependArchivedPage(&session.EvictedMessagesPage{
		Messages: []providers.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}},
		Seqs:     []int{1},
	})
	if added != 0 {
		t.Fatalf("expected 0 added for misaligned page, got %d", added)
	}
	if len(m.archivedPrefix) != 0 {
		t.Fatalf("expected empty prefix, got %v", contentsOf(m.archivedPrefix))
	}
}

func contentsOf(msgs []providers.Message) []string {
	out := make([]string, len(msgs))
	for i, msg := range msgs {
		out[i] = msg.Content
	}
	return out
}

func TestArchivedPrefix_SetCurrentChatKeyReloads(t *testing.T) {
	m := newEvictionTestModel(t)
	const keyA = "tui:chat:arch-switch-a"
	const keyB = "tui:chat:arch-switch-b"

	seedEvictionSession(t, m, keyA, 12, 5)
	seedEvictionSession(t, m, keyB, 8, 3)

	m.currentKey = keyA
	m.refreshArchivedHistory()
	if got := len(m.archivedForCurrentSession()); got != 19 {
		t.Fatalf("setup: expected 19 archived for A, got %d", got)
	}

	m.setCurrentChatKey(keyB)
	if got := len(m.archivedForCurrentSession()); got != 12 {
		t.Fatalf("switch to B: expected its own 12 archived messages, got %d", got)
	}
	if m.archivedKey != keyB {
		t.Fatalf("switch to B: archivedKey = %q", m.archivedKey)
	}
	m.setCurrentChatKey(keyA)
	if got := len(m.archivedForCurrentSession()); got != 19 {
		t.Fatalf("switch back to A: expected 19 archived, got %d (prefix=%d key=%q)",
			got, len(m.archivedPrefix), m.archivedKey)
	}
	if m.archivedPrefix[0].Content != "Evict question 0?" {
		t.Fatalf("switch back to A: unexpected first archived row %q", m.archivedPrefix[0].Content)
	}

	residentBefore := len(m.sessionMgr.GetHistory(keyA))
	m.loadOlderArchivedPage()
	if got := len(m.sessionMgr.GetHistory(keyA)); got != residentBefore {
		t.Fatalf("archived display paging inflated agent context: resident %d -> %d",
			residentBefore, got)
	}
}
