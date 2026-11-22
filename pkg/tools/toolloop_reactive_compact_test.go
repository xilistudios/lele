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

type overflowMainProvider struct {
	mu            sync.Mutex
	failMain      int
	alwaysFail    bool
	scripted      []*providers.LLMResponse
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

	if len(msgs) == 1 {
		p.summCalls++
		return &providers.LLMResponse{Content: "stub summary of earlier messages"}, nil
	}

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

func buildLargeLoopMessages() []providers.Message {
	msgs := []providers.Message{
		{Role: "system", Content: strings.Repeat("system prompt ", 200)},
		{Role: "user", Content: strings.Repeat("original task ", 200)},
	}
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
	counts := p.messageCounts()
	if len(counts) == 2 && counts[1] >= counts[0] {
		t.Errorf("expected compacted message list on retry: first call %d msgs, second call %d msgs", counts[0], counts[1])
	}
}

func TestRunToolLoop_ReactiveCompactionExhausted(t *testing.T) {
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

type nonOverflowProvider struct {
	calls int
}

func (p *nonOverflowProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.calls++
	return nil, errors.New("connection refused")
}

func (p *nonOverflowProvider) GetDefaultModel() string { return "test-model" }

func TestRunToolLoop_ReactiveCompactionBudgetResets(t *testing.T) {
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
		ContextWindow: 100000,
	}, buildLargeLoopMessages(), "native", "chat-1")
	if err != nil {
		t.Fatalf("expected recovery across two overflow events, got error: %v", err)
	}
	if res.Content != "done" {
		t.Errorf("expected final content 'done', got %q", res.Content)
	}
	if got := p.mainCallCount(); got != 4 {
		t.Errorf("expected 4 main LLM calls, got %d", got)
	}
}
