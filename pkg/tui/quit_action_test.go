package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func runsToQuit(t *testing.T, cmd tea.Cmd) bool {
	t.Helper()
	if cmd == nil {
		return false
	}

	msgs := make(chan tea.Msg, 1)
	go func() { msgs <- cmd() }()

	var msg tea.Msg
	select {
	case msg = <-msgs:
	case <-time.After(2 * time.Second):
		t.Fatal("the quit command blocked instead of resolving")
	}

	switch m := msg.(type) {
	case tea.QuitMsg:
		return true
	case tea.BatchMsg:
		for _, nested := range m {
			if runsToQuit(t, nested) {
				return true
			}
		}
	}
	return false
}

func TestQuitActionsAskTheProgramToQuit(t *testing.T) {
	t.Run("ctrl+c", func(t *testing.T) {
		m := newTestModel(t)
		t.Cleanup(m.Close)

		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		if !runsToQuit(t, cmd) {
			t.Fatal("ctrl+c did not produce tea.Quit: the TUI would never exit")
		}
	})

	t.Run("/quit", func(t *testing.T) {
		m := newTestModel(t)
		t.Cleanup(m.Close)

		cmd := m.executeCommand("/quit")
		if !runsToQuit(t, cmd) {
			t.Fatal("/quit did not produce tea.Quit: the TUI would never exit")
		}
	})
}
