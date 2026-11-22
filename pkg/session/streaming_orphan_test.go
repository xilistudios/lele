package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func assertNoStreamingMessage(t *testing.T, history []providers.Message, ctx string) {
	t.Helper()
	for i, m := range history {
		if m.Streaming {
			t.Fatalf("%s: message %d is still Streaming=true: %+v", ctx, i, m)
		}
	}
}

func assertRoles(t *testing.T, history []providers.Message, want []string, ctx string) {
	t.Helper()
	got := make([]string, 0, len(history))
	for _, m := range history {
		got = append(got, m.Role)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: roles changed: got %v, want %v (history: %+v)", ctx, got, want, history)
	}
}

func loopGuidanceToolCall(id string) providers.Message {
	return providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:        id,
			Type:      "function",
			Name:      "exec",
			Arguments: map[string]interface{}{"command": "true"},
		}},
	}
}

func TestAddFullMessage_FinalizesStreamingTailBeforeNonAssistantAppend(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-p1"

	sm.AddMessage(key, "user", "question")
	sm.AppendAssistantChunk(key, "streamed part")
	sm.AddMessage(key, "user", "⚠️ GUIDANCE: stop repeating the same tool calls")
	sm.AddFullMessage(key, loopGuidanceToolCall("call_1"))
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result", ToolCallID: "call_1"})

	h := sm.GetHistory(key)
	if len(h) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(h), h)
	}
	assertRoles(t, h, []string{"user", "assistant", "user", "assistant", "tool"}, "P1")

	if h[1].Role != "assistant" {
		t.Fatalf("message 1 must stay the streamed assistant, got %q", h[1].Role)
	}
	if h[1].Content != "streamed part" {
		t.Fatalf("streamed content was mutated: got %q", h[1].Content)
	}
	if h[1].Streaming {
		t.Fatalf("streamed assistant at index 1 is still Streaming=true: %+v", h[1])
	}
	if h[2].Role != "user" || !strings.Contains(h[2].Content, "GUIDANCE") {
		t.Fatalf("the guidance must stay at index 2, got %+v", h[2])
	}
	assertNoStreamingMessage(t, h, "P1")
}

func TestAddFullMessage_ResolvesTailForP2Sequence(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-p2"

	sm.AddMessage(key, "user", "question")
	sm.AppendAssistantChunk(key, "streamed")
	sm.AddMessage(key, "user", "guidance")
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "final content"})
	sm.AddMessage(key, "user", "malformed retry guidance")

	h := sm.GetHistory(key)
	assertRoles(t, h, []string{"user", "assistant", "user", "assistant", "user"}, "P2")

	if h[1].Content != "streamed" || h[1].Streaming {
		t.Fatalf("streamed assistant was not finalized in place: %+v", h[1])
	}
	if h[3].Content != "final content" || h[3].Streaming {
		t.Fatalf("plain-text assistant is wrong: %+v", h[3])
	}
	assertNoStreamingMessage(t, h, "P2")
}

func TestAddMessage_ResolvesStreamingTailForP3Sequence(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-p3"

	sm.AddMessage(key, "user", "question")
	sm.AppendReasoningChunk(key, "think")
	sm.AddMessage(key, "user", "⚠️ Your previous tool call was malformed")
	sm.AddMessage(key, "user", "retry guidance")

	h := sm.GetHistory(key)
	assertRoles(t, h, []string{"user", "assistant", "user", "user"}, "P3")

	if h[1].Role != "assistant" || h[1].ReasoningContent != "think" {
		t.Fatalf("reasoning placeholder was mutated: %+v", h[1])
	}
	if h[1].Streaming {
		t.Fatalf("reasoning placeholder is still Streaming=true: %+v", h[1])
	}
	assertNoStreamingMessage(t, h, "P3")
}

func TestAddMessage_KeepsCrashRecoveryOrphanVisibleThenFinalizesIt(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-crash"

	sm.AddMessage(key, "user", "first question")
	sm.AppendAssistantChunk(key, "half answer")

	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("expected 2 messages before recovery, got %d: %+v", len(h), h)
	}
	last := h[len(h)-1]
	if last.Role != "assistant" || !last.Streaming || last.Content != "half answer" {
		t.Fatalf("crash-recovery orphan is not visible as a streaming tail: %+v", last)
	}

	sm.AddMessage(key, "user", "next")

	h = sm.GetHistory(key)
	if len(h) != 3 {
		t.Fatalf("len must grow by exactly 1 (nothing dropped), got %d: %+v", len(h), h)
	}
	if h[1].Role != "assistant" || h[1].Content != "half answer" {
		t.Fatalf("the orphan must stay at the same index with its content: %+v", h[1])
	}
	if h[1].Streaming {
		t.Fatalf("the orphan was not finalized: %+v", h[1])
	}
	assertNoStreamingMessage(t, h, "crash recovery")
}

