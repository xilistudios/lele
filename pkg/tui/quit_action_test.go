package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// runsToQuit reports whether executing cmd (walking batches, which is how
// Update wraps a handler's command) ends in tea.Quit. The command is executed
// on a goroutine with a deadline: some batch members schedule work, and a
// regression that stops the quit from being batched must fail here instead of
// hanging the suite.
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

// TestQuitActionsAskTheProgramToQuit pins the user-facing half of the exit
// path: ctrl+c and /quit must resolve to tea.Quit, which is the only thing that
// makes the program return and hand control to the teardown (shutdownTUI in
// cmd/lele). Without these the terminal would never be given back at all, and
// the ordered, bounded teardown would never run.
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
