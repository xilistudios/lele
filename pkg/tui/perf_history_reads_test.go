package tui

// T2 regression tests: one history read per render frame.
//
// Context: pkg/tui used to call GetHistoryView three times per View() — the
// viewport rebuild, the bottom-bar token readout and the sidebar token readout,
// the last two through getTokenUsage -> getHistoryMessageCount. The caches that
// were supposed to avoid the extra reads (tokenCacheTTL, historyCountLen)
// checked their key AFTER reading the history, so they saved nothing.
//
// The fix hoists the read to the frame: View() opens a render frame, the first
// consumer fills the snapshot and every other consumer in that frame (including
// both getTokenUsage call sites through the hoisted message count) reuses it.
//
// Tests here pin three things:
//
//  1. an idle or streaming frame performs EXACTLY ONE history read
//     (TestView_OneHistoryReadPerFrame). "Zero reads when everything is cached"
//     is deliberately NOT the target: history can be mutated in place without
//     its length changing (the streaming assistant message is swapped for its
//     final version), so a snapshot kept across frames would render stale
//     content — exactly the bug TestFrameHistory_NotReusedAcrossFrames locks
//     down;
//  2. both token-usage call sites of a frame use the frame's hoisted count
//     (TestView_TokenUsageCallSitesShareHoistedCount) and the token path never
//     touches the history itself, on hit or miss
//     (TestGetTokenUsage_NeverReadsHistory);
//  3. the caches check freshness BEFORE reading: the token cache key is compared
//     against the hoisted count, and an appended message invalidates it while
//     the TTL is still valid (TestView_TokenCacheInvalidatedByHistoryChange).
//
// Red-checks: the guards were verified by mutating the production code and
// confirming the failure, then reverting —
//
//	a) frame snapshot kept across frames (no invalidation on frame begin/end)
//	   ⇒ TestView_OneHistoryReadPerFrame, TestView_TokenCacheInvalidatedByHistoryChange
//	   and TestFrameHistory_NotReusedAcrossFrames fail;
//	b) message count dropped from the token cache key ⇒ the three tests above
//	   that assert the key fail;
//	c) the pre-fix shape (getHistoryMessageCount reading GetHistoryView directly,
//	   getTokenUsage ignoring its hoisted argument) ⇒ the direct call bypasses the
//	   read counter, so the structural scan
//	   TestHistoryReadsGoThroughHistoryView and
//	   TestGetHistoryMessageCount_FrameSnapshot fail.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// historyReadCounter installs the model's history-read hook and returns a
// function reporting how many reads happened since. The hook is the single
// choke point of the TUI (Model.historyView), so the count is exhaustive for
// the render path.
func historyReadCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	reads := 0
	m.onHistoryRead = func(string) { reads++ }
	t.Cleanup(func() { m.onHistoryRead = nil })
	return func() int { return reads }
}

// newFrameReadTestModel builds a chat model with `pairs` user/assistant turns,
// sized like the perf benchmarks (200x50) so the full chat layout — bottom bar
// and sidebar — is rendered.
func newFrameReadTestModel(t *testing.T, key string, pairs int) *Model {
	t.Helper()
	m := newTestModel(t)
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	for i := 0; i < pairs; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question %d about the fix?", i))
		m.sessionMgr.AddMessage(key, "assistant", fmt.Sprintf("Answer %d: here is the plan.", i))
	}
	m.currentKey = key
	m.showWelcome = false
	m.width, m.height = 200, 50
	return m
}

// TestView_OneHistoryReadPerFrame is the T2 red-check: an idle frame must read
// the history exactly once. Before the hoist this frame read it three times
// (viewport rebuild + two getTokenUsage call sites), i.e. three O(n) copies of
// a long chat per key press.
func TestView_OneHistoryReadPerFrame(t *testing.T) {
	const key = "tui:chat:one-read-per-frame"
	m := newFrameReadTestModel(t, key, 3)

	_ = m.View() // cold frame: markdown render, not what we measure here

	count := historyReadCounter(t, m)

	if frame := m.View(); frame == "" {
		t.Fatal("View() returned an empty frame")
	}
	if got := count(); got != 1 {
		t.Fatalf("idle View() performed %d history reads, want 1", got)
	}

	// A streaming frame builds an overlay on top of the base and must stay at
	// one read as well.
	m.processing = true
	m.currentStream = "partial answer"
	before := count()
	if frame := m.View(); frame == "" {
		t.Fatal("View() returned an empty frame while streaming")
	}
	if got := count() - before; got != 1 {
		t.Fatalf("streaming View() performed %d history reads, want 1", got)
	}

	// Two measured frames (idle + streaming) ⇒ two reads: the snapshot is never
	// reused across frames (it may be stale: history is mutated in place while
	// streaming).
	if got := count(); got != 2 {
		t.Fatalf("two View() calls performed %d history reads, want 2", got)
	}
}

