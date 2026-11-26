package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func deliverOutbound(t *testing.T, m *Model) outboundMsg {
	t.Helper()
	cmd := m.startOutboundListener()
	msg := cmd()
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("outbound listener returned %T, want outboundMsg", msg)
	}
	_, _ = m.Update(om)
	return om
}

func setupApprovalModel(t *testing.T) (*Model, string, string) {
	t.Helper()
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)
	i18n.InitWithLanguage("en")

	const a, b = "tui:chat:A", "tui:chat:B"
	m.sessionMgr.GetOrCreate(a)
	m.sessionMgr.GetOrCreate(b)
	m.setCurrentChatKey(a)
	m.showWelcome = false
	return m, a, b
}

func createAndPublish(t *testing.T, m *Model, chatID, command, reason string) string {
	t.Helper()
	am := m.agentLoop.GetApprovalManager()
	if am == nil {
		t.Fatal("approval manager is nil")
	}
	approval := am.CreateApproval(chatID, command, reason, 0, "")
	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native",
		ChatID:  chatID,
		Event:   "approval.request",
		Metadata: map[string]string{
			"id":      approval.ID,
			"command": command,
			"reason":  reason,
		},
	})
	return approval.ID
}

func TestApprovalFromBackgroundSessionIsQueued(t *testing.T) {
	m, _, b := setupApprovalModel(t)

	id := createAndPublish(t, m, b, "rm -rf /", "dangerous")
	deliverOutbound(t, m)

	if m.pendingApprovalID != "" {
		t.Fatalf("background approval leaked into visible state: pendingApprovalID=%q", m.pendingApprovalID)
	}
	snap, ok := m.pendingApprovals[b]
	if !ok {
		t.Fatalf("approval not stashed for %q (map=%v)", b, m.pendingApprovals)
	}
	if snap.id != id {
		t.Fatalf("stashed id=%q, want %q", snap.id, id)
	}
	if snap.cmd != "rm -rf /" || snap.reason != "dangerous" {
		t.Fatalf("stashed snapshot mismatch: cmd=%q reason=%q", snap.cmd, snap.reason)
	}
}

func TestApprovalSurfacesOnSwitch(t *testing.T) {
	m, _, b := setupApprovalModel(t)

	id := createAndPublish(t, m, b, "rm -rf /", "dangerous")
	deliverOutbound(t, m)

	m.setCurrentChatKey(b)
	m.clearStreamingState()

	if m.pendingApprovalID != id {
		t.Fatalf("after switch pendingApprovalID=%q, want %q", m.pendingApprovalID, id)
	}
	if m.pendingApprovalCmd != "rm -rf /" || m.pendingApprovalReason != "dangerous" {
		t.Fatalf("restored fields mismatch: cmd=%q reason=%q", m.pendingApprovalCmd, m.pendingApprovalReason)
	}
	if _, ok := m.pendingApprovals[b]; ok {
		t.Fatalf("snapshot for %q not consumed on restore", b)
	}
}

func TestApprovalStashedOnSwitchAway(t *testing.T) {
	m, a, b := setupApprovalModel(t)

	id := createAndPublish(t, m, a, "drop table users", "irreversible")
	deliverOutbound(t, m)
	if m.pendingApprovalID != id {
		t.Fatalf("visible approval not set: pendingApprovalID=%q, want %q", m.pendingApprovalID, id)
	}

	m.setCurrentChatKey(b)
	m.clearStreamingState()
	if m.pendingApprovalID != "" || m.pendingApprovalCmd != "" || m.pendingApprovalReason != "" {
		t.Fatalf("visible fields not cleared on switch away: id=%q cmd=%q", m.pendingApprovalID, m.pendingApprovalCmd)
	}
	snap, ok := m.pendingApprovals[a]
	if !ok || snap.id != id || snap.cmd != "drop table users" || snap.reason != "irreversible" {
		t.Fatalf("A's snapshot missing or wrong: %+v ok=%v", snap, ok)
	}

	m.setCurrentChatKey(a)
	m.clearStreamingState()
	if m.pendingApprovalID != id {
		t.Fatalf("approval not restored on switch back: pendingApprovalID=%q, want %q", m.pendingApprovalID, id)
	}
	if len(m.pendingApprovals) != 0 {
		t.Fatalf("pendingApprovals not clean after restore: %v", m.pendingApprovals)
	}
}

