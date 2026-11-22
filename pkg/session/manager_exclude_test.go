package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func countExcluded(msgs []providers.Message) int {
	n := 0
	for _, m := range msgs {
		if m.ExcludeFromContext {
			n++
		}
	}
	return n
}

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

func assertNoOrphanToolPairs(t *testing.T, msgs []providers.Message) {
	t.Helper()

	var ctx []providers.Message
	for _, m := range msgs {
		if !m.ExcludeFromContext {
			ctx = append(ctx, m)
		}
	}

	for i, m := range ctx {
		if !isToolResultMessage(m) {
			continue
		}
		found := false
		for j := i - 1; j >= 0; j-- {
			if ctx[j].Role != "assistant" {
				continue
			}
			for _, tc := range ctx[j].ToolCalls {
				if tc.ID == m.ToolCallID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("orphan tool result in context at index %d: ToolCallID=%q content=%q — no owning assistant kept",
				i, m.ToolCallID, m.Content)
		}
	}

	for i, m := range ctx {
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID == "" {
				continue
			}
			found := false
			for j := i + 1; j < len(ctx); j++ {
				if ctx[j].ToolCallID == tc.ID {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("orphan assistant tool_call in context at index %d: call ID=%q content=%q — no matching result kept",
					i, tc.ID, m.Content)
			}
		}
	}
}

func TestExcludeOldMessages_NormalExclusion(t *testing.T) {
	sm := NewSessionManager()
	key := "test:normal"

	for i := 0; i < 6; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, "msg")
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{1, 3})
	if countExcluded(session.Messages) != 2 {
		t.Errorf("expected 2 excluded, got %d", countExcluded(session.Messages))
	}
}

