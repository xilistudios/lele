package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

const (
	subagentResultPrefix = "[System: subagent]"
	subagentResultText   = "the subagent result"
)

func subagentResultHistory(t *testing.T, al *AgentLoop, sessionKey string) []providers.Message {
	t.Helper()
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("registry returned no default agent")
	}
	var found []providers.Message
	for _, msg := range agent.Sessions.GetHistory(sessionKey) {
		if strings.Contains(msg.Content, subagentResultPrefix) {
			found = append(found, msg)
		}
	}
	return found
}

func publishUserTurnWithID(t *testing.T, msgBus *bus.MessageBus, sessionKey, messageID string) {
	t.Helper()
	if !msgBus.PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "user1",
		ChatID:     "chat-1",
		Content:    "hello",
		SessionKey: sessionKey,
		Metadata:   map[string]string{"message_id": messageID},
	}) {
		t.Fatal("user inbound publish rejected")
	}
}

func runIdleTurnAndResume(t *testing.T, provider *requestRecordingProvider, al *AgentLoop,
	msgBus *bus.MessageBus, sessionKey, taskID, messageID, resultText string) {
	t.Helper()

	publishUserTurnWithID(t, msgBus, sessionKey, messageID)
	if !waitFor(t, 15*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the initial user turn never reached the provider")
	}
	drainUntil(t, msgBus, 15*time.Second, "parent answer")

	before := provider.requestCount()
	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", taskID,
		&tools.ToolResult{ForLLM: resultText})
	if !waitFor(t, 15*time.Second, func() bool { return provider.requestCount() > before }) {
		t.Fatalf("the parent loop never resumed after %s completed (provider calls = %d)", taskID, provider.requestCount())
	}
	waitForOutboundContent(t, msgBus, "parent answer", 15*time.Second)
}

func waitForOutboundContent(t *testing.T, msgBus *bus.MessageBus, marker string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("outbound content %q never arrived within %s", marker, within)
		}
		msg, ok := recvOutbound(t, msgBus, left)
		if !ok {
			t.Fatalf("outbound content %q never arrived within %s", marker, within)
		}
		if strings.Contains(msg.Content, marker) {
			return
		}
	}
}

func TestSubagentResult_PersistedInParentHistory(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-history"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	runIdleTurnAndResume(t, provider, al, msgBus, sessionKey, "subagent-1", "m-1", subagentResultText)

	if !waitFor(t, 15*time.Second, func() bool {
		return len(subagentResultHistory(t, al, sessionKey)) == 1
	}) {
		t.Fatalf("persisted subagent results = %d, want exactly 1", len(subagentResultHistory(t, al, sessionKey)))
	}
	msg := subagentResultHistory(t, al, sessionKey)[0]
	if msg.Role != "user" {
		t.Errorf("role = %q, want %q (a mid-history system message is not a supported wire shape)", msg.Role, "user")
	}
	if !strings.HasPrefix(msg.Content, subagentResultPrefix) {
		t.Errorf("content = %q, want the %q prefix", msg.Content, subagentResultPrefix)
	}
	if !strings.Contains(msg.Content, subagentResultText) {
		t.Errorf("content = %q, want it to contain %q", msg.Content, subagentResultText)
	}
}

func TestSubagentResult_NotPersistedWhenWaitConsumed(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-history-wait"
	sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	if sm.ClaimResult("subagent-1") {
		t.Fatal("ClaimResult lost on a fresh task")
	}

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: subagentResultText})
	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "false" {
		t.Fatalf("event metadata[resumed] = %q, want %q", got, "false")
	}

	time.Sleep(300 * time.Millisecond)
	if n := provider.requestCount(); n != 0 {
		t.Errorf("provider calls = %d after a consumed result, want 0", n)
	}
	if got := subagentResultHistory(t, al, sessionKey); len(got) != 0 {
		t.Errorf("persisted subagent results = %d, want 0:\n%+v", len(got), got)
	}
}

func TestSubagentResult_VisibleToNextTurn(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-history-next"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	runIdleTurnAndResume(t, provider, al, msgBus, sessionKey, "subagent-1", "m-1", subagentResultText)

	publishUserTurnWithID(t, msgBus, sessionKey, "m-3")
	if !waitFor(t, 15*time.Second, func() bool { return provider.requestCount() >= 3 }) {
		t.Fatalf("the third turn never reached the provider (provider calls = %d)", provider.requestCount())
	}
	if text := provider.lastRequestText(); !strings.Contains(text, subagentResultText) {
		t.Errorf("the third turn's request does not contain the subagent result:\n%s", text)
	}
}

func TestSubagentResult_PersistedExactlyOnce(t *testing.T) {
	const (
		firstResult  = "the subagent result one"
		secondResult = "the subagent result two"
	)
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-history-once"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	addSubagentTaskForTest(t, al, "subagent-2", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	runIdleTurnAndResume(t, provider, al, msgBus, sessionKey, "subagent-1", "m-1", firstResult)
	runIdleTurnAndResume(t, provider, al, msgBus, sessionKey, "subagent-2", "m-2", secondResult)

	if !waitFor(t, 15*time.Second, func() bool {
		return len(subagentResultHistory(t, al, sessionKey)) == 2
	}) {
		t.Fatalf("persisted subagent results = %d, want exactly 2 (one per task)", len(subagentResultHistory(t, al, sessionKey)))
	}
	history := subagentResultHistory(t, al, sessionKey)
	for _, want := range []string{firstResult, secondResult} {
		count := 0
		for _, msg := range history {
			if strings.Contains(msg.Content, want) {
				count++
			}
		}
		if count != 1 {
			t.Errorf("messages containing %q = %d, want exactly 1 (the result must have ONE writer)", want, count)
		}
	}
}
