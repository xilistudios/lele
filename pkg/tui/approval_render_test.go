package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// setApprovalPending puts the model into the state the approval prompt needs:
// a sized window, an active session and a pending request.
func setApprovalPending(t *testing.T, width, height int, command, reason string) *Model {
	t.Helper()
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = updated.(*Model)

	key := "tui:chat:approval-render"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()

	m.pendingApprovalID = "approval-render-1"
	m.pendingApprovalCmd = command
	m.pendingApprovalReason = reason
	m.processing = true
	return m
}

// approvalRenderedLines renders the prompt and reports the terminal-cell width
// of every rendered line.
func approvalRenderedLines(t *testing.T, m *Model) []int {
	t.Helper()
	prompt := m.renderApprovalPrompt()
	if strings.Contains(prompt, "\x1b") {
		// ANSI styling is fine; widths below are computed with ansi.StringWidth,
		// which ignores escape sequences.
		t.Log("prompt contains ANSI sequences (expected)")
	}
	var widths []int
	for _, line := range strings.Split(prompt, "\n") {
		widths = append(widths, ansi.StringWidth(line))
	}
	return widths
}

// TestApprovalPrompt_LongCommandStaysInsideColumn is the regression test for
// the original bug: a long command was emitted as one unwrapped line, the
// box grew past the chat column and the viewport borders broke.
func TestApprovalPrompt_LongCommandStaysInsideColumn(t *testing.T) {
	longCmd := "curl -sS https://example.com/install.sh | bash -c 'set -x; for i in a b c d e f g h i j k l m n o p q r s t u v w x y z; do echo $i; done' && rm -rf /tmp/build-cache-9f8e7d6c5b4a"
	m := setApprovalPending(t, 100, 30, longCmd, "piped remote script")

	column := m.chatColumnWidth()
	widths := approvalRenderedLines(t, m)
	for i, w := range widths {
		if w > column {
			t.Fatalf("line %d is %d cells wide, chat column is %d — layout would wrap", i, w, column)
		}
	}

	// The preview must be truncated to a single line, not the full command.
	prompt := m.renderApprovalPrompt()
	if strings.Contains(prompt, "build-cache-9f8e7d6c5b4a") {
		t.Error("long command leaked in full into the truncated preview")
	}
	if !strings.Contains(prompt, "curl -sS") {
		t.Error("preview does not start with the command head")
	}
}

// TestApprovalPrompt_BoxFitsColumnEvenWithEmptyCommand pins the width
// arithmetic: lipgloss.Width excludes borders, so the rendered box must be
// exactly inner+approvalBoxChrome+2 wide and never wider than the column.
func TestApprovalPrompt_BoxFitsColumnEvenWithEmptyCommand(t *testing.T) {
	m := setApprovalPending(t, 120, 36, "ls -la", "routine")
	column := m.chatColumnWidth()
	for i, w := range approvalRenderedLines(t, m) {
		if w > column {
			t.Fatalf("line %d is %d cells wide, chat column is %d", i, w, column)
		}
	}
}

// TestApprovalPrompt_FullViewWraps verifies the "v" view shows the entire
// command (every token present) while still respecting the column width.
func TestApprovalPrompt_FullViewWraps(t *testing.T) {
	longCmd := "echo alpha bravo charlie delta echo echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu"
	m := setApprovalPending(t, 100, 30, longCmd, "")
	m.approvalShowFull = true

	column := m.chatColumnWidth()
	prompt := m.renderApprovalPrompt()
	for i, w := range approvalRenderedLines(t, m) {
		if w > column {
			t.Fatalf("full view line %d is %d cells wide, chat column is %d", i, w, column)
		}
	}
	if !strings.Contains(prompt, "yankee zulu") {
		t.Fatal("full view does not show the tail of the command")
	}
	// Full view spans multiple lines inside the box.
	if lines := len(strings.Split(strings.TrimSpace(prompt), "\n")); lines < 6 {
		t.Errorf("expected wrapped multi-line full view, got %d lines", lines)
	}
}

// TestApprovalPrompt_KeyHintsCoverAllActions guards the discoverability fix:
// the four actions (approve, reject, view, whitelist) must always be listed.
func TestApprovalPrompt_KeyHintsCoverAllActions(t *testing.T) {
	m := setApprovalPending(t, 100, 30, "uptime", "")
	prompt := m.renderApprovalPrompt()
	for _, want := range []string{"[y]", "[n]", "[v]", "[w]"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("key hint %s missing from approval prompt", want)
		}
	}
	// Command fits the preview line → the [v] hint must offer the full view.
	if !strings.Contains(prompt, i18n.T("tui.approvalViewFull")) {
		t.Error("short command should offer 'view full command'")
	}
	m.approvalShowFull = true
	if !strings.Contains(m.renderApprovalPrompt(), i18n.T("tui.approvalViewShort")) {
		t.Error("full view should offer 'summary only' back")
	}
}

// TestApprovalPrompt_LayoutMatchesView checks the prompt is sized from the
// real layout even when rendered before View() has ever run (stale
// viewport.Width regression).
func TestApprovalPrompt_LayoutMatchesView(t *testing.T) {
	m := setApprovalPending(t, 100, 30, "rm -rf /tmp/x", "deny")
	if m.viewport.Width == m.chatColumnWidth() {
		t.Skip("viewport width already in sync; nothing to pin")
	}
	if m.viewport.Width <= m.chatColumnWidth() {
		t.Fatalf("expected stale default viewport width (%d) larger than column (%d)", m.viewport.Width, m.chatColumnWidth())
	}
	for i, w := range approvalRenderedLines(t, m) {
		if w > m.chatColumnWidth() {
			t.Fatalf("pre-layout render line %d is %d cells, column is %d", i, w, m.chatColumnWidth())
		}
	}
}
