package tui

// T8 regression tests: the session listing is coalesced and the batched
// message-count scan is cached.
//
// Context (docs/perf/tui-long-chat-baseline.md, §2): reloadSessions() —
// pkg/tui/model.go — rebuilt the whole session listing on every one of its
// call sites: ListSessions() (a walk of sessions + sessionMeta, one shell
// allocation per cold session, plus a sort) and AllTotalMessageCounts(), whose
// cold half is a full `GROUP BY session_key` scan of session_messages. Measured
// with 200 sessions: 444 µs / 179 KB / 815 allocs per call. The call sites that
// multiply it are the burst ones — every completeMsg (each finished subagent
// restarts a turn on the visible chat), every tab/ESC and every sidebar click.
//
// The contract pinned here:
//
//  1. a burst of events inside sessionsRefreshTTL performs exactly ONE
//     re-walk of the listing, and the next window performs exactly one more
//     (TestSessionsCache_CoalescesBursts);
//  2. the call sites documented as "must be exact" bypass the window: a switch
//     onto a chat the snapshot does not know yet, and the /sessions picker
//     (TestSessionsCache_SwitchOntoUnknownChatRefreshes,
//     TestSessionsCache_SessionsPickerIsExact);
//  3. a switch between chats the snapshot already knows does NOT re-walk —
//     coalescing is not defeated by ordinary navigation between known chats
//     (TestSessionsCache_KnownSwitchDoesNotRewalk);
//  4. the counts stay correct where they are visible: a chat whose only
//     evidence is its stored message count (cold shell, no name) is listed, and
//     the count is re-read as soon as the resident/cold split changes
//     (TestSessionsCache_ColdCountsFollowEviction);
//  5. structurally, the heavy listing walk has exactly one call site on the
//     update path (refreshSessionsCache) and the counts are only asked for by
//     the choke point that owns the listing; none of it is reachable from a
//     render path (TestSessionsListingCallSitesAreUpdateOnly).
//
// How (1) is verified without timing: the model carries the onSessionsRefresh
// hook, called by refreshSessionsCache — the single point where the walk can
// happen — so the counter is exhaustive for every event path (T6's style). The
// timing half is the ScaleReloadSessions / ScaleCombined benchmarks.
//
// Red-checks — each was verified by mutating the production code, watching the
// test fail and restoring it:
//
//	a) refreshSessionsCache ignoring the window (a walk per reloadSessions) ⇒
//	   (1) fails;
//	b) setCurrentChatKey not forcing for an unknown chat ⇒ (2) fails;
//	c) the /sessions picker reading a stale snapshot ⇒ (2) fails;
//	d) dropping pkg/session's cold-set validation (TTL-only count cache) ⇒
//	   (4) fails: the evicted nameless chat loses its only visibility evidence.

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
	"github.com/xilistudios/lele/pkg/providers"
)

// sessionsRefreshCounter installs the model's session-listing refresh hook and
// returns a function reporting how many listing walks happened since. Every walk
// goes through refreshSessionsCache, so the count is exhaustive.
func sessionsRefreshCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	refreshes := 0
	m.onSessionsRefresh = func() { refreshes++ }
	t.Cleanup(func() { m.onSessionsRefresh = nil })
	return func() int { return refreshes }
}

// completeTurnEvent is the event a finished turn produces for the visible chat —
// the burst trigger described in the task (one per finished subagent).
func completeTurnEvent(key string) completeMsg {
	return completeMsg{sessionKey: key}
}