// TestView_TokenUsageCallSitesShareHoistedCount proves the two token-usage call
// sites of a frame (bottom bar and sidebar) consume the SAME hoisted value
// instead of each deriving it from its own history read: with the token cache
// poisoned and still fresh, both panes must render the poisoned value, and the
// frame must have used its single history read to build the cache key.
func TestView_TokenUsageCallSitesShareHoistedCount(t *testing.T) {
	const key = "tui:chat:token-call-sites"
	m := newFrameReadTestModel(t, key, 2) // 4 user+assistant messages

	// Warm the token cache through a real frame, then poison its numbers: a
	// frame that re-reads the history would notice nothing (the key still
	// matches), so the rendered values are the proof that the cache was hit.
	_ = m.View()
	const poisonedCurrent, poisonedWindow = 4242, 10000
	m.tokenCacheCurrent = poisonedCurrent
	m.tokenCacheWindow = poisonedWindow
	m.tokenCacheTime = time.Now() // TTL still valid

	count := historyReadCounter(t, m)
	frame := stripAnsi(m.View())
	if got := count(); got != 1 {
		t.Fatalf("frame with warm token cache performed %d history reads, want 1", got)
	}

	// Bottom bar: "%d (%.1f%%)" of the poisoned current/window.
	bottomBar := fmt.Sprintf("%d (%.1f%%)", poisonedCurrent, float64(poisonedCurrent)/float64(poisonedWindow)*100)
	if !strings.Contains(frame, bottomBar) {
		t.Fatalf("bottom bar does not show the frame's cached token usage %q:\n%s", bottomBar, frame)
	}
	// Sidebar: the same number, thousands-separated by formatNumber.
	sidebar := formatNumber(poisonedCurrent)
	if !strings.Contains(frame, sidebar) {
		t.Fatalf("sidebar does not show the frame's cached token usage %q:\n%s", sidebar, frame)
	}
	if want := fmt.Sprintf("%s:%d", key, 4); m.tokenCacheKey != want {
		t.Fatalf("token cache key = %q, want %q (hoisted count of the frame)", m.tokenCacheKey, want)
	}
}

// TestGetTokenUsage_NeverReadsHistory is the unit-level half of the hoist: the
// token path must take the message count as input, check the cache key first,
// and never touch the history itself — neither on hit nor on miss.
func TestGetTokenUsage_NeverReadsHistory(t *testing.T) {
	const key = "tui:chat:token-no-read"
	m := newFrameReadTestModel(t, key, 2)

	count := historyReadCounter(t, m)

	// Miss (nothing cached yet): the expensive backend reads run, but the
	// history does not.
	m.tokenCacheKey = ""
	_, _, _, _ = m.getTokenUsage(4)
	if got := count(); got != 0 {
		t.Fatalf("getTokenUsage miss performed %d history reads, want 0", got)
	}
	if want := fmt.Sprintf("%s:%d", key, 4); m.tokenCacheKey != want {
		t.Fatalf("cache key = %q, want %q", m.tokenCacheKey, want)
	}

	// Hit (identical key, TTL valid): no backend read either — verified by
	// poisoning the cached numbers with a fresh timestamp.
	m.tokenCacheCurrent = 777
	m.tokenCacheTime = time.Now()
	current, _, _, _ := m.getTokenUsage(4)
	if current != 777 {
		t.Fatalf("cache hit returned %d, want the cached 777", current)
	}
	if got := count(); got != 0 {
		t.Fatalf("getTokenUsage hit performed %d history reads, want 0", got)
	}

	// A different count (history grew) must miss and refresh: the freshness
	// check is the key comparison, so it happens before any history access.
	current, _, _, _ = m.getTokenUsage(6)
	if current == 777 {
		t.Fatal("stale token cache served for a changed message count")
	}
	if want := fmt.Sprintf("%s:%d", key, 6); m.tokenCacheKey != want {
		t.Fatalf("cache key after count change = %q, want %q", m.tokenCacheKey, want)
	}
	if got := count(); got != 0 {
		t.Fatalf("getTokenUsage after count change performed %d history reads, want 0", got)
	}
}

