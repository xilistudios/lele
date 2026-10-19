package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// seedSubagentViewWithParent points the model at a subagent session whose
// parent chat is remembered in parentSessionKey — the state in which the
// status line shows the "back to parent chat" affordance.
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

// renderedBackToParentRow returns the rendered row index of the "back to
// parent chat" text inside the current frame, or fails the test.
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

// TestBackToParentClickTargetAlignsWithRenderedText is the regression guard for
// the click-target geometry: the tracked row must be exactly the row where the
// "back to parent chat" text is rendered (idle variant), not its style margin
// or a neighbouring band.
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

// TestBackToParentClickTargetAlignsWhileProcessing repeats the geometry check
// on the processing variant of the status line (spinner + hint + affordance):
// the extra leading text must not shift the tracked row.
func TestBackToParentClickTargetAlignsWhileProcessing(t *testing.T) {
	m, _, _ := seedSubagentViewWithParent(t)

	// Force the processing branch of renderStatusLine.
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

// TestBackToParentClickReturnsToParent is the end-to-end check: a mouse press
// on the rendered "back to parent chat" row must switch back to the parent
// chat and clear the parent marker — the same route ctrl+b takes.
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

// TestBackToParentClickIgnoredOutsideRow guards the hit area: a click on any
// other row of the left pane must not navigate away from the subagent view.
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

// TestBackToParentClickTargetAbsentWithoutParent: when no parent is remembered
// (plain chat view) the affordance is not on screen and no click target may be
// registered.
func TestBackToParentClickTargetAbsentWithoutParent(t *testing.T) {
	m, _, _ := seedSubagentViewWithParent(t)
	m.parentSessionKey = ""

	_ = m.View()

	if m.hasBackToParentClick {
		t.Fatal("back-to-parent click target must be absent when parentSessionKey is empty")
	}
}
