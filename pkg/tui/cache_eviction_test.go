package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// Tests for hallazgo P5 of the TUI perf audit: the per-message render cache
// (m.msgRenderCacheLines) was never evicted on compaction and had no upper
// bound. PR-4 adds (A) invalidation on compactResultMsg and (B) pruning to the
// render window on every base rebuild in buildRenderedHistoryLines.

// fingerprintsOf computes the render-cache keys buildRenderedHistoryLines uses
// for msgs at the given viewport width, skipping the messages the renderer
// itself skips (internal compaction summaries).
func fingerprintsOf(msgs []providers.Message, width int) map[string]bool {
	out := make(map[string]bool, len(msgs))
	for _, msg := range msgs {
		if isCompactionSummary(msg) {
			continue
		}
		out[messageFingerprint(msg, width)] = true
	}
	return out
}

// TestMsgRenderCacheEvictedOnCompact verifies piece A: dispatching
// compactResultMsg (the real /compact completion path) clears the per-message
// render cache, and the next rebuild repopulates it with ONLY the fingerprints
// of the compacted history — pre-compact entries must not survive.
func TestMsgRenderCacheEvictedOnCompact(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:compact-evict"
	seedLazySession(t, m, key, 10) // 20 messages, all below the render window
	renderLazyModel(t, m)          // first rebuild at final viewport width

	if len(m.msgRenderCacheLines) == 0 {
		t.Fatal("precondition: render cache should be populated after first rebuild")
	}
	before := len(m.msgRenderCacheLines)

	// Simulate what the real /compact does to the session: summarize and drop
	// the old messages from history (same shape as
	// SessionManager.CompactSession + EvictExcludedMessages).
	hist := m.sessionMgr.GetHistory(key)
	trimmed := append([]providers.Message{
		{Role: "user", Content: "## Summary of Previous Conversation\n\ncompacted context for the test."},
	}, hist[len(hist)-2:]...)
	m.sessionMgr.SetHistory(key, trimmed)

	// Dispatch the compaction result through the real Update path.
	if _, cmd := m.Update(compactResultMsg{sessionKey: key, result: "ok"}); cmd != nil {
		_ = cmd() // drain any trailing command
	}

	// Piece A: the compact handler must have dropped the whole cache.
	if m.msgRenderCacheLines != nil {
		t.Fatalf("render cache should be nil right after compactResultMsg, got %d entries",
			len(m.msgRenderCacheLines))
	}

	// The next rebuild must render the new (compacted) history and repopulate
	// the cache exclusively with its fingerprints. (The reloadSessions inside
	// the handler already rebuilt the base; force the post-invalidation one.)
	m.renderedBaseValid = false
	m.updateViewport()

	base := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(base, "Answer 9.") {
		t.Errorf("after compact, rendered base should contain the surviving history, got:\n%s", base)
	}
	if strings.Contains(base, "Question number 0") {
		t.Errorf("after compact, rendered base must not contain dropped history")
	}

	after := m.msgRenderCacheLines
	if len(after) == 0 {
		t.Fatal("render cache should be repopulated after the post-compact rebuild")
	}
	if len(after) >= before {
		t.Fatalf("cache not evicted on compact: %d entries after (was %d)", len(after), before)
	}
	want := fingerprintsOf(m.sessionMgr.GetHistoryView(key), m.viewport.Width)
	for fp := range after {
		if !want[fp] {
			t.Errorf("render cache holds fingerprint %s that is not in the compacted history", fp)
		}
	}
	// The dropped prefix must be gone; the two surviving messages keep their
	// fingerprints (same content, same width — the cache is content-keyed).
	for _, msg := range hist[:len(hist)-2] {
		fp := messageFingerprint(msg, m.viewport.Width)
		if _, ok := after[fp]; ok {
			t.Errorf("dropped pre-compact message fingerprint %s still in render cache", fp)
		}
	}
	for _, msg := range hist[len(hist)-2:] {
		fp := messageFingerprint(msg, m.viewport.Width)
		if _, ok := after[fp]; !ok {
			t.Errorf("surviving message fingerprint %s missing from render cache", fp)
		}
	}
	if len(after) != len(want) {
		t.Fatalf("render cache size = %d, want %d (renderable messages in new history)", len(after), len(want))
	}
}

