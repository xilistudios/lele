// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// Subagent completion resumes the parent loop.
//
// The reported bug: a subagent's result was published as a system inbound and
// then unconditionally pre-marked "delivered", so processSystemMessage skipped
// it and the parent never ran the continuation turn — the chat sat "loading"
// while the answer was already in the task.
//
// The fix has two halves: the injector claims the result atomically
// (pkg/agent/subagent_helpers.go) and the claim is the ONLY gate on the system
// inbound (pkg/tools owns the primitives). The subagent.result UI event is
// published unconditionally, with metadata["resumed"] carrying whether the
// claim won, so neither client is left showing a finished task as running.
// ============================================================================

// requestRecordingProvider records every request the loop sends to the
// provider, and can hold its first call open so a test can create a
// deterministic "the parent is mid-turn" condition (the session semaphore is
// held for the whole call).
type requestRecordingProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	response string

	// blockFirst, when non-nil, blocks the FIRST Chat call until it is closed.
	blockFirst chan struct{}
	// entered is closed when the first Chat call is entered.
	entered     chan struct{}
	enteredOnce sync.Once
}

func newRequestRecordingProvider(response string) *requestRecordingProvider {
	return &requestRecordingProvider{response: response, entered: make(chan struct{})}
}

func (p *requestRecordingProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	n := len(p.requests)
	p.mu.Unlock()

	if n == 1 {
		p.enteredOnce.Do(func() { close(p.entered) })
		if p.blockFirst != nil {
			<-p.blockFirst
		}
	}
	return &providers.LLMResponse{Content: p.response, ToolCalls: []providers.ToolCall{}}, nil
}

func (p *requestRecordingProvider) GetDefaultModel() string { return "mock-recording-model" }

func (p *requestRecordingProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *requestRecordingProvider) request(i int) []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.requests) {
		return nil
	}
	return p.requests[i]
}

func (p *requestRecordingProvider) lastRequest() []providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		return nil
	}
	return p.requests[len(p.requests)-1]
}

func (p *requestRecordingProvider) lastRequestText() string {
	return flattenMessages(p.lastRequest())
}

// flattenMessages renders a request as one string so a test can assert on the
// text the model was actually given.
func flattenMessages(messages []providers.Message) string {
	var sb strings.Builder
	for _, m := range messages {
		sb.WriteString(m.Role)
		sb.WriteString(": ")
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// addSubagentTaskForTest registers a terminal subagent task owned by sessionKey
// in one of the loop's managers, so the coordinator's claim can find it. It
// returns that manager.
func addSubagentTaskForTest(t *testing.T, al *AgentLoop, taskID, sessionKey string) *tools.SubagentManager {
	t.Helper()

	var chosen *tools.SubagentManager
	for _, sm := range al.toolCoordinator.GetSubagents() {
		chosen = sm
		break
	}
	if chosen == nil {
		t.Fatal("the loop has no subagent manager to register a task with")
	}
	chosen.AddTaskForTest(&tools.SubagentTask{
		ID:                taskID,
		Task:              "test subagent task",
		Status:            tools.SubagentStatusCompleted,
		Summary:           "Done",
		Result:            "test result",
		SpawnerSessionKey: sessionKey,
		OriginSessionKey:  sessionKey,
		Created:           time.Now().UnixMilli(),
		Updated:           time.Now().UnixMilli(),
	}, nil)
	return chosen
}

// startResumeLoop runs the loop until the test (or its cleanup) cancels it.
func startResumeLoop(t *testing.T, al *AgentLoop) context.CancelFunc {
	t.Helper()
	runCtx, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("agent loop did not stop")
		}
	})
	return cancelRun
}

// publishUserTurn sends an ordinary user message for sessionKey.
func publishUserTurn(t *testing.T, msgBus *bus.MessageBus, sessionKey string) {
	t.Helper()
	if !msgBus.PublishInbound(bus.InboundMessage{
		Channel:    "native",
		SenderID:   "user1",
		ChatID:     "chat-1",
		Content:    "hello",
		SessionKey: sessionKey,
		Metadata:   map[string]string{"message_id": "m-1"},
	}) {
		t.Fatal("user inbound publish rejected")
	}
}

// waitForOutboundEvent drains the outbound bus until the named event arrives.
// The bus is shared with the rest of the turn, so unrelated messages are
// skipped rather than failing the test.
func waitForOutboundEvent(t *testing.T, msgBus *bus.MessageBus, event string, within time.Duration) bus.OutboundMessage {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("outbound event %q never arrived within %s", event, within)
		}
		msg, ok := recvOutbound(t, msgBus, left)
		if !ok {
			t.Fatalf("outbound event %q never arrived within %s", event, within)
		}
		if msg.Event == event {
			return msg
		}
	}
}

