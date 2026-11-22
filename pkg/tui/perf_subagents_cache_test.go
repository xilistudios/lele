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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
)

func subagentRefreshCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	refreshes := 0
	m.onSubagentsRefresh = func() { refreshes++ }
	t.Cleanup(func() { m.onSubagentsRefresh = nil })
	return func() int { return refreshes }
}

func seedSubagentListing(m *Model, key string, at time.Time, list []channels.SubagentTaskInfo) {
	m.subagentsCache = subagentsCacheState{
		key:     "native:" + key,
		list:    list,
		running: countRunningSubagents(list),
		at:      at,
	}
}

func subagentListing(n int) []channels.SubagentTaskInfo {
	list := make([]channels.SubagentTaskInfo, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, channels.SubagentTaskInfo{
			TaskID:     fmt.Sprintf("subagent-%d", i),
			Label:      fmt.Sprintf("Phase %d", i),
			Status:     "completed",
			SessionKey: fmt.Sprintf("native:tui:chat:cached:subagent-%d", i),
		})
	}
	return list
}

func subagentResultEvent(key, taskID string) outboundMsg {
	return outboundMsg{msg: bus.OutboundMessage{
		Channel:  "tui",
		ChatID:   key,
		Event:    "subagent.result",
		Metadata: map[string]string{"task_id": taskID},
	}}
}

func TestView_SubagentListingIsCacheOnly(t *testing.T) {
	const key = "tui:chat:t6-cache-only"

	for _, n := range []int{0, 10, 200} {
		t.Run(fmt.Sprintf("subagents%d", n), func(t *testing.T) {
			m := newFrameReadTestModel(t, key, 3)
			seedSubagentListing(m, key, time.Now().Add(-time.Hour), subagentListing(n))

			count := subagentRefreshCounter(t, m)
			var frame string
			for i := 0; i < 3; i++ {
				frame = m.View()
				if frame == "" {
					t.Fatal("View() returned an empty frame")
				}
			}
			if got := count(); got != 0 {
				t.Fatalf("%d frames performed %d subagent listing lookups, want 0 — View() must serve the cache", 3, got)
			}
			wantRows := n > 0
			if got := len(m.subagentClickTargets) > 0; got != wantRows {
				t.Fatalf("frame rendered click targets = %v, want %v (n=%d)", got, wantRows, n)
			}
			if wantRows && !strings.Contains(stripAnsi(frame), "Phase 0") {
				t.Fatalf("the cached listing was not rendered into the sidebar (n=%d):\n%s", n, stripAnsi(frame))
			}
		})
	}
}

func TestSubagentsCache_RefreshesFromUpdateOnly(t *testing.T) {
	const key = "tui:chat:t6-refresh"
	m := newFrameReadTestModel(t, key, 2)

	count := subagentRefreshCounter(t, m)

	updated, _ := m.Update(subagentResultEvent(key, "subagent-1"))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("after 1 subagent.result event: %d refreshes, want 1", got)
	}
	if m.subagentsCache.key != "native:"+key {
		t.Fatalf("cache key = %q, want %q", m.subagentsCache.key, "native:"+key)
	}

	for i := 0; i < 20; i++ {
		updated, _ = m.Update(subagentResultEvent(key, fmt.Sprintf("subagent-%d", i+2)))
		m = updated.(*Model)
	}
	if got := count(); got != 1 {
		t.Fatalf("20 further events inside the TTL window performed %d extra lookups, want 0 (coalesced)", got-1)
	}

	m.subagentsCache.at = time.Now()
	updated, _ = m.Update(subagentResultEvent(key, "subagent-99"))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("an Update with a fresh cache performed %d extra lookups, want 0", got-1)
	}

	m.subagentsCache.at = time.Now().Add(-2 * subagentsCacheTTL)
	updated, _ = m.Update(subagentResultEvent(key, "subagent-100"))
	m = updated.(*Model)
	if got := count(); got != 2 {
		t.Fatalf("after the TTL window: %d refreshes, want 2 (one per window)", got)
	}
	if time.Since(m.subagentsCache.at) > time.Second {
		t.Fatal("the refresh did not update the cache clock")
	}
}

func TestSubagentsCache_SessionSwitchRefreshes(t *testing.T) {
	const outgoing, incoming = "tui:chat:t6-switch-out", "tui:chat:t6-switch-in"
	m := newFrameReadTestModel(t, outgoing, 2)

	old := []channels.SubagentTaskInfo{{
		TaskID: "subagent-1", Label: "OutgoingPhase", Status: "completed", SessionKey: "native:out:subagent-1",
	}}
	seedSubagentListing(m, outgoing, time.Now(), old)

	count := subagentRefreshCounter(t, m)
	m.setCurrentChatKey(incoming)
	if got := count(); got != 1 {
		t.Fatalf("session switch performed %d lookups, want 1", got)
	}
	if m.subagentsCache.key != "native:"+incoming {
		t.Fatalf("cache key after switch = %q, want %q", m.subagentsCache.key, "native:"+incoming)
	}
	if frame := stripAnsi(m.View()); strings.Contains(frame, "OutgoingPhase") {
		t.Fatalf("the frame still renders the outgoing session's subagent listing:\n%s", frame)
	}
}

