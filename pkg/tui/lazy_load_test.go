package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/store"
)

// seedLazySession creates a session with the given number of user+assistant
// message pairs, opens it in the model, and triggers a render so the lazy-load
// render window is initialized.
func seedLazySession(t *testing.T, m *Model, key string, pairs int) {
	t.Helper()

	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < pairs; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question number %d?", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d.\nLine two of answer %d.\nLine three of answer %d.", i, i, i))
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
}

// renderLazyModel applies a window size and renders once so the viewport has
// real dimensions and the render window is initialized.
func renderLazyModel(t *testing.T, m *Model) {
	t.Helper()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m2 := updated.(*Model)
	*m = *m2
	m.View()
}

// TestLazyLoad_InitialWindow verifies that with more messages than the render
// cap, the initial render window starts at the default index and shows the
// "earlier messages" header.
func TestLazyLoad_InitialWindow(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:lazy-a", 160) // 320 messages > 200 cap
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected renderStartIdx=120, got %d", m.renderStartIdx)
	}

	// Scroll to top so the header line is visible in the rendered view.
	m.viewport.GotoTop()
	out := m.View()
	if !strings.Contains(out, "↑ 120 earlier messages") {
		t.Fatalf("expected header '↑ 120 earlier messages' in view, got:\n%s", out)
	}
}

// TestLazyLoad_ExpandOnScrollUp verifies that scrolling to the top expands the
// render window backwards by lazyLoadBatchSize, compensates YOffset, and stops
// once all messages are loaded.
func TestLazyLoad_ExpandOnScrollUp(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:lazy-b", 160) // 320 messages
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected initial renderStartIdx=120, got %d", m.renderStartIdx)
	}

	// First expansion: 120 -> 70
	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return true on first expansion")
	}
	if m.renderStartIdx != 70 {
		t.Fatalf("expected renderStartIdx=70 after first expand, got %d", m.renderStartIdx)
	}
	if m.viewport.YOffset <= 0 {
		t.Fatalf("expected YOffset>0 after compensation, got %d", m.viewport.YOffset)
	}
	m.viewport.GotoTop()
	if out := m.View(); !strings.Contains(out, "↑ 70 earlier messages") {
		t.Fatalf("expected header '↑ 70 earlier messages', got:\n%s", out)
	}

	// Second expansion: 70 -> 20
	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return true on second expansion")
	}
	if m.renderStartIdx != 20 {
		t.Fatalf("expected renderStartIdx=20 after second expand, got %d", m.renderStartIdx)
	}

	// Third expansion: 20 -> 0, header disappears
	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return true on third expansion")
	}
	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0 after third expand, got %d", m.renderStartIdx)
	}
	m.viewport.GotoTop()
	if out := m.View(); strings.Contains(out, "earlier messages") {
		t.Fatalf("expected header to be absent when renderStartIdx=0, got:\n%s", out)
	}

	// Fourth call: nothing left to load
	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return false when nothing left to load")
	}
}

// TestLazyLoad_NoExpandWhenNotAtTop verifies that the window does not expand
// when the user is not scrolled to the very top.
func TestLazyLoad_NoExpandWhenNotAtTop(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:lazy-c", 160) // 320 messages
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected initial renderStartIdx=120, got %d", m.renderStartIdx)
	}

	m.viewport.YOffset = 5
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return false when not at top")
	}
	if m.renderStartIdx != 120 {
		t.Fatalf("expected renderStartIdx to remain 120, got %d", m.renderStartIdx)
	}
}

// TestLazyLoad_SessionSwitchResets verifies that switching sessions resets the
// render window to the default for the new session, and switching back restores
// the previous default.
func TestLazyLoad_SessionSwitchResets(t *testing.T) {
	m := newTestModel(t)
	keyA := "tui:chat:lazy-d"
	seedLazySession(t, m, keyA, 160) // 320 messages
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected initial renderStartIdx=120 for A, got %d", m.renderStartIdx)
	}

	// Expand once so the window is no longer at the default.
	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected expansion to succeed before switching")
	}
	if m.renderStartIdx != 70 {
		t.Fatalf("expected renderStartIdx=70 after expand, got %d", m.renderStartIdx)
	}

	// Create and switch to a small session B.
	keyB := "tui:chat:lazy-e"
	m.sessionMgr.GetOrCreate(keyB)
	_ = m.sessionMgr.SetMode(keyB, "agent")
	m.sessionMgr.AddMessage(keyB, "user", "hi")
	m.sessionMgr.AddMessage(keyB, "assistant", "hello")
	m.currentKey = keyB
	m.forceGotoBottom = true
	m.updateViewport()

	if m.renderWindowSessionKey != keyB {
		t.Fatalf("expected renderWindowSessionKey=%s, got %s", keyB, m.renderWindowSessionKey)
	}
	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0 for small session B, got %d", m.renderStartIdx)
	}

	// Switch back to A — the render window should reset to A's default.
	m.currentKey = keyA
	m.forceGotoBottom = true
	m.updateViewport()

	if m.renderWindowSessionKey != keyA {
		t.Fatalf("expected renderWindowSessionKey=%s after switching back, got %s", keyA, m.renderWindowSessionKey)
	}
	if m.renderStartIdx != 120 {
		t.Fatalf("expected renderStartIdx=120 back for A, got %d", m.renderStartIdx)
	}
}

