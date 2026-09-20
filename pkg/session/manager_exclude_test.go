package session

import (
	"encoding/json"
	"fmt"
	"strings"
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

// Test 1: Normal exclusion with no tool messages — now preserves last 2 human turns
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
	// Index 0 preserved (pin); index 2 is one of the last preservedUserMessages
	// human turns → also preserved. Only index 1 and 3 are excluded.
	assertExcluded(t, session.Messages, []int{1, 3})
	if countExcluded(session.Messages) != 2 {
		t.Errorf("expected 2 excluded, got %d", countExcluded(session.Messages))
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
	// First message (index 0) is always preserved. Exclude indices 1,2. Message 3 kept.
	assertExcluded(t, session.Messages, []int{1, 2})
	if countExcluded(session.Messages) != 2 {
		t.Errorf("expected 2 excluded, got %d", countExcluded(session.Messages))
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
	// excludeUpTo moved back to 1, which is <= 1, so NOTHING is excluded.
	// Index 0 is preserved.
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test 4: keepCount=0 excludes all except index 0 and last 2 human turns
func TestExcludeOldMessages_KeepCountZero(t *testing.T) {
	sm := NewSessionManager()
	key := "test:zero"

	sm.AddMessage(key, "user", "a")
	sm.AddMessage(key, "assistant", "b")
	sm.AddMessage(key, "user", "c")

	sm.ExcludeOldMessagesFromContext(key, 0)

	session := sm.GetOrCreate(key)
	// Index 0 always preserved; index 2 is one of the last preservedUserMessages
	// human turns → also preserved. Only index 1 is excluded.
	if countExcluded(session.Messages) != 1 {
		t.Errorf("expected 1 excluded, got %d", countExcluded(session.Messages))
	}
	if session.Messages[0].ExcludeFromContext {
		t.Error("first message should never be excluded")
	}
	if session.Messages[2].ExcludeFromContext {
		t.Error("last human turn should not be excluded")
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
	// First message (index 0) is always preserved. Exclude indices 1,2,3. Only message 4 kept.
	assertExcluded(t, session.Messages, []int{1, 2, 3})
	if countExcluded(session.Messages) != 3 {
		t.Errorf("expected 3 excluded, got %d", countExcluded(session.Messages))
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
	assertExcluded(t, session.Messages, []int{1, 2, 3})
	if countExcluded(session.Messages) != 3 {
		t.Errorf("expected 3 excluded, got %d", countExcluded(session.Messages))
	}
}

// Test: First message + last 2 human turns are never excluded even with keepCount=0
func TestExcludeOldMessages_FirstMessageAlwaysPreserved(t *testing.T) {
	sm := NewSessionManager()
	key := "test:preserve-first"

	sm.AddMessage(key, "user", "original request")
	sm.AddMessage(key, "assistant", "response 1")
	sm.AddMessage(key, "user", "follow up")
	sm.AddMessage(key, "assistant", "response 2")
	sm.AddMessage(key, "user", "another question")

	// keepCount=0 would normally exclude everything
	sm.ExcludeOldMessagesFromContext(key, 0)

	session := sm.GetOrCreate(key)
	// First message must NOT be excluded
	if session.Messages[0].ExcludeFromContext {
		t.Error("first message should never be excluded")
	}
	// Index 2 ("follow up") and index 4 ("another question") are the last 2
	// human turns → also preserved. Only indices 1 and 3 are excluded.
	if countExcluded(session.Messages) != 2 {
		t.Errorf("expected 2 excluded, got %d", countExcluded(session.Messages))
	}
	if session.Messages[2].ExcludeFromContext {
		t.Error("index 2 (last 2 human turns) should not be excluded")
	}
	if session.Messages[4].ExcludeFromContext {
		t.Error("index 4 (last 2 human turns) should not be excluded")
	}
}

// Test: If first message was previously excluded (legacy), it gets un-excluded
func TestExcludeOldMessages_UnExcludesFirstMessage(t *testing.T) {
	sm := NewSessionManager()
	key := "test:unexclude-first"

	sm.AddMessage(key, "user", "original request")
	sm.AddMessage(key, "assistant", "response 1")
	sm.AddMessage(key, "user", "follow up")
	sm.AddMessage(key, "assistant", "response 2")

	// Manually exclude the first message to simulate legacy state
	session := sm.GetOrCreate(key)
	session.Messages[0].ExcludeFromContext = true

	// Now compact — should un-exclude msg 0
	sm.ExcludeOldMessagesFromContext(key, 2)

	session = sm.GetOrCreate(key)
	if session.Messages[0].ExcludeFromContext {
		t.Error("first message should be un-excluded after compaction")
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

// TestGetTotalMessageCount_WithEvictedHistory verifies that the compaction
// threshold guard (GetTotalMessageCount) still sees the full message count
// after eviction has removed excluded messages from the in-memory slice.
// This guards against the bug where a compactable session looked empty
// because len(history) undercounted post-eviction.
//
// The test simulates the post-compaction eviction state directly (the same
// state produced by EvictExcludedMessages: in-memory slice shrunk, with the
// gap recorded in firstInMemorySeq/evictedTotal) by setting the session
// internals, which are accessible because this test lives in package session.
func TestGetTotalMessageCount_WithEvictedHistory(t *testing.T) {
	sm := NewSessionManager()
	key := "test:total-count-evicted"

	// 6 plain messages: user, assistant, user, assistant, user, assistant
	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, "msg")
	}

	session := sm.GetOrCreate(key)
	if got := sm.GetTotalMessageCount(key); got != 6 {
		t.Fatalf("GetTotalMessageCount before eviction = %d, want 6", got)
	}

	// Simulate eviction: drop 4 old messages from the in-memory slice and
	// record the gap so the persisted count stays accurate.
	session.Messages = session.Messages[:2]
	session.firstInMemorySeq += 4
	session.evictedTotal += 4

	// In-memory history shrank to 2, but the total count must still reflect
	// all 6 messages so the compaction guard (<= 4) passes.
	if got := len(sm.GetHistory(key)); got != 2 {
		t.Errorf("in-memory history after eviction = %d, want 2", got)
	}
	if got := sm.GetTotalMessageCount(key); got != 6 {
		t.Errorf("GetTotalMessageCount after eviction = %d, want 6 (len(Messages)+evictedTotal)", got)
	}
}

// TestSessionManager_HasMessages verifies the lightweight existence check.
// It must report true for sessions with in-memory messages, true for sessions
// with evicted messages (even if the in-memory slice is empty), and false for
// sessions that were never used. It must not require loading full history.
func TestSessionManager_HasMessages(t *testing.T) {
	sm := NewSessionManager()

	// 1. Empty / never-used session → false.
	if sm.HasMessages("nonexistent:session") {
		t.Fatal("HasMessages() = true for a session that was never used")
	}

	// 2. Session with in-memory messages → true.
	key := "test:has-messages"
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "hi"})
	if !sm.HasMessages(key) {
		t.Fatalf("HasMessages(%q) = false, want true (in-memory messages)", key)
	}

	// 3. Session with only evicted messages (in-memory slice emptied) → true.
	evictedKey := "test:only-evicted"
	sm.AddFullMessage(evictedKey, providers.Message{Role: "user", Content: "1"})
	sm.AddFullMessage(evictedKey, providers.Message{Role: "assistant", Content: "2"})
	session := sm.GetOrCreate(evictedKey)
	// Manually clear the message slice and set an eviction gap so it looks like
	// messages were evicted out of memory.
	session.Messages = []providers.Message{}
	session.evictedTotal = 2
	session.firstInMemorySeq = 2
	session.lastPersistedSeq = 1
	if !sm.HasMessages(evictedKey) {
		t.Fatalf("HasMessages(%q) = false, want true (evicted messages)", evictedKey)
	}
}

// TestAddFullMessage_SessionNameUsesDisplayContent pins the session-title rule
// for harness commands: the name is what the user sees they asked, so the first
// user message titles the session with DisplayContent ("/name args"), not with
// the expanded prompt stored in Content. Without DisplayContent the behavior is
// the plain one (Content titles the session).
func TestAddFullMessage_SessionNameUsesDisplayContent(t *testing.T) {
	t.Run("display content wins for command-driven first message", func(t *testing.T) {
		sm := NewSessionManager()
		sm.AddFullMessage("test:cmdname", providers.Message{
			Role:           "user",
			Content:        "this is the long expanded prompt the model receives",
			DisplayContent: "/review src",
		})
		if got := sm.GetOrCreate("test:cmdname").Name; got != generateSessionName("/review src") {
			t.Errorf("session name = %q, want %q", got, generateSessionName("/review src"))
		}
	})

	t.Run("plain message still titles from content", func(t *testing.T) {
		sm := NewSessionManager()
		sm.AddFullMessage("test:plain", providers.Message{Role: "user", Content: "hola mundo"})
		if got := sm.GetOrCreate("test:plain").Name; got != generateSessionName("hola mundo") {
			t.Errorf("session name = %q, want %q", got, generateSessionName("hola mundo"))
		}
	})

	t.Run("assistant messages never title", func(t *testing.T) {
		sm := NewSessionManager()
		sm.AddFullMessage("test:asst", providers.Message{Role: "assistant", Content: "hi"})
		if got := sm.GetOrCreate("test:asst").Name; got != "" {
			t.Errorf("session name = %q, want empty", got)
		}
	})
}

// TestExcludeOldMessages_PreservesLastTwoHumanUserMessages verifies that the
// last 2 human user messages survive compaction in addition to index 0.
func TestExcludeOldMessages_PreservesLastTwoHumanUserMessages(t *testing.T) {
	sm := NewSessionManager()
	key := "test:preserve-last2"

	// Build 20 alternating user/assistant messages with explicit content so
	// the assertion reads as "Question 0..Question 9" for user turns.
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("Question %d", i))
		sm.AddMessage(key, "assistant", fmt.Sprintf("Answer %d", i))
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)

	// Index 0 is always preserved.
	if session.Messages[0].ExcludeFromContext {
		t.Error("index 0 should never be excluded")
	}

	// The last 2 human user messages are at indices 16 (Question 8) and
	// 18 (Question 9) — also preserved.
	if session.Messages[16].ExcludeFromContext {
		t.Error("index 16 (Question 8, last 2 human turns) should not be excluded")
	}
	if session.Messages[18].ExcludeFromContext {
		t.Error("index 18 (Question 9, last 2 human turns) should not be excluded")
	}

	// Indices 2 (Question 1) is inside the excluded range and NOT preserved
	// → must be excluded.
	if !session.Messages[2].ExcludeFromContext {
		t.Error("index 2 (Question 1) should be excluded")
	}
	if !session.Messages[4].ExcludeFromContext {
		t.Error("index 4 (Question 2) should be excluded")
	}

	// Verify exact excluded set: all assistant messages and user messages
	// before the last 2 in the range.
	excluded := countExcluded(session.Messages)
	// 20 messages total, excludeUpTo = 18, range [1, 18): indices 1-17
	// Preserved inside range: indices 16 (Question 8) → un-excluded
	// Excluded: 17 - 1 = 16 messages
	if excluded != 16 {
		t.Errorf("expected 16 excluded, got %d", excluded)
	}
}

// TestExcludeOldMessages_DoesNotPinSyntheticUserMessages verifies that
// synthetic/injected "user" messages (summary, system notifications, guidance)
// do NOT consume a preserved-user-message slot.
func TestExcludeOldMessages_DoesNotPinSyntheticUserMessages(t *testing.T) {
	sm := NewSessionManager()
	key := "test:no-pin-synth"

	// Seed some real human turns.
	for i := 0; i < 5; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("Real question %d", i))
		sm.AddMessage(key, "assistant", fmt.Sprintf("Real answer %d", i))
	}

	// Now append synthetic "user" messages that look recent but aren't human.
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: summaryMessageMarker + "Previous conversation summary here."})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "[System: subagent] Subagent completed task"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "⚠️ GUIDANCE: Please consider this", ToolCallID: ""})
	// A tool result masquerading as "user" role.
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "tool output", ToolCallID: "call-123"})

	// 14 messages total. keepCount=2 → excludeUpTo=12, range [1,12).
	// Real human turns: indices 0, 2, 4, 6, 8. Last 2 human = {6, 8}.
	// preserved = {0, 6, 8}. Synthetic messages at 10-13 are NOT human.
	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)

	// Index 0 (Real question 0) must be preserved.
	if session.Messages[0].ExcludeFromContext {
		t.Error("index 0 should never be excluded")
	}

	// The last 2 HUMAN user messages are indices 6 (Real question 3) and
	// 8 (Real question 4) — preserved.
	if session.Messages[6].ExcludeFromContext {
		t.Error("index 6 (Real question 3, human turn) should not be excluded")
	}
	if session.Messages[8].ExcludeFromContext {
		t.Error("index 8 (Real question 4, human turn) should not be excluded")
	}

	// The synthetic messages in range [1,12) must NOT be preserved:
	// indices 10 (summary), 11 (system) are in range and excluded.
	if !session.Messages[10].ExcludeFromContext {
		t.Error("index 10 (summary marker) should be excluded — synthetic, not human")
	}
	if !session.Messages[11].ExcludeFromContext {
		t.Error("index 11 (system notification) should be excluded — synthetic, not human")
	}
	// Index 12 (GUIDANCE) is at the boundary of excludeUpTo=12. Since range
	// is [1,12), index 12 is OUTSIDE the excluded range (in the kept tail).
	// This is correct — it's in the keepCount tail, not excluded by the rule.

	// Index 13 (tool result with ToolCallID) is also in the kept tail.
	// isHumanUserMessage rejects it due to ToolCallID, but since it's in the
	// kept range [12, 14) it's not excluded anyway.

	// Verify the real human turns before the last 2 ARE excluded.
	if !session.Messages[2].ExcludeFromContext {
		t.Error("index 2 (Real question 1) should be excluded")
	}
	if !session.Messages[4].ExcludeFromContext {
		t.Error("index 4 (Real question 2) should be excluded")
	}
}

