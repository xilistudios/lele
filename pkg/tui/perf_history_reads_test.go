package tui

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

func historyReadCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	reads := 0
	m.onHistoryRead = func(string) { reads++ }
	t.Cleanup(func() { m.onHistoryRead = nil })
	return func() int { return reads }
}

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

func TestView_OneHistoryReadPerFrame(t *testing.T) {
	const key = "tui:chat:one-read-per-frame"
	m := newFrameReadTestModel(t, key, 3)

	_ = m.View()

	count := historyReadCounter(t, m)

	if frame := m.View(); frame == "" {
		t.Fatal("View() returned an empty frame")
	}
	if got := count(); got != 1 {
		t.Fatalf("idle View() performed %d history reads, want 1", got)
	}

	m.processing = true
	m.currentStream = "partial answer"
	before := count()
	if frame := m.View(); frame == "" {
		t.Fatal("View() returned an empty frame while streaming")
	}
	if got := count() - before; got != 1 {
		t.Fatalf("streaming View() performed %d history reads, want 1", got)
	}

	if got := count(); got != 2 {
		t.Fatalf("two View() calls performed %d history reads, want 2", got)
	}
}

func TestView_TokenUsageCallSitesShareHoistedCount(t *testing.T) {
	const key = "tui:chat:token-call-sites"
	m := newFrameReadTestModel(t, key, 2)

	_ = m.View()
	const poisonedCurrent, poisonedWindow = 4242, 10000
	m.tokenCacheCurrent = poisonedCurrent
	m.tokenCacheWindow = poisonedWindow
	m.tokenCacheTime = time.Now()

	count := historyReadCounter(t, m)
	frame := stripAnsi(m.View())
	if got := count(); got != 1 {
		t.Fatalf("frame with warm token cache performed %d history reads, want 1", got)
	}

	bottomBar := fmt.Sprintf("%d (%.1f%%)", poisonedCurrent, float64(poisonedCurrent)/float64(poisonedWindow)*100)
	if !strings.Contains(frame, bottomBar) {
		t.Fatalf("bottom bar does not show the frame's cached token usage %q:\n%s", bottomBar, frame)
	}
	sidebar := formatNumber(poisonedCurrent)
	if !strings.Contains(frame, sidebar) {
		t.Fatalf("sidebar does not show the frame's cached token usage %q:\n%s", sidebar, frame)
	}
	if want := fmt.Sprintf("%s:%d", key, 4); m.tokenCacheKey != want {
		t.Fatalf("token cache key = %q, want %q (hoisted count of the frame)", m.tokenCacheKey, want)
	}
}

func TestGetTokenUsage_NeverReadsHistory(t *testing.T) {
	const key = "tui:chat:token-no-read"
	m := newFrameReadTestModel(t, key, 2)

	count := historyReadCounter(t, m)

	m.tokenCacheKey = ""
	_, _, _, _ = m.getTokenUsage(4)
	if got := count(); got != 0 {
		t.Fatalf("getTokenUsage miss performed %d history reads, want 0", got)
	}
	if want := fmt.Sprintf("%s:%d", key, 4); m.tokenCacheKey != want {
		t.Fatalf("cache key = %q, want %q", m.tokenCacheKey, want)
	}

	m.tokenCacheCurrent = 777
	m.tokenCacheTime = time.Now()
	current, _, _, _ := m.getTokenUsage(4)
	if current != 777 {
		t.Fatalf("cache hit returned %d, want the cached 777", current)
	}
	if got := count(); got != 0 {
		t.Fatalf("getTokenUsage hit performed %d history reads, want 0", got)
	}

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

func TestView_TokenCacheInvalidatedByHistoryChange(t *testing.T) {
	const key = "tui:chat:token-freshness"
	m := newFrameReadTestModel(t, key, 2)

	_ = m.View()
	if want := fmt.Sprintf("%s:%d", key, 4); m.tokenCacheKey != want {
		t.Fatalf("warm cache key = %q, want %q", m.tokenCacheKey, want)
	}

	m.tokenCacheCurrent = 4242
	m.tokenCacheTime = time.Now()
	if !strings.Contains(stripAnsi(m.View()), formatNumber(4242)) {
		t.Fatal("idle frame with a fresh cache did not reuse the cached token value")
	}

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

func TestFrameHistory_NotReusedAcrossFrames(t *testing.T) {
	const key = "tui:chat:frame-scope"
	m := newFrameReadTestModel(t, key, 1)
	m.sessionMgr.AddMessage(key, "tool", "tool output")

	if frame := stripAnsi(m.View()); !strings.Contains(frame, "Question 0 about the fix?") {
		t.Fatalf("frame 1 does not render the original history:\n%s", frame)
	}

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
