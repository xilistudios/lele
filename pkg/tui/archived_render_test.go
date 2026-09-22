package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// testEvictedPageSeqs builds an EvictedMessagesPage from role/content pairs
// for unit tests that construct the archived prefix manually, with an explicit
// first seq. Pages prepended after another one must use strictly lower seqs,
// because prependArchivedPage trims any row whose seq is not older than the
// current oldest prefix message (that is how a real older page looks on disk).
func testEvictedPageSeqs(firstSeq int, pairs []struct{ role, content string }) *session.EvictedMessagesPage {
	msgs := make([]providers.Message, len(pairs))
	seqs := make([]int, len(pairs))
	for i, p := range pairs {
		msgs[i] = providers.Message{Role: p.role, Content: p.content}
		seqs[i] = firstSeq + i
	}
	return &session.EvictedMessagesPage{
		Messages: msgs,
		Seqs:     seqs,
		HasOlder: false,
	}
}

// renderedBase returns the FULL base line buffer the viewport is fed.
//
// View() paints only viewport.Height lines starting at YOffset (see
// lineViewport.visibleLines), so a transcript longer than the window cannot be
// asserted against the painted output: the top of the history and its bottom
// are never on screen at the same time. Assertions about whether a message,
// the "earlier messages" banner is RENDERED must run
// against this buffer; assertions about what the user actually SEES must use
// View() with an explicit scroll position.
func renderedBase(m *Model) string {
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	return strings.Join(m.buildRenderedHistoryLines(history), "\n")
}

// earlierHeaderRe derives the regexp that extracts the hidden-message count
// from the "↑ %d earlier messages" banner. The pattern is built from the
// localized template instead of hardcoded English, because the test suite runs
// under whatever locale i18n resolves to.
func earlierHeaderRe() *regexp.Regexp {
	quoted := regexp.QuoteMeta(i18n.T("tui.earlierMessages"))
	return regexp.MustCompile(strings.Replace(quoted, "%d", `(\d+)`, 1))
}

// headerCount extracts the banner's count from a rendered string, or -1 when
// the banner is absent (everything above the fold is already rendered).
func headerCount(s string) int {
	matches := earlierHeaderRe().FindStringSubmatch(s)
	if len(matches) < 2 {
		return -1
	}
	n, err := strconv.Atoi(matches[1])
	if err != nil {
		return -1
	}
	return n
}

