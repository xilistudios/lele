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

type requestRecordingProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	response string

	blockFirst  chan struct{}
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

func waitForTaskClaim(t *testing.T, sm *tools.SubagentManager, taskID string, within time.Duration) bool {
	t.Helper()
	return waitFor(t, within, func() bool {
		task, ok := sm.GetTask(taskID)
		return ok && task != nil && task.ResultClaimed()
	})
}

func TestSubagentCompletion_ParentIdle_ResumesLoop(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-idle"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishUserTurn(t, msgBus, sessionKey)
	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the initial user turn never reached the provider")
	}
	drainUntil(t, msgBus, 10*time.Second, "parent answer")

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 2 }) {
		t.Fatalf("the parent loop never resumed after the subagent completed (provider calls = %d, want >= 2)", provider.requestCount())
	}
	if text := provider.lastRequestText(); !strings.Contains(text, "the subagent result") {
		t.Errorf("the continuation request does not contain the subagent result:\n%s", text)
	}
}

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

func TestSubagentCompletion_ClaimLost_StillPublishesEventWithoutResume(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-consumed"
	sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

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

	time.Sleep(250 * time.Millisecond)
	if n := provider.requestCount(); n != 0 {
		t.Errorf("provider calls = %d after the AM1 claim, want 0 (the waiter owns the result)", n)
	}
}

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

func TestSubagentCompletion_EndOfTurnDoesNotStealUnclaimedResults(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-sweep"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)
	cancel := startResumeLoop(t, al)
	defer cancel()

	publishUserTurn(t, msgBus, sessionKey)
	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 1 }) {
		t.Fatal("the initial user turn never reached the provider")
	}
	drainUntil(t, msgBus, 10*time.Second, "parent answer")

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	if !waitFor(t, 10*time.Second, func() bool { return provider.requestCount() >= 2 }) {
		t.Fatal("the turn-boundary sweep stole the unclaimed result: the parent never resumed")
	}
}

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

func recvInbound(t *testing.T, msgBus *bus.MessageBus, within time.Duration) (bus.InboundMessage, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	return msgBus.ConsumeInbound(ctx)
}

func assertNoInbound(t *testing.T, msgBus *bus.MessageBus, what string) {
	t.Helper()
	if msg, ok := recvInbound(t, msgBus, 200*time.Millisecond); ok {
		t.Fatalf("%s: an inbound was published anyway: %+v", what, msg)
	}
}

func TestPublishSubagentAsyncResult_ParentIdle_PublishesInbound(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-midturn-idle"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "true" {
		t.Errorf("event metadata[resumed] = %q, want %q", got, "true")
	}
	if got := ev.Metadata["injected"]; got != "" {
		t.Errorf("event metadata[injected] = %q, want it absent on the idle path", got)
	}

	msg, ok := recvInbound(t, msgBus, 2*time.Second)
	if !ok {
		t.Fatal("no inbound was published for an idle parent: the parent would never resume")
	}
	if msg.Content != "the subagent result" {
		t.Errorf("inbound content = %q, want the RAW result text", msg.Content)
	}
	if msg.Channel != "system" || msg.SenderID != midTurnNoticeSender {
		t.Errorf("inbound = channel %q sender %q, want system/%s", msg.Channel, msg.SenderID, midTurnNoticeSender)
	}
	if msg.SessionKey != sessionKey {
		t.Errorf("inbound session key = %q, want %q", msg.SessionKey, sessionKey)
	}
	if got := msg.Metadata["task_id"]; got != "subagent-1" {
		t.Errorf("inbound task_id = %q, want %q", got, "subagent-1")
	}
}

func TestPublishSubagentAsyncResult_MidTurn_InjectsInsteadOfInbound(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-midturn-busy"
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)

	queue := al.activateMidTurnNotices(sessionKey)

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "true" {
		t.Errorf("event metadata[resumed] = %q, want %q (the claim was uncontested)", got, "true")
	}
	if got := ev.Metadata["injected"]; got != "true" {
		t.Errorf("event metadata[injected] = %q, want %q", got, "true")
	}

	assertNoInbound(t, msgBus, "mid-turn injection")

	notices := queue.drain()
	if len(notices) != 1 {
		t.Fatalf("queued notices = %d, want 1", len(notices))
	}
	if notices[0].content != "the subagent result" {
		t.Errorf("queued content = %q, want the raw result text", notices[0].content)
	}
	if got, want := notices[0].messageContent(), "[System: subagent] the subagent result"; got != want {
		t.Errorf("messageContent() = %q, want %q", got, want)
	}
	if notices[0].taskID != "subagent-1" {
		t.Errorf("queued task id = %q, want %q", notices[0].taskID, "subagent-1")
	}
}

func TestPublishSubagentAsyncResult_ClaimLost_NeverEnqueues(t *testing.T) {
	provider := newRequestRecordingProvider("parent answer")
	al, msgBus := newDurableInboundTestLoop(t, provider)
	const sessionKey = "session-midturn-consumed"
	sm := addSubagentTaskForTest(t, al, "subagent-1", sessionKey)

	if sm.ClaimResult("subagent-1") {
		t.Fatal("ClaimResult lost on a fresh task")
	}
	queue := al.activateMidTurnNotices(sessionKey)

	publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
		&tools.ToolResult{ForLLM: "the subagent result"})

	ev := waitForOutboundEvent(t, msgBus, "subagent.result", 5*time.Second)
	if got := ev.Metadata["resumed"]; got != "false" {
		t.Errorf("event metadata[resumed] = %q, want %q", got, "false")
	}
	if got := ev.Metadata["injected"]; got != "" {
		t.Errorf("event metadata[injected] = %q, want it absent for a lost claim", got)
	}
	assertNoInbound(t, msgBus, "lost claim")
	if notices := queue.drain(); len(notices) != 0 {
		t.Fatalf("queued notices = %d after a lost claim, want 0: the waiter owns the result", len(notices))
	}
}
