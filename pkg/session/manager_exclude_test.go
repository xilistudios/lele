package session

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// helper to count excluded messages
func countExcluded(msgs []providers.Message) int {
	n := 0
	for _, m := range msgs {
		if m.ExcludeFromContext {
			n++
		}
	}
	return n
}

// helper to check if specific indices are excluded
func assertExcluded(t *testing.T, msgs []providers.Message, excluded []int) {
	t.Helper()
	expected := make(map[int]bool)
	for _, i := range excluded {
		expected[i] = true
	}
	for i, m := range msgs {
		if expected[i] && !m.ExcludeFromContext {
			t.Errorf("message[%d] should be excluded but isn't (role=%q, toolCallID=%q)", i, m.Role, m.ToolCallID)
		}
		if !expected[i] && m.ExcludeFromContext {
			t.Errorf("message[%d] should NOT be excluded but is (role=%q)", i, m.Role)
		}
	}
}

// Test 1: Normal exclusion with no tool messages works as before
func TestExcludeOldMessages_NormalExclusion(t *testing.T) {
	sm := NewSessionManager()
	key := "test:normal"

	// Create 6 plain messages: user, assistant, user, assistant, user, assistant
	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, "msg")
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	// Should exclude first 4 messages (6-2=4), keep last 2
	assertExcluded(t, session.Messages, []int{0, 1, 2, 3})
	if countExcluded(session.Messages) != 4 {
		t.Errorf("expected 4 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 2: Boundary that would split a tool_use/tool_result pair
// moves forward to include the tool_results in the excluded set
func TestExcludeOldMessages_BoundarySplitsToolPair_Forward(t *testing.T) {
	sm := NewSessionManager()
	key := "test:split-forward"

	// Messages:
	// 0: user       "hello"
	// 1: assistant  tool_use (tool call "call1")
	// 2: tool       tool_result (toolCallID "call1")
	// 3: user       "thanks"
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "let me search",
		ToolCalls: []providers.ToolCall{{ID: "call1", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result", ToolCallID: "call1"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "thanks"})

	// keepCount=1 means excludeUpTo=3 → boundary would be after index 2
	// But index 2 is a tool_result whose tool_use (index 1) is excluded.
	// Boundary should move forward to also exclude index 2.
	sm.ExcludeOldMessagesFromContext(key, 1)

	session := sm.GetOrCreate(key)
	// Messages 0, 1, 2 should all be excluded (3 total). Only message 3 kept.
	assertExcluded(t, session.Messages, []int{0, 1, 2})
	if countExcluded(session.Messages) != 3 {
		t.Errorf("expected 3 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 3: Assistant with tool_use at the boundary with no following tool_results
// → boundary moves back to exclude the assistant message
func TestExcludeOldMessages_OrphanedToolUse_MoveBack(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back"

	// Messages:
	// 0: user       "hello"
	// 1: assistant  tool_use (tool call "call1")  ← this is at the boundary
	// 2: user       "next question"                ← NOT a tool_result
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "let me search",
		ToolCalls: []providers.ToolCall{{ID: "call1", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "next question"})

	// keepCount=1 means excludeUpTo=2 → last excluded is index 1 (assistant with tool_use).
	// The first kept (index 2) is NOT a tool_result, so the tool_use is orphaned.
	// Boundary should move back to exclude only index 0, keeping indices 1 and 2.
	sm.ExcludeOldMessagesFromContext(key, 1)

	session := sm.GetOrCreate(key)
	// Only message 0 should be excluded. Messages 1 and 2 are kept.
	assertExcluded(t, session.Messages, []int{0})
	if countExcluded(session.Messages) != 1 {
		t.Errorf("expected 1 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 4: keepCount=0 should exclude all messages
func TestExcludeOldMessages_KeepCountZero(t *testing.T) {
	sm := NewSessionManager()
	key := "test:zero"

	sm.AddMessage(key, "user", "a")
	sm.AddMessage(key, "assistant", "b")
	sm.AddMessage(key, "user", "c")

	sm.ExcludeOldMessagesFromContext(key, 0)

	session := sm.GetOrCreate(key)
	if countExcluded(session.Messages) != 3 {
		t.Errorf("expected all 3 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 5: keepCount >= len(messages) → no change
func TestExcludeOldMessages_KeepCountLargerThanMessages(t *testing.T) {
	sm := NewSessionManager()
	key := "test:large-keep"

	sm.AddMessage(key, "user", "a")
	sm.AddMessage(key, "assistant", "b")

	sm.ExcludeOldMessagesFromContext(key, 10)

	session := sm.GetOrCreate(key)
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 6: Multiple tool calls in a single assistant message
func TestExcludeOldMessages_MultipleToolResults(t *testing.T) {
	sm := NewSessionManager()
	key := "test:multi-tool"

	// Messages:
	// 0: user       "do two things"
	// 1: assistant  tool_use calls "call1" and "call2"
	// 2: tool       tool_result for call1
	// 3: tool       tool_result for call2
	// 4: user       "thanks"
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "do two things"})
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "let me do both",
		ToolCalls: []providers.ToolCall{
			{ID: "call1", Function: &providers.FunctionCall{Name: "func1"}},
			{ID: "call2", Function: &providers.FunctionCall{Name: "func2"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result1", ToolCallID: "call1"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result2", ToolCallID: "call2"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "thanks"})

	// keepCount=1 means excludeUpTo=4 → boundary after index 3
	// Index 3 is a tool_result → move forward
	// Index 4 is user (not tool_result) → stop
	sm.ExcludeOldMessagesFromContext(key, 1)

	session := sm.GetOrCreate(key)
	// All 4 messages should be excluded, only message 4 kept
	assertExcluded(t, session.Messages, []int{0, 1, 2, 3})
	if countExcluded(session.Messages) != 4 {
		t.Errorf("expected 4 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 7: Non-existent session key should not panic
func TestExcludeOldMessages_NonExistentSession(t *testing.T) {
	sm := NewSessionManager()
	// Should not panic
	sm.ExcludeOldMessagesFromContext("nonexistent", 5)
}

// Test 8: Full tool cycle — user, assistant+tool_use, tool_result, assistant final
// with keepCount set so the tool pair straddles the boundary
func TestExcludeOldMessages_FullToolCycle(t *testing.T) {
	sm := NewSessionManager()
	key := "test:full-cycle"

	// Messages:
	// 0: user       "search for X"
	// 1: assistant  tool_use "call_a"
	// 2: tool       result for call_a
	// 3: assistant  "Here is the result"
	// 4: user       "now do Y"
	// 5: assistant  tool_use "call_b"
	// 6: tool       result for call_b
	// 7: assistant  "Done with Y"
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "search for X"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "searching",
		ToolCalls: []providers.ToolCall{{ID: "call_a", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result_a", ToolCallID: "call_a"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "Here is the result"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "now do Y"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "doing Y",
		ToolCalls: []providers.ToolCall{{ID: "call_b", Function: &providers.FunctionCall{Name: "action"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result_b", ToolCallID: "call_b"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "Done with Y"})

	// keepCount=4 means excludeUpTo=4 → boundary after index 3
	// Index 3 is assistant (no tool calls) → fine, no split
	// Exclude indices 0-3
	sm.ExcludeOldMessagesFromContext(key, 4)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{0, 1, 2, 3})
	if countExcluded(session.Messages) != 4 {
		t.Errorf("expected 4 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 9: isToolResultMessage helper
func TestIsToolResultMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  providers.Message
		want bool
	}{
		{
			name: "tool role with ToolCallID",
			msg:  providers.Message{Role: "tool", ToolCallID: "abc"},
			want: true,
		},
		{
			name: "tool role without ToolCallID",
			msg:  providers.Message{Role: "tool", ToolCallID: ""},
			want: false,
		},
		{
			name: "user role with ToolCallID",
			msg:  providers.Message{Role: "user", ToolCallID: "abc"},
			want: true,
		},
		{
			name: "user role without ToolCallID",
			msg:  providers.Message{Role: "user", ToolCallID: ""},
			want: false,
		},
		{
			name: "assistant role with ToolCallID",
			msg:  providers.Message{Role: "assistant", ToolCallID: "abc"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isToolResultMessage(tt.msg)
			if got != tt.want {
				t.Errorf("isToolResultMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}
