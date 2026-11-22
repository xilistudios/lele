package session

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

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
	if h[0].Role != "user" || h[0].Content != "question" {
		t.Fatalf("unrelated message mutated: %+v", h[0])
	}
}

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

func TestDiscardStreamingDraft_NoOpOnEmptyDraft(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-empty"

	sm.mu.Lock()
	session := sm.getOrCreateUnlocked(key)
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

	sm.DiscardStreamingDraft("test:draft-never-seen")
	if h := sm.GetHistory("test:draft-never-seen"); len(h) != 0 {
		t.Fatalf("discard on an unknown session created messages: %+v", h)
	}
}

func TestDiscardStreamingDraft_SequentialAccumulationStartsFresh(t *testing.T) {
	sm := NewSessionManager()
	key := "test:draft-sequential"

	sm.AddMessage(key, "user", "question")

	sm.AppendReasoningChunk(key, "R1")
	sm.DiscardStreamingDraft(key)

	sm.AppendReasoningChunk(key, "R2")

	h := sm.GetHistory(key)
	if len(h) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(h), h)
	}
	if h[1].ReasoningContent != "R2" {
		t.Fatalf("discarded reasoning leaked into the next iteration: got %q, want %q", h[1].ReasoningContent, "R2")
	}
}
