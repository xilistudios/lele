package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestToolLoopEmptyRetryBackoff(t *testing.T) {
	if got := toolLoopEmptyRetryBackoff(0); got != time.Second {
		t.Fatalf("backoff(0) = %v", got)
	}
	if got := toolLoopEmptyRetryBackoff(1); got != 2*time.Second {
		t.Fatalf("backoff(1) = %v", got)
	}
	if got := toolLoopEmptyRetryBackoff(2); got != 3*time.Second {
		t.Fatalf("backoff(2) = %v", got)
	}
	// Capped for indices >= 3.
	for _, i := range []int{3, 4, 50} {
		if got := toolLoopEmptyRetryBackoff(i); got != 3*time.Second {
			t.Fatalf("backoff(%d) = %v, want cap 3s", i, got)
		}
	}
}

func TestEstimateLoopTokens(t *testing.T) {
	messages := []providers.Message{
		{Role: "user", Content: strings.Repeat("a", 100)},
		{Role: "assistant", Content: strings.Repeat("b", 50), ReasoningContent: strings.Repeat("c", 25)},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{Function: &providers.FunctionCall{Arguments: `${"x":"y"}`}},
				{Function: nil}, // nil function skipped
			},
		},
		{Role: "tool", Content: strings.Repeat("d", 10)},
	}

	// totalChars = 195; tokens = 195*2/5 = 78
	got := EstimateLoopTokens(messages)
	if got != 78 {
		t.Fatalf("EstimateLoopTokens = %d, want 78", got)
	}

	// Empty list -> 0
	if got := EstimateLoopTokens(nil); got != 0 {
		t.Fatalf("EstimateLoopTokens(nil) = %d, want 0", got)
	}
}

// compactProvider is a scripted provider for CompactLoopMessages.
type compactProvider struct {
	response string
	err      error
	calls    int
}

func (p *compactProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return &providers.LLMResponse{Content: p.response}, nil
}

func (p *compactProvider) GetDefaultModel() string { return "compactor" }

// TestCompactLoopMessages_notEnoughMessages verifies the short-input path.
func TestCompactLoopMessages_notEnoughMessages(t *testing.T) {
	p := &compactProvider{}
	messages := []providers.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}
	out, ok := CompactLoopMessages(context.Background(), p, "m", messages, 6)
	if ok {
		t.Fatal("expected ok=false for short input")
	}
	if len(out) != len(messages) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(messages))
	}
	if p.calls != 0 {
		t.Fatalf("calls = %d, want 0 (no compaction)", p.calls)
	}
}

// TestCompactLoopMessages_success verifies the summarization path.
func TestCompactLoopMessages_success(t *testing.T) {
	p := &compactProvider{response: "a concise summary"}
	messages := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "step one"},
		{Role: "assistant", Content: stringOf('a', 600), ToolCalls: []providers.ToolCall{
			{Name: "tool1", Function: &providers.FunctionCall{Name: "tool1", Arguments: stringOf('f', 250)}},
		}},
		{Role: "tool", Content: stringOf('r', 400)},
		{Role: "user", Content: "step two"},
		{Role: "assistant", Content: "I will continue"},
		{Role: "tool", Content: "result two"},
		// keepLast=6 tail from here onward would be messages[len-keepLast:]
		{Role: "user", Content: "final prompt"},
		{Role: "assistant", Content: "final answer"},
	}

	out, ok := CompactLoopMessages(context.Background(), p, "", messages, 6)
	if !ok {
		t.Fatal("expected ok=true for compaction")
	}
	if len(out) != 3+6 { // system + summary + continue + tail(6)
		t.Fatalf("len(out) = %d, want %d", len(out), 3+6)
	}
	if out[0].Content != "system prompt" {
		t.Fatalf("out[0] = %q", out[0].Content)
	}
	if !containsStr(out[1].Content, "a concise summary") {
		t.Fatalf("summary message = %q", out[1].Content)
	}
	if !containsStr(out[2].Content, "CONTINUE executing") {
		t.Fatalf("continue message = %q", out[2].Content)
	}
	if out[len(out)-1].Content != "final answer" {
		t.Fatalf("last tail = %q", out[len(out)-1].Content)
	}
	if p.calls != 1 {
		t.Fatalf("calls = %d, want 1", p.calls)
	}
}

// TestCompactLoopMessages_providerError verifies the error path skips compaction.
func TestCompactLoopMessages_providerError(t *testing.T) {
	p := &compactProvider{err: errors.New("boom")}
	messages := buildManyMessages(10)
	out, ok := CompactLoopMessages(context.Background(), p, "m", messages, 6)
	if ok {
		t.Fatal("expected ok=false on provider error")
	}
	if len(out) != len(messages) {
		t.Fatalf("expected original messages returned, len=%d", len(out))
	}
	if p.calls != 1 {
		t.Fatalf("calls = %d, want 1", p.calls)
	}
}

// TestCompactLoopMessages_emptyResponse verifies empty scheduled provider
// response also skips compaction.
func TestCompactLoopMessages_emptyResponse(t *testing.T) {
	p := &compactProvider{response: ""}
	messages := buildManyMessages(10)
	out, ok := CompactLoopMessages(context.Background(), p, "m", messages, 6)
	if ok {
		t.Fatal("expected ok=false on empty response")
	}
	if len(out) != len(messages) {
		t.Fatalf("expected original messages, len=%d", len(out))
	}
}

// TestCompactLoopMessages_nilResponse verifies nil response skip.
func TestCompactLoopMessages_nilResponse(t *testing.T) {
	p := &compactProvider{} // returns nil response, nil err
	p.response = ""
	out, ok := CompactLoopMessages(context.Background(), p, "m", buildManyMessages(10), 6)
	if ok {
		t.Fatal("expected ok=false on nil response")
	}
	for _, m := range out {
		_ = m
	}
}

func buildManyMessages(n int) []providers.Message {
	out := make([]providers.Message, 0, n)
	out = append(out, providers.Message{Role: "system", Content: "sys"})
	for i := 0; i < n-1; i++ {
		out = append(out, providers.Message{Role: "user", Content: strings.Repeat(string(rune('a'+i%26)), 3)})
	}
	return out
}

func stringOf(ch byte, n int) string {
	return strings.Repeat(string(ch), n)
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