func TestView_ServesLastListingWhileCacheIsStale(t *testing.T) {
	const key = "tui:chat:t6-stale"
	m := newFrameReadTestModel(t, key, 3)

	stale := []channels.SubagentTaskInfo{{
		TaskID: "subagent-7", Label: "StalePhase7", Status: "completed", SessionKey: "native:stale:subagent-7",
	}}
	seedSubagentListing(m, key, time.Now().Add(-time.Hour), stale)

	count := subagentRefreshCounter(t, m)
	if frame := stripAnsi(m.View()); !strings.Contains(frame, "StalePhase7") {
		t.Fatalf("a stale cache was not served to the frame:\n%s", frame)
	}
	if got := count(); got != 0 {
		t.Fatalf("serving a stale cache performed %d lookups, want 0", got)
	}
	if m.subagentsCache.key != "native:"+key || len(m.subagentsCache.list) != 1 {
		t.Fatal("the frame mutated the cache while serving it")
	}

	m.subagentsCache.at = time.Time{}
	updated, _ := m.Update(subagentResultEvent(key, "subagent-7"))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("the refresh from Update() performed %d lookups, want 1", got)
	}
	if frame := stripAnsi(m.View()); strings.Contains(frame, "StalePhase7") {
		t.Fatalf("the frame still renders the pre-refresh listing after an Update() refresh:\n%s", frame)
	}
}

func TestView_RendersRefreshedListing(t *testing.T) {
	const key = "tui:chat:t6-render-refreshed"
	m := newFrameReadTestModel(t, key, 3)

	seedSubagentListing(m, key, time.Now(), subagentListing(3))
	if frame := stripAnsi(m.View()); !strings.Contains(frame, "Phase 2") {
		t.Fatalf("the sidebar does not render the cached listing:\n%s", frame)
	}

	seedSubagentListing(m, key, time.Now(), []channels.SubagentTaskInfo{{
		TaskID: "subagent-99", Label: "FreshPhase99", Status: "completed", SessionKey: "native:fresh:subagent-99",
	}})
	frame := stripAnsi(m.View())
	if !strings.Contains(frame, "FreshPhase99") {
		t.Fatalf("the frame did not pick up the refreshed listing:\n%s", frame)
	}
	if strings.Contains(frame, "Phase 2") {
		t.Fatalf("the frame still renders the previous listing:\n%s", frame)
	}
}

func TestHasRunningSubagents_IsO1FromCache(t *testing.T) {
	const key = "tui:chat:t6-running-o1"
	m := newFrameReadTestModel(t, key, 3)

	seedSubagentListing(m, key, time.Now().Add(-time.Hour), subagentListing(200))

	count := subagentRefreshCounter(t, m)
	if m.hasRunningSubagents() {
		t.Fatal("hasRunningSubagents() = true with 200 completed subagents")
	}
	if got := count(); got != 0 {
		t.Fatalf("hasRunningSubagents() performed %d listing lookups, want 0", got)
	}
	if !raceEnabled {
		if allocs := testing.AllocsPerRun(20, func() { _ = m.hasRunningSubagents() }); allocs != 0 {
			t.Fatalf("hasRunningSubagents() allocated %.1f objects per call, want 0 (no listing built)", allocs)
		}
	}

	list := subagentListing(200)
	list[42].Status = "running"
	seedSubagentListing(m, key, time.Now().Add(-time.Hour), list)
	if !m.hasRunningSubagents() {
		t.Fatal("hasRunningSubagents() = false with a running subagent in the cached listing")
	}
	if got := count(); got != 0 {
		t.Fatalf("hasRunningSubagents() with a running subagent performed %d listing lookups, want 0", got)
	}

	seedSubagentListing(m, key, time.Now(), list[:42])
	if m.hasRunningSubagents() {
		t.Fatal("the cached running count was not updated when the listing was reloaded")
	}
}

type site struct{ file, fn string }

func TestSubagentListingCallSitesAreUpdateOnly(t *testing.T) {
	const (
		callBackend  = "GetSessionSubagents"
		callRefresh  = "refreshSubagentsCache"
		backendAllow = "refreshSubagentsCache,executeCommand"
		refreshAllow = "handlers.go,model.go"
	)

	var backendSites []site
	var refreshSites []site

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
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
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case callBackend:
					backendSites = append(backendSites, site{name, fn.Name.Name})
				case callRefresh:
					refreshSites = append(refreshSites, site{name, fn.Name.Name})
				}
				return true
			})
		}
	}

	assertSites(t, callBackend, backendSites, backendAllow)
	assertSites(t, callRefresh, refreshSites, refreshAllow)

	found := false
	for _, s := range backendSites {
		if s.fn == callRefresh {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s call inside %s: the refresh no longer loads the listing", callBackend, callRefresh)
	}
}

func assertSites(t *testing.T, call string, sites []site, allowed string) {
	t.Helper()
	allow := make(map[string]bool)
	for _, name := range strings.Split(allowed, ",") {
		allow[name] = true
	}
	var offenders []string
	for _, s := range sites {
		if !allow[s.file] && !allow[s.fn] {
			offenders = append(offenders, fmt.Sprintf("%s:%s", s.file, s.fn))
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("%s is called from %v, want only %s", call, offenders, allowed)
	}
	if len(sites) == 0 {
		t.Fatalf("%s has no call site in pkg/tui, want the update-only one", call)
	}
}

func TestUpdateTailAlwaysRefreshesCache(t *testing.T) {
	const key = "tui:chat:t6-choke-point"
	m := newFrameReadTestModel(t, key, 2)

	count := subagentRefreshCounter(t, m)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("an Update() that never reaches finishUpdate performed %d refreshes, want 1", got)
	}
}