// TestSessionsCache_CoalescesBursts is the T8 core: 20 completed turns plus 10
// cancels inside the window must not re-walk the listing 30 times.
func TestSessionsCache_CoalescesBursts(t *testing.T) {
	const key = "tui:chat:t8-burst"
	m := newFrameReadTestModel(t, key, 2)
	m.processing = true // makes the ESC cancel path run reloadSessions

	count := sessionsRefreshCounter(t, m)

	// The first event of the window walks the listing exactly once.
	updated, _ := m.Update(completeTurnEvent(key))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("after 1 completeMsg: %d listing walks, want 1", got)
	}
	if !m.sessionsCache.loaded {
		t.Fatal("the first refresh did not mark the snapshot as loaded")
	}

	// 20 completions + 10 double-ESC cancels inside the window: no more walks.
	for i := 0; i < 20; i++ {
		updated, _ = m.Update(completeTurnEvent(key))
		m = updated.(*Model)
	}
	for i := 0; i < 5; i++ {
		m.escLastPress = time.Time{} // force the "second press" branch
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(*Model)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(*Model)
	}
	if got := count(); got != 1 {
		t.Fatalf("30 events inside the window performed %d extra listing walks, want 0 (coalesced)", got-1)
	}
	// The visible list is still served (and re-derived) on every event.
	if len(m.visibleSessions) == 0 {
		t.Fatal("the coalesced refresh stopped serving the listing")
	}

	// A fresh snapshot is not re-walked either, however many events arrive.
	m.sessionsCache.at = time.Now()
	updated, _ = m.Update(completeTurnEvent(key))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("an event with a fresh snapshot performed %d extra walks, want 0", got-1)
	}

	// Past the window the next event walks exactly once more, and the clock moves.
	m.sessionsCache.at = time.Now().Add(-2 * sessionsRefreshTTL)
	updated, _ = m.Update(completeTurnEvent(key))
	m = updated.(*Model)
	if got := count(); got != 2 {
		t.Fatalf("after the window: %d walks, want 2 (one per window)", got)
	}
	if time.Since(m.sessionsCache.at) > time.Second {
		t.Fatal("the post-window refresh did not update the snapshot clock")
	}
}