// newEvictionTestModel builds a TUI model like newTestModel but attaches a real
// SQLite store to the session manager so eviction and lazy-load of evicted
// messages are enabled.
func newEvictionTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	s, err := store.Open(filepath.Join(t.TempDir(), "tui-evict.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	m.sessionMgr.SetStore(s)
	return m
}

// seedEvictionSession seeds a session with the given number of user+assistant
// pairs, excludes the older prefix from context (keeping `keep` messages), and
// evicts the excluded messages from memory. The excluded prefix remains in
// SQLite and can be lazy-loaded. Returns the number of messages evicted.
func seedEvictionSession(t *testing.T, m *Model, key string, pairs, keep int) int {
	t.Helper()

	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < pairs; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Evict question %d?", i))
		m.sessionMgr.AddMessage(key, "assistant", fmt.Sprintf("Evict answer %d.", i))
	}

	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	m.sessionMgr.ExcludeOldMessagesFromContext(key, keep)
	if err := m.sessionMgr.Save(key); err != nil {
		t.Fatalf("exclude Save: %v", err)
	}

	evicted := m.sessionMgr.EvictExcludedMessages(key)
	if evicted <= 0 {
		t.Fatalf("EvictExcludedMessages = %d, want > 0", evicted)
	}
	if got := m.sessionMgr.GetEvictedMessageCount(key); got != evicted {
		t.Fatalf("GetEvictedMessageCount after evict = %d, want %d", got, evicted)
	}
	return evicted
}

// TestLazyLoad_EvictedMessagesNotLoadedInMemory verifies that when a session
// has evicted messages (e.g. 19 evicted, 5 kept in context in memory), the TUI
// only loads and renders the in-context messages. Scrolling to top does NOT
// cross the eviction boundary, does NOT load evicted messages into RAM, and
// does NOT show an 'earlier messages' header when all in-context messages are visible.
func TestLazyLoad_EvictedMessagesNotLoadedInMemory(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:evict-a"

	// 12 pairs = 24 messages; keep 5 → 19 excluded/evicted.
	evicted := seedEvictionSession(t, m, key, 12, 5)
	if evicted != 19 {
		t.Fatalf("expected 19 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	renderLazyModel(t, m)

	// Few in-memory messages (5) are below the render cap, so the window starts at 0.
	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0, got %d", m.renderStartIdx)
	}
	if got := m.sessionMgr.GetEvictedMessageCount(key); got != evicted {
		t.Fatalf("GetEvictedMessageCount before scroll = %d, want %d", got, evicted)
	}
	if inMemCount := len(m.sessionMgr.GetHistory(key)); inMemCount != 5 {
		t.Fatalf("in-memory message count before scroll = %d, want 5", inMemCount)
	}

	// Scrolling to the top does not trigger lazy-loading of evicted messages into memory.
	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return false (no in-memory expansion needed)")
	}

	// Evicted messages remain evicted in SQLite and are not inflated into memory.
	if got := m.sessionMgr.GetEvictedMessageCount(key); got != evicted {
		t.Fatalf("GetEvictedMessageCount after scroll = %d, want %d", got, evicted)
	}
	if inMemCount := len(m.sessionMgr.GetHistory(key)); inMemCount != 5 {
		t.Fatalf("in-memory message count after scroll = %d, want 5", inMemCount)
	}

	// Render view should NOT show an earlier messages header because all in-context messages are rendered.
	out := m.View()
	if strings.Contains(out, "earlier messages") {
		t.Fatalf("expected view not to contain earlier messages header for evicted messages, got:\n%s", out)
	}
}