func TestAddFullMessage_NoOpWhenTailIsNotStreaming(t *testing.T) {
	t.Run("finalized assistant tail", func(t *testing.T) {
		sm := NewSessionManager()
		key := "test:orphan-noop-final"

		sm.AddMessage(key, "user", "q")
		sm.AppendAssistantChunk(key, "a")
		sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "a"})
		sm.AddMessage(key, "user", "q2")

		want := []providers.Message{
			{Role: "user", Content: "q"},
			{Role: "assistant", Content: "a"},
			{Role: "user", Content: "q2"},
		}
		if got := sm.GetHistory(key); !reflect.DeepEqual(got, want) {
			t.Fatalf("history differs from the expected slice:\n got: %+v\nwant: %+v", got, want)
		}
	})

	t.Run("tool tail", func(t *testing.T) {
		sm := NewSessionManager()
		key := "test:orphan-noop-tool"

		sm.AddMessage(key, "user", "q")
		sm.AddFullMessage(key, loopGuidanceToolCall("call_9"))
		sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "out", ToolCallID: "call_9"})
		sm.AddMessage(key, "user", "q2")

		want := []providers.Message{
			{Role: "user", Content: "q"},
			loopGuidanceToolCall("call_9"),
			{Role: "tool", Content: "out", ToolCallID: "call_9"},
			{Role: "user", Content: "q2"},
		}
		if got := sm.GetHistory(key); !reflect.DeepEqual(got, want) {
			t.Fatalf("history differs from the expected slice:\n got: %+v\nwant: %+v", got, want)
		}
	})
}

func TestResolveStreamingTailLocked_EmptyPlaceholderIsReused(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-empty-placeholder"

	sm.mu.Lock()
	session := sm.getOrCreateUnlocked(key)
	sm.getOrCreateStreamingMsg(session)
	sm.mu.Unlock()

	h := sm.GetHistory(key)
	if len(h) != 1 || h[0].Role != "assistant" || !h[0].Streaming {
		t.Fatalf("placeholder not created as expected: %+v", h)
	}

	sm.AddMessage(key, "user", "hi")

	h = sm.GetHistory(key)
	if len(h) != 1 {
		t.Fatalf("the empty placeholder slot must be reused, got %d messages: %+v", len(h), h)
	}
	if h[0].Role != "user" || h[0].Content != "hi" {
		t.Fatalf("the reused slot must hold the appended message, got %+v", h[0])
	}
	if h[0].Streaming {
		t.Fatalf("the reused slot kept the streaming flag: %+v", h[0])
	}
}

func TestStreamingOrphan_NotPersistedAsStreaming(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:orphan-durability"
	sm.AddMessage(key, "user", "question")
	sm.AppendAssistantChunk(key, "streamed part")
	sm.AddMessage(key, "user", "⚠️ GUIDANCE: stop repeating the same tool calls")
	sm.AddFullMessage(key, loopGuidanceToolCall("call_1"))
	sm.AddFullMessage(key, providers.Message{Role: "tool", Content: "result", ToolCallID: "call_1"})

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	rows, err := s.DB().Query(
		"SELECT message FROM session_messages WHERE session_key = ? ORDER BY seq",
		key,
	)
	if err != nil {
		t.Fatalf("query stored messages: %v", err)
	}
	defer rows.Close()

	var count int
	var sawOrphan bool
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			t.Fatalf("scan stored message: %v", err)
		}
		count++
		if strings.Contains(message, `"streaming":true`) {
			t.Fatalf("store holds a streaming message for %s: %s", key, message)
		}
		if strings.Contains(message, "streamed part") {
			sawOrphan = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate stored messages: %v", err)
	}
	if count != 5 {
		t.Fatalf("expected 5 persisted rows, got %d", count)
	}
	if !sawOrphan {
		t.Fatalf("the streamed orphan content was not persisted at all for %s", key)
	}
}
