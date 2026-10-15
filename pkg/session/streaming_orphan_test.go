package session

// Regression tests for the orphaned streaming assistant message.
//
// DEFECT: a streaming assistant message is only ever finalized by
// AddFullMessage when the incoming message is itself an assistant message
// (manager.go, the in-place branch) or by a later append that happens to be a
// streaming assistant again. Every other writer path — the loop-detector
// guidance in pkg/agent/llm_runner.go, the malformed-tool-call guidance, the
// early returns — appends a NON-assistant message (user/tool) right after the
// streamed assistant, so the streaming flag is never cleared. The orphan stays
// in the middle of the history with Streaming=true: it is persisted to disk,
// survives restarts and is replayed to the LLM on every later turn.
//
// The fix resolves the tail in the session layer, in the single append funnel
// (AddFullMessage), before a non-assistant message is appended. The tests below
// pin one route each plus the two guard contracts (no-op when the tail is not a
// streaming assistant, empty placeholder slot reuse) and durability.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// assertNoStreamingMessage fails when any persisted message of the history is
// still flagged Streaming=true. A streaming flag left in the history is the
// defect this file pins: it is replayed to the model on every later turn.
func assertNoStreamingMessage(t *testing.T, history []providers.Message, ctx string) {
	t.Helper()
	for i, m := range history {
		if m.Streaming {
			t.Fatalf("%s: message %d is still Streaming=true: %+v", ctx, i, m)
		}
	}
}

// assertRoles fails when the history roles do not match want exactly. The fix
// must finalize the tail in place, never reorder or drop messages.
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

// loopGuidanceToolCall mirrors the assistant message the runner persists after
// the loop detector injected its guidance (llm_runner.go, AddFullMessage with
// tool calls).
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

// T1 — the reported route (P1): the loop-detector guidance is appended with
// AddMessage(sessionKey, "user", guidance) while the streamed assistant is the
// tail, and the assistant message with tool calls follows right after.
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

// T2 — P2: the guidance is followed by a plain-text assistant answer and then
// the malformed-tool-call guidance, with no assistant ever overwriting the
// streamed tail.
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

// T3 — P3: invalid tool calls with empty content, so the assistant message is
// never persisted, and the malformed-call guidance lands next to the streamed
// reasoning placeholder.
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

// T4 — P4 (early return / crash) plus the crash-recovery contract of
// pkg/channels/rest_chat.go: a leftover streaming assistant is a crash-recovery
// orphan and MUST stay visible (the REST layer only hides a streaming message
// while the session is actively processing). The next turn's AddMessage must
// finalize it in place — same index, same content — and only then append, so
// nothing is deleted silently.
func TestAddMessage_KeepsCrashRecoveryOrphanVisibleThenFinalizesIt(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-crash"

	sm.AddMessage(key, "user", "first question")
	sm.AppendAssistantChunk(key, "half answer")

	// The process died mid-stream: the last message is still streaming and must
	// remain the visible tail.
	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("expected 2 messages before recovery, got %d: %+v", len(h), h)
	}
	last := h[len(h)-1]
	if last.Role != "assistant" || !last.Streaming || last.Content != "half answer" {
		t.Fatalf("crash-recovery orphan is not visible as a streaming tail: %+v", last)
	}

	// Next turn: the orphan must be finalized in place, not deleted and not left
	// streaming.
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

// T5 — the guard must be a strict no-op whenever the tail is not a streaming
// assistant. This test also passes on the unfixed base: it pins that the fix
// cannot change any normal path.
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

// T6 — white-box: an empty streaming placeholder (created before the first
// chunk arrived) carries no content, so the non-assistant message takes its
// slot instead of appending a second one. Reusing the slot is what avoids the
// RemoveLastMessage delete watermark (deleteFromSeq) composed with the
// msgsAppended++ of the same call.
func TestResolveStreamingTailLocked_EmptyPlaceholderIsReused(t *testing.T) {
	sm := NewSessionManager()
	key := "test:orphan-empty-placeholder"

	sm.mu.Lock()
	session := sm.getOrCreateUnlocked(key)
	// Exactly the state AppendAssistantChunk leaves before its first chunk:
	// {assistant, Streaming:true} with neither content nor reasoning.
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

// T7 — durability: the orphan must not be written to SQLite as a streaming
// message. The store is read directly (raw SQL) because loading a message runs
// it through the session decoder, which is exactly what hid the poisoned rows
// in the tool-call regression.
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
