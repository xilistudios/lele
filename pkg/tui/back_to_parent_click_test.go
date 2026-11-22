package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func seedSubagentViewWithParent(t *testing.T) (m *Model, parentKey, childKey string) {
	t.Helper()

	m = newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	parentKey = "tui:chat:back-parent"
	childKey = "tui:chat:subagent-back-child"
	m.sessionMgr.GetOrCreate(parentKey)
	m.sessionMgr.GetOrCreate(childKey)
	m.sessionMgr.AddMessage(childKey, "user", "hi")
	m.sessionMgr.AddMessage(childKey, "assistant", "hello")
	m.currentKey = childKey
	m.parentSessionKey = parentKey
	m.showWelcome = false
	return m, parentKey, childKey
}

func renderedBackToParentRow(t *testing.T, m *Model) int {
	t.Helper()
	text := i18n.T("tui.backToParent")
	for i, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(ansi.Strip(line), text) {
			return i
		}
	}
	t.Fatalf("back-to-parent text %q not found in the rendered frame", text)
	return -1
}

func TestBackToParentClickTargetAlignsWithRenderedText(t *testing.T) {
	m, parentKey, _ := seedSubagentViewWithParent(t)

	row := renderedBackToParentRow(t, m)

	if !m.hasBackToParentClick {
		t.Fatal("expected a back-to-parent click target while parentSessionKey is set")
	}
	if m.backToParentClick.yStart != row || m.backToParentClick.yEnd != row+1 {
		t.Fatalf("click target spans y=%d..%d, want %d..%d",
			m.backToParentClick.yStart, m.backToParentClick.yEnd, row, row+1)
	}
	if m.backToParentClick.key != parentKey {
		t.Fatalf("click target key = %q, want %q", m.backToParentClick.key, parentKey)
	}
}

func TestBackToParentClickTargetAlignsWhileProcessing(t *testing.T) {
	m, _, _ := seedSubagentViewWithParent(t)

	m.processing = true
	m.startTime = time.Now()

	row := renderedBackToParentRow(t, m)

	if !m.hasBackToParentClick {
		t.Fatal("expected a back-to-parent click target while parentSessionKey is set")
	}
	if m.backToParentClick.yStart != row || m.backToParentClick.yEnd != row+1 {
		t.Fatalf("click target spans y=%d..%d, want %d..%d",
			m.backToParentClick.yStart, m.backToParentClick.yEnd, row, row+1)
	}
}

func TestBackToParentClickReturnsToParent(t *testing.T) {
	m, parentKey, _ := seedSubagentViewWithParent(t)

	row := renderedBackToParentRow(t, m)

	updated, _ := m.Update(tea.MouseMsg{
		X:      2,
		Y:      row,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(*Model)

	if m.currentKey != parentKey {
		t.Fatalf("clicking the back-to-parent row selected %q, want %q",
			m.currentKey, parentKey)
	}
	if m.parentSessionKey != "" {
		t.Fatalf("parentSessionKey = %q after going back, want it cleared", m.parentSessionKey)
	}
}

func TestBackToParentClickIgnoredOutsideRow(t *testing.T) {
	m, _, childKey := seedSubagentViewWithParent(t)

	row := renderedBackToParentRow(t, m)

	updated, _ := m.Update(tea.MouseMsg{
		X:      2,
		Y:      row + 3,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(*Model)

	if m.currentKey != childKey {
		t.Fatalf("a click outside the row switched to %q, want to stay on %q",
			m.currentKey, childKey)
	}
	if m.parentSessionKey == "" {
		t.Fatal("parentSessionKey must survive a click outside the back-to-parent row")
	}
}

func TestBackToParentClickTargetAbsentWithoutParent(t *testing.T) {
	m, _, _ := seedSubagentViewWithParent(t)
	m.parentSessionKey = ""

	_ = m.View()

	if m.hasBackToParentClick {
		t.Fatal("back-to-parent click target must be absent when parentSessionKey is empty")
	}
}
