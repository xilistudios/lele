package tui

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// TestArchivedPrefix_LoadsNewestPage verifies that refreshArchivedHistory loads
// the full evicted prefix when all messages fit within the render window cap.
// 12 pairs = 24 messages; keep 5 → 19 evicted. With maxRenderedMessages=200
// and resident=5, needed=195 which exceeds 19, so all 19 are loaded.
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

	// Verify chronological order: first message is the oldest evicted.
	if archived[0].Content != "Evict question 0?" {
		t.Fatalf("expected first archived message to be 'Evict question 0?', got %q", archived[0].Content)
	}

	// Last archived message is the most recent evicted (index 18 = user "Evict question 9?").
	if archived[18].Content != "Evict question 9?" {
		t.Fatalf("expected last archived message to be 'Evict question 9?', got %q", archived[18].Content)
	}

	// Spot-check an assistant message in the middle.
	if archived[1].Content != "Evict answer 0." {
		t.Fatalf("expected archived[1] to be 'Evict answer 0.', got %q", archived[1].Content)
	}
}

// TestArchivedPrefix_SessionSwitchIsolates verifies that switching sessions
// returns nil from archivedForCurrentSession and that refreshArchivedHistory
// reloads for the new session.
func TestArchivedPrefix_SessionSwitchIsolates(t *testing.T) {
	m := newEvictionTestModel(t)
	const keyA = "tui:chat:arch-b"
	const keyB = "tui:chat:arch-b2"

	// Seed session A with evictions.
	seedEvictionSession(t, m, keyA, 12, 5)

	m.currentKey = keyA
	m.refreshArchivedHistory()

	if len(m.archivedForCurrentSession()) != 19 {
		t.Fatalf("expected 19 archived for A, got %d", len(m.archivedForCurrentSession()))
	}

	// Switch to session B (no evictions).
	m.sessionMgr.GetOrCreate(keyB)
	_ = m.sessionMgr.SetMode(keyB, "agent")
	m.sessionMgr.AddMessage(keyB, "user", "hello")
	m.sessionMgr.AddMessage(keyB, "assistant", "hi")

	m.currentKey = keyB
	if got := m.archivedForCurrentSession(); got != nil {
		t.Fatalf("expected nil for switched session, got %d messages", len(got))
	}

	// refreshArchivedHistory for B should return false (no evictions).
	if m.refreshArchivedHistory() {
		t.Fatal("expected refreshArchivedHistory to return false for session without evictions")
	}
	if len(m.archivedForCurrentSession()) != 0 {
		t.Fatalf("expected 0 archived for B, got %d", len(m.archivedForCurrentSession()))
	}

	// Switch back to A — should reload.
	m.currentKey = keyA
	if !m.refreshArchivedHistory() {
		t.Fatal("expected refreshArchivedHistory to return true when switching back to A")
	}
	if len(m.archivedForCurrentSession()) != 19 {
		t.Fatalf("expected 19 archived after switching back to A, got %d", len(m.archivedForCurrentSession()))
	}
}

// TestArchivedPrefix_StaleAfterSecondCompaction verifies that a second
// compaction+eviction invalidates the cached archived prefix and the next
// refresh reloads the new total.
func TestArchivedPrefix_StaleAfterSecondCompaction(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-c"

	// Seed: 20 pairs = 40 messages.
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	for i := 0; i < 20; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Evict question %d?", i))
		m.sessionMgr.AddMessage(key, "assistant", fmt.Sprintf("Evict answer %d.", i))
	}
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	// First compaction: keep 20 → 20 evicted.
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

	// Second compaction on the remaining resident messages.
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

	// Next refresh should detect staleness and reload.
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

// TestArchivedPrefix_NeverInflatesSessionMemory verifies that loading the
// archived prefix does NOT add messages back to the in-memory session history.
// This is the critical guarantee: archived messages are display-only.
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

	// Load more pages to stress the invariant.
	m.loadOlderArchivedPage()
	m.loadOlderArchivedPage()

	residentAfter := len(m.sessionMgr.GetHistory(key))
	if residentAfter != 5 {
		t.Fatalf("expected 5 resident messages after loading archived pages, got %d", residentAfter)
	}

	// Evicted count must not change.
	if got := m.sessionMgr.GetEvictedMessageCount(key); got != 19 {
		t.Fatalf("expected 19 evicted after archived load, got %d", got)
	}
}

// TestArchivedPrefix_DeepPagingAndCap verifies that the archived prefix never
// exceeds maxArchivedPrefixMessages, and that loadOlderArchivedPage returns 0
// when the cap is reached or pages are exhausted.
func TestArchivedPrefix_DeepPagingAndCap(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-e"

	// Save and restore the global cap.
	origCap := maxArchivedPrefixMessages
	maxArchivedPrefixMessages = 30
	t.Cleanup(func() { maxArchivedPrefixMessages = origCap })

	// 200 pairs = 400 messages; keep 20 → 380 evicted.
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

	// Keep loading older pages until exhaustion.
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

	// After exhausting, loadOlderArchivedPage should return 0.
	if got := m.loadOlderArchivedPage(); got != 0 {
		t.Fatalf("expected loadOlderArchivedPage to return 0 after exhaustion, got %d", got)
	}

	// archivedHasOlder should be false when all pages are loaded (or cap hit).
	if len(m.archivedPrefix) < m.archivedTotal && m.archivedHasOlder {
		// Cap was hit before loading all pages — archivedHasOlder may still be true.
		// That's expected; the cap prevents further loading.
		t.Logf("cap reached: archivedPrefix=%d, archivedTotal=%d, archivedHasOlder=true", len(m.archivedPrefix), m.archivedTotal)
	}
}