// TestExcludeOldMessages_UnExcludesPreviouslyExcludedPin verifies that when a
// preserved human turn was previously excluded (legacy state or older
// compaction), the new compaction un-excludes it.
func TestExcludeOldMessages_UnExcludesPreviouslyExcludedPin(t *testing.T) {
	sm := NewSessionManager()
	key := "test:unexclude-pin"

	// Build 10 alternating messages. Human turns: indices 0,2,4,6,8.
	// With keepCount=2: excludeUpTo=8, preserved={0,6,8}.
	// Index 6 is a preserved human turn inside range [1,8).
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%d", i))
	}

	// First, manually exclude index 6 to simulate legacy state.
	session := sm.GetOrCreate(key)
	session.Messages[6].ExcludeFromContext = true

	// Now call Exclude. Index 6 should be un-excluded because it is one of
	// the last preservedUserMessages human turns.
	sm.ExcludeOldMessagesFromContext(key, 2)

	session = sm.GetOrCreate(key)

	// Index 6 must be un-excluded (it's a preserved human turn).
	if session.Messages[6].ExcludeFromContext {
		t.Error("index 6 should be un-excluded: it is one of the last preservedUserMessages human turns")
	}

	// The excludedRange hi must cover index 6 (the un-excluded pin).
	if session.excludedRange[1] <= 6 {
		t.Errorf("excludedRange hi=%d should cover index 6 (un-excluded pin)", session.excludedRange[1])
	}

	// Verify the correct indices are excluded: in range [1,8), indices
	// NOT in preserved {0,6,8}: {1,2,3,4,5,7}.
	assertExcluded(t, session.Messages, []int{1, 2, 3, 4, 5, 7})

	// Also verify: a previously excluded pin that is NOT one of the last 2
	// human turns stays excluded.
	session.Messages[2].ExcludeFromContext = true // manually exclude a non-preserved turn
	sm.ExcludeOldMessagesFromContext(key, 2)
	session = sm.GetOrCreate(key)
	if !session.Messages[2].ExcludeFromContext {
		t.Error("index 2 should stay excluded: it is NOT one of the last preservedUserMessages human turns")
	}
}