// waitForTaskClaim polls until the manager reports the task's result claimed.
func waitForTaskClaim(t *testing.T, sm *tools.SubagentManager, taskID string, within time.Duration) bool {
	t.Helper()
	return waitFor(t, within, func() bool {
		task, ok := sm.GetTask(taskID)
		return ok && task != nil && task.ResultClaimed()
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// Race (a): the parent is idle when the subagent completes
// ──────────────────────────────────────────────────────────────────────────────

// TestSubagentCompletion_ParentIdle_ResumesLoop is the headline regression test
// for the reported bug: the completion must start a SECOND parent turn whose
// request carries the subagent's result. Before the fix the result was
// pre-marked delivered, processSystemMessage skipped it, and no second turn
// ever ran.
func TestSubagentCompletion_ParentIdle_ResumesLoop(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-idle"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	// First turn: an ordinary user message.
	publishUserTurn(t, msgBus, sessionKey)
	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the initial user turn never reached the provider")
	}
	drainUntil(t, msgBus, 10*time.Second, "parent answer")

	// The subagent completes while the session is idle.
	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 2 }) {
		t.Fatalf("the parent loop never resumed after the subagent completed (provider calls = %d, want >= 2)", provider.requestCount())
	}
	if text := provider.lastRequestText(); !strings.Contains(text, "the subagent result") {
		t.Errorf("the continuation request does not contain the subagent result:\n%s", text)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// AM2: the UI event is decoupled from the injection claim
// ──────────────────────────────────────────────────────────────────────────────

// TestSubagentCompletion_PublishesResultEventEvenWhenBusy pins AM2: the
// subagent.result event must be published while the parent holds the session
// semaphore. The old `if !isSessionProcessing(sessionKey)` gate suppressed it
// exactly when the parent was busy, leaving the TUI progress row and the WebUI
// card stuck on "running".
func TestSubagentCompletion_PublishesResultEventEvenWhenBusy(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	provider.blockFirst = make(chan struct{})
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-busy"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishUserTurn(t, msgBus, sessionKey)
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the initial turn never entered the provider")
	}
	if !al.isSessionProcessing(sessionKey) {
		t.Fatal("the session is not processing while the turn blocks in the provider")
	}

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["task_id"]; got != "subagent-1" {
		t.Errorf("event task_id = %q, want %q", got, "subagent-1")
	}
	if got := ev.Metadata["resumed"]; got != "true" {
		t.Errorf("event metadata[resumed] = %q, want %q (the claim was uncontested)", got, "true")
	}

	close(provider.blockFirst)
}

// TestSubagentCompletion_ClaimLost_StillPublishesEventWithoutResume is the
// other half of AM2: when the result is already consumed (a wait_for_subagent
// read it in-turn), the UI must still be told — with resumed=false — and no
// continuation turn may run.
func TestSubagentCompletion_ClaimLost_StillPublishesEventWithoutResume(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-consumed"
	sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	// Simulate wait_for_subagent having consumed the result in-turn.
	if sm.ClaimResult("subagent-1") {
		t.Fatal("ClaimResult lost on a fresh task")
	}

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "false" {
		t.Errorf("event metadata[resumed] = %q, want %q", got, "false")
	}

	time.Sleep(250 * time.Millisecond)
	if n := provider.requestCount(); n != 0 {
		t.Errorf("provider calls = %d after a consumed result, want 0 — a duplicate continuation was injected", n)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Race (b): the parent is inside wait_for_subagent
// ──────────────────────────────────────────────────────────────────────────────

// TestSubagentCompletion_WaitInFlight_NoInjection pins race (b): with a waiter
// registered, the injector must not claim and must not publish a system
// inbound — the waiter owns the result and will return it in-turn. The UI event
// still goes out.
func TestSubagentCompletion_WaitInFlight_NoInjection(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-waiting"
	sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	sm.AddResultWaiter("subagent-1")

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "false" {
		t.Errorf("event metadata[resumed] = %q, want %q (a waiter is registered)", got, "false")
	}
	if task, ok := sm.GetTask("subagent-1"); ok && task.ResultClaimed() {
		t.Error("ResultClaimed() = true while a waiter is registered, want false")
	}

	// The waiter gives up (timeout). Per AM1 that release must not simply drop
	// the registration: the injector refused to publish because a waiter was
	// registered, so the last waiter out owns the result and hands it back.
	claimed := sm.ReleaseResultWaiterAndClaim("subagent-1")
	if claimed == nil {
		t.Fatal("ReleaseResultWaiterAndClaim = nil on a terminal unclaimed task: the result would be lost (AM1 window)")
	}
	if !claimed.ResultClaimed() {
		t.Error("ResultClaimed() = false after the AM1 claim-on-release, want true")
	}
	if sm.ClaimResultForInjection("subagent-1") {
		t.Error("ClaimResultForInjection = true after the AM1 claim, want false")
	}

	// The waiter hands the result back in-turn, so the parent must not also be
	// woken by a continuation turn.
	time.Sleep(250 * time.Millisecond)
	if n := provider.requestCount(); n != 0 {
		t.Errorf("provider calls = %d after the AM1 claim, want 0 (the waiter owns the result)", n)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Race (c): the parent is busy and NOT waiting
// ──────────────────────────────────────────────────────────────────────────────

// TestSubagentCompletion_ParentBusyNotWaiting_InjectsAtTurnEnd pins race (c):
// the claim is taken immediately, the inbound is queued, and the continuation
// runs only after the in-flight turn releases the per-session semaphore. The
// result must reach the provider exactly once across both requests.
func TestSubagentCompletion_ParentBusyNotWaiting_InjectsAtTurnEnd(t *testing.T) {
	const resultText = "the subagent result"
	provider := newRequestRecordingProvider("parent answer")
	provider.blockFirst = make(chan struct{})
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-busy-nowait"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishUserTurn(t, msgBus, sessionKey)
	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the initial turn never entered the provider")
	}

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: resultText})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "true" {
		t.Fatalf("event metadata[resumed] = %q, want %q (no waiter registered)", got, "true")
	}
	if n := provider.requestCount(); n != 1 {
		t.Fatalf("provider calls = %d while the first turn still holds the semaphore, want 1", n)
	}

	close(provider.blockFirst)

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 2 }) {
		t.Fatal("the queued continuation never ran after the first turn ended")
	}
	first := flattenMessages(provider.request(0))
	second := flattenMessages(provider.request(1))
	if got := strings.Count(first+second, resultText); got != 1 {
		t.Errorf("the result appears %d times across both requests, want exactly 1", got)
	}
	if !strings.Contains(second, resultText) {
		t.Errorf("the continuation request (#2) does not contain the result:\n%s", second)
	}
}

