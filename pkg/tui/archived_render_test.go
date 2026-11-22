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

func renderedBase(m *Model) string {
	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	return strings.Join(m.buildRenderedHistoryLines(history), "\n")
}

func earlierHeaderRe() *regexp.Regexp {
	quoted := regexp.QuoteMeta(i18n.T("tui.earlierMessages"))
	return regexp.MustCompile(strings.Replace(quoted, "%d", `(\d+)`, 1))
}

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

func TestArchivedRender_ShowsEvictedMessagesAfterCompact(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-a"

	evicted := seedEvictionSession(t, m, key, 12, 5)
	if evicted != 19 {
		t.Fatalf("expected 19 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	outBottom := m.View()
	if !strings.Contains(outBottom, "Evict answer 11.") {
		t.Fatalf("bottom view should contain resident 'Evict answer 11.', got:\n%s", outBottom)
	}

	m.viewport.GotoTop()
	outTop := m.View()
	if !strings.Contains(outTop, "Evict question 0?") {
		t.Fatalf("top view should contain archived 'Evict question 0?', got:\n%s", outTop)
	}
}

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

func TestArchivedRender_ScrollUpLoadsOlderPages(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-d"

	origCap := maxArchivedPrefixMessages
	maxArchivedPrefixMessages = 60
	t.Cleanup(func() { maxArchivedPrefixMessages = origCap })

	seedEvictionSession(t, m, key, 200, 20)

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 60
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	loadedAfterRefresh := len(m.archivedPrefix)
	if loadedAfterRefresh == 0 {
		t.Fatal("expected some archived messages to be loaded")
	}

	initialBase := renderedBase(m)
	initialHeaderNum := headerCount(initialBase)
	if initialHeaderNum < 0 {
		t.Fatalf("expected %q header in rendered base, got:\n%s",
			i18n.T("tui.earlierMessages"), initialBase)
	}

	m.viewport.GotoTop()
	expanded := m.maybeExpandRenderWindow()
	if !expanded {
		t.Fatal("expected maybeExpandRenderWindow to return true (archived pages available)")
	}

	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0 after archive expansion, got %d", m.renderStartIdx)
	}

	if len(m.archivedPrefix) <= loadedAfterRefresh {
		t.Fatalf("expected archivedPrefix to grow: before=%d, after=%d", loadedAfterRefresh, len(m.archivedPrefix))
	}

	expandedHeaderNum := headerCount(renderedBase(m))
	if expandedHeaderNum >= 0 && expandedHeaderNum >= initialHeaderNum {
		t.Fatalf("expected header number to decrease: initial=%d, after expand=%d",
			initialHeaderNum, expandedHeaderNum)
	}
}

func TestArchivedRender_ExhaustedArchiveStops(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-e"

	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

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

	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow=false after exhaustion")
	}
}

func TestArchivedRender_SessionSwitchHidesOtherPrefix(t *testing.T) {
	m := newEvictionTestModel(t)
	const keyA = "tui:chat:render-f"
	const keyB = "tui:chat:render-f2"

	seedEvictionSession(t, m, keyA, 12, 5)
	m.currentKey = keyA
	m.showWelcome = false
	m.forceGotoBottom = true
	m.refreshArchivedHistory()

	archivedA := m.archivedForCurrentSession()
	if len(archivedA) == 0 {
		t.Fatal("expected non-empty archived prefix for session A")
	}
	msgA := archivedA[0].Content

	seedLazySession(t, m, keyB, 5)
	renderLazyModel(t, m)
	m.viewport.GotoTop()
	outB := m.View()

	if strings.Contains(outB, msgA) {
		t.Fatalf("session B view should not contain session A's archived message %q, got:\n%s", msgA, outB)
	}
}

func TestArchivedRender_WindowHeaderCountsCombined(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-g"

	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 10
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	out := renderedBase(m)
	headerNum := headerCount(out)
	if headerNum < 0 {
		t.Fatalf("expected %q header in rendered base, got:\n%s",
			i18n.T("tui.earlierMessages"), out)
	}

	expected := m.renderStartIdx + m.archivedHiddenCount()
	if headerNum != expected {
		t.Fatalf("header number=%d, expected renderStartIdx(%d)+archivedHidden(%d)=%d",
			headerNum, m.renderStartIdx, m.archivedHiddenCount(), expected)
	}
}

