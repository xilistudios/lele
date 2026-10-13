// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// The injected subagent result belongs in the parent's history.
//
// A resume that forgets what it resumed on is half a fix: the continuation
// turn's request carries the result (that is A2), but the NEXT turn's request
// would not, so the parent could answer "the subagent finished" and nothing
// more, and compaction/eviction could never see it either.
//
// The persistence is therefore the ordinary user-message write in
// llmRunnerImpl.runAgentLoop: the injected turn is a normal turn and its
// "[System: subagent] <result>" message goes through SkipUserMessage=false.
// One writer only — processSystemMessage must NOT also append it.
// ============================================================================

const (
	// subagentResultPrefix is the marker processSystemMessage puts in front of a
	// subagent-produced user message.
	subagentResultPrefix = "[System: subagent]"
	// subagentResultText is the payload every test in this file publishes.
	subagentResultText = "the subagent result"
)

// subagentResultHistory returns the persisted messages of sessionKey that carry
// an injected subagent result.
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

// publishUserTurnWithID is publishUserTurn with an explicit message id: the
// durable-inbound ledger dedupes replayed ids, so each turn needs its own.
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

// runIdleTurnAndResume publishes one user turn, waits for it to finish, then
// completes a subagent and waits for the continuation turn to reach the
// provider. It returns the provider call count reached before the completion.
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
	// The injected turn arrives on the internal "system" channel, so it has no
	// turn.end (publishTurnEnd skips internal channels). Its final message is the
	// terminal signal available: turn 1's copy was already drained above.
	waitForOutboundContent(t, msgBus, "parent answer", 15*time.Second)
}

// waitForOutboundContent drains the outbound bus until a message whose content
// contains marker arrives.
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

// TestSubagentResult_PersistedInParentHistory is the A3 headline test: after the
// resume, the parent's transcript contains the result as ONE user message whose
// text is the "[System: subagent] <result>" rendering (the same shape
// buildSummaryMessage and the cron/goal continuations use). Role "system" is
// deliberately not used: mid-history system messages are only supported by the
// CLI-style providers.
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

// TestSubagentResult_NotPersistedWhenWaitConsumed is the negative control for
// race (b): when a wait_for_subagent already consumed the result, the injector
// never runs a turn, so nothing may enter the history.
func TestSubagentResult_NotPersistedWhenWaitConsumed(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-history-wait"
	sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	// The in-turn wait read the result.
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

// TestSubagentResult_VisibleToNextTurn proves the persistence is what makes the
// resume durable: a third, ordinary user turn also sees the result, which can
// only come from the history (BuildMessages carries no other copy of it).
func TestSubagentResult_VisibleToNextTurn(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-history-next"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	runIdleTurnAndResume(t, provider, al, msgBus, sessionKey, "subagent-1", "m-1", subagentResultText)

	// Turn 3: a plain user message.
	publishUserTurnWithID(t, msgBus, sessionKey, "m-3")
	if !waitFor(t, 15*time.Second, func() bool { return provider.requestCount() >= 3 }) {
		t.Fatalf("the third turn never reached the provider (provider calls = %d)", provider.requestCount())
	}
	if text := provider.lastRequestText(); !strings.Contains(text, subagentResultText) {
		t.Errorf("the third turn's request does not contain the subagent result:\n%s", text)
	}
}

// TestSubagentResult_PersistedExactlyOnce guards against the tempting
// "belt and braces" fix of appending the message in processSystemMessage as
// well: two writers would put two copies of the result in the transcript.
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
