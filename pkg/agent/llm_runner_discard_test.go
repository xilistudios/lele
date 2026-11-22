package agent

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

type discardScriptedProvider struct {
	script []*providers.LLMResponse

	mu        sync.Mutex
	callCount int
}

func (p *discardScriptedProvider) nextCall() (*providers.LLMResponse, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.callCount
	p.callCount++
	if idx >= len(p.script) {
		return &providers.LLMResponse{Content: "script exhausted"}, idx
	}
	return p.script[idx], idx
}

func (p *discardScriptedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

func (p *discardScriptedProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	response, _ := p.nextCall()
	return response, nil
}

func (p *discardScriptedProvider) ChatStream(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]interface{}, onChunk func(chunk string, done bool),
	onReasoning func(reasoningChunk string)) (*providers.LLMResponse, error) {
	response, idx := p.nextCall()
	label := idx + 1
	if onReasoning != nil {
		onReasoning(fmt.Sprintf("think-%d", label))
	}
	if onChunk != nil {
		onChunk(fmt.Sprintf("draft-%d", label), false)
	}
	return response, nil
}

func (p *discardScriptedProvider) GetDefaultModel() string { return "mock-model" }

func outboundLabel(msg bus.OutboundMessage) string {
	if msg.Event == "" {
		return "final:" + msg.MessageID
	}
	return msg.Event + ":" + msg.MessageID
}

func indexOfOutbound(msgs []bus.OutboundMessage, label string) int {
	for i := range msgs {
		if outboundLabel(msgs[i]) == label {
			return i
		}
	}
	return -1
}

func discards(msgs []bus.OutboundMessage) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for _, msg := range msgs {
		if msg.Event == "message.discard" {
			out = append(out, msg)
		}
	}
	return out
}

func discardTestOptions(sessionKey string) processOptions {
	return processOptions{
		SessionKey:      sessionKey,
		Channel:         channels.ChannelName,
		ChatID:          "test-chat-id",
		UserMessage:     "Hi",
		DefaultResponse: "Default fallback",
		SendResponse:    true,
		MessageID:       "m1",
	}
}

func TestRunAgentLoop_EmptyResponsePublishesMessageDiscard(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: "", ToolCalls: []providers.ToolCall{}},
		{Content: "   ", ToolCalls: []providers.ToolCall{}},
		{Content: "final answer", ToolCalls: []providers.ToolCall{}},
	}}
	agent.Provider = provider

	opts := discardTestOptions(sessionKey)
	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned an error: %v", err)
	}

	if got, want := provider.calls(), 3; got != want {
		t.Fatalf("provider calls = %d, want %d", got, want)
	}

	msgs := drainOutbound(al.bus)
	got := discards(msgs)
	if len(got) != 2 {
		t.Fatalf("message.discard count = %d, want 2 (one per blank iteration); events: %v",
			len(got), labelsOf(msgs))
	}

	wantIDs := []string{"m1", "m1-2"}
	for i, want := range wantIDs {
		msg := got[i]
		if msg.MessageID != want {
			t.Errorf("discard[%d] message_id = %q, want %q", i, msg.MessageID, want)
		}
		if msg.Metadata["reason"] != "empty_response" {
			t.Errorf("discard[%d] reason = %q, want empty_response", i, msg.Metadata["reason"])
		}
		if msg.Channel != channels.ChannelName {
			t.Errorf("discard[%d] channel = %q, want %q", i, msg.Channel, channels.ChannelName)
		}
		if msg.ChatID != opts.ChatID {
			t.Errorf("discard[%d] chat_id = %q, want %q", i, msg.ChatID, opts.ChatID)
		}
		if msg.Content != "" {
			t.Errorf("discard[%d] content = %q, want empty (nothing of the iteration survives)", i, msg.Content)
		}
	}

	order := []string{
		"message.stream:m1",
		"message.discard:m1",
		"message.thinking:m1-2",
		"message.discard:m1-2",
		"message.thinking:m1-3",
		"message.stream:m1-3",
		"final:m1-3",
	}
	prev := -1
	for _, label := range order {
		idx := indexOfOutbound(msgs, label)
		if idx < 0 {
			t.Fatalf("%s was never published; events: %v", label, labelsOf(msgs))
		}
		if idx <= prev {
			t.Fatalf("%s is out of order (index %d after %d); events: %v", label, idx, prev, labelsOf(msgs))
		}
		prev = idx
	}
}