func TestExcludeOldMessages_BoundarySplitsToolPair_MoveBackKeepsToolGroup(t *testing.T) {
	sm := NewSessionManager()
	key := "test:split-move-back"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "let me search",
		ToolCalls: []providers.ToolCall{{ID: "call1", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result", ToolCallID: "call1"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "thanks"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
	assertNoOrphanToolPairs(t, session.Messages)
}

func TestExcludeOldMessages_OrphanedToolUse_MoveBack(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "let me search",
		ToolCalls: []providers.ToolCall{{ID: "call1", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "next question"})

	sm.ExcludeOldMessagesFromContext(key, 1)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
}

func TestExcludeOldMessages_KeepCountZero(t *testing.T) {
	sm := NewSessionManager()
	key := "test:zero"

	sm.AddMessage(key, "user", "a")
	sm.AddMessage(key, "assistant", "b")
	sm.AddMessage(key, "user", "c")

	sm.ExcludeOldMessagesFromContext(key, 0)

	session := sm.GetOrCreate(key)
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

func TestExcludeOldMessages_MultipleToolResults_KeepsWholeGroup(t *testing.T) {
	sm := NewSessionManager()
	key := "test:multi-tool"

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

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
	assertNoOrphanToolPairs(t, session.Messages)
}

func TestExcludeOldMessages_NonExistentSession(t *testing.T) {
	sm := NewSessionManager()
	sm.ExcludeOldMessagesFromContext("nonexistent", 5)
}

func TestExcludeOldMessages_FullToolCycle(t *testing.T) {
	sm := NewSessionManager()
	key := "test:full-cycle"

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

	sm.ExcludeOldMessagesFromContext(key, 4)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{1, 2, 3})
	if countExcluded(session.Messages) != 3 {
		t.Errorf("expected 3 excluded, got %d", countExcluded(session.Messages))
	}
}

func TestExcludeOldMessages_FirstMessageAlwaysPreserved(t *testing.T) {
	sm := NewSessionManager()
	key := "test:preserve-first"

	sm.AddMessage(key, "user", "original request")
	sm.AddMessage(key, "assistant", "response 1")
	sm.AddMessage(key, "user", "follow up")
	sm.AddMessage(key, "assistant", "response 2")
	sm.AddMessage(key, "user", "another question")

	sm.ExcludeOldMessagesFromContext(key, 0)

	session := sm.GetOrCreate(key)
	if session.Messages[0].ExcludeFromContext {
		t.Error("first message should never be excluded")
	}
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

func TestExcludeOldMessages_UnExcludesFirstMessage(t *testing.T) {
	sm := NewSessionManager()
	key := "test:unexclude-first"

	sm.AddMessage(key, "user", "original request")
	sm.AddMessage(key, "assistant", "response 1")
	sm.AddMessage(key, "user", "follow up")
	sm.AddMessage(key, "assistant", "response 2")

	session := sm.GetOrCreate(key)
	session.Messages[0].ExcludeFromContext = true

	sm.ExcludeOldMessagesFromContext(key, 2)

	session = sm.GetOrCreate(key)
	if session.Messages[0].ExcludeFromContext {
		t.Error("first message should be un-excluded after compaction")
	}
}

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

func TestGetTotalMessageCount_WithEvictedHistory(t *testing.T) {
	sm := NewSessionManager()
	key := "test:total-count-evicted"

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

	session.Messages = session.Messages[:2]
	session.firstInMemorySeq += 4
	session.evictedTotal += 4

	if got := len(sm.GetHistory(key)); got != 2 {
		t.Errorf("in-memory history after eviction = %d, want 2", got)
	}
	if got := sm.GetTotalMessageCount(key); got != 6 {
		t.Errorf("GetTotalMessageCount after eviction = %d, want 6 (len(Messages)+evictedTotal)", got)
	}
}

func TestSessionManager_HasMessages(t *testing.T) {
	sm := NewSessionManager()

	if sm.HasMessages("nonexistent:session") {
		t.Fatal("HasMessages() = true for a session that was never used")
	}

	key := "test:has-messages"
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "hello"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "hi"})
	if !sm.HasMessages(key) {
		t.Fatalf("HasMessages(%q) = false, want true (in-memory messages)", key)
	}

	evictedKey := "test:only-evicted"
	sm.AddFullMessage(evictedKey, providers.Message{Role: "user", Content: "1"})
	sm.AddFullMessage(evictedKey, providers.Message{Role: "assistant", Content: "2"})
	session := sm.GetOrCreate(evictedKey)
	session.Messages = []providers.Message{}
	session.evictedTotal = 2
	session.firstInMemorySeq = 2
	session.lastPersistedSeq = 1
	if !sm.HasMessages(evictedKey) {
		t.Fatalf("HasMessages(%q) = false, want true (evicted messages)", evictedKey)
	}
}

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

func TestExcludeOldMessages_PreservesLastTwoHumanUserMessages(t *testing.T) {
	sm := NewSessionManager()
	key := "test:preserve-last2"

	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("Question %d", i))
		sm.AddMessage(key, "assistant", fmt.Sprintf("Answer %d", i))
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)

	if session.Messages[0].ExcludeFromContext {
		t.Error("index 0 should never be excluded")
	}

	if session.Messages[16].ExcludeFromContext {
		t.Error("index 16 (Question 8, last 2 human turns) should not be excluded")
	}
	if session.Messages[18].ExcludeFromContext {
		t.Error("index 18 (Question 9, last 2 human turns) should not be excluded")
	}

	if !session.Messages[2].ExcludeFromContext {
		t.Error("index 2 (Question 1) should be excluded")
	}
	if !session.Messages[4].ExcludeFromContext {
		t.Error("index 4 (Question 2) should be excluded")
	}

	excluded := countExcluded(session.Messages)
	if excluded != 16 {
		t.Errorf("expected 16 excluded, got %d", excluded)
	}
}

func TestExcludeOldMessages_DoesNotPinSyntheticUserMessages(t *testing.T) {
	sm := NewSessionManager()
	key := "test:no-pin-synth"

	for i := 0; i < 5; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("Real question %d", i))
		sm.AddMessage(key, "assistant", fmt.Sprintf("Real answer %d", i))
	}

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: summaryMessageMarker + "Previous conversation summary here."})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "[System: subagent] Subagent completed task"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "⚠️ GUIDANCE: Please consider this", ToolCallID: ""})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "tool output", ToolCallID: "call-123"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)

	if session.Messages[0].ExcludeFromContext {
		t.Error("index 0 should never be excluded")
	}

	if session.Messages[6].ExcludeFromContext {
		t.Error("index 6 (Real question 3, human turn) should not be excluded")
	}
	if session.Messages[8].ExcludeFromContext {
		t.Error("index 8 (Real question 4, human turn) should not be excluded")
	}

	if !session.Messages[10].ExcludeFromContext {
		t.Error("index 10 (summary marker) should be excluded — synthetic, not human")
	}
	if !session.Messages[11].ExcludeFromContext {
		t.Error("index 11 (system notification) should be excluded — synthetic, not human")
	}

	if !session.Messages[2].ExcludeFromContext {
		t.Error("index 2 (Real question 1) should be excluded")
	}
	if !session.Messages[4].ExcludeFromContext {
		t.Error("index 4 (Real question 2) should be excluded")
	}
}

