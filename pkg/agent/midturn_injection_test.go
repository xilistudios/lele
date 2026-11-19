// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// Mid-turn injection inside the LLM loop.
//
// The bug: a subagent completion published as an inbound only ran after the
// parent's turn released the per-session semaphore, so the parent always
// learned about it too late. With the notice queue active, runLLMIteration has
// two injection points — the top of each iteration (role-safe boundary) and the
// final-exit check before breaking — so the model reacts inside the SAME turn.
// Anything the loop never reached is flushed back to the inbound bus by the
// safety defer in runAgentLoop.
//
// The loop drains through the queue pointer the turn CAPTURED at activation
// (processOptions.midTurnQueue), never by re-resolving the session key, so a
// /new alias remap mid-turn cannot split producer and consumer across two
// queues.
// ============================================================================

// midTurnTestToolCall is the tool call the scripted provider returns on its
// first call: any registered tool works, the point is closing an iteration with
// a tool result so the next boundary is role-safe.
func midTurnTestToolCall(name, id string) providers.ToolCall {
	return providers.ToolCall{
		ID:        id,
		Type:      "function",
		Name:      name,
		Arguments: map[string]interface{}{"path": "/test/file.txt"},
	}
}

// collectRequests wraps the scripted provider so a test can assert on exactly
// what each provider call received.
type midTurnRequestLog struct {
	requests [][]providers.Message
}

func (l *midTurnRequestLog) record(messages []providers.Message) {
	l.requests = append(l.requests, append([]providers.Message(nil), messages...))
}

func (l *midTurnRequestLog) lastText() string {
	if len(l.requests) == 0 {
		return ""
	}
	return flattenMessages(l.requests[len(l.requests)-1])
}

// userNotices returns the text of every mid-turn notice the last request
// carried, i.e. the persisted rendering of the drained subagent completions.
func (l *midTurnRequestLog) userNotices() []string {
	if len(l.requests) == 0 {
		return nil
	}
	var found []string
	for _, msg := range l.requests[len(l.requests)-1] {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, midTurnNoticePrefix) {
			found = append(found, msg.Content)
		}
	}
	return found
}

// countUserNotices returns how many messages of the last request are the
// persisted rendering of a mid-turn subagent notice.
func (l *midTurnRequestLog) countUserNotices() int {
	return len(l.userNotices())
}

// persistedNoticeMessages returns the session messages that carry the
// synthetic subagent marker.
func persistedNoticeMessages(agent *AgentInstance, sessionKey string) []providers.Message {
	var found []providers.Message
	for _, msg := range agent.Sessions.GetHistory(sessionKey) {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, midTurnNoticePrefix) {
			found = append(found, msg)
		}
	}
	return found
}

