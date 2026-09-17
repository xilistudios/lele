package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/channels"
)

// seedSubagentSidebarModel opens a chat whose sidebar shows n subagent rows.
func seedSubagentSidebarModel(t *testing.T, n int) *Model {
	t.Helper()

	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:subagent-click-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hi")
	m.sessionMgr.AddMessage(key, "assistant", "hello")
	m.currentKey = key
	m.showWelcome = false

	var subagents []channels.SubagentTaskInfo
	for i := 0; i < n; i++ {
		subagents = append(subagents, channels.SubagentTaskInfo{
			TaskID:     fmt.Sprintf("task-%d", i),
			Label:      fmt.Sprintf("SubClick%d", i),
			Status:     "running",
			SessionKey: fmt.Sprintf("subagent-key-%d", i),
		})
	}
	m.subagentsCacheKey = "native:" + key
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = subagents

	return m
}

// TestSidebarSubagentClickTargetsAlignWithRenderedRows is the regression guard
// for the sidebar offset bug: the click target Y-coordinates used to be one
// row below the rendered subagent item, so clicking an item selected the row
// underneath (and the last item needed a click below the panel).
//
// It renders the sidebar, locates each "SubClickN" item row, and asserts the
// target recorded for that subagent starts exactly on that row.
func TestSidebarSubagentClickTargetsAlignWithRenderedRows(t *testing.T) {
	const n = 3
	m := seedSubagentSidebarModel(t, n)

	out := m.View()
	lines := strings.Split(out, "\n")

	// rowForLabel returns the rendered row index of the subagent label, or -1.
	rowForLabel := func(label string) int {
		for i, line := range lines {
			if strings.Contains(ansi.Strip(line), label) {
				return i
			}
		}
		return -1
	}

	if len(m.subagentClickTargets) != n {
		t.Fatalf("expected %d click targets, got %d", n, len(m.subagentClickTargets))
	}

	// Each target's yStart must match the rendered row of its own item, and the
	// target must span exactly one row.
	for _, target := range m.subagentClickTargets {
		if target.yEnd != target.yStart+1 {
			t.Errorf("target %s spans %d..%d, expected a single row",
				target.key, target.yStart, target.yEnd)
			continue
		}
		// target.key == "subagent-key-<i>" → label "SubClick<i>"
		idx := strings.TrimPrefix(target.key, "subagent-key-")
		label := "SubClick" + idx
		row := rowForLabel(label)
		if row < 0 {
			t.Fatalf("subagent %q was not rendered in the sidebar", label)
		}
		if target.yStart != row {
			t.Errorf("click target for %q starts at y=%d but the row is rendered at y=%d (offset %+d)",
				label, target.yStart, row, target.yStart-row)
		}
	}
}

// TestSidebarSubagentClickSelectsClickedItem is an end-to-end check: a mouse
// press on an item's rendered row must switch the current chat to that
// subagent's session key.
func TestSidebarSubagentClickSelectsClickedItem(t *testing.T) {
	const n = 3
	m := seedSubagentSidebarModel(t, n)

	out := m.View()
	lines := strings.Split(out, "\n")

	// Find the rendered row of the middle subagent.
	row := -1
	for i, line := range lines {
		if strings.Contains(ansi.Strip(line), "SubClick1") {
			row = i
			break
		}
	}
	if row < 0 {
		t.Fatal("SubClick1 not rendered in the sidebar")
	}

	// Derive the sidebar start X the same way handlers_mouse.go does.
	leftWidth := int(float64(m.width) * leftColumnRatio)
	sidebarStartX := leftWidth + chatSidebarGutter

	updated, _ := m.Update(tea.MouseMsg{
		X:      sidebarStartX + 2,
		Y:      row,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(*Model)

	if m.currentKey != "subagent-key-1" {
		t.Fatalf("clicking the row of SubClick1 selected %q, want %q",
			m.currentKey, "subagent-key-1")
	}
	if m.parentSessionKey == "" {
		t.Fatal("parentSessionKey must be recorded when switching into a subagent")
	}
}
