package tui

// T6 regression tests: the subagent listing is served from a cache that is
// refreshed in Update(), never in View().
//
// Context (hallazgo M1 of docs/perf/tui-long-chat-baseline.md):
// GetSessionSubagents was called from View() twice per frame — the sidebar and
// renderStatusLine → isSessionProcessing → hasRunningSubagents — and its 500 ms
// TTL was invalidated by every subagent.result / spawn tool.result event, i.e.
// once per finished subagent. The frames the user reported as slow (many
// finished subagents) therefore paid 2.0 ms / 414 KB at 200 subagents and
// 6.9 ms / 1.2 MB at 500 cold ones on the render path, repeatedly.
//
// The contract pinned here:
//
//  1. a frame performs ZERO listing lookups — with 0, 10 or 200 cached
//     subagents, and even when the cache is past its TTL
//     (TestView_SubagentListingIsCacheOnly);
//  2. the refresh happens from Update(): a lifecycle event refreshes the cache
//     once and a burst of events inside the TTL window does not refresh again
//     (TestSubagentsCache_RefreshesFromUpdateOnly, coalescing);
//  3. the frame serves the LAST known listing when the cache is stale — no
//     panic, no lookup — and the next Update()-driven refresh is what changes
//     what is drawn (TestView_ServesLastListingWhileCacheIsStale);
//  4. hasRunningSubagents answers in O(1) from the cached count: no listing
//     construction, no allocation, no lookup, even with 200 cached subagents
//     and an expired cache (TestHasRunningSubagents_IsO1FromCache);
//  5. structurally, the listing's backend call has exactly two call sites —
//     refreshSubagentsCache (Update()) and the /subagents modal (an
//     Update()-path command) — and refreshSubagentsCache is only called from
//     handlers.go / model.go, never from a view*.go file
//     (TestSubagentListingCallSitesAreUpdateOnly).
//
// Red-checks — each one was verified by mutating the production code, watching
// the test fail and restoring it:
//
//	a) refreshSubagentsCache called from renderSidebarSubagents (the old
//	   getSessionSubagentsCached behaviour) ⇒ (1), (3) and (5) fail;
//	b) hasRunningSubagents rebuilding the listing instead of using the cached
//	   count ⇒ (4) fails (one lookup + allocations with a stale cache);
//	c) no TTL/coalescing in refreshSubagentsCache (refresh on every Update)
//	   ⇒ (2) fails (N events ⇒ N lookups).

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

// subagentRefreshCounter installs the model's subagent-listing refresh hook and
// returns a function reporting how many backend lookups happened since. The
// hook is the single choke point of the listing (Model.refreshSubagentsCache),
// so the count is exhaustive for both the render and the Update path.
func subagentRefreshCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	refreshes := 0
	m.onSubagentsRefresh = func() { refreshes++ }
	t.Cleanup(func() { m.onSubagentsRefresh = nil })
	return func() int { return refreshes }
}

// seedSubagentListing installs `list` in the subagent cache as if the last
// Update()-side refresh had loaded it for `key`, with `at` as the refresh clock
// (time.Now() = fresh window, anything older = stale). The running count is
// derived exactly like the production refresh does, so seeded models behave
// like refreshed ones.
func seedSubagentListing(m *Model, key string, at time.Time, list []channels.SubagentTaskInfo) {
	m.subagentsCache = subagentsCacheState{
		key:     "native:" + key,
		list:    list,
		running: countRunningSubagents(list),
		at:      at,
	}
}

// subagentListing builds n completed subagent entries labelled by index.
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

// subagentResultEvent is the outbound event the agent loop emits when a
// subagent finishes — one of the two lifecycle events the old code used to
// invalidate the cache on.
func subagentResultEvent(key, taskID string) outboundMsg {
	return outboundMsg{msg: bus.OutboundMessage{
		Channel:  "tui",
		ChatID:   key,
		Event:    "subagent.result",
		Metadata: map[string]string{"task_id": taskID},
	}}
}

// TestView_SubagentListingIsCacheOnly is the T6 red-check: however many
// subagents the chat has accumulated, a frame must not look them up. The cache
// is deliberately STALE in every case (the old implementation refreshed from
// the frame as soon as the TTL expired), and the counter is exhaustive because
// every backend lookup goes through refreshSubagentsCache.
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
			// The frame must actually have consumed the cache: the seeded rows
			// are on screen (the sidebar caps how many fit, so only the first
			// entries are asserted) and have click targets.
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

// TestSubagentsCache_RefreshesFromUpdateOnly pins where the refresh lives and
// the coalescing that keeps a burst of completions from turning into a burst of
// listing scans: the first lifecycle event refreshes, the 20 that follow inside
// the TTL window do not, and only a new window refreshes again.
func TestSubagentsCache_RefreshesFromUpdateOnly(t *testing.T) {
	const key = "tui:chat:t6-refresh"
	m := newFrameReadTestModel(t, key, 2)

	count := subagentRefreshCounter(t, m)

	// A lifecycle event arriving through Update() refreshes the (empty) cache.
	updated, _ := m.Update(subagentResultEvent(key, "subagent-1"))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("after 1 subagent.result event: %d refreshes, want 1", got)
	}
	if m.subagentsCache.key != "native:"+key {
		t.Fatalf("cache key = %q, want %q", m.subagentsCache.key, "native:"+key)
	}

	// 20 more results inside the window: still one lookup (coalesced).
	for i := 0; i < 20; i++ {
		updated, _ = m.Update(subagentResultEvent(key, fmt.Sprintf("subagent-%d", i+2)))
		m = updated.(*Model)
	}
	if got := count(); got != 1 {
		t.Fatalf("20 further events inside the TTL window performed %d extra lookups, want 0 (coalesced)", got-1)
	}

	// A fresh cache is not refreshed either: Update() must respect the window.
	m.subagentsCache.at = time.Now()
	updated, _ = m.Update(subagentResultEvent(key, "subagent-99"))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("an Update with a fresh cache performed %d extra lookups, want 0", got-1)
	}

	// Past the window the next Update() refreshes exactly once.
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

