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

// countUserNotices returns how many messages of the last request are the
// persisted rendering of a mid-turn subagent notice.
func (l *midTurnRequestLog) countUserNotices() int {
	if len(l.requests) == 0 {
		return 0
	}
	count := 0
	for _, msg := range l.requests[len(l.requests)-1] {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, midTurnNoticePrefix) {
			count++
		}
	}
	return count
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

// TestRunLLMIteration_InjectsNoticeAtIterationBoundary is drain point (a): a
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

	opts := processOptions{
		SessionKey:   "test-midturn-boundary",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	// The turn is running: the queue is active and a subagent finished.
	al.activateMidTurnNotices(opts.SessionKey)
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
	if notices := al.drainMidTurnNotices(opts.SessionKey); len(notices) != 0 {
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

	opts := processOptions{
		SessionKey:   "test-midturn-role-safe",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	al.activateMidTurnNotices(opts.SessionKey)
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
}

// TestRunLLMIteration_FinalExitCheckDeliversInsideSameTurn is drain point (b):
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

	opts := processOptions{
		SessionKey:   "test-midturn-final-exit",
		Channel:      "test-channel",
		ChatID:       "test-chat-id",
		SendResponse: false,
	}

	al.activateMidTurnNotices(opts.SessionKey)
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

// TestRunAgentLoop_AbnormalExitFlushesNoticesToInbound is the safety net: the
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

	al.activateMidTurnNotices(opts.SessionKey)
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
const midTurnE2ESessionKey = "test-midturn-e2e"
