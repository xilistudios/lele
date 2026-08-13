package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// seedScrollSession creates a session with enough history to overflow the
// viewport and opens it in the model.
func seedScrollSession(t *testing.T, m *Model) {
	t.Helper()

	key := "tui:chat:scroll-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < 30; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question number %d?", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d.\nLine two of answer %d.\nLine three of answer %d.\nLine four of answer %d.", i, i, i, i))
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
}

// renderLines returns the rendered view split into lines.
func renderLines(m *Model) []string {
	return strings.Split(m.View(), "\n")
}

// TestScroll_InputFieldRemainsVisible verifies that after scrolling up with
// the mouse wheel, the input field (placeholder or prompt) is still rendered
// and the total layout does not exceed the terminal height.
func TestScroll_InputFieldRemainsVisible(t *testing.T) {
	m := newTestModel(t)

	// Simulate initial window size
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	seedScrollSession(t, m)

	placeholder := i18n.T("tui.placeholder")

	// Initial render: input must be visible.
	out := m.View()
	if !strings.Contains(out, placeholder) {
		t.Fatalf("initial render missing input placeholder.\nview lines=%d", len(renderLines(m)))
	}
	if got := len(renderLines(m)); got > m.height {
		t.Fatalf("initial render exceeds terminal height: %d lines > %d", got, m.height)
	}

	// Scroll up with the mouse wheel (several notches).
	for i := 0; i < 10; i++ {
		updated, _ := m.Update(tea.MouseMsg{
			X:      10,
			Y:      5,
			Action: tea.MouseActionPress,
			Button: tea.MouseButtonWheelUp,
		})
		m = updated.(*Model)
	}

	if m.viewport.AtBottom() {
		t.Fatal("viewport should have scrolled up after wheel-up events")
	}

	out = m.View()
	lines := renderLines(m)

	if !strings.Contains(out, placeholder) {
		t.Fatalf("BUG REPRODUCED: input placeholder lost after scrolling up.\nview has %d lines (height=%d), viewport YOffset=%d\nlast 5 lines:\n%s",
			len(lines), m.height, m.viewport.YOffset, strings.Join(lines[max(0, len(lines)-5):], "\n"))
	}
	if len(lines) > m.height {
		t.Fatalf("render after scroll exceeds terminal height: %d lines > %d", len(lines), m.height)
	}
}

// TestScroll_KeyboardScrollKeepsInput verifies pgup/home scrolling keeps the
// input field visible.
func TestScroll_KeyboardScrollKeepsInput(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	seedScrollSession(t, m)

	placeholder := i18n.T("tui.placeholder")

	// Send proper key messages for scrolling.
	for _, kt := range []tea.KeyType{tea.KeyPgUp, tea.KeyHome, tea.KeyUp} {
		updated, _ := m.Update(tea.KeyMsg{Type: kt})
		m = updated.(*Model)
	}

	out := m.View()
	if !strings.Contains(out, placeholder) {
		t.Fatalf("BUG: input placeholder lost after keyboard scroll. YOffset=%d lines=%d",
			m.viewport.YOffset, len(renderLines(m)))
	}
}

// TestScroll_WithSubagentsAndToolCalls verifies that scrolling in a session
// with active subagents and tool calls never drops the input or exceeds terminal height.
func TestScroll_WithSubagentsAndToolCalls(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	m = updated.(*Model)

	key := "tui:chat:subagents-scroll-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < 20; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Execute step %d with tool command", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Running tool for step %d:\nexec: command=cd /mnt/hdd1/Development/xilistudios/lele && echo 'testing step %d'\nOutput for step %d was generated successfully.", i, i, i))
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true

	// Add 10 subagents
	var subagents []channels.SubagentTaskInfo
	for i := 0; i < 10; i++ {
		subagents = append(subagents, channels.SubagentTaskInfo{
			TaskID:     fmt.Sprintf("task-%d", i),
			Label:      fmt.Sprintf("Task %d with a very long descriptive label that could wrap", i),
			Status:     "completed",
			SessionKey: fmt.Sprintf("subagent-key-%d", i),
		})
	}
	m.subagentsCacheKey = "native:" + key
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = subagents

	m.reloadSessions()

	placeholder := i18n.T("tui.placeholder")
	placeholderPrefix := "Pregunta cualquier cosa"
	if !strings.Contains(placeholder, placeholderPrefix) {
		placeholderPrefix = placeholder[:min(20, len(placeholder))]
	}

	// Scroll up and down repeatedly using both mouse wheel and keys
	for i := 0; i < 15; i++ {
		updated, _ := m.Update(tea.MouseMsg{
			X:      10,
			Y:      5,
			Action: tea.MouseActionPress,
			Button: tea.MouseButtonWheelUp,
		})
		m = updated.(*Model)

		out := m.View()
		lines := strings.Split(out, "\n")
		if len(lines) > m.height {
			t.Fatalf("iteration %d: view exceeded height: %d lines > %d", i, len(lines), m.height)
		}
		if !strings.Contains(out, placeholderPrefix) {
			t.Fatalf("iteration %d: input placeholder lost during scroll up.\nlines=%d\nout:\n%s", i, len(lines), out)
		}
	}

	for i := 0; i < 15; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(*Model)

		out := m.View()
		lines := strings.Split(out, "\n")
		if len(lines) > m.height {
			t.Fatalf("iteration %d: view exceeded height: %d lines > %d", i, len(lines), m.height)
		}
		if !strings.Contains(out, placeholderPrefix) {
			t.Fatalf("iteration %d: input placeholder lost during scroll down", i)
		}
	}
}