// TestSubagentCompletion_EndOfTurnDoesNotStealUnclaimedResults guards against
// reinstating the end-of-turn sweep that used to mark every terminal subagent of
// a session as delivered. A task that is already terminal but whose completion
// callback has not run yet is NOT owned by any turn boundary: if the turn swept
// it, the callback's claim would lose and the result would be lost forever —
// the same "no resume" bug in a narrower window.
func TestSubagentCompletion_EndOfTurnDoesNotStealUnclaimedResults(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-sweep"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	// A full turn runs and ends while the task is already terminal.
	publishUserTurn(t, msgBus, sessionKey)
	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the initial user turn never reached the provider")
	}
	drainUntil(t, msgBus, 10*time.Second, "parent answer") // turn.end: the turn is over

	// The completion callback lands only now.
	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 2 }) {
		t.Fatal("the turn-boundary sweep stole the unclaimed result: the parent never resumed")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Claim semantics at the publish site
// ──────────────────────────────────────────────────────────────────────────────

// TestSubagentCompletion_RetryAttempts_SingleInjection proves the injector
// accepts at most one delivery per task, so the retry chain can never resume
// the parent twice. (pkg/tools gates the callback so the transient attempt is
// not published at all — TestRunTask_CallbackFiresOnlyForFinalAttempt — and
// this test pins the second line of defence: even if two payloads arrive, only
// the first injects.)
func TestSubagentCompletion_RetryAttempts_SingleInjection(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, _ := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-retry"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "attempt one failed", IsError: true})
	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the successful retry"})

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the parent never resumed after the subagent completed")
	}
	time.Sleep(400 * time.Millisecond)
	if n := provider.requestCount(); n != 1 {
		t.Errorf("provider calls = %d for one task, want exactly 1 continuation turn", n)
	}
}

// TestPublishSubagentAsyncResult_ClaimIsOnceOnly pins the claim as the single
// gate on the inbound: two publications for one task yield one turn, and the
// second still produces the UI event (with resumed=false).
func TestPublishSubagentAsyncResult_ClaimIsOnceOnly(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-once"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})
	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	// First event claims, second loses.
	first := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	second := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if first.Metadata["resumed"] != "true" || second.Metadata["resumed"] != "false" {
		t.Errorf("resumed flags = %q/%q, want true/false", first.Metadata["resumed"], second.Metadata["resumed"])
	}

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the first publication never resumed the parent")
	}
	time.Sleep(400 * time.Millisecond)
	if n := provider.requestCount(); n != 1 {
		t.Errorf("provider calls = %d, want exactly 1", n)
	}
}

