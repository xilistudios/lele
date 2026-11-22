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
	"github.com/xilistudios/lele/pkg/providers"
)

func sessionsRefreshCounter(t *testing.T, m *Model) func() int {
	t.Helper()
	refreshes := 0
	m.onSessionsRefresh = func() { refreshes++ }
	t.Cleanup(func() { m.onSessionsRefresh = nil })
	return func() int { return refreshes }
}

func completeTurnEvent(key string) completeMsg {
	return completeMsg{sessionKey: key}
}

func TestSessionsCache_CoalescesBursts(t *testing.T) {
	const key = "tui:chat:t8-burst"
	m := newFrameReadTestModel(t, key, 2)
	m.processing = true

	count := sessionsRefreshCounter(t, m)

	updated, _ := m.Update(completeTurnEvent(key))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("after 1 completeMsg: %d listing walks, want 1", got)
	}
	if !m.sessionsCache.loaded {
		t.Fatal("the first refresh did not mark the snapshot as loaded")
	}

	for i := 0; i < 20; i++ {
		updated, _ = m.Update(completeTurnEvent(key))
		m = updated.(*Model)
	}
	for i := 0; i < 5; i++ {
		m.escLastPress = time.Time{}
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(*Model)
		updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = updated.(*Model)
	}
	if got := count(); got != 1 {
		t.Fatalf("30 events inside the window performed %d extra listing walks, want 0 (coalesced)", got-1)
	}
	if len(m.visibleSessions) == 0 {
		t.Fatal("the coalesced refresh stopped serving the listing")
	}

	m.sessionsCache.at = time.Now()
	updated, _ = m.Update(completeTurnEvent(key))
	m = updated.(*Model)
	if got := count(); got != 1 {
		t.Fatalf("an event with a fresh snapshot performed %d extra walks, want 0", got-1)
	}

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

func TestSessionsCache_SwitchOntoUnknownChatRefreshes(t *testing.T) {
	m := seedSubagentSidebarModel(t, 2)
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
	m.sessionsCache.at = time.Now()

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

func TestSessionsCache_KnownSwitchDoesNotRewalk(t *testing.T) {
	const outgoing, incoming = "tui:chat:t8-known-out", "tui:chat:t8-known-in"
	m := newFrameReadTestModel(t, outgoing, 2)
	m.sessionMgr.GetOrCreate(incoming)
	_ = m.sessionMgr.SetMode(incoming, "agent")
	m.sessionMgr.AddMessage(incoming, "user", "incoming chat")

	m.reloadSessions()
	if !m.sessionListedInCache(incoming) {
		t.Fatal("precondition: the incoming chat must be in the snapshot")
	}
	m.sessionsCache.at = time.Now()

	count := sessionsRefreshCounter(t, m)
	m.setCurrentChatKey(incoming)
	m.reloadSessions()
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

func TestSessionsCache_SessionsPickerIsExact(t *testing.T) {
	const current = "tui:chat:t8-picker-current"
	m := newFrameReadTestModel(t, current, 2)
	m.reloadSessions()
	m.sessionsCache.at = time.Now()

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

func TestSessionsCache_ColdCountsFollowEviction(t *testing.T) {
	m := newTestModel(t)
	m.width, m.height = 200, 50

	takeSnapshot := func() {
		m.refreshSessionsCache(true)
		m.reloadSessions()
	}

	anchor := "tui:chat:t8-cold-anchor"
	seedNamelessChat(t, m, anchor)
	if !m.sessionMgr.EvictSession(anchor) {
		t.Fatal("EvictSession(anchor) = false")
	}
	takeSnapshot()

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

func (m *Model) visibleSessionsContains(key string) bool {
	for _, s := range m.visibleSessions {
		if s.Key == key {
			return true
		}
	}
	return false
}

func TestSessionsListingCallSitesAreUpdateOnly(t *testing.T) {
	const (
		callListing  = "ListSessions"
		callCounts   = "AllTotalMessageCounts"
		callRefresh  = "refreshSessionsCache"
		listingAllow = "refreshSessionsCache,NewModel"
		countsAllow  = "reloadSessions"
		refreshAllow = "model.go"
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

	inRefresh := false
	for _, s := range listingSites {
		if s.fn == callRefresh {
			inRefresh = true
		}
	}
	if !inRefresh {
		t.Fatalf("no %s call inside %s: the refresh no longer walks the listing", callListing, callRefresh)
	}

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

func TestSessionsCache_CountsAreReadOnEveryRefresh(t *testing.T) {
	const key = "tui:chat:t8-counts-per-refresh"
	m := newFrameReadTestModel(t, key, 2)
	m.reloadSessions()
	m.sessionsCache.at = time.Now()

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
