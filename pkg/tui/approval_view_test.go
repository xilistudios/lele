package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestApproval_ViewRenders verifies the approval prompt appears in View()
// output when an approval is pending.
func TestApproval_ViewRenders(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)

	key := "tui:chat:approval-view"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()

	// Set pending approval state
	m.pendingApprovalID = "approval-view-1"
	m.pendingApprovalCmd = "rm -rf /tmp/test"
	m.pendingApprovalReason = "deny pattern"
	m.processing = true

	// Render the view
	view := m.View()

	// Check the approval text is visible
	if !strings.Contains(view, "rm -rf /tmp/test") {
		t.Fatalf("BUG: approval command not visible in View(). view:\n%s", view)
	}
	if !strings.Contains(view, "Aprobar") && !strings.Contains(view, "Approve") {
		t.Fatalf("BUG: approval action hint not visible in View(). view:\n%s", view)
	}
}

// TestApproval_ViewRendersWide verifies the approval renders in a wider terminal.
func TestApproval_ViewRendersWide(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 45})
	m = updated.(*Model)

	key := "tui:chat:approval-view-wide"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()

	m.pendingApprovalID = "approval-view-2"
	m.pendingApprovalCmd = "sudo systemctl stop nginx && rm -rf /var/www/html"
	m.pendingApprovalReason = "dangerous command"
	m.processing = true

	view := m.View()

	if !strings.Contains(view, "sudo systemctl stop nginx") {
		t.Fatalf("BUG: approval command not visible in View(). view:\n%s", view)
	}
}

// TestApproval_ViewSanitizesControlChars verifies that terminal control
// characters and ANSI escapes in the pending approval command are stripped
// from the rendered output (terminal-injection guard).
func TestApproval_ViewSanitizesControlChars(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)

	key := "tui:chat:approval-sanitize"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()

	m.pendingApprovalID = "approval-sanitize-1"
	m.pendingApprovalCmd = "echo \x1b[2J\rhi"
	m.pendingApprovalReason = "\x1b[3mdanger\x1b[0m \rreason"
	m.processing = true

	view := m.View()

	if strings.Contains(view, "\x1b") {
		t.Fatalf("BUG: raw ESC (\x1b) leaked into view. view:\n%s", view)
	}
	if strings.Contains(view, "\r") {
		t.Fatalf("BUG: carriage return leaked into view. view:\n%s", view)
	}
	if !strings.Contains(view, "hi") {
		t.Fatalf("BUG: sanitized command text missing from view. view:\n%s", view)
	}
	if !strings.Contains(view, "danger") {
		t.Fatalf("BUG: sanitized reason text missing from view. view:\n%s", view)
	}
}
