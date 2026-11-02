package agent

// Tests for the message.discard event published by the LLM loop.
//
// The runner streams EVERY iteration to the client under its own message id
// (iterationMsgID(opts.MessageID, iteration)) but several loop paths throw an
// iteration away without persisting anything: the bounded empty-response retry,
// the plain-text tool-call path (which even removes the assistant message it
// just persisted) and the all-tool-calls-invalid path when the response carries
// no content.
//
// Only the LAST iteration of a turn is ever confirmed by message.complete, so
// the client's bubble for a discarded iteration could never be matched against
// history and stayed on screen forever — typically BELOW the final answer. The
// runner therefore publishes message.discard for it; these tests pin that the
// event exists, carries the iteration-suffixed message id, and is published
// before the next iteration starts streaming.

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

// discardScriptedProvider is a scripted streaming provider: it implements
// providers.StreamingLLMProvider so the runner takes its streaming branch, and
// each call streams one reasoning chunk plus one answer chunk before returning
// the scripted response — mirroring a provider that produced output for an
// iteration the runner then decides to throw away.
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

// outboundLabel renders one published message as "event:message_id" so a test
// can assert on the relative ORDER of the stream segment and the discard that
// ends it. A plain content message carries no Event and is labelled "final".
func outboundLabel(msg bus.OutboundMessage) string {
	if msg.Event == "" {
		return "final:" + msg.MessageID
	}
	return msg.Event + ":" + msg.MessageID
}

// indexOfOutbound returns the position of the first message matching the label.
func indexOfOutbound(msgs []bus.OutboundMessage, label string) int {
	for i := range msgs {
		if outboundLabel(msgs[i]) == label {
			return i
		}
	}
	return -1
}

// discards filters the published messages down to message.discard events.
func discards(msgs []bus.OutboundMessage) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for _, msg := range msgs {
		if msg.Event == "message.discard" {
			out = append(out, msg)
		}
	}
	return out
}

// discardTestOptions is the processOptions shape that turns streaming ON for a
// turn (the native channel is the only channel that streams) with a stable base
// message id, so iteration ids are predictable ("m1", "m1-2", "m1-3", ...).
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

// TestRunAgentLoop_EmptyResponsePublishesMessageDiscard covers the bounded
// empty-response retry: two blanks in a row are discarded, the third call
// answers. Each discard must carry the id of the bubble that iteration streamed
// (iteration 1 keeps the base id, iteration 2 gets the "-2" suffix), the reason
// empty_response, the native channel/ChatID the stream used, and must be
// published BEFORE the next iteration's stream segment.
func TestRunAgentLoop_EmptyResponsePublishesMessageDiscard(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: "", ToolCalls: []providers.ToolCall{}},             // iteration 1: blank → discarded
		{Content: "   ", ToolCalls: []providers.ToolCall{}},          // iteration 2: blank → discarded
		{Content: "final answer", ToolCalls: []providers.ToolCall{}}, // iteration 3: survives
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

	// Ordering: iteration N's stream segment, its discard, then iteration N+1's
	// segment. A client that renders the discard must never see the next
	// iteration's chunks before it.
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

// TestRunAgentLoop_InvalidToolCallsDiscardWhenContentEmpty covers the
// all-tool-calls-invalid path: the response carries a tool call that cannot be
// canonicalized and no content, so nothing of the iteration is persisted and
// its bubble must be discarded with reason invalid_tool_calls.
func TestRunAgentLoop_InvalidToolCallsDiscardWhenContentEmpty(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		// No name ⇒ CanonicalToolCalls drops it ⇒ nothing survives this iteration.
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

// discardFakeTool is the minimal tools.Tool the plain-text tool-call path needs:
// containsPlainToolCall only reads the registry's tool NAMES.
type discardFakeTool struct{}

func (discardFakeTool) Name() string                       { return "discard_fake_tool" }
func (discardFakeTool) Description() string                { return "test tool" }
func (discardFakeTool) Parameters() map[string]interface{} { return nil }
func (discardFakeTool) Execute(_ context.Context, _ map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: ""}
}

// TestRunAgentLoop_PlainTextToolCallDiscardsPublished covers the plain-text
// tool-call path: the assistant message was persisted and then removed again
// (RemoveLastMessage), so the bubble the client rendered for it can never be
// confirmed and must be discarded with reason plain_text_tool_call.
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
		// Plain-text invocation instead of a real function call.
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

	// The discarded plain-text message must not be in the history either: the
	// event only reports the bubble, it never re-adds anything.
	for _, msg := range agent.Sessions.GetHistory(sessionKey) {
		if msg.Role == "assistant" && msg.Content == `discard_fake_tool{"path":"/tmp/x"}` {
			t.Fatalf("discarded plain-text message is still persisted: %+v", msg)
		}
	}
}

// TestRunAgentLoop_NoDiscardedIterationPublishesNoDiscard is the negative case:
// a turn whose only iteration survives publishes the stream segment and the
// final message, and NOT a single message.discard — the event is a signal about
// a throw-away iteration, never a per-turn marker.
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
	// Guard against a false pass: streaming WAS active for this turn, so the
	// absence above is about the loop's decisions, not about a silent client.
	if indexOfOutbound(msgs, "message.thinking:m1") < 0 {
		t.Fatalf("the turn did not stream, so the test proves nothing; events: %v", labelsOf(msgs))
	}
	if indexOfOutbound(msgs, "final:m1") < 0 {
		t.Fatalf("the turn did not publish its final message; events: %v", labelsOf(msgs))
	}
}

// labelsOf renders every published message for failure output.
func labelsOf(msgs []bus.OutboundMessage) []string {
	labels := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		labels = append(labels, outboundLabel(msg))
	}
	return labels
}

// TestRunAgentLoop_DiscardCarriesTheIdTheStreamUsed covers a turn with NO inbound
// message id (the goal-continuation loop builds its processOptions without one,
// and so do cron/API-driven turns). newStreamHandler substitutes a generated
// UUID in that case and the client keys its bubble on it, so the discard must
// carry that same id: emitting the derived `iterationMsgID(opts.MessageID, …)`
// would send an EMPTY message_id, which every client drops on the floor and the
// stray bubble survives — in exactly the turns nobody can reproduce by hand.
func TestRunAgentLoop_DiscardCarriesTheIdTheStreamUsed(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	runner := newLLMRunner(al)
	runner.retryWait = instantRetryWait
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.MaxIterations = 10

	const sessionKey = "test-session"
	provider := &discardScriptedProvider{script: []*providers.LLMResponse{
		{Content: "", ToolCalls: []providers.ToolCall{}},             // discarded
		{Content: "final answer", ToolCalls: []providers.ToolCall{}}, // survives
	}}
	agent.Provider = provider

	opts := processOptions{
		SessionKey:      sessionKey,
		Channel:         channels.ChannelName,
		ChatID:          "test-chat-id",
		UserMessage:     "Hi",
		DefaultResponse: "Default fallback",
		SendResponse:    true,
		// MessageID deliberately empty: no inbound id for this turn.
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
	// It must be the id the streamed segment actually used.
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