// TestArchivedRender_ShowsEvictedMessagesAfterCompact is the primary bug-fix
// test: after /compact, the TUI must still render evicted messages from the
// display-only archived prefix alongside the live resident history.
func TestArchivedRender_ShowsEvictedMessagesAfterCompact(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-a"

	// 12 pairs = 24 messages; keep 5 → 19 evicted.
	evicted := seedEvictionSession(t, m, key, 12, 5)
	if evicted != 19 {
		t.Fatalf("expected 19 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	// At the bottom of the viewport, resident messages should be visible.
	outBottom := m.View()
	if !strings.Contains(outBottom, "Evict answer 11.") {
		t.Fatalf("bottom view should contain resident 'Evict answer 11.', got:\n%s", outBottom)
	}

	// Scroll to top — archived messages should be visible.
	m.viewport.GotoTop()
	outTop := m.View()
	if !strings.Contains(outTop, "Evict question 0?") {
		t.Fatalf("top view should contain archived 'Evict question 0?', got:\n%s", outTop)
	}
}

// TestArchivedRender_DoesNotTouchSessionMemory verifies that rendering the
// archived prefix does NOT inflate the session history in memory.
func TestArchivedRender_DoesNotTouchSessionMemory(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-b"

	seedEvictionSession(t, m, key, 12, 5)

	residentBefore := len(m.sessionMgr.GetHistory(key))
	evictedBefore := m.sessionMgr.GetEvictedMessageCount(key)

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()
	renderLazyModel(t, m)
	_ = m.View()

	residentAfter := len(m.sessionMgr.GetHistory(key))
	evictedAfter := m.sessionMgr.GetEvictedMessageCount(key)

	if residentBefore != residentAfter {
		t.Fatalf("resident count changed: before=%d after=%d", residentBefore, residentAfter)
	}
	if evictedBefore != evictedAfter {
		t.Fatalf("evicted count changed: before=%d after=%d", evictedBefore, evictedAfter)
	}
}

// TestArchivedRender_ScrollUpLoadsOlderPages verifies that scrolling to the
// top loads older archived pages from SQLite and updates the header count.
func TestArchivedRender_ScrollUpLoadsOlderPages(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-d"

	// Lower the cap so refresh loads fewer than the total evicted.
	origCap := maxArchivedPrefixMessages
	maxArchivedPrefixMessages = 60
	t.Cleanup(func() { maxArchivedPrefixMessages = origCap })

	// 200 pairs = 400 messages; keep 20 → 380 evicted.
	seedEvictionSession(t, m, key, 200, 20)

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 60
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	// Not all evicted messages are loaded (cap=60).
	loadedAfterRefresh := len(m.archivedPrefix)
	if loadedAfterRefresh == 0 {
		t.Fatal("expected some archived messages to be loaded")
	}

	// Record the header number from the rendered base buffer: the banner is the
	// FIRST line of the history, so with a transcript longer than the viewport
	// it is not part of the painted window (View() renders Height lines from
	// YOffset) unless the user is scrolled to the very top.
	initialBase := renderedBase(m)
	initialHeaderNum := headerCount(initialBase)
	if initialHeaderNum < 0 {
		t.Fatalf("expected %q header in rendered base, got:\n%s",
			i18n.T("tui.earlierMessages"), initialBase)
	}

	// Scroll to top and expand.
	m.viewport.GotoTop()
	expanded := m.maybeExpandRenderWindow()
	if !expanded {
		t.Fatal("expected maybeExpandRenderWindow to return true (archived pages available)")
	}

	// renderStartIdx should be 0 (in-memory window fully expanded, loading from archive).
	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0 after archive expansion, got %d", m.renderStartIdx)
	}

	// archivedPrefix should have grown.
	if len(m.archivedPrefix) <= loadedAfterRefresh {
		t.Fatalf("expected archivedPrefix to grow: before=%d, after=%d", loadedAfterRefresh, len(m.archivedPrefix))
	}

	// The header number should decrease (fewer hidden messages).
	expandedHeaderNum := headerCount(renderedBase(m))
	if expandedHeaderNum >= 0 && expandedHeaderNum >= initialHeaderNum {
		t.Fatalf("expected header number to decrease: initial=%d, after expand=%d",
			initialHeaderNum, expandedHeaderNum)
	}
	// A negative value means the banner is gone: everything is rendered.
}

// TestArchivedRender_ExhaustedArchiveStops verifies that once all archived
// pages are loaded, maybeExpandRenderWindow returns false.
func TestArchivedRender_ExhaustedArchiveStops(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-e"

	// Small session: 12 pairs, keep 5 → 19 evicted. All fit in one page.
	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	// Load all remaining pages.
	for i := 0; i < 50; i++ {
		m.viewport.GotoTop()
		if !m.maybeExpandRenderWindow() {
			break
		}
	}

	if m.archivedHasOlder {
		t.Fatal("expected archivedHasOlder=false after exhausting all pages")
	}
	if m.archivedHiddenCount() != 0 {
		t.Fatalf("expected archivedHiddenCount=0 after exhaustion, got %d", m.archivedHiddenCount())
	}

	// Should return false now.
	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow=false after exhaustion")
	}
}

// TestArchivedRender_SessionSwitchHidesOtherPrefix verifies that switching
// sessions hides the archived prefix of the previous session.
func TestArchivedRender_SessionSwitchHidesOtherPrefix(t *testing.T) {
	m := newEvictionTestModel(t)
	const keyA = "tui:chat:render-f"
	const keyB = "tui:chat:render-f2"

	// Seed session A with evictions.
	seedEvictionSession(t, m, keyA, 12, 5)
	m.currentKey = keyA
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()

	archivedA := m.archivedForCurrentSession()
	if len(archivedA) == 0 {
		t.Fatal("expected non-empty archived prefix for session A")
	}
	msgA := archivedA[0].Content // "Evict question 0?"

	// Switch to session B (no evictions, no refresh).
	seedLazySession(t, m, keyB, 5)
	renderLazyModel(t, m)
	m.viewport.GotoTop()
	outB := m.View()

	if strings.Contains(outB, msgA) {
		t.Fatalf("session B view should not contain session A's archived message %q, got:\n%s", msgA, outB)
	}
}