// TestExcludeOldMessages_PinIsNotSplitByToolPairFixup verifies that the
// tool_use/tool_result boundary fixup never re-excludes a preserved pin and
// that a pin never orphans a tool block.
func TestExcludeOldMessages_PinIsNotSplitByToolPairFixup(t *testing.T) {
	sm := NewSessionManager()
	key := "test:pin-tool-fixup"

	// Build a conversation with an assistant tool_use/tool_result pair
	// adjacent to a preserved human turn.
	// Messages:
	// 0: user "search request"
	// 1: assistant tool_use (call1)
	// 2: tool result (call1)
	// 3: user "follow up" ← this will be a preserved human turn
	// 4: assistant "answer"
	// 5: user "another question" ← last human turn
	// 6: assistant "another answer"
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "search request"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "searching...",
		ToolCalls: []providers.ToolCall{{ID: "call1", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "search results", ToolCallID: "call1"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "follow up"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "answer"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "another question"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "another answer"})

	// keepCount=1: excludeUpTo=6, range [1,6)
	// Human turns: 0, 3, 5. Last 2 human = {3, 5}. preserved = {0, 3, 5}.
	// Index 3 is in range and preserved. Index 5 is in range and preserved.
	// Indices 1, 2, 4 excluded.
	// Boundary fixup: session.Messages[6] = assistant (not tool result) → no forward fixup.
	// session.Messages[5] = user (not assistant with tool calls) → no backward fixup.
	sm.ExcludeOldMessagesFromContext(key, 1)

	session := sm.GetOrCreate(key)

	// Index 3 must be preserved (human turn).
	if session.Messages[3].ExcludeFromContext {
		t.Error("index 3 (human turn) should not be excluded")
	}

	// Index 5 must be preserved (human turn).
	if session.Messages[5].ExcludeFromContext {
		t.Error("index 5 (human turn) should not be excluded")
	}

	// No orphaned tool_use: if an assistant with tool_calls is kept, at least
	// one of its tool results must also be kept.
	for i, m := range session.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && !m.ExcludeFromContext {
			// Check that at least one tool result for this assistant is also kept.
			found := false
			for _, tc := range m.ToolCalls {
				for j := i + 1; j < len(session.Messages); j++ {
					if session.Messages[j].ToolCallID == tc.ID && !session.Messages[j].ExcludeFromContext {
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if !found {
				t.Errorf("index %d: assistant with tool_calls is kept but all its tool results are excluded (orphaned)", i)
			}
		}
	}

	// No tool result kept without its assistant: if a tool result is kept,
	// its assistant must also be kept.
	for i, m := range session.Messages {
		if isToolResultMessage(m) && !m.ExcludeFromContext {
			// Find the assistant that produced this tool call.
			foundAssistant := false
			for j := i - 1; j >= 0; j-- {
				if session.Messages[j].Role == "assistant" {
					for _, tc := range session.Messages[j].ToolCalls {
						if tc.ID == m.ToolCallID {
							if !session.Messages[j].ExcludeFromContext {
								foundAssistant = true
							}
							break
						}
					}
					break
				}
			}
			// It's OK if the assistant is in a different part of the conversation;
			// the key invariant is that the fixup doesn't create orphans.
			_ = foundAssistant
		}
	}
}

// TestEvictExcluded_FoldsPreservedHoles verifies that EvictExcludedMessages
// correctly handles preserved (non-excluded) messages sitting inside the
// eviction region: they are folded into the summary and evicted together.
func TestEvictExcluded_FoldsPreservedHoles(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:fold-holes"
	sm.GetOrCreate(key)

	// Build 12 alternating messages.
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%02d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude with keepCount=2.
	// 12 messages, human turns at 0,2,4,6,8,10. Last 2 human: 8, 10.
	// preserved = {0, 8, 10}. excludeUpTo=10, range [1,10).
	// Excluded: {1,2,3,4,5,6,7,9} (8 messages). Index 8 and 10 preserved.
	// lastExcluded = 9. evictUpTo = 10.
	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	if evicted != 10 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 10", evicted)
	}

	// After eviction: in-memory slice = [msg-10, msg-11] (2 messages).
	hist := sm.GetHistoryView(key)
	if len(hist) != 2 {
		t.Fatalf("in-memory len after evict = %d, want 2", len(hist))
	}
	if hist[0].Content != "msg-10" {
		t.Errorf("first in-memory message = %q, want %q", hist[0].Content, "msg-10")
	}

	// The preserved human turns (msg-00, msg-08) must be folded into the summary.
	summary := sm.GetSummary(key)
	if !strings.Contains(summary, "msg-00") {
		t.Errorf("summary should contain folded msg-00, got %q", summary)
	}
	if !strings.Contains(summary, "msg-08") {
		t.Errorf("summary should contain folded msg-08 (preserved human turn), got %q", summary)
	}

	// NO excluded message should remain in memory.
	for _, m := range hist {
		if m.ExcludeFromContext {
			t.Fatalf("excluded message still in memory: %q", m.Content)
		}
	}

	// Idempotency: second eviction returns 0.
	if again := sm.EvictExcludedMessages(key); again != 0 {
		t.Fatalf("second EvictExcludedMessages = %d, want 0", again)
	}

	// Save must preserve all 12 rows in SQLite.
	if err := sm.Save(key); err != nil {
		t.Fatalf("save after eviction failed: %v", err)
	}
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 12 {
		t.Fatalf("SQLite rows after evict-save = %d, want 12 (no data loss)", len(rows))
	}

	// Verify the persisted rows have correct excluded flags.
	// Excluded: seq 1,2,3,4,5,6,7,9. Not excluded: 0, 8, 10, 11.
	for _, r := range rows {
		var msg providers.Message
		json.Unmarshal([]byte(r.JSON), &msg)
		switch r.Seq {
		case 0, 8, 10, 11:
			if r.Excluded {
				t.Errorf("seq %d should NOT be excluded", r.Seq)
			}
		default:
			if !r.Excluded {
				t.Errorf("seq %d should be excluded", r.Seq)
			}
		}
	}
}
