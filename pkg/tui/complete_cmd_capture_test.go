package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

func setupCompleteCmdModel(t *testing.T) (*Model, string, string) {
	t.Helper()
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)

	const a, b = "tui:chat:A-turn-in-flight", "tui:chat:B-other"
	m.sessionMgr.GetOrCreate(a)
	m.sessionMgr.GetOrCreate(b)
	m.setCurrentChatKey(a)
	m.showWelcome = false

	m.processing = true
	m.currentMessageID = "msg-1"
	return m, a, b
}

func extractCompletionCmd(t *testing.T, m *Model, chatID string) tea.Cmd {
	t.Helper()
	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel:   "native",
		ChatID:    chatID,
		Event:     "",
		MessageID: "msg-1",
	})
	cmd := m.startOutboundListener()
	msg := cmd()
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("outbound listener returned %T, want outboundMsg", msg)
	}
	_, returned := m.Update(om)
	if returned == nil {
		t.Fatal("Update returned nil cmd; expected a batch containing the completion command")
	}
	got := returned()
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		t.Fatalf("batch cmd returned %T, want tea.BatchMsg", got)
	}
	if len(batch) == 0 {
		t.Fatal("empty batch; expected the completion command")
	}
	candidates := batch[:len(batch)-1]
	for _, c := range candidates {
		got := c()
		if _, isComplete := got.(completeMsg); isComplete {
			return c
		}
	}
	t.Fatalf("no completeMsg-producing command among %d non-listener batch members", len(candidates))
	return nil
}

func TestCompleteMsgCapturesKeyAtCreation(t *testing.T) {
	m, a, b := setupCompleteCmdModel(t)
	completeCmd := extractCompletionCmd(t, m, a)

	m.setCurrentChatKey(b)

	msg := completeCmd()
	cm, ok := msg.(completeMsg)
	if !ok {
		t.Fatalf("expected completeMsg, got %T", msg)
	}
	if cm.sessionKey != a {
		t.Fatalf("completeMsg carries sessionKey=%q, want %q: key was read at execution time, not captured at creation", cm.sessionKey, a)
	}
	if cm.sessionKey == m.currentKey {
		t.Fatalf("guard would pass for the wrong session %q — processing state of B would be cleared", cm.sessionKey)
	}
}

func TestCompleteMsgDeliveredToOriginalSessionStillClears(t *testing.T) {
	m, a, _ := setupCompleteCmdModel(t)
	completeCmd := extractCompletionCmd(t, m, a)

	msg := completeCmd()
	cm, ok := msg.(completeMsg)
	if !ok {
		t.Fatalf("expected completeMsg, got %T", msg)
	}
	if cm.sessionKey != a {
		t.Fatalf("completeMsg sessionKey=%q, want %q", cm.sessionKey, a)
	}
	if cm.sessionKey != m.currentKey {
		t.Fatalf("same-session completion would be dropped by the guard")
	}
}