func TestRunAgentLoop_InvalidToolCallsDiscardWhenContentEmpty(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: "", ToolCalls: []providers.ToolCall{{ID: "call-1"}}},
		{Content: "final answer", ToolCalls: []providers.ToolCall{}},
	}}
	agent.Provider = provider

	opts := discardTestOptions(sessionKey)
	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned an error: %v", err)
	}

	msgs := drainOutbound(al.bus)
	got := discards(msgs)
	if len(got) != 1 {
		t.Fatalf("message.discard count = %d, want 1; events: %v", len(got), labelsOf(msgs))
	}
	if got[0].MessageID != "m1" {
		t.Errorf("discard message_id = %q, want m1", got[0].MessageID)
	}
	if got[0].Metadata["reason"] != "invalid_tool_calls" {
		t.Errorf("discard reason = %q, want invalid_tool_calls", got[0].Metadata["reason"])
	}
}

type discardFakeTool struct{}

func (discardFakeTool) Name() string                       { return "discard_fake_tool" }
func (discardFakeTool) Description() string                { return "test tool" }
func (discardFakeTool) Parameters() map[string]interface{} { return nil }
func (discardFakeTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: ""}
}

func TestRunAgentLoop_PlainTextToolCallDiscardsPublished(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10
	agent.Tools.Register(discardFakeTool{})

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: `discard_fake_tool{"path":"/tmp/x"}`, ToolCalls: []providers.ToolCall{}},
		{Content: "final answer", ToolCalls: []providers.ToolCall{}},
	}}
	agent.Provider = provider

	opts := discardTestOptions(sessionKey)
	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned an error: %v", err)
	}

	msgs := drainOutbound(al.bus)
	got := discards(msgs)
	if len(got) != 1 {
		t.Fatalf("message.discard count = %d, want 1; events: %v", len(got), labelsOf(msgs))
	}
	if got[0].MessageID != "m1" {
		t.Errorf("discard message_id = %q, want m1", got[0].MessageID)
	}
	if got[0].Metadata["reason"] != "plain_text_tool_call" {
		t.Errorf("discard reason = %q, want plain_text_tool_call", got[0].Metadata["reason"])
	}

	for _, msg := range agent.Sessions.GetHistory(sessionKey) {
		if msg.Role == "assistant" && msg.Content == `discard_fake_tool{"path":"/tmp/x"}` {
			t.Fatalf("discarded plain-text message is still persisted: %+v", msg)
		}
	}
}

func TestRunAgentLoop_NoDiscardedIterationPublishesNoDiscard(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: "final answer", ToolCalls: []providers.ToolCall{}},
	}}
	agent.Provider = provider

	opts := discardTestOptions(sessionKey)
	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned an error: %v", err)
	}

	msgs := drainOutbound(al.bus)
	if got := discards(msgs); len(got) != 0 {
		t.Fatalf("message.discard published for a turn with no discarded iteration: %v", labelsOf(msgs))
	}
	if indexOfOutbound(msgs, "message.thinking:m1") < 0 {
		t.Fatalf("the turn did not stream, so the test proves nothing; events: %v", labelsOf(msgs))
	}
	if indexOfOutbound(msgs, "final:m1") < 0 {
		t.Fatalf("the turn did not publish its final message; events: %v", labelsOf(msgs))
	}
}

func labelsOf(msgs []bus.OutboundMessage) []string {
	labels := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		labels = append(labels, outboundLabel(msg))
	}
	return labels
}

func TestRunAgentLoop_DiscardCarriesTheIdTheStreamUsed(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: "", ToolCalls: []providers.ToolCall{}},
		{Content: "final answer", ToolCalls: []providers.ToolCall{}},
	}}
	agent.Provider = provider

	opts := processOptions{
		SessionKey:      sessionKey,
		Channel:         channels.ChannelName,
		ChatID:          "test-chat-id",
		UserMessage:     "Hi",
		DefaultResponse: "Default fallback",
		SendResponse:    true,
	}

	if _, err := runner.runAgentLoop(context.Background(), agent, opts); err != nil {
		t.Fatalf("runAgentLoop returned an error: %v", err)
	}

	msgs := drainOutbound(al.bus)
	got := discards(msgs)
	if len(got) != 1 {
		t.Fatalf("message.discard count = %d, want 1; events: %v", len(got), labelsOf(msgs))
	}
	if got[0].MessageID == "" {
		t.Fatal("discard carries an empty message_id: no client can match it to a bubble")
	}
	var streamedID string
	for _, msg := range msgs {
		if msg.Event == "message.stream" {
			streamedID = msg.MessageID
			break
		}
	}
	if streamedID == "" {
		t.Fatalf("the turn never streamed, so the test proves nothing; events: %v", labelsOf(msgs))
	}
	if got[0].MessageID != streamedID {
		t.Fatalf("discard message_id = %q, streamed id = %q: they must be identical", got[0].MessageID, streamedID)
	}
}
