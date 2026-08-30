package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

// Tests for audit M1: the completion command emitted by the outboundMsg
// handler (stream-end, "" case) must capture m.currentKey at closure
// CREATION time. bubbletea invokes tea.Cmd functions asynchronously on its
// own goroutine ("this can be long"), so reading mutable per-session Model
// state at execution time lets a session switch in between misattribute the
// completion: the guard `msg.sessionKey == m.currentKey` in the completeMsg
// handler would then pass for the WRONG session, clearing its loading state
// while the real session's completion is lost. It is also a data race on
// m.currentKey.
//
// These tests drive the REAL code path: publish an outbound stream-end event,
// deliver it via m.startOutboundListener() + m.Update, extract the completion
// tea.Cmd from the batch Update returned, switch sessions, then run the Cmd
// exactly as bubbletea would.

// setupCompleteCmdModel builds a model with two sessions, current session A,
// with a turn in flight (processing + matching message id).
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

// extractCompletionCmd delivers a stream-end outbound event for chatID
// through the real Update path and returns the completeMsg-producing tea.Cmd
// from the batch Update returned.
func extractCompletionCmd(t *testing.T, m *Model, chatID string) tea.Cmd {
	t.Helper()
	m.agentLoop.MessageBus().PublishOutbound(bus.OutboundMessage{
		Channel:   "native",
		ChatID:    chatID,
		Event:     "", // stream finished
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
	// Running the batch cmd only CONSTRUCTS the tea.BatchMsg (the slice of
	// sub-commands); bubbletea executes each sub-cmd separately afterwards,
	// so nothing has run yet at this point.
	got := returned()
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		t.Fatalf("batch cmd returned %T, want tea.BatchMsg", got)
	}
	// The handler appends the completion cmd first, then re-subscribes the
	// outbound listener (which would block the test if executed). Scan the
	// ordered batch and run ONLY members that yield a completeMsg; the
	// listener is guaranteed to be the LAST member, so drop it up front.
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

	// The user switches sessions before bubbletea's async goroutine runs the
	// command — the exact race window from the audit.
	m.setCurrentChatKey(b)

	msg := completeCmd()
	cm, ok := msg.(completeMsg)
	if !ok {
		t.Fatalf("expected completeMsg, got %T", msg)
	}
	if cm.sessionKey != a {
		t.Fatalf("completeMsg carries sessionKey=%q, want %q: key was read at execution time, not captured at creation", cm.sessionKey, a)
	}
	// Downstream guard sanity: the completion must NOT pass the
	// `msg.sessionKey == m.currentKey` check now that B is on screen.
	if cm.sessionKey == m.currentKey {
		t.Fatalf("guard would pass for the wrong session %q — processing state of B would be cleared", cm.sessionKey)
	}
}

func TestCompleteMsgDeliveredToOriginalSessionStillClears(t *testing.T) {
	// Regression companion: without a session switch the captured key still
	// equals currentKey, so the normal completion path is unaffected.
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
