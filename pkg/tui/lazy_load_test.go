package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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

func renderLazyModel(t *testing.T, m *Model) {
	t.Helper()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m2 := updated.(*Model)
	*m = *m2
	m.View()
}

func TestLazyLoad_InitialWindow(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:lazy-a", 160)
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected renderStartIdx=120, got %d", m.renderStartIdx)
	}

	m.viewport.GotoTop()
	out := m.View()
	want := fmt.Sprintf(i18n.T("tui.earlierMessages"), 120)
	if !strings.Contains(out, want) {
		t.Fatalf("expected header %q in view, got:\n%s", want, out)
	}
}

func TestLazyLoad_ExpandOnScrollUp(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:lazy-b", 160)
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected initial renderStartIdx=120, got %d", m.renderStartIdx)
	}

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
	want70 := fmt.Sprintf(i18n.T("tui.earlierMessages"), 70)
	if out := m.View(); !strings.Contains(out, want70) {
		t.Fatalf("expected header %q, got:\n%s", want70, out)
	}

	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return true on second expansion")
	}
	if m.renderStartIdx != 20 {
		t.Fatalf("expected renderStartIdx=20 after second expand, got %d", m.renderStartIdx)
	}

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

	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return false when nothing left to load")
	}
}

func TestLazyLoad_NoExpandWhenNotAtTop(t *testing.T) {
	m := newTestModel(t)
	seedLazySession(t, m, "tui:chat:lazy-c", 160)
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

func TestLazyLoad_SessionSwitchResets(t *testing.T) {
	m := newTestModel(t)
	keyA := "tui:chat:lazy-d"
	seedLazySession(t, m, keyA, 160)
	renderLazyModel(t, m)

	if m.renderStartIdx != 120 {
		t.Fatalf("expected initial renderStartIdx=120 for A, got %d", m.renderStartIdx)
	}

	m.viewport.GotoTop()
	if !m.maybeExpandRenderWindow() {
		t.Fatal("expected expansion to succeed before switching")
	}
	if m.renderStartIdx != 70 {
		t.Fatalf("expected renderStartIdx=70 after expand, got %d", m.renderStartIdx)
	}

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

func newEvictionTestModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	s, err := store.Open(filepath.Join(t.TempDir(), "tui-evict.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	m.sessionMgr.SetSessionRepo(s.Sessions())
	return m
}

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

func TestLazyLoad_EvictedMessagesStayOutOfMemory(t *testing.T) {
	m := newEvictionTestModel(t)
	const key = "tui:chat:evict-a"

	evicted := seedEvictionSession(t, m, key, 12, 5)
	if evicted != 19 {
		t.Fatalf("expected 19 evicted, got %d", evicted)
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	renderLazyModel(t, m)

	if m.renderStartIdx != 0 {
		t.Fatalf("expected renderStartIdx=0, got %d", m.renderStartIdx)
	}
	if got := m.sessionMgr.GetEvictedMessageCount(key); got != evicted {
		t.Fatalf("GetEvictedMessageCount before scroll = %d, want %d", got, evicted)
	}
	if inMemCount := len(m.sessionMgr.GetHistory(key)); inMemCount != 5 {
		t.Fatalf("in-memory message count before scroll = %d, want 5", inMemCount)
	}

	m.viewport.GotoTop()
	if m.maybeExpandRenderWindow() {
		t.Fatal("expected maybeExpandRenderWindow to return false (no archived prefix loaded)")
	}

	if got := m.sessionMgr.GetEvictedMessageCount(key); got != evicted {
		t.Fatalf("GetEvictedMessageCount after scroll = %d, want %d", got, evicted)
	}
	if inMemCount := len(m.sessionMgr.GetHistory(key)); inMemCount != 5 {
		t.Fatalf("in-memory message count after scroll = %d, want 5", inMemCount)
	}

	outBefore := m.View()
	if strings.Contains(outBefore, "earlier messages") {
		t.Fatalf("expected view not to contain earlier messages header without archive, got:\n%s", outBefore)
	}

	m.refreshArchivedHistory()
	renderLazyModel(t, m)
	m.viewport.GotoTop()
	outAfter := m.View()
	if !strings.Contains(outAfter, "Evict question 0?") {
		t.Fatalf("expected view to contain evicted message 'Evict question 0?' after refresh, got:\n%s", outAfter)
	}
	if strings.Contains(outAfter, "earlier messages") {
		t.Fatalf("expected no earlier messages header when all loaded, got:\n%s", outAfter)
	}
}
