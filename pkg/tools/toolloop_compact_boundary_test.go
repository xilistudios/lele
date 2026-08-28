package tools

import (
	"context"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// okProvider is a minimal LLMProvider whose Chat call returns a fixed summary
// response. It is used as the compaction summarizer in boundary tests.
type okProvider struct {
	mu   sync.Mutex
	calls int
}

func (p *okProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "summary of previous work"}, nil
}

func (p *okProvider) GetDefaultModel() string {
	return "test-model"
}

func (p *okProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// msg builds a providers.Message with the given role and content.
func msg(role, content string) providers.Message {
	return providers.Message{Role: role, Content: content}
}

// assistantWithCalls builds an assistant message carrying the given tool-call IDs.
func assistantWithCalls(ids ...string) providers.Message {
	m := providers.Message{Role: "assistant", Content: ""}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, providers.ToolCall{
			ID:   id,
			Type: "function",
			Function: &providers.FunctionCall{
				Name:      "exec",
				Arguments: "{}",
			},
		})
	}
	return m
}

// toolResult builds a tool result message answering the given tool-call ID.
func toolResult(id string) providers.Message {
	return providers.Message{Role: "tool", Content: "result", ToolCallID: id}
}

// validateToolSequence asserts that every tool message in messages is preceded
// by an assistant message whose ToolCalls include the tool message's ID.
func validateToolSequence(t *testing.T, messages []providers.Message) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				seen[tc.ID] = true
			}
		case "tool":
			if !seen[m.ToolCallID] {
				t.Errorf("tool message with ToolCallID %q has no preceding assistant tool_calls", m.ToolCallID)
			}
		}
	}
}

func TestCompactLoopMessages_TrimsOrphanedToolResults(t *testing.T) {
	messages := []providers.Message{
		msg("system", "system prompt"),
		msg("user", "task"),
		assistantWithCalls("call-1"),
		toolResult("call-1"),
		msg("assistant", "intermediate thought"),
		toolResult("orphan-1"), // tail starts with orphaned tool result
		toolResult("orphan-2"),
		msg("user", "follow-up"),
		msg("assistant", "recent answer"),
	}

	compacted, ok := CompactLoopMessages(context.Background(), &okProvider{}, "test-model", messages, 4)
	if !ok {
		t.Fatalf("expected compaction to succeed")
	}

	// Expected shape: system, summary, continue, then tail from the first safe
	// boundary (the "follow-up" user message). The two orphaned tool results
	// and the preceding "intermediate thought" assistant message are trimmed.
	if len(compacted) != 5 {
		t.Fatalf("expected 5 messages after compaction, got %d: %+v", len(compacted), compacted)
	}
	if compacted[0].Role != "system" {
		t.Errorf("expected system message first, got %s", compacted[0].Role)
	}
	if compacted[3].Role != "user" || compacted[3].Content != "follow-up" {
		t.Errorf("expected tail to start at the follow-up user message, got %s: %s", compacted[3].Role, compacted[3].Content)
	}
	validateToolSequence(t, compacted)
}

func TestCompactLoopMessages_KeepsCompleteToolGroup(t *testing.T) {
	messages := []providers.Message{
		msg("system", "system prompt"),
		msg("user", "task"),
		msg("assistant", "old answer"),
		assistantWithCalls("call-a", "call-b"),
		toolResult("call-a"),
		toolResult("call-b"),
		msg("user", "recent question"),
		msg("assistant", "recent answer"),
	}

	// Tail = [assistant+calls, result-a, result-b, user, assistant] (keepLast=5).
	// Index 0 is a safe boundary because the assistant's tool results are
	// present, so the whole group is kept.
	compacted, ok := CompactLoopMessages(context.Background(), &okProvider{}, "test-model", messages, 5)
	if !ok {
		t.Fatalf("expected compaction to succeed")
	}

	// Tail = [assistant+calls, result-a, result-b, user, assistant]. Index 0 is
	// a safe boundary because the assistant's tool results are present, so the
	// whole group is kept.
	if len(compacted) != 8 {
		t.Fatalf("expected 8 messages after compaction, got %d: %+v", len(compacted), compacted)
	}
	if compacted[3].Role != "assistant" || len(compacted[3].ToolCalls) != 2 {
		t.Errorf("expected complete assistant tool group preserved at index 3")
	}
	validateToolSequence(t, compacted)
}

func TestCompactLoopMessages_NoSafeBoundary(t *testing.T) {
	messages := []providers.Message{
		msg("system", "system prompt"),
		msg("user", "task"),
		msg("assistant", "old answer"),
		toolResult("x-1"),
		toolResult("x-2"),
		toolResult("x-3"),
		toolResult("x-4"),
	}

	// keepLast=4 → tail is all tool results with no user/assistant boundary.
	compacted, ok := CompactLoopMessages(context.Background(), &okProvider{}, "test-model", messages, 4)
	if ok {
		t.Fatalf("expected compaction to be skipped, got compacted: %+v", compacted)
	}
	if len(compacted) != len(messages) {
		t.Errorf("expected original messages returned unchanged, got %d vs %d", len(compacted), len(messages))
	}
}

func TestSafeTailBoundary(t *testing.T) {
	cases := []struct {
		name string
		tail []providers.Message
		want int
	}{
		{
			name: "user first",
			tail: []providers.Message{msg("user", "hi"), msg("assistant", "hello")},
			want: 0,
		},
		{
			name: "orphaned tool results first",
			tail: []providers.Message{toolResult("o-1"), msg("user", "hi")},
			want: 1,
		},
		{
			name: "assistant with covered calls first",
			tail: []providers.Message{assistantWithCalls("c-1"), toolResult("c-1"), msg("user", "next")},
			want: 0,
		},
		{
			name: "assistant with uncovered calls first",
			tail: []providers.Message{assistantWithCalls("c-1"), msg("user", "next")},
			want: 1,
		},
		{
			name: "all tool results",
			tail: []providers.Message{toolResult("a"), toolResult("b")},
			want: -1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := safeTailBoundary(tc.tail)
			if got != tc.want {
				t.Errorf("safeTailBoundary = %d, want %d", got, tc.want)
			}
		})
	}
}