// TestMsgRenderCacheBounded verifies piece B: every base rebuild prunes the
// cache to the fingerprints of the current render window, so entries for
// messages outside the window (older than maxRenderedMessages, or removed by
// compaction/eviction/history edits) cannot accumulate.
func TestMsgRenderCacheBounded(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:cache-bound"
	const pairs = 160 // 320 messages > the 200-message render window
	seedLazySession(t, m, key, pairs)
	renderLazyModel(t, m)

	m.updateViewport()

	if m.renderStartIdx != 120 {
		t.Fatalf("precondition: expected default render window startIdx=120, got %d", m.renderStartIdx)
	}
	// Exact bound: the cache holds precisely the fingerprints used by the
	// window (200 messages → 200 distinct keys). No margin needed because
	// pruning is by construction.
	if got, want := len(m.msgRenderCacheLines), m.maxRenderedMessages; got != want {
		t.Fatalf("render cache size = %d, want exactly %d (window size)", got, want)
	}

	// Every entry must belong to a message inside the window.
	hist := m.sessionMgr.GetHistoryView(key)
	inWindow := fingerprintsOf(hist[m.renderStartIdx:], m.viewport.Width)
	for fp := range m.msgRenderCacheLines {
		if !inWindow[fp] {
			t.Errorf("render cache holds out-of-window fingerprint %s", fp)
		}
	}

	// Expand the window backwards (lazy load): the cache follows the window
	// and stays bounded by its size.
	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected render window expansion on scroll-to-top")
	}
	if got, want := len(m.msgRenderCacheLines), len(hist)-m.renderStartIdx; got != want {
		t.Fatalf("after expansion: render cache size = %d, want %d (window size)", got, want)
	}

	// Shrink the history behind the model's back (simulates compaction/
	// eviction removing messages). The next rebuild must drop the now-orphan
	// entries even WITHOUT piece A's explicit invalidation.
	orphansBefore := len(m.msgRenderCacheLines)
	trimmed := append([]providers.Message{}, hist[len(hist)-6:]...)
	m.sessionMgr.SetHistory(key, trimmed)
	m.renderedBaseValid = false // force a rebuild, as a message-count change would
	m.updateViewport()

	if len(m.msgRenderCacheLines) >= orphansBefore {
		t.Fatalf("cache was not pruned after history shrink: %d entries (was %d)",
			len(m.msgRenderCacheLines), orphansBefore)
	}
	want := fingerprintsOf(m.sessionMgr.GetHistoryView(key), m.viewport.Width)
	if got := len(m.msgRenderCacheLines); got != len(want) {
		t.Fatalf("after shrink: render cache size = %d, want %d (remaining history)", got, len(want))
	}
	for fp := range m.msgRenderCacheLines {
		if !want[fp] {
			t.Errorf("orphaned fingerprint %s survived the pruning rebuild", fp)
		}
	}
}

// TestMsgRenderCacheIncrementalHitsPreserved is a regression guard for the
// pruning in piece B: unchanged messages must keep hitting the cache across
// rebuilds instead of going through glamour again. The test poisons the cached
// lines of the first message with a sentinel; if the rebuild reads from the
// cache (hit path) and carries the entry into the pruned map, the sentinel
// shows up both in the rendered base and in the cache after the rebuild.
func TestMsgRenderCacheIncrementalHitsPreserved(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:cache-hits"
	seedLazySession(t, m, key, 3) // 6 messages
	renderLazyModel(t, m)

	hist := m.sessionMgr.GetHistory(key)
	fpFirst := messageFingerprint(hist[0], m.viewport.Width)
	if _, ok := m.msgRenderCacheLines[fpFirst]; !ok {
		t.Fatal("precondition: first message fingerprint missing from render cache")
	}

	const sentinel = "SENTINEL-FROM-CACHE-MUST-BE-REUSED"
	m.msgRenderCacheLines[fpFirst] = []string{sentinel}

	// A new message changes the count → rebuild. The old messages must come
	// from the cache (hit path), the new one is rendered fresh.
	m.sessionMgr.AddMessage(key, "user", "a brand new question")
	m.updateViewport()

	base := strings.Join(m.viewport.baseLines, "\n")
	if !strings.Contains(base, sentinel) {
		t.Errorf("cached lines were not reused on rebuild — incremental hit semantics broken")
	}
	if got := m.msgRenderCacheLines[fpFirst]; len(got) != 1 || got[0] != sentinel {
		t.Errorf("cache hit entry was not carried into the pruned cache: %v", got)
	}
	// The new message must also be cached now.
	hist = m.sessionMgr.GetHistory(key)
	fpNew := messageFingerprint(hist[len(hist)-1], m.viewport.Width)
	if _, ok := m.msgRenderCacheLines[fpNew]; !ok {
		t.Errorf("newly rendered message missing from cache after rebuild")
	}
	// Cache still bounded to exactly the 7 rendered messages.
	if got, want := len(m.msgRenderCacheLines), 7; got != want {
		t.Fatalf("render cache size = %d, want %d", got, want)
	}
}
