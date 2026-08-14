package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

// TestApprovalRequest_ShowsPromptInViewport simulates the agent publishing an
// approval.request outbound event and verifies the TUI displays the inline
// approval prompt with the command and reason.
func TestApprovalRequest_ShowsPromptInViewport(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	// Create a session and switch to it
	key := "tui:chat:approval-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.viewport.Width = 118
	m.updateViewport()

	// Publish an approval.request outbound event (same as executeWithApproval)
	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native",
		ChatID:  key,
		Event:   "approval.request",
		Metadata: map[string]string{
			"id":      "approval-123",
			"command": "rm -rf /tmp/foo",
			"reason":  "command matches deny pattern",
		},
	})

	// Deliver the outbound message to the model
	got := false
	for i := 0; i < 5; i++ {
		// The outbound listener is a tea.Cmd — run it to get the message
		cmd := m.startOutboundListener()
		if cmd == nil {
			t.Fatal("outbound listener returned nil cmd")
		}
		msg := cmd()
		if msg == nil {
			t.Fatal("outbound listener returned nil msg")
		}
		om, ok := msg.(outboundMsg)
		if !ok {
			t.Fatalf("expected outboundMsg, got %T", msg)
		}
		m2, _ := m.Update(om)
		m = m2.(*Model)
		if m.pendingApprovalID != "" {
			got = true
			break
		}
	}
	if !got {
		t.Fatal("BUG: TUI did not receive approval.request event")
	}

	if m.pendingApprovalID != "approval-123" {
		t.Fatalf("pendingApprovalID = %q, want approval-123", m.pendingApprovalID)
	}
	if m.pendingApprovalCmd != "rm -rf /tmp/foo" {
		t.Fatalf("pendingApprovalCmd = %q, want rm -rf /tmp/foo", m.pendingApprovalCmd)
	}

	// Verify the approval prompt is rendered in the viewport overlay
	m.updateViewport()
	overlay := m.viewport.overlayLines
	found := false
	for _, line := range overlay {
		if contains(line, "rm -rf /tmp/foo") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("BUG: approval prompt not rendered in viewport overlay. overlay=%v", overlay)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestApproval_KeyHandling verifies y/n keys approve/reject the pending approval.
func TestApproval_KeyHandling(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:approval-keys"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.viewport.Width = 118

	// Manually set the pending approval state (as if approval.request arrived)
	m.pendingApprovalID = "approval-456"
	m.pendingApprovalCmd = "sudo rm -rf /"
	m.pendingApprovalReason = "dangerous"
	m.updateViewport()

	// Press 'y' to approve
	m2 := sendKeys(m, "y")
	if m2.pendingApprovalID != "" {
		t.Fatalf("pendingApprovalID not cleared after y: %q", m2.pendingApprovalID)
	}
	if m2.approvalResult == "" {
		t.Fatal("approvalResult not set after y")
	}

	// Press 'n' to reject
	m.pendingApprovalID = "approval-789"
	m.pendingApprovalCmd = "rm -rf /"
	m.pendingApprovalReason = "dangerous"
	m.updateViewport()

	m3 := sendKeys(m, "n")
	if m3.pendingApprovalID != "" {
		t.Fatalf("pendingApprovalID not cleared after n: %q", m3.pendingApprovalID)
	}
	if m3.approvalResult == "" {
		t.Fatal("approvalResult not set after n")
	}
}