// TestSessionsCache_SwitchOntoUnknownChatRefreshes covers the first eager site:
// the frame after a switch reads the listing for the sidebar rows and the
// selected index, so switching to a chat created after the snapshot was taken
// must walk it — the click that enters a brand new subagent chat is that case.
func TestSessionsCache_SwitchOntoUnknownChatRefreshes(t *testing.T) {
	m := seedSubagentSidebarModel(t, 2)
	// The subagent chats exist in the manager, but the snapshot was taken before
	// they did (a chat created after the last walk — the case a stale listing
	// cannot represent).
	for i := 0; i < 2; i++ {
		m.sessionMgr.GetOrCreate(fmt.Sprintf("subagent-key-%d", i))
	}
	m.sessionsCache = sessionsCacheState{loaded: true, at: time.Now()}

	row := -1
	for i, line := range strings.Split(stripAnsi(m.View()), "\n") {
		if strings.Contains(line, "SubClick1") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("SubClick1 not rendered in the sidebar")
	}
	m.sessionsCache.at = time.Now() // still fresh: only the forced path can walk it

	count := sessionsRefreshCounter(t, m)
	if m.sessionListedInCache("subagent-key-1") {
		t.Fatal("precondition: the subagent chat must be missing from the snapshot")
	}

	leftWidth := int(float64(m.width) * leftColumnRatio)
	updated, _ := m.Update(tea.MouseMsg{
		X:      leftWidth + chatSidebarGutter + 2,
		Y:      row,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(*Model)

	if got := count(); got != 1 {
		t.Fatalf("switching onto an unknown chat performed %d listing walks, want 1", got)
	}
	if m.currentKey != "subagent-key-1" {
		t.Fatalf("currentKey = %q, want the clicked subagent", m.currentKey)
	}
	if !m.sessionListedInCache("subagent-key-1") {
		t.Fatal("the forced refresh did not put the switched chat in the snapshot")
	}
}

// TestSessionsCache_KnownSwitchDoesNotRewalk is the other half: switching between
// chats the snapshot already knows is fully described by it, so it must not
// defeat the coalescing (otherwise a user cycling chats would pay a walk per
// keystroke — the burst T8 removes).
func TestSessionsCache_KnownSwitchDoesNotRewalk(t *testing.T) {
	const outgoing, incoming = "tui:chat:t8-known-out", "tui:chat:t8-known-in"
	m := newFrameReadTestModel(t, outgoing, 2)
	m.sessionMgr.GetOrCreate(incoming)
	_ = m.sessionMgr.SetMode(incoming, "agent")
	m.sessionMgr.AddMessage(incoming, "user", "incoming chat")

	// Load a snapshot that knows both chats, and make it fresh.
	m.reloadSessions()
	if !m.sessionListedInCache(incoming) {
		t.Fatal("precondition: the incoming chat must be in the snapshot")
	}
	m.sessionsCache.at = time.Now()

	count := sessionsRefreshCounter(t, m)
	m.setCurrentChatKey(incoming)
	m.reloadSessions() // what every switch handler runs right after
	if got := count(); got != 0 {
		t.Fatalf("a switch between two known chats performed %d listing walks, want 0", got)
	}
	if m.currentKey != incoming {
		t.Fatalf("currentKey = %q, want %q", m.currentKey, incoming)
	}
	if !m.visibleSessionsContains(incoming) {
		t.Fatal("the switched chat is missing from the served listing")
	}
}

// TestSessionsCache_SessionsPickerIsExact pins the modal path: /sessions exists
// to list the chats as they are now, so it must bypass the window — a chat
// created outside the TUI (or after the snapshot was taken) has to be offered
// for selection.
func TestSessionsCache_SessionsPickerIsExact(t *testing.T) {
	const current = "tui:chat:t8-picker-current"
	m := newFrameReadTestModel(t, current, 2)
	m.reloadSessions()              // the snapshot is loaded...
	m.sessionsCache.at = time.Now() // ...and fresh: only a forced walk knows the late chat

	// A chat that appeared after the snapshot was taken (e.g. created by the
	// WebUI/another client).
	late := "tui:chat:t8-picker-late"
	m.sessionMgr.GetOrCreate(late)
	_ = m.sessionMgr.SetMode(late, "agent")
	m.sessionMgr.AddMessage(late, "user", "created elsewhere")
	if m.sessionListedInCache(late) {
		t.Fatal("precondition: the late chat must be missing from the snapshot")
	}

	count := sessionsRefreshCounter(t, m)
	if cmd := m.executeCommand("/sessions"); cmd != nil {
		t.Fatal("opening the picker must not return a command")
	}
	if got := count(); got != 1 {
		t.Fatalf("opening /sessions performed %d listing walks, want 1 (exact picker)", got)
	}
	if m.modalMode != ModalSessions {
		t.Fatalf("modalMode = %v, want ModalSessions", m.modalMode)
	}
	found := false
	for _, key := range m.modalSessionKeys {
		if key == late {
			found = true
		}
	}
	if !found {
		t.Fatalf("the picker does not offer the chat created after the snapshot: %v", m.modalSessionKeys)
	}
}

// seedNamelessChat persists a chat whose only evidence of having messages is its
// stored count: assistant-only content, so no session name is ever generated,
// and nothing but msgCounts[key] > 0 can keep it in the listing once it is cold.
func seedNamelessChat(t *testing.T, m *Model, key string) {
	t.Helper()
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddFullMessage(key, providers.Message{Role: "assistant", Content: "reply without a user turn"})
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("Save(%s): %v", key, err)
	}
	if name := m.sessionMgr.GetName(key); name != "" {
		t.Fatalf("precondition: %s got the name %q; the test needs a nameless chat", key, name)
	}
}

// TestSessionsCache_ColdCountsFollowEviction is the freshness half, at the only
// place the TUI can observe it: a nameless cold shell is listed purely because
// its stored message count is non-zero, so a count served one refresh too late
// makes the chat disappear from the sidebar. Both orders are covered:
//
//   - a chat that was resident when the count cache was warmed and is evicted
//     afterwards (its count was live then, is the query's job now);
//   - a chat that appears AFTER the cache was warmed and is then evicted — the
//     cached map has no entry for it at all.
//
// The second order is what a TTL-only cache gets wrong in the sharpest form: it
// serves a map that was built before the chat existed, so the chat is reported
// as empty and its sidebar row disappears. Either order catches it (the cached
// map predates the chat being persisted in both), which is why the cache
// validates the cold set instead of trusting the clock. The listing snapshot is
// re-walked on purpose (refreshSessionsCache(true)) so what the assertions
// observe is only the counts, never a stale walk.
func TestSessionsCache_ColdCountsFollowEviction(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 200, 50

	// takeSnapshot re-walks the listing exactly, then runs the choke point that
	// asks for the counts (reloadSessions).
	takeSnapshot := func() {
		m.refreshSessionsCache(true)
		m.reloadSessions()
	}

	// The anchor is a nameless cold chat that keeps the store query running, so
	// the count cache does hold a map — one that does not know the late chat.
	anchor := "tui:chat:t8-cold-anchor"
	seedNamelessChat(t, m, anchor)
	if !m.sessionMgr.EvictSession(anchor) {
		t.Fatal("EvictSession(anchor) = false")
	}
	takeSnapshot()

	// Resident → cold: while resident the count comes from the live slice, and
	// after the eviction the stored count has to keep it visible.
	resident := "tui:chat:t8-cold-resident"
	seedNamelessChat(t, m, resident)
	takeSnapshot()
	if !m.visibleSessionsContains(resident) {
		t.Fatal("precondition: the nameless resident chat should be listed (live count)")
	}
	if !m.sessionMgr.EvictSession(resident) {
		t.Fatal("EvictSession(resident) = false")
	}
	takeSnapshot()
	if !m.visibleSessionsContains(resident) {
		t.Fatalf("the evicted nameless chat vanished from the listing; entries=%v",
			sessionListKeys(m.visibleSessions))
	}

	// Late → cold: persisted after the cache was warmed, so only a re-query can
	// count it. A TTL-only cache answers from the map built before it existed.
	late := "tui:chat:t8-cold-late"
	seedNamelessChat(t, m, late)
	if !m.sessionMgr.EvictSession(late) {
		t.Fatal("EvictSession(late) = false")
	}
	takeSnapshot()
	if !m.visibleSessionsContains(late) {
		t.Fatalf("the nameless chat created after the count cache was warmed vanished from the listing; entries=%v",
			sessionListKeys(m.visibleSessions))
	}
}

// visibleSessionsContains reports whether key is served by the current listing.
func (m *Model) visibleSessionsContains(key string) bool {
	for _, s := range m.visibleSessions {
		if s.Key == key {
			return true
		}
	}
	return false
}

// TestSessionsListingCallSitesAreUpdateOnly is the structural guard: the heavy
// listing walk (ListSessions) has exactly one call site on the update path —
// refreshSessionsCache — and the batched counts are only asked for by
// reloadSessions, the choke point that owns the listing. Neither may be reached
// from a render path: a future View()-side listing read fails here even if the
// counters above were bypassed.
func TestSessionsListingCallSitesAreUpdateOnly(t *testing.T) {
	const (
		callListing  = "ListSessions"
		callCounts   = "AllTotalMessageCounts"
		callRefresh  = "refreshSessionsCache"
		listingAllow = "refreshSessionsCache,NewModel"
		countsAllow  = "reloadSessions"
		refreshAllow = "model.go" // setCurrentChatKey + the two accessors
	)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}

	sites := func(call string) []site {
		var found []site
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
					c, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := c.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != call {
						return true
					}
					found = append(found, site{name, fn.Name.Name})
					return true
				})
			}
		}
		return found
	}

	listingSites := sites(callListing)
	assertSites(t, callListing, listingSites, listingAllow)
	countsSites := sites(callCounts)
	assertSites(t, callCounts, countsSites, countsAllow)
	assertSites(t, callRefresh, sites(callRefresh), refreshAllow)

	// The walk must be the one inside the refresh: refreshSessionsCache really is
	// the only producer of the snapshot.
	inRefresh := false
	for _, s := range listingSites {
		if s.fn == callRefresh {
			inRefresh = true
		}
	}
	if !inRefresh {
		t.Fatalf("no %s call inside %s: the refresh no longer walks the listing", callListing, callRefresh)
	}

	// No render path may read the listing or trigger its refresh. View() serves
	// visibleSessions, which reloadSessions derives from the snapshot.
	renderCalls := []string{"reloadSessions", "refreshSessionsCache", "cachedSessionListing", "freshSessionListing"}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "view") || !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
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
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := c.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				for _, banned := range renderCalls {
					if sel.Sel.Name == banned {
						t.Errorf("%s:%s calls %s from a render path", name, fn.Name.Name, banned)
					}
				}
				return true
			})
		}
	}
}

// TestSessionsCache_CountsAreReadOnEveryRefresh documents where the counts call
// lives and why: unlike the coalesced walk, AllTotalMessageCounts is asked for on
// every refresh, because its resident half is computed from live slices — that is
// what keeps the chat on screen exact — while its expensive cold half is what
// pkg/session memoizes (TestStoreCountsCache_* there). Moving it behind the
// window would make the visible counts stale for no gain.
func TestSessionsCache_CountsAreReadOnEveryRefresh(t *testing.T) {
	const key = "tui:chat:t8-counts-per-refresh"
	m := newFrameReadTestModel(t, key, 2)
	m.reloadSessions()              // load the snapshot
	m.sessionsCache.at = time.Now() // freshen the clock: the walk must be skipped now

	walks := 0
	m.onSessionsRefresh = func() { walks++ }
	t.Cleanup(func() { m.onSessionsRefresh = nil })

	before := len(m.visibleSessions)
	m.reloadSessions()
	if walks != 0 {
		t.Fatalf("a fresh snapshot was re-walked %d times, want 0", walks)
	}
	if len(m.visibleSessions) != before {
		t.Fatalf("visibleSessions changed without a walk: %d → %d", before, len(m.visibleSessions))
	}
}
