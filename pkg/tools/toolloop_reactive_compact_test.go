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
	// stays too large: the loop must give up after maxReactiveCompactions
	// compaction attempts instead of looping forever.
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
	// 1 initial call + maxReactiveCompactions (3) compacted retries = 4 main calls.
	if got := p.mainCallCount(); got != 1+maxReactiveCompactions {
		t.Errorf("expected %d main LLM calls before giving up, got %d", 1+maxReactiveCompactions, got)
	}
	if got := p.summarizerCallCount(); got != maxReactiveCompactions {
		t.Errorf("expected %d summarizer calls, got %d", maxReactiveCompactions, got)
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
	// Overflow on main calls 1 and 2, success (with a tool call) on call 3,
	// overflow on call 4, success on call 5: the reactive budget must reset
	// after the successful response so the second overflow event gets a
	// fresh set of attempts.
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
	p.scripted = []*providers.LLMResponse{nil, nil, toolCallResp, nil}

	res, err := RunToolLoop(context.Background(), ToolLoopConfig{
		Provider:      p,
		Model:         "test-model",
		ContextWindow: 1000,
	}, buildLargeLoopMessages(), "native", "chat-1")
	if err != nil {
		t.Fatalf("expected recovery across two overflow events, got error: %v", err)
	}
	if res.Content != "done" {
		t.Errorf("expected final content 'done', got %q", res.Content)
	}
	if got := p.mainCallCount(); got != 5 {
		t.Errorf("expected 5 main LLM calls, got %d", got)
	}
}
