package session

// Tests for SealStreamingTail.
//
// WHY THIS EXISTS: FinalizeAssistantMessage deliberately keeps the Streaming
// flag set, so AddFullMessage's in-place branch can swap a streamed draft for the
// final version of the SAME iteration. A writer that persists a DIFFERENT
// message after a failed turn (the LLM-failure path in pkg/agent) needs the
// opposite: the streamed text stays where it is and the failure is appended.
// SealStreamingTail is that transition, and its guards are what keep it from
// destroying real content or leaving orphan placeholders.

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// A draft that already streamed visible text is closed in place: same slot, same
// content, Streaming cleared, so the next assistant message appends after it.
func TestSealStreamingTail_ClosesDraftWithContent(t *testing.T) {
	sm := NewSessionManager()
	key := "test:seal-content"

	sm.AddMessage(key, "user", "question")
	sm.AppendAssistantChunk(key, "partial answer")

	sm.SealStreamingTail(key)

	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("message count changed: got %d, want 2: %+v", len(h), h)
	}
	if h[1].Content != "partial answer" {
		t.Fatalf("content lost: got %q", h[1].Content)
	}
	if h[1].Streaming {
		t.Fatalf("tail must no longer be streaming: %+v", h[1])
	}

	// The following assistant message now appends instead of replacing it.
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "❌ Error", ErrorDetail: "boom"})
	h = sm.GetHistory(key)
	if len(h) != 3 {
		t.Fatalf("expected the failure to be appended, got %d messages: %+v", len(h), h)
	}
	if h[1].Content != "partial answer" {
		t.Fatalf("appended message overwrote the streamed tail: %+v", h[1])
	}
	if h[2].ErrorDetail != "boom" {
		t.Fatalf("failure message missing: %+v", h[2])
	}
}

// An EMPTY draft is still a placeholder: sealing it must be a no-op so the next
// assistant message reuses its slot instead of leaving a blank bubble behind.
func TestSealStreamingTail_EmptyDraftIsNoOp(t *testing.T) {
	sm := NewSessionManager()
	key := "test:seal-empty"

	sm.AddMessage(key, "user", "question")
	sm.AppendReasoningChunk(key, "")

	sm.SealStreamingTail(key)

	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("expected the untouched placeholder, got %d messages: %+v", len(h), h)
	}
	if !h[1].Streaming || h[1].Content != "" {
		t.Fatalf("empty draft must stay streaming: %+v", h[1])
	}

	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "❌ Error", ErrorDetail: "boom"})
	h = sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("the failure must take the placeholder slot, got %d messages: %+v", len(h), h)
	}
	if h[1].ErrorDetail != "boom" || h[1].Streaming {
		t.Fatalf("failure not stored in the reused slot: %+v", h[1])
	}
}

// No streaming tail at all (user tail, finalized assistant tail, empty session,
// unknown session): nothing to do, nothing created.
func TestSealStreamingTail_NoTailIsNoOp(t *testing.T) {
	sm := NewSessionManager()
	key := "test:seal-none"

	sm.AddMessage(key, "user", "question")

	// User tail.
	sm.SealStreamingTail(key)
	if h := sm.GetHistory(key); len(h) != 1 || h[0].Role != "user" {
		t.Fatalf("user tail was modified: %+v", h)
	}

	// Finalized assistant tail.
	sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "answer"})
	sm.SealStreamingTail(key)
	h := sm.GetHistory(key)
	if len(h) != 2 || h[1].Content != "answer" || h[1].Streaming {
		t.Fatalf("finalized tail was modified: %+v", h)
	}

	// Unknown session: no messages created.
	sm.SealStreamingTail("test:seal-unknown")
	if h := sm.GetHistory("test:seal-unknown"); len(h) != 0 {
		t.Fatalf("seal on an unknown session created messages: %+v", h)
	}
}
