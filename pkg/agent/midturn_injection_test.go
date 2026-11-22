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

func midTurnTestToolCall(name, id string) providers.ToolCall {
	return providers.ToolCall{
		ID:        id,
		Type:      "function",
		Name:      name,
		Arguments: map[string]interface{}{"path": "/test/file.txt"},
	}
}

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

func (l *midTurnRequestLog) countUserNotices() int {
	return len(l.userNotices())
}

func persistedNoticeMessages(agent *AgentInstance, sessionKey string) []providers.Message {
	var found []providers.Message
	for _, msg := range agent.Sessions.GetHistory(sessionKey) {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, midTurnNoticePrefix) {
			found = append(found, msg)
		}
	}
	return found
}

func withMidTurnQueue(al *AgentLoop, opts processOptions) processOptions {
	opts.midTurnQueue = al.activateMidTurnNotices(opts.SessionKey)
	return opts
}
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

	if len(log.requests) < 2 {
		t.Fatalf("provider calls = %d, want at least 2", len(log.requests))
	}
	for _, msg := range log.requests[1] {
		if strings.HasPrefix(msg.Content, midTurnNoticePrefix) {
			t.Fatalf("the notice was injected at a user-role boundary, producing consecutive user messages: %+v", log.requests[1])
		}
	}

	persisted := persistedNoticeMessages(agent, opts.SessionKey)
	if len(persisted) != 1 {
		t.Fatalf("persisted notices = %d, want 1 (the final-exit check must still deliver it)", len(persisted))
	}
	if count := log.countUserNotices(); count != 1 {
		t.Errorf("the last request carries %d notices, want 1:\n%s", count, log.lastText())
	}
}
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

	if al.enqueueMidTurnNotice(opts.SessionKey, "another result", "subagent-2") {
		t.Error("enqueue succeeded after the loop exited; the queue must be inactive")
	}
}

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
}
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

	if al.enqueueMidTurnNotice(opts.SessionKey, "another result", "subagent-8") {
		t.Error("enqueue succeeded after the safety flush; the queue must be inactive")
	}
	if notices := queue.drain(); len(notices) != 0 {
		t.Errorf("the flushed queue still holds %d notices", len(notices))
	}
}

func TestRunAgentLoop_InjectsNoticeWithoutExtraTurn(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	log := &midTurnRequestLog{}
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

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if msg, ok := al.bus.ConsumeInbound(ctx); ok {
		t.Errorf("an inbound was published for a mid-turn delivered notice: %+v", msg)
	}
}

const midTurnE2ESessionKey = "test-midturn-e2e"

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

	if got := len(requestLog.requests); got != 1 {
		t.Fatalf("provider calls = %d, want 1 (MaxIterations=1)", got)
	}
	if persisted := persistedNoticeMessages(agent, sessionKey); len(persisted) != 0 {
		t.Fatalf("persisted %d notices inside a turn that had no budget left: %+v", len(persisted), persisted)
	}

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

	if al.enqueueMidTurnNotice(sessionKey, "another result", "subagent-2") {
		t.Error("enqueue succeeded after the turn ended; the queue must be inactive")
	}
}

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
				al.setSessionAlias(baseKey, remappedKey)
				publishSubagentAsyncResult(al, originalKey, "native", "chat-1", "subagent-1",
					&tools.ToolResult{ForLLM: "pre-remap subagent result"})
				publishSubagentAsyncResult(al, remappedKey, "native", "chat-2", "subagent-2",
					&tools.ToolResult{ForLLM: "post-remap subagent result"})
			}
			return &providers.LLMResponse{Content: "the parent answer", ToolCalls: []providers.ToolCall{}}, nil
		},
	}
	al, msgBus = newDurableInboundTestLoop(t, provider)
	addSubagentTaskForTest(t, al, "subagent-1", originalKey)
	addSubagentTaskForTest(t, al, "subagent-2", remappedKey)

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