func TestExcludeOldMessages_UnExcludesPreviouslyExcludedPin(t *testing.T) {
	sm := NewSessionManager()
	key := "test:unexclude-pin"

	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%d", i))
	}

	session := sm.GetOrCreate(key)
	session.Messages[6].ExcludeFromContext = true

	sm.ExcludeOldMessagesFromContext(key, 2)

	session = sm.GetOrCreate(key)

	if session.Messages[6].ExcludeFromContext {
		t.Error("index 6 should be un-excluded: it is one of the last preservedUserMessages human turns")
	}

	if session.excludedRange[1] <= 6 {
		t.Errorf("excludedRange hi=%d should cover index 6 (un-excluded pin)", session.excludedRange[1])
	}

	assertExcluded(t, session.Messages, []int{1, 2, 3, 4, 5, 7})

	session.Messages[2].ExcludeFromContext = true
	sm.ExcludeOldMessagesFromContext(key, 2)
	session = sm.GetOrCreate(key)
	if !session.Messages[2].ExcludeFromContext {
		t.Error("index 2 should stay excluded: it is NOT one of the last preservedUserMessages human turns")
	}
}

func TestExcludeOldMessages_PinIsNotSplitByToolPairFixup(t *testing.T) {
	sm := NewSessionManager()
	key := "test:pin-tool-fixup"

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

	sm.ExcludeOldMessagesFromContext(key, 1)

	session := sm.GetOrCreate(key)

	if session.Messages[3].ExcludeFromContext {
		t.Error("index 3 (human turn) should not be excluded")
	}

	if session.Messages[5].ExcludeFromContext {
		t.Error("index 5 (human turn) should not be excluded")
	}

	for i, m := range session.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 && !m.ExcludeFromContext {
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

	var ctx []providers.Message
	for _, m := range session.Messages {
		if !m.ExcludeFromContext {
			ctx = append(ctx, m)
		}
	}

	for i, m := range ctx {
		if isToolResultMessage(m) {
			foundAssistant := false
			for j := i - 1; j >= 0; j-- {
				if ctx[j].Role == "assistant" {
					for _, tc := range ctx[j].ToolCalls {
						if tc.ID == m.ToolCallID {
							foundAssistant = true
							break
						}
					}
					if foundAssistant {
						break
					}
				}
			}
			if !foundAssistant {
				t.Errorf("orphaned tool result in context at index %d: ToolCallID=%q content=%q — no matching assistant tool_call found",
					i, m.ToolCallID, m.Content)
			}
		}
	}

	for i, m := range ctx {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.ID == "" {
					continue
				}
				foundResult := false
				for j := i + 1; j < len(ctx); j++ {
					if ctx[j].ToolCallID == tc.ID {
						foundResult = true
						break
					}
				}
				if !foundResult {
					t.Errorf("orphaned assistant tool_call in context at index %d: call ID=%q content=%q — no matching tool result found",
						i, tc.ID, m.Content)
				}
			}
		}
	}
}