// TestPublishSubagentAsyncResult_NilCoordinator_DoesNotInject pins the
// deliberate behaviour change: with no coordinator there is no task registry,
// hence no dedup, so injecting would be unguarded. The UI is still told.
func TestPublishSubagentAsyncResult_NilCoordinator_DoesNotInject(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	al.toolCoordinator = nil

	publishSubagentAsyncResult(al, "session-nil-coordinator", "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "false" {
		t.Errorf("event metadata[resumed] = %q, want %q", got, "false")
	}
	cancel := startResumeLoop(t, al)
	defer cancel()
	time.Sleep(250 * time.Millisecond)
	if n := provider.requestCount(); n != 0 {
		t.Errorf("provider calls = %d without a coordinator, want 0", n)
	}
}

// TestPublishSubagentAsyncResult_UnknownTask_SkipsInjection covers the unknown
// task id (a task evicted between the callback and the publish): the event is
// published with resumed=false and nothing is injected.
func TestPublishSubagentAsyncResult_UnknownTask_SkipsInjection(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	_ = addSubagentTaskForTest(t, al, "subagent-known", "session-unknown")
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishSubagentAsyncResult(al, "session-unknown", "native", "chat-1", "subagent-gone",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "false" {
		t.Errorf("event metadata[resumed] = %q, want %q for an unknown task", got, "false")
	}
	time.Sleep(250 * time.Millisecond)
	if n := provider.requestCount(); n != 0 {
		t.Errorf("provider calls = %d for an unknown task, want 0", n)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// /stop must not resurrect the session
// ──────────────────────────────────────────────────────────────────────────────

// TestStopSession_SealsSubagentResults_NoResurrection is parametrised over all
// three production stop entry points. Each funnels through cancelSessionTree ->
// stopSessionSubagents, which seals every owned task; a completion callback
// arriving afterwards must produce the UI event but NO continuation turn.
func TestStopSession_SealsSubagentResults_NoResurrection(t *testing.T) {
	type stopStep struct {
		name string
		stop func(t *testing.T, al *AgentLoop, msgBus *bus.MessageBus, sessionKey string)
	}
	steps := []stopStep{
		{
			name: "slash_command",
			stop: func(t *testing.T, al *AgentLoop, _ *bus.MessageBus, sessionKey string) {
				if _, handled := al.commandHandler.handleCommand(context.Background(), bus.InboundMessage{
					Channel: "tui", SenderID: "user1", ChatID: "chat-1",
					Content: "/stop", SessionKey: sessionKey,
				}); !handled {
					t.Fatal("/stop was not handled by the command handler")
				}
			},
		},
		{
			name: "inbound_message",
			stop: func(t *testing.T, _ *AgentLoop, msgBus *bus.MessageBus, sessionKey string) {
				if !msgBus.PublishInbound(bus.InboundMessage{
					Channel: "tui", SenderID: "user1", ChatID: "chat-1",
					Content: "/stop", SessionKey: sessionKey,
				}) {
					t.Fatal("/stop inbound publish rejected")
				}
			},
		},
		{
			name: "stop_agent",
			stop: func(t *testing.T, al *AgentLoop, _ *bus.MessageBus, sessionKey string) {
				if resp := al.providable.StopAgent(sessionKey); resp == "" {
					t.Fatal("StopAgent returned an empty response")
				}
			},
		},
	}

	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			provider := newRequestRecordingProvider("should never run")
			al, msgBus := newDurableInboundTestLoop(t, provider)
			const sessionKey = "session-stop"
			sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
			cancel := startResumeLoop(t, al)
			defer cancel()

			step.stop(t, al, msgBus, sessionKey)

			if !waitForTaskClaim(t, sm, "subagent-1", 5*time.Second) {
				t.Fatal("the stop path did not seal the subagent result")
			}

			// The cancelled task's completion callback still fires.
			publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
				&tools.ToolResult{ForLLM: "Task cancelled during execution"})

			ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
			if got := ev.Metadata["resumed"]; got != "false" {
				t.Errorf("event metadata[resumed] = %q, want %q after a stop", got, "false")
			}
			time.Sleep(300 * time.Millisecond)
			if n := provider.requestCount(); n != 0 {
				t.Errorf("provider calls = %d after /stop, want 0 (no resurrection turn)", n)
			}
		})
	}
}
