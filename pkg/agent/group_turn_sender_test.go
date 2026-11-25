package agent

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

func TestRunGroupTurn_CarriesTriggeringSender(t *testing.T) {
	response := &providers.LLMResponse{ToolCalls: []providers.ToolCall{}}
	lr, agent, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	provider := &senderTestProvider{
		toolCall: providers.ToolCall{ID: "call_group", Name: "exec", Arguments: map[string]interface{}{"command": "rm -rf /tmp/x"}},
		repeats:  1,
	}
	lr.al.registry.mu.Lock()
	agent.Provider = provider
	lr.al.registry.mu.Unlock()

	fake := &senderCapturingTool{name: "exec"}
	agent.Tools.Register(fake)

	observed := captureApprovalRequester(t, lr.al.approvalManager, lr.al.bus.SubscribeOutbound)

	const senderID = "111|alice"
	const groupID = "g-sender"
	if _, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
		GroupID:        groupID,
		Speaker:        "test-agent",
		SystemPrompt:   "sys",
		Instruction:    "run it",
		EnableTools:    true,
		OriginChannel:  channels.ChannelName,
		OriginChatID:   "555",
		OriginSenderID: senderID,
	}); err != nil {
		t.Fatalf("runGroupTurn: %v", err)
	}

	select {
	case approval := <-observed:
		if approval.RequesterID != senderID {
			t.Fatalf("group approval requester = %q, want the triggering sender %q", approval.RequesterID, senderID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the group turn never requested an approval")
	}

	senders := fake.observedSenders()
	if len(senders) == 0 {
		t.Fatal("the group turn never executed the tool")
	}
	if senders[0] != senderID {
		t.Fatalf("group tool sender = %q, want the triggering sender %q (not the group id %q)", senders[0], senderID, groupID)
	}
}