func TestAnsweredApprovalNotResurrected(t *testing.T) {
	m, _, b := setupApprovalModel(t)

	id := createAndPublish(t, m, b, "rm -rf /", "dangerous")
	deliverOutbound(t, m)

	m.setCurrentChatKey(b)
	m.clearStreamingState()
	if m.pendingApprovalID != id {
		t.Fatalf("precondition: pendingApprovalID=%q, want %q", m.pendingApprovalID, id)
	}

	res := make(chan bool, 1)
	approval := m.agentLoop.GetApprovalManager().GetApproval(id)
	if approval == nil {
		t.Fatal("approval not found in manager before answering")
	}
	go func() {
		approved, err := approval.WaitForResponse(context.Background(), 5*time.Second)
		if err != nil {
			res <- false
			return
		}
		res <- approved
	}()

	m.handleApproval(true)

	if m.pendingApprovalID != "" || m.pendingApprovalCmd != "" || m.pendingApprovalReason != "" {
		t.Fatalf("visible fields not cleared after answering: id=%q", m.pendingApprovalID)
	}
	if len(m.pendingApprovals) != 0 {
		t.Fatalf("stale snapshot survived answering: %v", m.pendingApprovals)
	}
	select {
	case approved := <-res:
		if !approved {
			t.Fatal("agent received rejection, expected approval")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handleApproval did not unblock the waiting agent")
	}

	m.setCurrentChatKey(b)
	m.clearStreamingState()
	m.setCurrentChatKey("tui:chat:A")
	m.clearStreamingState()
	m.setCurrentChatKey(b)
	m.clearStreamingState()
	if m.pendingApprovalID != "" {
		t.Fatalf("answered approval resurrected after switch: id=%q", m.pendingApprovalID)
	}
}

func TestHandleApprovalExpiredWarns(t *testing.T) {
	m, a, _ := setupApprovalModel(t)

	const bogus = "nonexistent-approval-id"
	m.pendingApprovalID = bogus
	m.pendingApprovalCmd = "rm -rf /"
	m.pendingApprovalReason = "stale"
	m.pendingApprovals = map[string]pendingApprovalSnapshot{
		a: {id: bogus, cmd: "rm -rf /", reason: "stale"},
	}

	m.handleApproval(true)

	if !strings.Contains(m.approvalResult, "no longer active") {
		t.Fatalf("expected expiry warning, got %q", m.approvalResult)
	}
	if strings.Contains(m.approvalResult, "approved — executing") {
		t.Fatalf("expired approval wrongly reported as approved: %q", m.approvalResult)
	}
	if m.pendingApprovalID != "" {
		t.Fatalf("visible fields not cleared: id=%q", m.pendingApprovalID)
	}
	if _, ok := m.pendingApprovals[a]; ok {
		t.Fatalf("matching snapshot not deleted after answering")
	}

	m.pendingApprovalID = bogus
	m.pendingApprovals = map[string]pendingApprovalSnapshot{
		a: {id: "newer-id", cmd: "ls", reason: "fresh"},
	}
	m.handleApproval(false)
	if snap, ok := m.pendingApprovals[a]; !ok || snap.id != "newer-id" {
		t.Fatalf("newer snapshot clobbered: %+v ok=%v", snap, ok)
	}
}

func TestInvertedAuditRepro(t *testing.T) {
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)

	const current, other = "tui:chat:A-visible", "tui:chat:B-background"
	m.sessionMgr.GetOrCreate(current)
	m.sessionMgr.GetOrCreate(other)
	m.setCurrentChatKey(current)
	m.showWelcome = false

	am := m.agentLoop.GetApprovalManager()
	approval := am.CreateApproval(other, "rm -rf /", "dangerous", 0, "")

	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native", ChatID: other, Event: "approval.request",
		Metadata: map[string]string{"id": approval.ID, "command": "rm -rf /", "reason": "dangerous"},
	})

	cmd := m.startOutboundListener()
	msg := cmd()
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("listener returned %T", msg)
	}
	up, _ = m.Update(om)
	m = up.(*Model)

	if m.pendingApprovalID != "" {
		t.Fatalf("background approval surfaced on the wrong session: %q", m.pendingApprovalID)
	}
	snap, ok := m.pendingApprovals[other]
	if !ok {
		t.Fatalf("REGRESSION: approval for %q was dropped (pendingApprovals=%v)", other, m.pendingApprovals)
	}
	if snap.id != approval.ID {
		t.Fatalf("queued id=%q, want %q", snap.id, approval.ID)
	}

	m.setCurrentChatKey(other)
	m.clearStreamingState()
	if m.pendingApprovalID != approval.ID {
		t.Fatalf("approval not restored on switch: id=%q, want %q", m.pendingApprovalID, approval.ID)
	}

	res := make(chan error, 1)
	go func() {
		_, err := approval.WaitForResponse(context.Background(), 5*time.Second)
		res <- err
	}()
	m.handleApproval(false)
	if err := <-res; err != nil {
		t.Fatalf("blocked agent did not unblock after answering: %v", err)
	}
}