func TestArchivedRender_HeaderNumberDecreasesOnExpand(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-h"

	origCap := maxArchivedPrefixMessages
	maxArchivedPrefixMessages = 40
	t.Cleanup(func() { maxArchivedPrefixMessages = origCap })

	seedEvictionSession(t, m, key, 200, 20)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 60
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	extractNum := func(rendered string) int { return headerCount(rendered) }

	prev := extractNum(renderedBase(m))
	if prev < 0 {
		t.Fatalf("expected initial header number, got -1")
	}

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

func TestArchivedRender_AllArchivedFitBelowCap(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-i"

	seedEvictionSession(t, m, key, 12, 5)
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.maxRenderedMessages = 0
	m.refreshArchivedHistory()
	renderLazyModel(t, m)

	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	baseLines := m.buildRenderedHistoryLines(history)
	baseStr := strings.Join(baseLines, "\n")

	if strings.Contains(baseStr, "earlier messages") {
		t.Fatalf("expected no 'earlier messages' header when everything fits")
	}

	if !strings.Contains(baseStr, "Evict question 0?") {
		t.Fatalf("expected archived message in base lines")
	}
	if !strings.Contains(baseStr, "Evict answer 11.") {
		t.Fatalf("expected resident message in base lines")
	}
}

func TestArchivedRender_RenderStartIdxZeroAndNoArchiveIsNoop(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:render-j", 5)
	renderLazyModel(t, m)

	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0, got %d", m.renderStartIdx)
	}

	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow=false with no evictions and renderStartIdx=0")
	}
}

func TestArchivedRender_DisplayHistoryMessageCount(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:render-k"

	seedEvictionSession(t, m, key, 12, 5)

	history := m.sessionMgr.GetHistory(key)
	residentCount := 0
	for _, msg := range history {
		if msg.Role == "user" || msg.Role == "assistant" {
			residentCount++
		}
	}

	m.currentKey = key
	before := m.displayHistoryMessageCount(history)
	if before != residentCount {
		t.Fatalf("before refresh: expected %d, got %d", residentCount, before)
	}

	m.refreshArchivedHistory()
	after := m.displayHistoryMessageCount(history)
	if after != 19+residentCount {
		t.Fatalf("after refresh: expected %d, got %d", 19+residentCount, after)
	}
}

func TestArchivedRender_ArchivedVisibleCountIncremental(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:render-l"
	m.archivedKey = m.currentKey

	m.prependArchivedPage(testEvictedPageSeqs(11, []struct {
		role, content string
	}{
		{"user", "q1"},
		{"assistant", "a1"},
		{"system", "s1"},
		{"user", "q2"},
		{"assistant", "a2"},
	}))

	if m.archivedVisibleCount != 4 {
		t.Fatalf("expected archivedVisibleCount=4, got %d", m.archivedVisibleCount)
	}

	m.prependArchivedPage(testEvictedPageSeqs(9, []struct {
		role, content string
	}{
		{"user", "q0"},
		{"assistant", "a0"},
	}))

	if m.archivedVisibleCount != 6 {
		t.Fatalf("expected archivedVisibleCount=6, got %d", m.archivedVisibleCount)
	}

	m.resetArchivedHistory()
	if m.archivedVisibleCount != 0 {
		t.Fatalf("expected archivedVisibleCount=0 after reset, got %d", m.archivedVisibleCount)
	}
}

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

	linesCached := m.viewport.baseLines
	m.updateViewport()
	if &m.viewport.baseLines[0] != &linesCached[0] {
		t.Fatal("updateViewport rebuilt the base with no state change (over-invalidation)")
	}
	if &m.viewport.baseLines[0] == &linesBefore[0] {
		t.Fatal("base was never rebuilt by the archive change")
	}

	if !strings.Contains(after, "Evict question 0?") {
		t.Fatalf("rebuilt base lost the archived content:\n%s", after)
	}
}