// TestView_TokenCacheInvalidatedByHistoryChange covers the freshness half: with
// a warm cache and a still-valid TTL, appending to the history must invalidate
// the token cache (the hoisted count changed) and the next frame must show the
// recalculated value. Broken invalidation ⇒ the poisoned value survives.
func TestView_TokenCacheInvalidatedByHistoryChange(t *testing.T) {
	const key = "tui:chat:token-freshness"
	m := newFrameReadTestModel(t, key, 2) // 4 user+assistant messages

	_ = m.View() // warm
	if want := fmt.Sprintf("%s:%d", key, 4); m.tokenCacheKey != want {
		t.Fatalf("warm cache key = %q, want %q", m.tokenCacheKey, want)
	}

	// Poison so a served cache is unmistakable.
	m.tokenCacheCurrent = 4242
	m.tokenCacheTime = time.Now() // TTL must not be the reason for a refresh
	if !strings.Contains(stripAnsi(m.View()), formatNumber(4242)) {
		t.Fatal("idle frame with a fresh cache did not reuse the cached token value")
	}

	// Append a turn: the count 4 -> 6 must invalidate the cache even though the
	// TTL is still valid.
	m.sessionMgr.AddMessage(key, "user", "One more question")
	m.sessionMgr.AddMessage(key, "assistant", "One more answer")

	_ = m.View()
	if want := fmt.Sprintf("%s:%d", key, 6); m.tokenCacheKey != want {
		t.Fatalf("cache key after append = %q, want %q (stale invalidation)", m.tokenCacheKey, want)
	}
	if m.tokenCacheCurrent == 4242 {
		t.Fatal("stale token value served after the history changed")
	}
	if got := m.getHistoryMessageCount(); got != 6 {
		t.Fatalf("history count after append = %d, want 6", got)
	}
	if strings.Contains(stripAnsi(m.View()), formatNumber(4242)) {
		t.Fatal("frame still renders the stale token value after the history changed")
	}
}

// TestFrameHistory_NotReusedAcrossFrames guards the reason the snapshot is
// frame-scoped. The history can be replaced IN PLACE without its length
// changing (the streaming assistant message is swapped for its final version),
// so a snapshot carried across frames — or a count cache keyed on
// len(history), which is what the previous historyCountLen cache did — would
// keep serving the previous content and never notice.
func TestFrameHistory_NotReusedAcrossFrames(t *testing.T) {
	const key = "tui:chat:frame-scope"
	m := newFrameReadTestModel(t, key, 1)               // user + assistant
	m.sessionMgr.AddMessage(key, "tool", "tool output") // counted by neither term

	if frame := stripAnsi(m.View()); !strings.Contains(frame, "Question 0 about the fix?") {
		t.Fatalf("frame 1 does not render the original history:\n%s", frame)
	}

	// Same length (3 messages), different roles: the counted number changes
	// from 2 to 3 but len(history) does not.
	m.sessionMgr.SetHistory(key, []providers.Message{
		{Role: "assistant", Content: "final answer"},
		{Role: "assistant", Content: "final answer"},
		{Role: "assistant", Content: "final answer"},
	})

	frame := stripAnsi(m.View())
	if !strings.Contains(frame, "final answer") {
		t.Fatalf("frame 2 served the stale frame snapshot (in-place change invisible):\n%s", frame)
	}
	if strings.Contains(frame, "Question 0 about the fix?") {
		t.Fatalf("frame 2 still renders the replaced history:\n%s", frame)
	}
}

// TestHistoryReadsGoThroughHistoryView is the structural half of the frame
// contract: pkg/tui must contain exactly ONE GetHistoryView call site, inside
// Model.historyView. The bug T2 fixed had three (viewport rebuild + the two
// getTokenUsage call sites), and a new direct call would bypass both the frame
// snapshot and the read counter above, so it is rejected here at the source
// level instead of going unnoticed. Comments are irrelevant to the AST scan.
func TestHistoryReadsGoThroughHistoryView(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	var sites []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "GetHistoryView" {
				return true
			}
			sites = append(sites, fmt.Sprintf("%s:%d", name, fset.Position(call.Pos()).Line))
			return true
		})
	}

	if len(sites) != 1 {
		t.Fatalf("pkg/tui performs %d GetHistoryView calls (%v), want exactly 1 (Model.historyView)", len(sites), sites)
	}
	if !strings.HasPrefix(sites[0], "viewport.go:") {
		t.Fatalf("the single GetHistoryView call lives in %s, want viewport.go (Model.historyView)", sites[0])
	}
}