// withMidTurnQueue activates the session's notice queue and returns a copy of
// opts carrying the queue the turn captured, which is what runAgentLoop does
// right before entering the LLM loop. Tests that drive runLLMIteration directly
// must do the same: the loop drains through the captured pointer and never
// re-resolves the session key.
func withMidTurnQueue(al *AgentLoop, opts processOptions) processOptions {
	opts.midTurnQueue = al.activateMidTurnNotices(opts.SessionKey)
	return opts
} // TestRunLLMIteration_InjectsNoticeAtIterationBoundary is drain point (a): a
// notice queued while the turn runs must reach the model at the next iteration
// boundary, i.e. in the request that follows the tool execution.
func TestRunLLMIteration_InjectsNoticeAtIterationBoundary(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("File contents: test data")
		},
	})

	log := &midTurnRequestLog{}
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			log.record(messages)
			if len(log.requests) == 1 {
				// Iteration 1: call a tool, so iteration 2 opens on a
				// role-safe boundary (last message is a tool result).
				return &providers.LLMResponse{
					ToolCalls: []providers.ToolCall{midTurnTestToolCall("read_file", "call_1")},
				}, nil
			}
			return &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}, nil
		},
	}

	opts := withMidTurnQueue(al, processOptions{
		SessionKey:   "test-midturn-boundary",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	})

	// The turn is running: the queue is active and a subagent finished.
	if !al.enqueueMidTurnNotice(opts.SessionKey, "the subagent result", "subagent-1") {
		t.Fatal("enqueue on the active queue returned false")
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Read the file"},
	}
	content, iterations, err := runner.runLLMIteration(context.Background(), agent, messages, opts)
	if err != nil {
		t.Fatalf("runLLMIteration returned error: %v", err)
	}
	if content != "done" {
		t.Errorf("final content = %q, want %q", content, "done")
	}
	if iterations != 2 {
		t.Fatalf("iterations = %d, want 2 (tool call + answer)", iterations)
	}

	if count := log.countUserNotices(); count != 1 {
		t.Fatalf("the second request carries %d subagent notices, want 1:\n%s", count, log.lastText())
	}
	if !strings.Contains(log.lastText(), "the subagent result") {
		t.Errorf("the injected notice does not carry the result text:\n%s", log.lastText())
	}

	persisted := persistedNoticeMessages(agent, opts.SessionKey)
	if len(persisted) != 1 {
		t.Fatalf("persisted notices = %d, want 1", len(persisted))
	}
	want := "[System: subagent] the subagent result"
	if persisted[0].Content != want {
		t.Errorf("persisted notice = %q, want %q", persisted[0].Content, want)
	}
	if notices := opts.midTurnQueue.drain(); len(notices) != 0 {
		t.Errorf("queue still holds %d notices after injection, want 0", len(notices))
	}
}

// TestRunLLMIteration_NoticeWaitsForRoleSafeBoundary is the negative half of
// drain point (a): when the previous message is already a user message (here:
// the empty-response retry prompt), the notice must stay queued instead of
// producing two consecutive user messages the providers reject.
func TestRunLLMIteration_NoticeWaitsForRoleSafeBoundary(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	log := &midTurnRequestLog{}
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			log.record(messages)
			if len(log.requests) == 1 {
				// Empty response: the loop appends an assistant blank plus a
				// user retry prompt, making the next boundary user-role.
				return &providers.LLMResponse{Content: "  ", ToolCalls: []providers.ToolCall{}}, nil
			}
			return &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}, nil
		},
	}

	opts := withMidTurnQueue(al, processOptions{
		SessionKey:   "test-midturn-role-safe",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	})

	if !al.enqueueMidTurnNotice(opts.SessionKey, "the subagent result", "subagent-1") {
		t.Fatal("enqueue on the active queue returned false")
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Do the work"},
	}
	if _, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts); err != nil {
		t.Fatalf("runLLMIteration returned error: %v", err)
	}

	// Iteration 2 opened on a user message (the retry prompt), so the notice
	// had to stay queued instead of being appended right behind it.
	if len(log.requests) < 2 {
		t.Fatalf("provider calls = %d, want at least 2", len(log.requests))
	}
	for _, msg := range log.requests[1] {
		if strings.HasPrefix(msg.Content, midTurnNoticePrefix) {
			t.Fatalf("the notice was injected at a user-role boundary, producing consecutive user messages: %+v", log.requests[1])
		}
	}

	// ... but the final-exit check still delivered it in the same turn, right
	// behind the assistant answer.
	persisted := persistedNoticeMessages(agent, opts.SessionKey)
	if len(persisted) != 1 {
		t.Fatalf("persisted notices = %d, want 1 (the final-exit check must still deliver it)", len(persisted))
	}
	if count := log.countUserNotices(); count != 1 {
		t.Errorf("the last request carries %d notices, want 1:\n%s", count, log.lastText())
	}
} // TestRunLLMIteration_FinalExitCheckDeliversInsideSameTurn is drain point (b):
// the model produced a final answer, but a result arrived while it was
// thinking. The loop must run ONE more iteration inside the same call and hand
// the notice to the model there, instead of ending the turn and letting the
// inbound resume it later.
func TestRunLLMIteration_FinalExitCheckDeliversInsideSameTurn(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	log := &midTurnRequestLog{}
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			log.record(messages)
			return &providers.LLMResponse{Content: "an answer", ToolCalls: []providers.ToolCall{}}, nil
		},
	}

	opts := withMidTurnQueue(al, processOptions{
		SessionKey:   "test-midturn-final-exit",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	})

	if !al.enqueueMidTurnNotice(opts.SessionKey, "the subagent result", "subagent-1") {
		t.Fatal("enqueue on the active queue returned false")
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Do the work"},
	}
	content, iterations, err := runner.runLLMIteration(context.Background(), agent, messages, opts)
	if err != nil {
		t.Fatalf("runLLMIteration returned error: %v", err)
	}
	if content != "an answer" {
		t.Errorf("final content = %q, want %q", content, "an answer")
	}
	if iterations != 2 {
		t.Fatalf("iterations = %d, want 2 (the extra iteration reacts to the notice)", iterations)
	}
	if count := log.countUserNotices(); count != 1 {
		t.Fatalf("the extra request carries %d subagent notices, want 1:\n%s", count, log.lastText())
	}

	persisted := persistedNoticeMessages(agent, opts.SessionKey)
	if len(persisted) != 1 {
		t.Fatalf("persisted notices = %d, want 1", len(persisted))
	}

	// The exit check deactivated the queue: the next completion must take the
	// inbound path again.
	if al.enqueueMidTurnNotice(opts.SessionKey, "another result", "subagent-2") {
		t.Error("enqueue succeeded after the loop exited; the queue must be inactive")
	}
}

