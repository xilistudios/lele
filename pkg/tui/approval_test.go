package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

func TestApprovalRequest_ShowsPromptInViewport(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:approval-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()
	m.viewport.Width = 118
	m.updateViewport()

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

	got := false
	for i := 0; i < 5; i++ {
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

	m.pendingApprovalID = "approval-456"
	m.pendingApprovalCmd = "sudo rm -rf /"
	m.pendingApprovalReason = "dangerous"
	m.updateViewport()

	m2 := sendKeys(m, "y")
	if m2.pendingApprovalID != "" {
		t.Fatalf("pendingApprovalID not cleared after y: %q", m2.pendingApprovalID)
	}
	if m2.approvalResult == "" {
		t.Fatal("approvalResult not set after y")
	}

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
