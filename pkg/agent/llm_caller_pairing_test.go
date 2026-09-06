// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// assertPairingValid checks a request the way a strict provider does: every
// tool call announced by an assistant message must be answered by exactly one
// tool result, every result must answer a call that is present, and results
// must sit directly behind their assistant block.
func assertPairingValid(t *testing.T, messages []providers.Message) {
	t.Helper()

	waiting := map[string]int{}
	blockOpen := false

	for i, m := range messages {
		if m.Role == "tool" {
			if !blockOpen {
				t.Fatalf("message %d: tool result %q does not follow an assistant tool_calls block", i, m.ToolCallID)
			}
			if waiting[m.ToolCallID] == 0 {
				t.Fatalf("message %d: tool result %q answers nothing (orphan or duplicate)", i, m.ToolCallID)
			}
			waiting[m.ToolCallID]--
			if waiting[m.ToolCallID] == 0 {
				delete(waiting, m.ToolCallID)
			}
			continue
		}

		// Any other role closes the previous block: anything still waiting was
		// never answered, which is the 400 that bricks a session.
		if len(waiting) > 0 {
			t.Fatalf("message %d (%s): previous assistant block left %d call(s) unanswered", i, m.Role, len(waiting))
		}
		blockOpen = false

		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				t.Fatalf("message %d: tool call with no id can never be answered: %+v", i, tc)
			}
			if tc.FunctionName() == "" {
				t.Fatalf("message %d: tool call with no name is rejected by some providers: %+v", i, tc)
			}
			waiting[tc.ID]++
		}
		blockOpen = len(m.ToolCalls) > 0
	}
	if len(waiting) > 0 {
		t.Fatalf("request ends with %d unanswered tool call(s)", len(waiting))
	}
}

// The provider request is the last place a broken session can be repaired:
// healing must happen there, not in the stored history, so a session that was
// bricked by an interrupted turn recovers without losing its real tool output.
func TestLLMCaller_HealsPairingBeforeProviderCall(t *testing.T) {
	cases := []struct {
		name  string
		build func() []providers.Message
	}{
		{"dangling call", func() []providers.Message {
			return []providers.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", ToolCalls: []providers.ToolCall{namedCall("c1", "exec")}},
			}
		}},
		{"orphaned result", func() []providers.Message {
			return []providers.Message{
				{Role: "user", Content: "hi"},
				{Role: "tool", Content: "stale", ToolCallID: "ghost"},
				{Role: "assistant", Content: "hello"},
			}
		}},
		{"duplicate results", func() []providers.Message {
			return []providers.Message{
				{Role: "assistant", ToolCalls: []providers.ToolCall{namedCall("c1", "exec")}},
				{Role: "tool", Content: "first", ToolCallID: "c1"},
				{Role: "tool", Content: "second", ToolCallID: "c1"},
			}
		}},
		{"result excluded from context", func() []providers.Message {
			return []providers.Message{
				{Role: "assistant", ToolCalls: []providers.ToolCall{namedCall("c1", "exec")}},
				{Role: "tool", Content: "compacted away", ToolCallID: "c1", ExcludeFromContext: true},
				{Role: "assistant", Content: "done"},
			}
		}},
		{"nameless call", func() []providers.Message {
			return []providers.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "working", ToolCalls: []providers.ToolCall{
					{ID: "c_x", Type: "function", Function: &providers.FunctionCall{Name: "", Arguments: "{}"}},
				}},
			}
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, tmpDir := createLLMRunnerTestAgentLoop(t)
			defer os.RemoveAll(tmpDir)
			agent := createLLMRunnerTestAgentInstance(t, tmpDir)

			var sent [][]providers.Message
			agent.Provider = &llmRunnerMockLLMProvider{
				onChatCalled: func(ctx context.Context, messages []providers.Message,
					_ []providers.ToolDefinition, _ string, _ map[string]interface{},
				) (*providers.LLMResponse, error) {
					sent = append(sent, append([]providers.Message(nil), messages...))
					return &providers.LLMResponse{Content: "ok"}, nil
				},
			}

			caller := newLLMCaller(al)
			_, err := caller.call(llmCallOptions{
				ctx:        context.Background(),
				agent:      agent,
				messages:   tc.build(),
				model:      "mock-model",
				sessionKey: "test-session",
			})
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(sent) != 1 {
				t.Fatalf("provider called %d times, want 1", len(sent))
			}
			assertPairingValid(t, sent[0])
		})
	}
}

// A healthy request must reach the provider untouched: healing that rewrites
// valid conversations would reorder real tool output the model depends on.
func TestLLMCaller_LeavesValidPairingAlone(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)

	history := []providers.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{namedCall("c1", "exec"), namedCall("c2", "read")}},
		{Role: "tool", Content: "out 1", ToolCallID: "c1"},
		{Role: "tool", Content: "out 2", ToolCallID: "c2"},
		{Role: "assistant", Content: "done"},
	}
	want := append([]providers.Message(nil), history...)

	var sent []providers.Message
	agent.Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, messages []providers.Message,
			_ []providers.ToolDefinition, _ string, _ map[string]interface{},
		) (*providers.LLMResponse, error) {
			sent = append([]providers.Message(nil), messages...)
			return &providers.LLMResponse{Content: "ok"}, nil
		},
	}

	caller := newLLMCaller(al)
	if _, err := caller.call(llmCallOptions{
		ctx: context.Background(), agent: agent, messages: history,
		model: "mock-model", sessionKey: "test-session",
	}); err != nil {
		t.Fatalf("call: %v", err)
	}

	if len(sent) != len(want) {
		t.Fatalf("valid request was rewritten: %d messages in, %d out", len(want), len(sent))
	}
	for i := range want {
		if sent[i].Role != want[i].Role || sent[i].Content != want[i].Content ||
			sent[i].ToolCallID != want[i].ToolCallID {
			t.Fatalf("message %d changed: %+v -> %+v", i, want[i], sent[i])
		}
	}
}

// namedCall builds a tool call in the canonical shape the loop records.
func namedCall(id, name string) providers.ToolCall {
	return providers.ToolCall{
		ID: id, Type: "function", Name: name,
		Function:  &providers.FunctionCall{Name: name, Arguments: "{}"},
		Arguments: map[string]any{},
	}
}