// TestRunLLMIteration_BatchBecomesOneUserMessage pins the batch merge: two (or
// more) completions drained at the same boundary must be persisted as ONE user
// message. Appending one message per notice would put two consecutive user
// messages in the live context — rejected by Bedrock Converse with a 400 — and
// the same breakage is persisted, so it would replay on every later turn.
func TestRunLLMIteration_BatchBecomesOneUserMessage(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Tools.Register(&llmRunnerMockCustomTool{
		name: "read_file",
		executeFunc: func(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
			return tools.SilentResult("File contents: test data")
		},
	})

	log := &midTurnRequestLog{}
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			log.record(messages)
			if len(log.requests) == 1 {
				// Close the first iteration with a tool call so the next
				// boundary is role-safe (last message is a tool result).
				return &providers.LLMResponse{
					ToolCalls: []providers.ToolCall{midTurnTestToolCall("read_file", "call_1")},
				}, nil
			}
			return &providers.LLMResponse{Content: "done", ToolCalls: []providers.ToolCall{}}, nil
		},
	}

	opts := withMidTurnQueue(al, processOptions{
		SessionKey:   "test-midturn-batch",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	})

	// Two subagents finish while the first iteration runs.
	if !al.enqueueMidTurnNotice(opts.SessionKey, "first subagent result", "subagent-1") {
		t.Fatal("enqueue of the first notice returned false")
	}
	if !al.enqueueMidTurnNotice(opts.SessionKey, "second subagent result", "subagent-2") {
		t.Fatal("enqueue of the second notice returned false")
	}

	messages := []providers.Message{
		{Role: "system", Content: "System prompt"},
		{Role: "user", Content: "Read the file"},
	}
	if _, _, err := runner.runLLMIteration(context.Background(), agent, messages, opts); err != nil {
		t.Fatalf("runLLMIteration returned error: %v", err)
	}

	notices := log.userNotices()
	if len(notices) != 1 {
		t.Fatalf("the second request carries %d notice messages, want exactly 1 (batch merge):\n%s",
			len(notices), log.lastText())
	}
	want := midTurnNoticePrefix + "first subagent result" + midTurnNoticeSeparator + "second subagent result"
	if notices[0] != want {
		t.Errorf("merged notice = %q, want %q", notices[0], want)
	}

	persisted := persistedNoticeMessages(agent, opts.SessionKey)
	if len(persisted) != 1 {
		t.Fatalf("persisted notices = %d, want 1 (the batch is one message)", len(persisted))
	}
	if persisted[0].Content != want {
		t.Errorf("persisted notice = %q, want %q", persisted[0].Content, want)
	}
	if !strings.HasPrefix(persisted[0].Content, midTurnNoticePrefix) {
		t.Errorf("persisted notice does not start with the synthetic marker: %q", persisted[0].Content)
	}
} // TestRunAgentLoop_AbnormalExitFlushesNoticesToInbound is the safety net: the
// turn dies (provider error) with a notice still queued, and the deferred flush
// must hand it back to the inbound bus so a later turn can pick it up. Losing it
// would mean the claimed result is never delivered anywhere.
func TestRunAgentLoop_AbnormalExitFlushesNoticesToInbound(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = &failingMockProvider{err: errors.New("invalid request format: mid-turn flush test")}

	opts := processOptions{
		SessionKey:      "test-midturn-flush",
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Do the work",
		DefaultResponse: "default",
		SendResponse:    false,
	}

	if al.enqueueMidTurnNotice(opts.SessionKey, "the subagent result", "subagent-7") {
		t.Fatal("enqueue on an inactive queue returned true; the queue must only accept while a turn runs")
	}
	queue := al.activateMidTurnNotices(opts.SessionKey)
	if !al.enqueueMidTurnNotice(opts.SessionKey, "the subagent result", "subagent-7") {
		t.Fatal("enqueue on the active queue returned false")
	}

	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err == nil {
		t.Fatal("runAgentLoop succeeded with a failing provider, want an error")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	msg, ok := al.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("no inbound was published; the queued notice was lost on the abnormal exit")
	}
	if msg.Channel != "system" || msg.SenderID != midTurnNoticeSender {
		t.Errorf("flushed inbound = channel %q sender %q, want system/%s", msg.Channel, msg.SenderID, midTurnNoticeSender)
	}
	if msg.Content != "the subagent result" {
		t.Errorf("flushed content = %q, want the RAW result text (processSystemMessage adds the prefix)", msg.Content)
	}
	if msg.SessionKey != opts.SessionKey {
		t.Errorf("flushed session key = %q, want %q", msg.SessionKey, opts.SessionKey)
	}
	if got := msg.Metadata["task_id"]; got != "subagent-7" {
		t.Errorf("flushed task_id = %q, want %q", got, "subagent-7")
	}
	if got, want := msg.ChatID, "test-channel:test-chat-id"; got != want {
		t.Errorf("flushed chat id = %q, want %q (the origin coordinates the idle path uses)", got, want)
	}

	// The queue must be inert after the flush, or the result would be
	// delivered twice.
	if al.enqueueMidTurnNotice(opts.SessionKey, "another result", "subagent-8") {
		t.Error("enqueue succeeded after the safety flush; the queue must be inactive")
	}
	if notices := queue.drain(); len(notices) != 0 {
		t.Errorf("the flushed queue still holds %d notices", len(notices))
	}
}

