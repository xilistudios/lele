package session

// Regression tests for DiscardStreamingDraft.
//
// DEFECT: streamed reasoning/content chunks accumulate per SESSION, while
// pkg/agent/llm_runner.go reasons per ITERATION. The iterations that `continue`
// without persisting anything (bounded empty-response retry, all-tool-calls
// invalid with no content) never finalize the session's streaming tail, so the
// next iteration's AppendReasoningChunk appends on top of the discarded one and
// the persisted message ends up with N concatenated copies of the same
// thinking text.
//
// The fix adds a per-iteration reset, DiscardStreamingDraft, that empties the
// accumulated text of the in-progress (Streaming) tail while keeping that
// message in place. The tests below pin the one behaviour that matters for the
// escaping case (T5: a discarded draft must not survive into the next
// iteration) plus the four guard contracts that make the call safe to issue
// unconditionally from the runner.

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// T1 — the happy path: a draft that already streamed content AND reasoning is
// emptied in place. The message must survive (same count, same slot) and stay
// Streaming, because it is still the tail the next iteration writes into.
func TestDiscardStreamingDraft_ClearsContentAndReasoning(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-clear"

	sm.AddMessage(key, "user", "question")
	sm.AppendReasoningChunk(key, "thinking about it")
	sm.AppendAssistantChunk(key, "partial answer")

	before := sm.GetHistory(key)
	if len(before) != 2 || before[1].Role != "assistant" || !before[1].Streaming {
		t.Fatalf("precondition: expected a streaming assistant tail, got %+v", before)
	}

	sm.DiscardStreamingDraft(key)

	h := sm.GetHistory(key)
	if len(h) != len(before) {
		t.Fatalf("message count changed: got %d, want %d: %+v", len(h), len(before), h)
	}
	if h[1].Role != "assistant" {
		t.Fatalf("tail role changed: got %q, want \"assistant\"", h[1].Role)
	}
	if h[1].Content != "" {
		t.Fatalf("content not cleared: got %q", h[1].Content)
	}
	if h[1].ReasoningContent != "" {
		t.Fatalf("reasoning not cleared: got %q", h[1].ReasoningContent)
	}
	if !h[1].Streaming {
		t.Fatalf("the message must stay the live streaming tail, got %+v", h[1])
	}
	// The user message is untouched.
	if h[0].Role != "user" || h[0].Content != "question" {
		t.Fatalf("unrelated message mutated: %+v", h[0])
	}
}

// T2 — a FINALIZED tail (AddFullMessage's in-place branch cleared Streaming) is
// a real message: the method must not erase it.
func TestDiscardStreamingDraft_NoOpOnFinalizedTail(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-finalized"

	sm.AddMessage(key, "user", "question")
	sm.AppendReasoningChunk(key, "streamed thinking")
	sm.AppendAssistantChunk(key, "streamed answer")
	sm.AddFullMessage(key, providers.Message{
		Role:             "assistant",
		Content:          "final answer",
		ReasoningContent: "final reasoning",
	})

	sm.DiscardStreamingDraft(key)

	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(h), h)
	}
	if h[1].Streaming {
		t.Fatalf("precondition: finalized tail must not be Streaming, got %+v", h[1])
	}
	if h[1].Content != "final answer" {
		t.Fatalf("finalized content was cleared: got %q", h[1].Content)
	}
	if h[1].ReasoningContent != "final reasoning" {
		t.Fatalf("finalized reasoning was cleared: got %q", h[1].ReasoningContent)
	}
}

// T3 — a non-assistant tail is not a draft: no-op.
func TestDiscardStreamingDraft_NoOpOnUserTail(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-user-tail"

	sm.AddMessage(key, "user", "a question with no answer yet")

	sm.DiscardStreamingDraft(key)

	h := sm.GetHistory(key)
	if len(h) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(h), h)
	}
	if h[0].Role != "user" || h[0].Content != "a question with no answer yet" {
		t.Fatalf("user message mutated: %+v", h[0])
	}
	if h[0].Streaming {
		t.Fatalf("user message gained the streaming flag: %+v", h[0])
	}
}

// T4 — an empty streaming placeholder is already a clean draft: no-op, and in
// particular it must neither append a message nor panic.
func TestDiscardStreamingDraft_NoOpOnEmptyDraft(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-empty"

	sm.mu.Lock()
	session := sm.getOrCreateUnlocked(key)
	// Exactly the state a stream leaves before its first chunk arrives:
	// {assistant, Streaming:true} with neither content nor reasoning.
	sm.getOrCreateStreamingMsg(session)
	sm.mu.Unlock()

	sm.DiscardStreamingDraft(key)

	h := sm.GetHistory(key)
	if len(h) != 1 {
		t.Fatalf("expected the single placeholder, got %d messages: %+v", len(h), h)
	}
	if h[0].Role != "assistant" || !h[0].Streaming || h[0].Content != "" || h[0].ReasoningContent != "" {
		t.Fatalf("placeholder changed: %+v", h[0])
	}

	// Same guarantee for a session that does not exist at all.
	sm.DiscardStreamingDraft("test:draft-never-seen")
	if h := sm.GetHistory("test:draft-never-seen"); len(h) != 0 {
		t.Fatalf("discard on an unknown session created messages: %+v", h)
	}
}

// T5 — THE REGRESSION GATE: the discarded iteration's reasoning must not be
// concatenated with the next iteration's. Appending "R1", discarding, then
// appending "R2" must leave exactly "R2" in the tail (before the fix the tail
// held "R1R2", which is what a reload/TUI displayed once the empty-limit early
// return ended the turn).
func TestDiscardStreamingDraft_SequentialAccumulationStartsFresh(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-sequential"

	sm.AddMessage(key, "user", "question")

	// Iteration 1 streams reasoning, then gets discarded (empty response retry).
	sm.AppendReasoningChunk(key, "R1")
	sm.DiscardStreamingDraft(key)

	// Iteration 2 streams its own reasoning.
	sm.AppendReasoningChunk(key, "R2")

	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(h), h)
	}
	if h[1].ReasoningContent != "R2" {
		t.Fatalf("discarded reasoning leaked into the next iteration: got %q, want %q", h[1].ReasoningContent, "R2")
	}
}
