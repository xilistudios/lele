package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// overflowMainProvider simulates a provider whose main-loop calls (multi-message)
// fail with context-overflow errors, while summarizer calls made by
// CompactLoopMessages (single user message) always succeed with a stub summary.
// This mirrors reality: the summarizer prompt is tiny compared to the full
// conversation, so it succeeds even when the main prompt overflows.
type overflowMainProvider struct {
	mu            sync.Mutex
	failMain      int                      // number of initial main calls that fail with overflow
	alwaysFail    bool                     // if true, main calls never succeed
	scripted      []*providers.LLMResponse // per-main-call responses; nil entry = overflow error
	mainCalls     int
	summCalls     int
	mainMsgCounts []int
}

func newOverflowMainProvider(failMain int) *overflowMainProvider {
	return &overflowMainProvider{failMain: failMain}
}

func (p *overflowMainProvider) Chat(_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// CompactLoopMessages summarizer call: exactly one user message.
	if len(msgs) == 1 {
		p.summCalls++
		return &providers.LLMResponse{Content: "stub summary of earlier messages"}, nil
	}

	// Main loop call.
	p.mainCalls++
	p.mainMsgCounts = append(p.mainMsgCounts, len(msgs))
	if p.alwaysFail {
		return nil, errors.New("prompt is too long")
	}
	if p.scripted != nil {
		idx := p.mainCalls - 1
		if idx >= len(p.scripted) {
			return &providers.LLMResponse{Content: "done"}, nil
		}
		if resp := p.scripted[idx]; resp != nil {
			return resp, nil
		}
		return nil, errors.New("prompt is too long")
	}
	if p.mainCalls <= p.failMain {
		return nil, fmt.Errorf("400 Bad Request: prompt is too long: 250000 tokens > 200000 maximum")
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *overflowMainProvider) GetDefaultModel() string { return "test-model" }

func (p *overflowMainProvider) mainCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.mainCalls
}

func (p *overflowMainProvider) summarizerCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.summCalls
}

func (p *overflowMainProvider) messageCounts() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.mainMsgCounts))
	copy(out, p.mainMsgCounts)
	return out
}

// buildLargeLoopMessages builds a message list big enough for CompactLoopMessages
// to have a summarizable middle: len > keepLast(6)+2, i.e. 9+ messages, with a
// safe tail boundary (last message is an assistant without tool calls).
func buildLargeLoopMessages() []providers.Message {
	msgs := []providers.Message{
		{Role: "system", Content: strings.Repeat("system prompt ", 200)},
		{Role: "user", Content: strings.Repeat("original task ", 200)},
	}
	// Assistant/tool pairs with distinct IDs.
	for i := 0; i < 3; i++ {
		id := string(rune('a' + i))
		msgs = append(msgs, providers.Message{
			Role:    "assistant",
			Content: strings.Repeat("thinking "+id+" ", 100),
			ToolCalls: []providers.ToolCall{{
				ID:       "call-" + id,
				Type:     "function",
				Function: &providers.FunctionCall{Name: "exec", Arguments: "{}"},
			}},
		})
		msgs = append(msgs, providers.Message{Role: "tool", Content: strings.Repeat("result "+id+" ", 100), ToolCallID: "call-" + id})
	}
	msgs = append(msgs, providers.Message{Role: "user", Content: strings.Repeat("recent input ", 100)})
	msgs = append(msgs, providers.Message{Role: "assistant", Content: strings.Repeat("recent answer ", 100)})
	return msgs
}

func TestRunToolLoop_ReactiveCompactionOnOverflow(t *testing.T) {
	p := newOverflowMainProvider(1)

	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		ContextWindow: 1000,
	}, buildLargeLoopMessages(), "native", "chat-1")
	if err != nil {
		t.Fatalf("expected reactive compaction to recover, got error: %v", err)
	}
	if res.Content != "done" {
		t.Errorf("expected final content 'done', got %q", res.Content)
	}
	if got := p.mainCallCount(); got != 2 {
		t.Errorf("expected 2 main LLM calls (1 overflow + 1 success), got %d", got)
	}
	if got := p.summarizerCallCount(); got != 1 {
		t.Errorf("expected 1 summarizer call, got %d", got)
	}
	// The retry must have received a compacted (shorter) message list.
	counts := p.messageCounts()
	if len(counts) == 2 && counts[1] >= counts[0] {
		t.Errorf("expected compacted message list on retry: first call %d msgs, second call %d msgs", counts[0], counts[1])
	}
}

