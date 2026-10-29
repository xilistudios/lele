package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// repaintMsgFrom pulls a WindowSizeMsg out of a command (unwrapping a batch).
// A WindowSizeMsg is what makes the standard renderer drop its per-row cache
// and repaint every line, which is how the model asks for a full repaint.
func repaintMsgFrom(cmd tea.Cmd) (tea.WindowSizeMsg, bool) {
	if cmd == nil {
		return tea.WindowSizeMsg{}, false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if ws, ok := repaintMsgFrom(sub); ok {
				return ws, true
			}
		}
		return tea.WindowSizeMsg{}, false
	}
	ws, ok := msg.(tea.WindowSizeMsg)
	return ws, ok
}

// stableFrames runs n frames with the layout unchanged and fails if any of them
// asks for a repaint: the hook must not turn into a repaint on every frame.
func stableFrames(t *testing.T, m **Model, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		(*m).View()
		updated, cmd := (*m).Update(tickMsg{})
		*m = updated.(*Model)
		if _, ok := repaintMsgFrom(cmd); ok {
			t.Fatalf("stable layout requested a repaint on frame %d", i)
		}
	}
}

// A layout change is exactly when the bottom block moves to another row, which
// is when a stale row can survive on screen. The model must ask for one full
// repaint then — and must not keep asking while the layout is stable.
func TestLayoutChangeForcesSingleFullRepaint(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newTestModel(t)
	key := "tui:chat:repaint"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false
	m.sessionMgr.AddMessage(key, "user", "hi")
	m.sessionMgr.AddMessage(key, "assistant", "hello")

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)
	m.showWelcome = false

	// First frame: the model asks for a full repaint once, at the current size.
	m.View()
	updated, cmd := m.Update(tickMsg{})
	m = updated.(*Model)
	if ws, ok := repaintMsgFrom(cmd); ok {
		if ws.Width != 120 || ws.Height != 36 {
			t.Fatalf("repaint asked for %dx%d, want the current size 120x36", ws.Width, ws.Height)
		}
	} else {
		t.Fatal("the first frame did not request a full repaint")
	}

	// Same layout, more messages: no repaint.
	stableFrames(t, &m, 3)

	// The autocomplete popup changes the layout by several rows at once: the
	// status line, the input bar and the bottom bar all move up.
	m.chatInput.SetValue("/s")
	m.showAutocomplete = true
	m.filterAutocomplete("/s")
	m.View()
	updated, cmd = m.Update(tickMsg{})
	m = updated.(*Model)
	if _, ok := repaintMsgFrom(cmd); !ok {
		t.Fatal("autocomplete opening did not request a full repaint")
	}

	// Settled again with the popup open: no further repaints.
	stableFrames(t, &m, 3)

	// Closing the popup moves the bottom block back down: a layout change too.
	m.chatInput.SetValue("")
	m.showAutocomplete = false
	m.autocompleteItems = nil
	m.View()
	updated, cmd = m.Update(tickMsg{})
	m = updated.(*Model)
	if _, ok := repaintMsgFrom(cmd); !ok {
		t.Fatal("autocomplete closing did not request a full repaint")
	}
	stableFrames(t, &m, 3)
}

// A resize must always end with a full repaint, otherwise the rows the new
// frame does not touch keep the previous width's content.
func TestResizeForcesFullRepaint(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newTestModel(t)
	key := "tui:chat:repaint-resize"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)
	m.showWelcome = false
	m.View()
	updated, _ = m.Update(tickMsg{})
	m = updated.(*Model)

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)
	m.View()
	updated, cmd := m.Update(tickMsg{})
	m = updated.(*Model)
	ws, ok := repaintMsgFrom(cmd)
	if !ok {
		t.Fatal("resize did not request a full repaint")
	}
	if ws.Width != 100 || ws.Height != 30 {
		t.Fatalf("repaint asked for %dx%d, want the new size 100x30", ws.Width, ws.Height)
	}
	stableFrames(t, &m, 2)
}

// Going back to the welcome screen changes the whole layout: the viewport band
// disappears. It must repaint too.
func TestWelcomeSwitchForcesFullRepaint(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newTestModel(t)
	key := "tui:chat:repaint-welcome"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)
	m.showWelcome = false
	m.View()
	updated, _ = m.Update(tickMsg{})
	m = updated.(*Model)
	stableFrames(t, &m, 1)

	m.showWelcome = true
	m.View()
	updated, cmd := m.Update(tickMsg{})
	m = updated.(*Model)
	if _, ok := repaintMsgFrom(cmd); !ok {
		t.Fatal("switching to the welcome screen did not request a full repaint")
	}
}