// TestArchivedPrefix_NoArchivedIsNoop verifies that sessions without evictions
// produce no archived prefix and all query methods return zero/nil.
func TestArchivedPrefix_NoArchivedIsNoop(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-f"

	// Create a session with no evictions.
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

// TestArchivedPrefix_RespectsRenderWindow verifies that when
// maxRenderedMessages limits the needed count, only that many archived
// messages are loaded — not the full evicted total.
func TestArchivedPrefix_RespectsRenderWindow(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-h"

	// Seed a large session: 260 pairs = 520 messages; keep 150 → 370 evicted.
	// (We need enough resident messages to make maxRenderedMessages binding.)
	// Actually let's do: 175 pairs = 350 messages; keep 150 → 200 evicted.
	// But we need resident = 150 messages = 75 pairs.
	// Let's do 200 pairs = 400 messages; keep 150 → 250 evicted.
	// maxRenderedMessages = 200, resident = 150 → needed = 200-150 = 50.
	evicted := seedEvictionSession(t, m, key, 200, 150)
	if evicted != 250 {
		t.Fatalf("expected 250 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.maxRenderedMessages = 200
	m.refreshArchivedHistory()

	// resident = 150, maxRenderedMessages = 200 → needed = 50.
	// But evicted=250 > needed=50, so only ~50 should be loaded.
	if len(m.archivedPrefix) > 50 {
		t.Fatalf("expected archivedPrefix <= 50 (needed=200-150), got %d", len(m.archivedPrefix))
	}
	if len(m.archivedPrefix) == 0 {
		t.Fatal("expected non-empty archivedPrefix")
	}

	// The hidden count should be evicted - loaded.
	expectedHidden := evicted - len(m.archivedPrefix)
	if got := m.archivedHiddenCount(); got != expectedHidden {
		t.Fatalf("expected archivedHiddenCount=%d, got %d", expectedHidden, got)
	}
}

// TestArchivedPrefix_IdempotentRefresh verifies that calling
// refreshArchivedHistory multiple times without changes returns false.
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

// TestArchivedPrefix_EmptySession verifies that refreshArchivedHistory on an
// empty session (no messages at all) is a clean no-op.
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

// TestArchivedPrefix_NoSessionManager verifies that refreshArchivedHistory
// handles a nil sessionMgr gracefully.
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

// TestArchivedPrefix_OverlapGuardTrimsTail verifies prependArchivedPage never
// duplicates rows that are already in the prefix. A page is chronological
// (ascending seq), so an overlap produced by a stale cursor sits at the TAIL of
// the page and must be trimmed from the end — trimming from the head would drop
// the genuinely older rows and keep the duplicates.
func TestArchivedPrefix_OverlapGuardTrimsTail(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:overlap"
	m.archivedKey = m.currentKey
	m.archivedOldestSeq = 20

	// Existing prefix holds seqs 20..21.
	m.archivedPrefix = []providers.Message{
		{Role: "user", Content: "seq20"},
		{Role: "assistant", Content: "seq21"},
	}

	// A stale page covering seqs 18..20: only 18 and 19 are new.
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

// TestArchivedPrefix_MismatchedSeqsDiscardsPage verifies the defensive guard:
// a page whose Seqs slice does not align with Messages is dropped entirely
// rather than corrupting the cursor.
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

// contentsOf maps a message slice to its contents, for readable test failures.
func contentsOf(msgs []providers.Message) []string {
	out := make([]string, len(msgs))
	for i, msg := range msgs {
		out[i] = msg.Content
	}
	return out
}

// TestArchivedPrefix_SetCurrentChatKeyReloads is a regression test for the
// session-switch bug. setCurrentChatKey is the single choke point every switch
// goes through, and the modal session picker (ctrl+b) plus subagent navigation
// call it WITHOUT a following reloadSessions(). When the reload lived only in
// reloadSessions, switching chats through that picker left the archived prefix
// permanently empty, so compacted history stayed invisible — while tests that
// assigned m.currentKey directly and called refreshArchivedHistory by hand
// kept passing, because they exercised the helper instead of the entry point.
//
// This test drives the real entry point and asserts both halves of the
// contract: no stale rows from the session being left, and the archive back on
// return, with no manual refresh anywhere.
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

	// Leave A for B through the real switch path only.
	m.setCurrentChatKey(keyB)
	if got := len(m.archivedForCurrentSession()); got != 12 {
		t.Fatalf("switch to B: expected its own 12 archived messages, got %d", got)
	}
	if m.archivedKey != keyB {
		t.Fatalf("switch to B: archivedKey = %q", m.archivedKey)
	}
	// The differing counts prove isolation (A seeds 24 messages keeping 5 -> 19
	// evicted; B seeds 16 keeping 4 -> 12). Inheriting A's prefix would show 19;
	// showing nothing would be the original bug.
	// Return to A — again with no manual refresh.
	m.setCurrentChatKey(keyA)
	if got := len(m.archivedForCurrentSession()); got != 19 {
		t.Fatalf("switch back to A: expected 19 archived, got %d (prefix=%d key=%q)",
			got, len(m.archivedPrefix), m.archivedKey)
	}
	if m.archivedPrefix[0].Content != "Evict question 0?" {
		t.Fatalf("switch back to A: unexpected first archived row %q", m.archivedPrefix[0].Content)
	}

	// Display-only invariant: paging the archive must never touch agent memory.
	residentBefore := len(m.sessionMgr.GetHistory(keyA))
	m.loadOlderArchivedPage()
	if got := len(m.sessionMgr.GetHistory(keyA)); got != residentBefore {
		t.Fatalf("archived display paging inflated agent context: resident %d -> %d",
			residentBefore, got)
	}
}