// TestArchivedRender_WindowHeaderCountsCombined verifies that the "↑ N earlier
// messages" header counts both archived-hidden and in-memory-hidden messages.
func TestArchivedRender_WindowHeaderCountsCombined(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-g"

	// 12 pairs = 24 messages; keep 5 → 19 evicted. After refresh, all 19 archived
	// are loaded (fit in one page), plus 5 resident = 24 total.
	// maxRenderedMessages=10 → defaultRenderStartIdx(24)=14. Hidden=14+0=14.
	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 10
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	// The banner is the first line of the base buffer, so it is asserted on the
	// render — not on View(), which only paints the scrolled window.
	out := renderedBase(m)
	headerNum := headerCount(out)
	if headerNum < 0 {
		t.Fatalf("expected %q header in rendered base, got:\n%s",
			i18n.T("tui.earlierMessages"), out)
	}

	// The header should equal renderStartIdx + archivedHiddenCount().
	// With all archived loaded, archivedHiddenCount=0, so it's just renderStartIdx.
	expected := m.renderStartIdx + m.archivedHiddenCount()
	if headerNum != expected {
		t.Fatalf("header number=%d, expected renderStartIdx(%d)+archivedHidden(%d)=%d",
			headerNum, m.renderStartIdx, m.archivedHiddenCount(), expected)
	}
}

// TestArchivedRender_HeaderNumberDecreasesOnExpand verifies that each
// expansion reduces the header number, confirming combined counting works.
func TestArchivedRender_HeaderNumberDecreasesOnExpand(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-h"

	origCap := maxArchivedPrefixMessages
	maxArchivedPrefixMessages = 40
	t.Cleanup(func() { maxArchivedPrefixMessages = origCap })

	// 200 pairs = 400; keep 20 → 380 evicted.
	seedEvictionSession(t, m, key, 200, 20)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 60
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	// The banner is asserted on the rendered base buffer: View() only paints
	// viewport.Height lines, so on a long history the banner (first line of the
	// buffer) and the newest message are never on screen at the same time.
	extractNum := func(rendered string) int { return headerCount(rendered) }

	prev := extractNum(renderedBase(m))
	if prev < 0 {
		t.Fatalf("expected initial header number, got -1")
	}

	// Expand several times; the number should decrease or the header should disappear.
	for i := 0; i < 10; i++ {
		m.viewport.GotoTop()
		if !m.maybeExpandRenderWindow() {
			break
		}
		cur := extractNum(renderedBase(m))
		if cur >= 0 && cur >= prev {
			t.Fatalf("iteration %d: header number did not decrease: prev=%d, cur=%d", i, prev, cur)
		}
		if cur >= 0 {
			prev = cur
		}
	}
}

// TestArchivedRender_AllArchivedFitBelowCap verifies that when all evicted
// messages fit within maxRenderedMessages, the rendered base lines contain
// both archived and resident content, and no "earlier messages" header.
func TestArchivedRender_AllArchivedFitBelowCap(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-i"

	// 12 pairs, keep 5 → 19 evicted. All fit in one page.
	// maxRenderedMessages=0 (unlimited) → all visible.
	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 0 // unlimited
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	// Check rendered base lines directly.
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	baseLines := m.buildRenderedHistoryLines(history)
	baseStr := strings.Join(baseLines, "\n")

	// No "earlier messages" header since everything fits.
	if strings.Contains(baseStr, "earlier messages") {
		t.Fatalf("expected no 'earlier messages' header when everything fits")
	}

	// Both archived and resident messages should be present.
	if !strings.Contains(baseStr, "Evict question 0?") {
		t.Fatalf("expected archived message in base lines")
	}
	if !strings.Contains(baseStr, "Evict answer 11.") {
		t.Fatalf("expected resident message in base lines")
	}
}

// TestArchivedRender_RenderStartIdxZeroAndNoArchiveIsNoop verifies that
// maybeExpandRenderWindow returns false when renderStartIdx==0 and there
// are no archived messages to load (the common non-compact case).
func TestArchivedRender_RenderStartIdxZeroAndNoArchiveIsNoop(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:render-j", 5) // small, no evictions
	renderLazyModel(t, m)

	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0, got %d", m.renderStartIdx)
	}

	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow=false with no evictions and renderStartIdx=0")
	}
}

// TestArchivedRender_DisplayHistoryMessageCount verifies the combined counter
// method returns archived+resident and is O(1) after prependArchivedPage.
func TestArchivedRender_DisplayHistoryMessageCount(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-k"

	seedEvictionSession(t, m, key, 12, 5) // 19 evicted, 5 resident

	history := m.sessionMgr.GetHistory(key)
	residentCount := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			residentCount++
		}
	}

	m.currentKey = key
	before := m.displayHistoryMessageCount(history)
	// With no archived prefix loaded yet, should be just the resident count.
	if before != residentCount {
		t.Fatalf("before refresh: expected %d, got %d", residentCount, before)
	}

	m.refreshArchivedHistory()
	after := m.displayHistoryMessageCount(history)
	// Now archived + resident: 19 (all user/assistant in archived) + 5 = 24.
	if after != 19+residentCount {
		t.Fatalf("after refresh: expected %d, got %d", 19+residentCount, after)
	}
}