func TestExcludeOldMessages_PinIsNotSplitByToolPairFixup_MoveBackKeepsGroupAdjacentPin(t *testing.T) {
	sm := NewSessionManager()
	key := "test:pin-move-back-fixup"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "initial request"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "searching...",
		ToolCalls: []providers.ToolCall{{ID: "call_x", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "results_x", ToolCallID: "call_x"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "intermediate answer"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "pin-me: human question"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "searching again...",
		ToolCalls: []providers.ToolCall{{ID: "call_y", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "results_y", ToolCallID: "call_y"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "pin-me: final question"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)

	if session.Messages[4].ExcludeFromContext {
		t.Error("index 4 (adjacent pin) should NOT be excluded")
	}
	if session.Messages[0].ExcludeFromContext {
		t.Error("index 0 should never be excluded")
	}
	for _, idx := range []int{5, 6, 7} {
		if session.Messages[idx].ExcludeFromContext {
			t.Errorf("index %d should NOT be excluded (the group {5,6} is kept whole)", idx)
		}
	}

	for _, idx := range []int{1, 2, 3} {
		if !session.Messages[idx].ExcludeFromContext {
			t.Errorf("index %d should be excluded", idx)
		}
	}
	assertExcluded(t, session.Messages, []int{1, 2, 3})

	assertNoOrphanToolPairs(t, session.Messages)
}

func TestExcludeOldMessages_MoveBack_KeepsWholeToolGroup(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-whole-group"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "do the thing"})
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "running both",
		ToolCalls: []providers.ToolCall{
			{ID: "call1", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "call2", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result1", ToolCallID: "call1"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result2", ToolCallID: "call2"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
	assertNoOrphanToolPairs(t, session.Messages)
}

func TestExcludeOldMessages_MoveBack_FindsOwnerByToolCallID_Interleaved(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-interleaved"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "initial request"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "thinking"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "carry on"})
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "searching and grepping",
		ToolCalls: []providers.ToolCall{
			{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}},
			{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "great"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "here are the results"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "Found 5 repos", ToolCallID: "call_search"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "Found 3 files", ToolCallID: "call_grep"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)

	if session.Messages[3].ExcludeFromContext {
		t.Error("index 3 (owner of the kept tool group) should NOT be excluded")
	}
	for _, idx := range []int{3, 4, 5, 6, 7} {
		if session.Messages[idx].ExcludeFromContext {
			t.Errorf("index %d should NOT be excluded", idx)
		}
	}
	assertExcluded(t, session.Messages, []int{1})

	assertNoOrphanToolPairs(t, session.Messages)
}

func TestExcludeOldMessages_MoveBack_OrphanRunFromIndex0_NoOp(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-orphan-run"

	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "orphan result 1", ToolCallID: "call1"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "orphan result 2", ToolCallID: "call2"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "orphan result 3", ToolCallID: "call3"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "what did you find?"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "here you go"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "thanks"})

	sm.ExcludeOldMessagesFromContext(key, 4)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
	if session.excludeBoundary != 0 {
		t.Errorf("excludeBoundary = %d, want 0 (a no-op must not record a boundary)", session.excludeBoundary)
	}
}

func TestExcludeOldMessages_MoveBack_FallbackBeyondWindowKeepsRunAsOrphans(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-fallback-beyond-window"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "ORIGINAL GOAL"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "searching",
		ToolCalls: []providers.ToolCall{{ID: "call_x", Function: &providers.FunctionCall{Name: "web_search"}}},
	})
	for i := 2; i <= 9; i++ {
		sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: fmt.Sprintf("chatter %d", i)})
	}
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "found it", ToolCallID: "call_x"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "carry on"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "all done"})

	sm.ExcludeOldMessagesFromContext(key, 3)

	session := sm.GetOrCreate(key)

	if session.Messages[10].ExcludeFromContext {
		t.Error("index 10 (tool-result run) should NOT be excluded — the fallback keeps the run")
	}
	if !session.Messages[1].ExcludeFromContext {
		t.Error("index 1 (owner beyond the K=8 window) should stay excluded")
	}
	assertExcluded(t, session.Messages, []int{1, 2, 3, 4, 5, 6, 7, 8, 9})

	ctx := make([]providers.Message, 0, len(session.Messages))
	for _, m := range session.Messages {
		if !m.ExcludeFromContext {
			ctx = append(ctx, m)
		}
	}
	if len(ctx) != 4 {
		t.Errorf("in-context messages = %d, want 4 (pin + orphan result + user + assistant)", len(ctx))
	}
	orphanInContext := false
	for _, m := range ctx {
		if m.Role == "tool" && m.ToolCallID == "call_x" {
			orphanInContext = true
		}
	}
	if !orphanInContext {
		t.Error("expected the orphan tool result (call_x) to remain in the filtered context before healing")
	}

	healed, changed := providers.HealToolCallPairs(ctx)
	if !changed {
		t.Error("HealToolCallPairs should report a change: the orphan tool result was dropped")
	}
	if len(healed) != len(ctx)-1 {
		t.Errorf("HealToolCallPairs: len %d -> %d, want exactly one message dropped", len(ctx), len(healed))
	}
	for _, m := range healed {
		if m.Role == "tool" && m.ToolCallID == "call_x" {
			t.Error("HealToolCallPairs left the orphan tool result in the history")
		}
	}
}