// TestSubagentsCache_SessionSwitchRefreshes pins the other eager refresh site:
// a switch makes the cached entry stale by construction, so the new session
// must never serve the outgoing session's listing.
func TestSubagentsCache_SessionSwitchRefreshes(t *testing.T) {
	const outgoing, incoming = "tui:chat:t6-switch-out", "tui:chat:t6-switch-in"
	m := newFrameReadTestModel(t, outgoing, 2)

	old := []channels.SubagentTaskInfo{{
		TaskID: "subagent-1", Label: "OutgoingPhase", Status: "completed", SessionKey: "native:out:subagent-1",
	}}
	seedSubagentListing(m, outgoing, time.Now(), old) // fresh window: only the key change can refresh

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

// TestView_ServesLastListingWhileCacheIsStale is the TTL half of the contract:
// with a cache older than subagentsCacheTTL the frame must keep serving the
// last known state (no lookup, no panic), and it is the Update()-driven refresh
// that changes what is drawn afterwards — never the frame itself.
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

	// An Update() past the window refreshes (empty backend listing in this
	// model), and the next frame reflects that — the stale row is gone.
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

// TestView_RendersRefreshedListing pins the visible half: what Update() loads
// is what the next frame draws, so moving the refresh off the render path did
// not freeze the sidebar.
func TestView_RendersRefreshedListing(t *testing.T) {
	const key = "tui:chat:t6-render-refreshed"
	m := newFrameReadTestModel(t, key, 3)

	seedSubagentListing(m, key, time.Now(), subagentListing(3))
	if frame := stripAnsi(m.View()); !strings.Contains(frame, "Phase 2") {
		t.Fatalf("the sidebar does not render the cached listing:\n%s", frame)
	}

	// Swap the listing the way a refresh does and re-render.
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

// TestHasRunningSubagents_IsO1FromCache covers the status-line half of the M1
// finding: with 200 cached subagents and an EXPIRED cache, asking whether
// anything is running must neither look anything up (the old
// getSessionSubagentsCached refreshed here) nor build/scan the listing.
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
	// The zero-allocation half of the guard only runs without the race
	// detector: under -race AllocsPerRun reports 1 alloc/op for a call that
	// allocates nothing (see race_off_test.go). The lookup assertion above is
	// the allocation-independent half and always runs, so -race still fails
	// here if the call ever goes back to refreshing/building a listing.
	if !raceEnabled {
		if allocs := testing.AllocsPerRun(20, func() { _ = m.hasRunningSubagents() }); allocs != 0 {
			t.Fatalf("hasRunningSubagents() allocated %.1f objects per call, want 0 (no listing built)", allocs)
		}
	}

	// One running entry among the 200 is enough — still O(1), still no lookup.
	list := subagentListing(200)
	list[42].Status = "running"
	seedSubagentListing(m, key, time.Now().Add(-time.Hour), list)
	if !m.hasRunningSubagents() {
		t.Fatal("hasRunningSubagents() = false with a running subagent in the cached listing")
	}
	if got := count(); got != 0 {
		t.Fatalf("hasRunningSubagents() with a running subagent performed %d listing lookups, want 0", got)
	}

	// The cached count is what the refresh materialized: a listing whose
	// running entry is dropped stops reporting "running" without a lookup.
	seedSubagentListing(m, key, time.Now(), list[:42])
	if m.hasRunningSubagents() {
		t.Fatal("the cached running count was not updated when the listing was reloaded")
	}
}

// TestSubagentListingCallSitesAreUpdateOnly is the structural guard: the
// listing's backend call must have exactly the two known call sites (the
// Update()-side refresh and the /subagents modal command), and the refresh
// itself must be called only from files that are not render paths. A future
// `GetSessionSubagents` re-introduced inside View() — the M1 bug — fails here
// even if the counters above were somehow bypassed.
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

	// The backend call must be the one inside the refresh (model.go), so the
	// refresh is genuinely the only producer of the cached listing.
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

// assertSites fails when a call's call sites differ from the allowed set,
// reporting every offending site by file and enclosing function.
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

// TestUpdateTailAlwaysRefreshesCache pins the single choke point: every message
// goes through Update(), so no early-returning handler can skip the refresh —
// including the ones that never reach finishUpdate.
func TestUpdateTailAlwaysRefreshesCache(t *testing.T) {
	const key = "tui:chat:t6-choke-point"
	m := newFrameReadTestModel(t, key, 2)

	count := subagentRefreshCounter(t, m)
	// A window-size message returns from its own handler; the refresh must
	// still have run (it lives in Update, not in the handler).
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("an Update() that never reaches finishUpdate performed %d refreshes, want 1", got)
	}
}