// TestRunAgentLoop_InjectsNoticeWithoutExtraTurn proves the end-to-end shape of
// the fix on the entry point the inbound path uses: the notice is delivered by
// the turn that was ALREADY running (one runAgentLoop call, two provider
// calls), and no inbound is published for it.
func TestRunAgentLoop_InjectsNoticeWithoutExtraTurn(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	log := &midTurnRequestLog{}
	// The notice is enqueued DURING the first provider call, which is exactly
	// the real timing: the subagent finishes while the parent waits for the
	// model. The second call is where it must be visible.
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			log.record(messages)
			if len(log.requests) == 1 {
				if !al.enqueueMidTurnNotice(midTurnE2ESessionKey, "the subagent result", "subagent-1") {
					t.Error("enqueue during the running turn returned false; the parent is mid-turn")
				}
			}
			return &providers.LLMResponse{Content: "an answer", ToolCalls: []providers.ToolCall{}}, nil
		},
	}

	opts := processOptions{
		SessionKey:      midTurnE2ESessionKey,
		Channel:         "test-channel",
		ChatID:          "test-chat-id",
		UserMessage:     "Do the work",
		DefaultResponse: "default",
		SendResponse:    false,
	}

	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned error: %v", err)
	}

	if len(log.requests) != 2 {
		t.Fatalf("provider calls = %d, want 2 (the same turn reacts to the notice)", len(log.requests))
	}
	if count := log.countUserNotices(); count != 1 {
		t.Fatalf("the second request carries %d notices, want 1:\n%s", count, log.lastText())
	}

	// No inbound: the running turn owns delivery.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if msg, ok := al.bus.ConsumeInbound(ctx); ok {
		t.Errorf("an inbound was published for a mid-turn delivered notice: %+v", msg)
	}
}