// TestArchivedRender_ArchivedVisibleCountIncremental verifies that
// archivedVisibleCount is maintained incrementally by prependArchivedPage
// and reset to 0 by resetArchivedHistory.
func TestArchivedRender_ArchivedVisibleCountIncremental(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:render-l"
	m.archivedKey = m.currentKey

	// Add messages with mixed roles including system (which should not count).
	// Seqs 11..15: this is the NEWEST page, so it is loaded first.
	m.prependArchivedPage(testEvictedPageSeqs(11, []struct {
		role, content string
	}{
		{"user", "q1"},
		{"assistant", "a1"},
		{"system", "s1"}, // should NOT count
		{"user", "q2"},
		{"assistant", "a2"},
	}))

	if m.archivedVisibleCount != 4 {
		t.Fatalf("expected archivedVisibleCount=4, got %d", m.archivedVisibleCount)
	}

	// Prepend an OLDER page (seqs 9..10). It must use strictly lower seqs:
	// prependArchivedPage trims rows that are not older than the current
	// oldest prefix message, so reusing seqs 1..2 here would add nothing.
	m.prependArchivedPage(testEvictedPageSeqs(9, []struct {
		role, content string
	}{
		{"user", "q0"},
		{"assistant", "a0"},
	}))

	if m.archivedVisibleCount != 6 {
		t.Fatalf("expected archivedVisibleCount=6, got %d", m.archivedVisibleCount)
	}

	// Reset clears it.
	m.resetArchivedHistory()
	if m.archivedVisibleCount != 0 {
		t.Fatalf("expected archivedVisibleCount=0 after reset, got %d", m.archivedVisibleCount)
	}
}

// TestArchivedPrefix_RebuildsBaseWithoutCountChange pins the archived-prefix
// term of the rendered-base cache.
//
// The base cache is reused while the session, width and combined visible
// message count are unchanged. The archive can move without any of those
// changing: an eviction of rows that are not user/assistant (tool results,
// system rows) raises archivedTotal — and therefore the "↑ N earlier messages"
// banner — while archivedVisibleCount + resident stays exactly the same. Before
// the base carried its own archive fingerprint, that frame was served from the
// cache and the user kept seeing the previous banner.
func TestArchivedPrefix_RebuildsBaseWithoutCountChange(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:arch-rebuild"
	seedEvictionSession(t, m, key, 12, 5)

	m.currentKey = key
	m.showWelcome = false
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	*m = *(updated.(*Model))
	m.currentKey = key

	m.refreshArchivedHistory()
	m.updateViewport()

	base := renderedBase(m)
	if strings.Contains(base, "earlier messages") {
		t.Fatalf("expected no banner while the whole archive is loaded, got:\n%s", base)
	}
	countBefore := m.renderedBaseMsgCount
	linesBefore := m.viewport.baseLines

	// Only the archive fingerprint moves; the visible count provably does not.
	m.archivedTotal += 6
	if got := m.displayHistoryMessageCount(m.agentLoop.GetProvidable().GetHistoryView(key)); got != countBefore {
		t.Fatalf("test setup lost its point: visible count changed %d -> %d", countBefore, got)
	}

	m.updateViewport()

	if m.renderedBaseMsgCount != countBefore {
		t.Fatalf("visible message count changed (%d -> %d): the rebuild was forced by the count term, not the archive term",
			countBefore, m.renderedBaseMsgCount)
	}
	after := renderedBase(m)
	if !strings.Contains(after, fmt.Sprintf(i18n.T("tui.earlierMessages"), 6)) {
		t.Fatalf("archived change with an identical message count did not rebuild the base:\n%s", after)
	}

	// And the opposite half: with nothing changed, the base must still be
	// reused. A new backing array means buildRenderedHistoryLines ran again.
	linesCached := m.viewport.baseLines
	m.updateViewport()
	if &m.viewport.baseLines[0] != &linesCached[0] {
		t.Fatal("updateViewport rebuilt the base with no state change (over-invalidation)")
	}
	if &m.viewport.baseLines[0] == &linesBefore[0] {
		t.Fatal("base was never rebuilt by the archive change")
	}

	// The rebuild must still carry the archived rows themselves.
	if !strings.Contains(after, "Evict question 0?") {
		t.Fatalf("rebuilt base lost the archived content:\n%s", after)
	}
}