func TestExcludeOldMessages_MoveBack_MultiResultGroup(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-multi-result"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "do three things"})
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "running all three",
		ToolCalls: []providers.ToolCall{
			{ID: "c1", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "c2", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "c3", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "r1", ToolCallID: "c1"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "r2", ToolCallID: "c2"})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "r3", ToolCallID: "c3"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "thanks"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{})
	if countExcluded(session.Messages) != 0 {
		t.Errorf("expected 0 excluded, got %d", countExcluded(session.Messages))
	}
	assertNoOrphanToolPairs(t, session.Messages)
}

func TestExcludeOldMessages_MoveBack_SecondGuardNoCascade(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-no-cascade"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "go"})
	for i, id := range []string{"call_a", "call_b", "call_c"} {
		sm.AddFullMessage(key, providers.Message{
			Role:      "assistant",
			Content:   fmt.Sprintf("assistant turn %d", i),
			ToolCalls: []providers.ToolCall{{ID: id, Function: &providers.FunctionCall{Name: "exec"}}},
		})
	}
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result for call_c", ToolCallID: "call_c"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "thanks"})

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{1})
	if countExcluded(session.Messages) != 1 {
		t.Errorf("expected exactly 1 excluded (single-step retreat), got %d", countExcluded(session.Messages))
	}
}