// midTurnE2ESessionKey is the session key shared by the provider callback above
// and its test.
const midTurnE2ESessionKey = "test-midturn-e2e" // ──────────────────────────────────────────────────────────────────────────────
// Producer-driven regression tests
//
// These drive completions through the REAL producer
// (publishSubagentAsyncResult with a claimed task), so the claim, the queue
// decision, the UI event and the inbound fallback are all exercised as in
// production. Assertions stick to observable state: provider requests,
// persisted history and bus inbound — never private queue fields.
// ──────────────────────────────────────────────────────────────────────────────

// TestRunAgentLoop_IterationLimitFallsBackToInbound pins the iteration-budget
// guard on the final-exit check. With MaxIterations=1 the assistant answer IS
// the last iteration, so the loop must NOT drain the notice there (the
// `continue` would re-test the loop condition, exit, and leave the notice
// persisted with no turn left to react to it). It must leave the notice queued
// so the flush defer re-publishes it as inbound and a continuation turn runs.
func TestRunAgentLoop_IterationLimitFallsBackToInbound(t *testing.T) {
	const sessionKey = "session-midturn-budget"

	var (
		al     *AgentLoop
		msgBus *bus.MessageBus
	)
	requestLog := &midTurnRequestLog{}
	provider := &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			requestLog.record(messages)
			if len(requestLog.requests) == 1 {
				// The subagent finishes while the parent waits for the answer.
				publishSubagentAsyncResult(al, sessionKey, "native", "chat-1", "subagent-1",
					&tools.ToolResult{ForLLM: "the subagent result"})
			}
			return &providers.LLMResponse{Content: "the parent answer", ToolCalls: []providers.ToolCall{}}, nil
		},
	}
	al, msgBus = newDurableInboundTestLoop(t, provider)
	addSubagentTaskForTest(t, al, "subagent-1", sessionKey)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("registry returned no default agent")
	}
	// One iteration only: the final answer exhausts the budget.
	agent.MaxIterations = 1

	if got := provider.callCount; got != 0 {
		t.Fatalf("provider called %d times before the turn started", got)
	}

	opts := processOptions{
		SessionKey:      sessionKey,
		Channel:         "native",
		ChatID:          "chat-1",
		UserMessage:     "Do the work",
		DefaultResponse: "default",
		SendResponse:    false,
	}
	if _, err := newLLMRunner(al).runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned error: %v", err)
	}

	// The budget was spent: exactly one provider call, and the notice was NOT
	// persisted mid-turn (there was no iteration left to react to it).
	if got := len(requestLog.requests); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (MaxIterations=1)", got)
	}
	if persisted := persistedNoticeMessages(agent, sessionKey); len(persisted) != 0 {
		t.Fatalf("persisted %d notices inside a turn that had no budget left: %+v", len(persisted), persisted)
	}

	// ... and it came back through the ordinary inbound so a continuation turn
	// can deliver it. The content is raw: processSystemMessage adds the prefix.
	msg, ok := recvInbound(t, msgBus, 2*time.Second)
	if !ok {
		t.Fatal("no inbound was published; the notice was lost when the iteration budget ran out")
	}
	if msg.Channel != "system" || msg.SenderID != midTurnNoticeSender {
		t.Errorf("fallback inbound = channel %q sender %q, want system/%s", msg.Channel, msg.SenderID, midTurnNoticeSender)
	}
	if msg.Content != "the subagent result" {
		t.Errorf("fallback content = %q, want the RAW result text", msg.Content)
	}
	if msg.SessionKey != sessionKey {
		t.Errorf("fallback session key = %q, want %q", msg.SessionKey, sessionKey)
	}

	// The queue is inert afterwards: nothing can be delivered twice.
	if al.enqueueMidTurnNotice(sessionKey, "another result", "subagent-2") {
		t.Error("enqueue succeeded after the turn ended; the queue must be inactive")
	}
}