func TestRunToolLoop_ReactiveCompactionExhausted(t *testing.T) {
	// Summarizer always succeeds (so compaction "works") but the main prompt
	// stays too large: the loop must give up after the first compaction retry
	// because CompactLoopMessages detects the already-compacted context and
	// skips redundant re-summarization.
	p := newOverflowMainProvider(0)
	p.alwaysFail = true

	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		ContextWindow: 1000,
	}, buildLargeLoopMessages(), "native", "chat-1")
	if err == nil {
		t.Fatal("expected error after exhausting reactive compaction attempts")
	}
	// 1 initial call + 1 compacted retry = 2 main calls. The second
	// compaction is skipped because the context is already compacted and
	// no new messages were added (CompactLoopMessages optimization).
	if got := p.mainCallCount(); got != 2 {
		t.Errorf("expected 2 main LLM calls before giving up, got %d", got)
	}
	if got := p.summarizerCallCount(); got != 1 {
		t.Errorf("expected 1 summarizer call, got %d", got)
	}
}

func TestRunToolLoop_NonOverflowErrorNotCompacted(t *testing.T) {
	p := newOverflowMainProvider(0)
	p.alwaysFail = true

	// Replace the overflow message with a non-overflow error by using a
	// provider wrapper: simplest is a dedicated mock.
	np := &nonOverflowProvider{}
	_, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      np,
		Model:         "test-model",
		ContextWindow: 1000,
	}, buildLargeLoopMessages(), "native", "chat-1")
	if err == nil {
		t.Fatal("expected the non-overflow error to be returned")
	}
	if got := np.calls; got != 1 {
		t.Errorf("expected exactly 1 LLM call (no compaction retries), got %d", got)
	}
}

// nonOverflowProvider always fails with a non-overflow error.
type nonOverflowProvider struct {
	calls int
}

func (p *nonOverflowProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.calls++
	return nil, errors.New("connection refused")
}

func (p *nonOverflowProvider) GetDefaultModel() string { return "test-model" }

func TestRunToolLoop_ReactiveCompactionBudgetResets(t *testing.T) {
	// Overflow on call 1, success (with a tool call) on call 2 which adds
	// new messages, overflow on call 3, success on call 4: the reactive
	// budget must reset after the successful response so the second overflow
	// event gets a fresh compaction attempt. The tool call on call 2 adds
	// assistant + tool result messages, growing the context past the
	// "already compacted" guard in CompactLoopMessages.
	//
	// ContextWindow is set high enough to avoid proactive compaction
	// interfering with the reactive compaction flow.
	toolCallResp := &providers.LLMResponse{
		Content: "",
		ToolCalls: []providers.ToolCall{{
			ID:        "call-z",
			Type:      "function",
			Name:      "noop",
			Arguments: map[string]interface{}{},
		}},
	}
	p := newOverflowMainProvider(0)
	p.scripted = []*providers.LLMResponse{nil, toolCallResp, nil}

	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		ContextWindow: 100000, // high enough to avoid proactive compaction
	}, buildLargeLoopMessages(), "native", "chat-1")
	if err != nil {
		t.Fatalf("expected recovery across two overflow events, got error: %v", err)
	}
	if res.Content != "done" {
		t.Errorf("expected final content 'done', got %q", res.Content)
	}
	// Call 1: overflow → compact → retry (success with tool call)
	// Call 2: tool call processed, tool result added → loop continues
	// Call 3: overflow → compact → retry (success)
	// = 4 main LLM calls
	if got := p.mainCallCount(); got != 4 {
		t.Errorf("expected 4 main LLM calls, got %d", got)
	}
}