func TestExcludeOldMessages_MoveBack_ForceCompressionKeepCount(t *testing.T) {
	sm := NewSessionManager()
	key := "test:move-back-force-compression"

	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "start"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "searching",
		ToolCalls: []providers.ToolCall{{ID: "call_a", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result_a", ToolCallID: "call_a"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "answer a"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "more"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "grepping",
		ToolCalls: []providers.ToolCall{{ID: "call_b", Function: &providers.FunctionCall{Name: "exec"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result_b", ToolCallID: "call_b"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "answer b"})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "even more"})
	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "reading",
		ToolCalls: []providers.ToolCall{{ID: "call_c", Function: &providers.FunctionCall{Name: "read_file"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result_c", ToolCallID: "call_c"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "answer c"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "done"})

	historyLen := len(sm.GetOrCreate(key).Messages)
	mid := (historyLen - 1) / 2
	keepCount := historyLen - mid

	sm.ExcludeOldMessagesFromContext(key, keepCount)

	session := sm.GetOrCreate(key)
	assertExcluded(t, session.Messages, []int{1, 2, 3})
	for _, idx := range []int{5, 6} {
		if session.Messages[idx].ExcludeFromContext {
			t.Errorf("index %d should NOT be excluded (whole tool group kept)", idx)
		}
	}
	assertNoOrphanToolPairs(t, session.Messages)
}

func TestEvictExcluded_FoldsPreservedHoles(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:fold-holes"
	sm.GetOrCreate(key)

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

	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	if evicted != 10 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 10", evicted)
	}

	hist := sm.GetHistoryView(key)
	if len(hist) != 2 {
		t.Fatalf("in-memory len after evict = %d, want 2", len(hist))
	}
	if hist[0].Content != "msg-10" {
		t.Errorf("first in-memory message = %q, want %q", hist[0].Content, "msg-10")
	}

	summary := sm.GetSummary(key)
	if !strings.Contains(summary, "msg-00") {
		t.Errorf("summary should contain folded msg-00, got %q", summary)
	}
	if !strings.Contains(summary, "msg-08") {
		t.Errorf("summary should contain folded msg-08 (preserved human turn), got %q", summary)
	}

	for _, m := range hist {
		if m.ExcludeFromContext {
			t.Fatalf("excluded message still in memory: %q", m.Content)
		}
	}

	if again := sm.EvictExcludedMessages(key); again != 0 {
		t.Fatalf("second EvictExcludedMessages = %d, want 0", again)
	}

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

func TestExcludeOldMessages_ZeroLowerBoundStillPersistsIndex0(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:zero-bound"
	sm.GetOrCreate(key)

	sm.AddFullMessage(key, providers.Message{
		Role:      "assistant",
		Content:   "tool use answer",
		ToolCalls: []providers.ToolCall{{ID: "call-1", Function: &providers.FunctionCall{Name: "search"}}},
	})
	sm.AddFullMessage(key, providers.Message{Role: "user", Content: "follow up question"})
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "final answer"})

	session := sm.GetOrCreate(key)
	session.Messages[0].ExcludeFromContext = true

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session = sm.GetOrCreate(key)

	if session.Messages[0].ExcludeFromContext {
		t.Error("message 0 should be un-excluded in memory after legacy migration")
	}

	if session.excludedRange[1] <= session.excludedRange[0] {
		t.Errorf("excludedRange must be non-empty: [%d, %d] — saveUnlocked would skip the UPDATE",
			session.excludedRange[0], session.excludedRange[1])
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) < 1 {
		t.Fatal("expected at least 1 row in SQLite")
	}
	if rows[0].Excluded {
		t.Error("row 0 in SQLite has Excluded=true, want false (legacy un-exclusion should be persisted)")
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	session2 := sm2.GetOrCreate(key)
	if session2 == nil {
		t.Fatal("cold load returned nil session")
	}
	if len(session2.Messages) < 1 {
		t.Fatal("cold-loaded session has no messages")
	}
	if session2.Messages[0].ExcludeFromContext {
		t.Error("cold-loaded message 0 is Excluded=true — un-exclusion was lost (empty range bug)")
	}
}

func TestExcludeOldMessages_PinAboveExcludeUpToIsPersistedAndUnExcluded(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:pin-above"
	sm.GetOrCreate(key)

	for i := 0; i < 20; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%02d", i))
	}

	session := sm.GetOrCreate(key)
	session.Messages[18].ExcludeFromContext = true
	session.Messages[18].Content = "pin-me: human question"

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session = sm.GetOrCreate(key)

	if session.Messages[18].ExcludeFromContext {
		t.Error("index 18 (preserved human turn above excludeUpTo) should NOT be excluded in memory")
	}

	if session.excludedRange[1] != 19 {
		t.Errorf("excludedRange[1] = %d, want 19 (pin at index 18 needs widening)", session.excludedRange[1])
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.Seq == 18 {
			found = true
			if r.Excluded {
				t.Error("row 18 in SQLite has Excluded=true, want false (pin should be persisted as un-excluded)")
			}
			break
		}
	}
	if !found {
		t.Fatal("row 18 not found in SQLite")
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	session2 := sm2.GetOrCreate(key)
	if session2 == nil {
		t.Fatal("cold load returned nil session")
	}

	found = false
	for _, m := range session2.Messages {
		if strings.Contains(m.Content, "pin-me: human question") {
			found = true
			if m.ExcludeFromContext {
				t.Error("cold-loaded pin message is Excluded=true — un-exclusion was lost (widening bug)")
			}
			break
		}
	}
	if !found {
		t.Error("cold-loaded session does not contain the pin message — it was evicted or lost")
	}
}

func TestSetHistory_ClearsExclusionState(t *testing.T) {
	sm := NewSessionManager()
	key := "test:sethistory-clears-exclusion"

	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%d", i))
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	if session.excludedRange == [2]int{} {
		t.Fatal("precondition failed: excludedRange is empty after compaction")
	}
	if session.excludeBoundary == 0 {
		t.Fatal("precondition failed: excludeBoundary is 0 after compaction")
	}
	savedBoundary := session.excludeBoundary

	sm.SetHistory(key, []providers.Message{
		{Role: "user", Content: "new message 0"},
		{Role: "assistant", Content: "new message 1"},
		{Role: "user", Content: "new message 2"},
		{Role: "assistant", Content: "new message 3"},
		{Role: "user", Content: "new message 4"},
		{Role: "assistant", Content: "new message 5"},
		{Role: "user", Content: "new message 6"},
		{Role: "assistant", Content: "new message 7"},
	})

	session = sm.GetOrCreate(key)
	if session.excludedRange != [2]int{} {
		t.Errorf("(a) excludedRange not cleared after SetHistory: got %v", session.excludedRange)
	}
	if session.excludeBoundary != savedBoundary {
		t.Errorf("(b) excludeBoundary should survive when new slice len (%d) >= boundary (%d): got %d",
			8, savedBoundary, session.excludeBoundary)
	}

	sm.SetHistory(key, []providers.Message{
		{Role: "user", Content: "short 0"},
		{Role: "assistant", Content: "short 1"},
	})

	session = sm.GetOrCreate(key)
	if session.excludedRange != [2]int{} {
		t.Errorf("(c) excludedRange not cleared after SetHistory: got %v", session.excludedRange)
	}
	if session.excludeBoundary != 0 {
		t.Errorf("(c) excludeBoundary should be 0 when new slice len (2) < boundary (%d): got %d",
			savedBoundary, session.excludeBoundary)
	}
}

func TestTruncateHistory_ClearsExclusionState(t *testing.T) {
	sm := NewSessionManager()
	key := "test:truncate-clears-exclusion"

	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("msg-%d", i))
	}

	sm.ExcludeOldMessagesFromContext(key, 2)

	session := sm.GetOrCreate(key)
	if session.excludedRange == [2]int{} {
		t.Fatal("precondition failed: excludedRange is empty after compaction")
	}
	if session.excludeBoundary == 0 {
		t.Fatal("precondition failed: excludeBoundary is 0 after compaction")
	}

	sm.TruncateHistory(key, 4)

	session = sm.GetOrCreate(key)
	if session.excludedRange != [2]int{} {
		t.Errorf("excludedRange not cleared after TruncateHistory: got %v", session.excludedRange)
	}
	if session.excludeBoundary != 0 {
		t.Errorf("excludeBoundary not cleared after TruncateHistory: got %d", session.excludeBoundary)
	}
}

func TestRemoveLastMessage_BoundaryGuardPreventsWipe(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:rm-last-boundary-guard"

	sm.AddMessage(key, "user", "initial request")
	sm.AddMessage(key, "assistant", "I'll look into that.")
	sm.AddMessage(key, "user", "check both search and grep")
	sm.AddFullMessage(key, providers.Message{
		Role:    "assistant",
		Content: "Let me search and grep.",
		ToolCalls: []providers.ToolCall{
			{ID: "call_search", Function: &providers.FunctionCall{Name: "web_search"}},
			{ID: "call_grep", Function: &providers.FunctionCall{Name: "exec"}},
		},
	})
	sm.AddMessage(key, "user", "great, do both")
	sm.AddMessage(key, "assistant", "Here are the results.")
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    "Found 5 repos",
		ToolCallID: "call_search",
	})
	sm.AddFullMessage(key, providers.Message{
		Role:       "tool",
		Content:    "Found 3 files",
		ToolCallID: "call_grep",
	})

	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.mu.Lock()
	session, ok := sm.sessions[key]
	if !ok {
		sm.mu.Unlock()
		t.Fatalf("session %q not resident", key)
	}
	session.excludeBoundary = len(session.Messages)
	for i := 1; i < len(session.Messages); i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	session.excludedRange = [2]int{1, len(session.Messages)}
	sm.mu.Unlock()

	session = sm.GetOrCreate(key)
	t.Logf("after setup: len=%d boundary=%d", len(session.Messages), session.excludeBoundary)
	if session.excludeBoundary != 8 {
		t.Fatalf("precondition failed: excludeBoundary = %d, want 8 (= len)", session.excludeBoundary)
	}

	sm.RemoveLastMessage(key)

	session = sm.GetOrCreate(key)
	t.Logf("after RemoveLastMessage: len=%d boundary=%d", len(session.Messages), session.excludeBoundary)

	if session.excludeBoundary > len(session.Messages) {
		t.Errorf("boundary %d > len %d after RemoveLastMessage — guard did not fire",
			session.excludeBoundary, len(session.Messages))
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save after RemoveLastMessage failed: %v", err)
	}

	evicted := sm.EvictExcludedMessages(key)
	t.Logf("evicted=%d", evicted)

	hist := sm.GetHistory(key)
	t.Logf("resident after eviction: %d", len(hist))
	for i, m := range hist {
		t.Logf("  [%2d] role=%-10s excluded=%-5v content=%q", i, m.Role, m.ExcludeFromContext, m.Content)
	}

	nonExcluded := 0
	for _, m := range hist {
		if !m.ExcludeFromContext {
			nonExcluded++
		}
	}
	if nonExcluded == 0 {
		t.Errorf("all non-excluded context wiped — RemoveLastMessage boundary guard is load-bearing but had no test")
	}
	if len(hist) == 0 {
		t.Errorf("entire resident slice emptied — eviction wiped everything")
	}
}
