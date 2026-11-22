package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

func setupTwoSessions(t *testing.T) (*Model, string, string) {
	t.Helper()
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)

	const a, b = "tui:chat:A", "tui:chat:B"
	m.sessionMgr.GetOrCreate(a)
	m.sessionMgr.GetOrCreate(b)
	m.setCurrentChatKey(a)
	m.showWelcome = false
	return m, a, b
}

func seedStreamBuffer(m *Model) {
	m.processing = true
	m.currentAssistantMsgID = "msg-old"
	m.currentStream = "STALE FRAMES from the previous conversation"
	m.currentThinking = "stale thinking"
	m.streamRenderedLines = []string{"stale"}
}

func TestStreamBufferClearedOnSessionSwitch(t *testing.T) {
	m, _, b := setupTwoSessions(t)
	seedStreamBuffer(m)

	m.createNewChat()
	if m.currentKey == b {
		t.Fatal("createNewChat must produce a fresh key")
	}
	if m.currentStream != "" || m.currentThinking != "" {
		t.Fatalf("stream buffer leaked across session switch: stream=%q thinking=%q",
			m.currentStream, m.currentThinking)
	}
	if m.currentAssistantMsgID != "" {
		t.Fatalf("assistant message id survived session switch: %q", m.currentAssistantMsgID)
	}
	if len(m.streamRenderedLines) != 0 {
		t.Fatalf("rendered line cache survived session switch: %d lines", len(m.streamRenderedLines))
	}
}

func TestStreamBufferClearedOnModalSessionSwitch(t *testing.T) {
	m, _, b := setupTwoSessions(t)
	seedStreamBuffer(m)

	m.setCurrentChatKey(b)
	if m.currentStream != "" {
		t.Fatalf("stream buffer leaked on setCurrentChatKey: %q", m.currentStream)
	}
}

func TestStaleFramesCannotRepaintAfterSwitch(t *testing.T) {
	m, a, b := setupTwoSessions(t)
	seedStreamBuffer(m)

	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native", ChatID: a, Event: "message.stream",
		MessageID: "msg-old", Content: " more",
	})
	drain := pumpOutbound(t, m)
	_ = drain

	m.setCurrentChatKey(b)

	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native", ChatID: a, Event: "message.stream",
		MessageID: "msg-old", Content: "GHOST",
	})
	pumpOutbound(t, m)
	if strings.Contains(m.currentStream, "GHOST") || strings.Contains(m.currentStream, "STALE") {
		t.Fatalf("background-session frames polluted visible session buffer: %q", m.currentStream)
	}

	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel: "native", ChatID: b, Event: "message.stream",
		MessageID: "msg-new", Content: "hello from B",
	})
	pumpOutbound(t, m)
	if m.currentStream != "hello from B" {
		t.Fatalf("visible-session frame did not rebuild buffer cleanly: %q", m.currentStream)
	}
}

func pumpOutbound(t *testing.T, m *Model) string {
	t.Helper()
	cmd := m.startOutboundListener()
	if cmd == nil {
		t.Fatal("no listener command")
	}
	msg := cmd()
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("listener returned %T, want outboundMsg", msg)
	}
	_, _ = m.Update(om)
	return om.msg.Content
}
