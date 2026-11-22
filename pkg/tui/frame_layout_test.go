package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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

	stableFrames(t, &m, 3)

	m.chatInput.SetValue("/s")
	m.showAutocomplete = true
	m.filterAutocomplete("/s")
	m.View()
	updated, cmd = m.Update(tickMsg{})
	m = updated.(*Model)
	if _, ok := repaintMsgFrom(cmd); !ok {
		t.Fatal("autocomplete opening did not request a full repaint")
	}

	stableFrames(t, &m, 3)

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

func TestSameSizeWindowMessageDoesNotRebuildTheViewport(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newTestModel(t)
	key := "tui:chat:repaint-same-size"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false
	for i := 0; i < 40; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("question %d", i))
		m.sessionMgr.AddMessage(key, "assistant", fmt.Sprintf("answer %d with enough words to wrap at any width", i))
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)
	m.View()
	if !m.renderedBaseValid {
		t.Fatal("the first frame did not materialize the viewport")
	}

	rebuilds := 0
	m.onViewportRebuild = func(bool) { rebuilds++ }

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)
	if rebuilds != 0 {
		t.Errorf("a same-size message rebuilt the viewport %d times", rebuilds)
	}
	if !m.renderedBaseValid {
		t.Error("a same-size message invalidated the rendered base")
	}

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)
	if rebuilds == 0 {
		t.Error("a resize did not rebuild the viewport")
	}
}