// TestRunAgentLoop_RemapMidTurn_NeverStrandsNotice pins the captured-identity
// rule across a /new remap. The turn activates the queue of the key the alias
// pointed at when it started; if the alias is remapped while the turn runs, the
// producer either still reaches that (active) queue — mid-turn delivery — or
// finds the post-remap key's inactive queue and publishes the inbound fallback.
// Both deliver; neither strands.
func TestRunAgentLoop_RemapMidTurn_NeverStrandsNotice(t *testing.T) {
	const (
		baseKey     = "session-midturn-remap"
		originalKey = baseKey + ":chat:1"
		remappedKey = baseKey + ":chat:2"
	)

	var (
		al     *AgentLoop
		msgBus *bus.MessageBus
	)
	requestLog := &midTurnRequestLog{}
	provider := &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			requestLog.record(messages)
			if len(requestLog.requests) == 1 {
				// "/new" mid-turn: the base key now points at a fresh key.
				al.setSessionAlias(baseKey, remappedKey)
				// A subagent whose origin is the pre-remap key still reaches
				// the queue this turn captured (its key is no longer aliased).
				publishSubagentAsyncResult(al, originalKey, "native", "chat-1", "subagent-1",
					&tools.ToolResult{ForLLM: "pre-remap subagent result"})
				// A subagent reporting on the NEW key finds an inactive queue
				// and therefore takes the inbound fallback.
				publishSubagentAsyncResult(al, remappedKey, "native", "chat-2", "subagent-2",
					&tools.ToolResult{ForLLM: "post-remap subagent result"})
			}
			return &providers.LLMResponse{Content: "the parent answer", ToolCalls: []providers.ToolCall{}}, nil
		},
	}
	al, msgBus = newDurableInboundTestLoop(t, provider)
	addSubagentTaskForTest(t, al, "subagent-1", originalKey)
	addSubagentTaskForTest(t, al, "subagent-2", remappedKey)

	// Before the turn starts the alias resolves the base key to chat:1.
	al.setSessionAlias(baseKey, originalKey)

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("registry returned no default agent")
	}

	opts := processOptions{
		SessionKey:      baseKey,
		Channel:         "native",
		ChatID:          "chat-1",
		UserMessage:     "Do the work",
		DefaultResponse: "default",
		SendResponse:    false,
	}
	if _, err := newLLMRunner(al).runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned error: %v", err)
	}

	// The notice enqueued into the captured queue is delivered by THIS turn.
	if got := len(requestLog.requests); got != 2 {
		t.Fatalf("provider calls = %d, want 2 (the same turn reacts to the pre-remap notice)", got)
	}
	notices := requestLog.userNotices()
	if len(notices) != 1 || !strings.Contains(notices[0], "pre-remap subagent result") {
		t.Fatalf("the extra request carries %v, want the pre-remap result:\n%s", notices, requestLog.lastText())
	}
	if strings.Contains(requestLog.lastText(), "post-remap subagent result") {
		t.Errorf("the post-remap notice was injected into the old turn:\n%s", requestLog.lastText())
	}

	// The post-remap notice was NOT stranded: it arrives as the normal inbound
	// fallback, which is what resumes the remapped conversation.
	msg, ok := recvInbound(t, msgBus, 2*time.Second)
	if !ok {
		t.Fatal("the post-remap notice was neither injected nor published: it is stranded")
	}
	if msg.Content != "post-remap subagent result" {
		t.Errorf("fallback content = %q, want the post-remap result text", msg.Content)
	}
	if msg.SessionKey != remappedKey {
		t.Errorf("fallback session key = %q, want %q", msg.SessionKey, remappedKey)
	}
	if extra, ok := recvInbound(t, msgBus, 200*time.Millisecond); ok {
		t.Errorf("an unexpected second inbound was published: %+v", extra)
	}
}
