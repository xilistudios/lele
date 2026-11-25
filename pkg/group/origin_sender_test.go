package group

import (
	"context"
	"sync"

	"github.com/xilistudios/lele/pkg/bus"
	"testing"
)

type originRecordingExecutor struct {
	mu      sync.Mutex
	origins []struct {
		channel string
		chatID  string
		sender  string
	}
}

func (e *originRecordingExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	e.mu.Lock()
	e.origins = append(e.origins, struct {
		channel string
		chatID  string
		sender  string
	}{channel: req.OriginChannel, chatID: req.OriginChatID, sender: req.OriginSenderID})
	e.mu.Unlock()
	return "turn-" + req.Speaker, 1, nil
}

func (e *originRecordingExecutor) last() (string, string, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.origins) == 0 {
		return "", "", ""
	}
	o := e.origins[len(e.origins)-1]
	return o.channel, o.chatID, o.sender
}

func TestStartPropagatesOriginSenderToTurnRequest(t *testing.T) {
	resolver := newToggleResolver("a")
	executor := &originRecordingExecutor{}
	gm := NewGroupManager(resolver.resolve, executor.execute, func(bus.OutboundMessage) {})

	const senderID = "111|alice"
	const groupID = "origin-sender-1"
	if _, err := gm.Start(context.Background(), groupID, "", "task", "round_robin",
		[]Participant{{AgentID: "a", Role: RoleProposer}},
		GroupOptions{MaxTurns: 1, OriginSenderID: senderID}, "telegram", "555"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	channel, chatID, sender := executor.last()
	if channel != "telegram" || chatID != "555" {
		t.Fatalf("origin = %q/%q, want telegram/555", channel, chatID)
	}
	if sender != senderID {
		t.Fatalf("OriginSenderID = %q, want the triggering sender %q (not the group id %q)", sender, senderID, groupID)
	}

	state, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group state missing")
	}
	if state.OriginSenderID != senderID {
		t.Fatalf("state.OriginSenderID = %q, want %q", state.OriginSenderID, senderID)
	}
}
