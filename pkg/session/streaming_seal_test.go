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
//
// The matrix below covers every tail shape, because each one takes a different
// branch of the guard: a text-only draft and a reasoning-only draft both seal
// (the emptiness check needs BOTH fields empty), a finalized assistant tail and
// a user tail are left alone (they are not drafts), and an unknown session must
// not be created as a side effect.

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// tailSeed describes the history a session starts with, expressed only through
// the public append API so the test does not depend on internal fields.
type tailSeed struct {
	name string
	seed func(sm *SessionManager, key string)
}

// sealCase is one row of the SealStreamingTail matrix. wantStreamingAfterSeal is
// the Streaming flag of the tail after SealStreamingTail; wantMsgCount is the
// history length after the follow-up AddFullMessage, which is what makes the
// slot-reuse behaviour (placeholder reused vs. failure appended) observable.
type sealCase struct {
	name                   string
	seed                   tailSeed
	wantStreamingAfterSeal bool
	wantContentAfterSeal   string
	wantMsgCount           int
	wantAppendedIndex      int
}

func TestSealStreamingTail(t *testing.T) {
	cases := []sealCase{
		{
			// A draft that already streamed visible text is closed in place:
			// same slot, same content, Streaming cleared, so the next assistant
			// message appends after it.
			name: "text-only draft is sealed and the next message appends",
			seed: tailSeed{"text draft", func(sm *SessionManager, key string) {
				sm.AddMessage(key, "user", "question")
				sm.AppendAssistantChunk(key, "partial answer")
			}},
			wantStreamingAfterSeal: false,
			wantContentAfterSeal:   "partial answer",
			wantMsgCount:           3,
			wantAppendedIndex:      2,
		},
		{
			// Reasoning-only drafts are just as visible as content drafts (the
			// TUI renders the thinking block), so the emptiness guard must
			// require BOTH fields to be empty before sealing.
			name: "reasoning-only draft is sealed and the next message appends",
			seed: tailSeed{"reasoning draft", func(sm *SessionManager, key string) {
				sm.AddMessage(key, "user", "question")
				sm.AppendReasoningChunk(key, "thinking out loud")
			}},
			wantStreamingAfterSeal: false,
			wantContentAfterSeal:   "",
			wantMsgCount:           3,
			wantAppendedIndex:      2,
		},
		{
			// An EMPTY draft is still a placeholder: sealing it must be a no-op
			// so the next assistant message reuses its slot instead of leaving
			// a blank bubble behind.
			name: "empty draft stays a placeholder and is reused",
			seed: tailSeed{"empty draft", func(sm *SessionManager, key string) {
				sm.AddMessage(key, "user", "question")
				sm.AppendReasoningChunk(key, "")
			}},
			wantStreamingAfterSeal: true,
			wantContentAfterSeal:   "",
			wantMsgCount:           2,
			wantAppendedIndex:      1,
		},
		{
			// A finalized assistant tail is a REAL message (AddFullMessage
			// already cleared Streaming): nothing to seal, and the failure still
			// appends after it.
			name: "finalized assistant tail is untouched and the next message appends",
			seed: tailSeed{"finalized tail", func(sm *SessionManager, key string) {
				sm.AddMessage(key, "user", "question")
				sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "answer"})
			}},
			wantStreamingAfterSeal: false,
			wantContentAfterSeal:   "answer",
			wantMsgCount:           3,
			wantAppendedIndex:      2,
		},
		{
			// A user tail is not a draft at all; sealing must not touch it and
			// must not prevent the failure from being appended.
			name: "user tail is untouched and the next message appends",
			seed: tailSeed{"user tail", func(sm *SessionManager, key string) {
				sm.AddMessage(key, "user", "question")
			}},
			wantStreamingAfterSeal: false,
			wantContentAfterSeal:   "question",
			wantMsgCount:           2,
			wantAppendedIndex:      1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := NewSessionManager()
			key := "test:seal:" + tc.name
			tc.seed.seed(sm, key)

			before := sm.GetHistory(key)
			if len(before) == 0 {
				t.Fatal("seed produced an empty history")
			}

			sm.SealStreamingTail(key)

			after := sm.GetHistory(key)
			if len(after) != len(before) {
				t.Fatalf("seal changed the message count: got %d, want %d: %+v", len(after), len(before), after)
			}
			tail := after[len(after)-1]
			if tail.Streaming != tc.wantStreamingAfterSeal {
				t.Errorf("tail Streaming = %v, want %v: %+v", tail.Streaming, tc.wantStreamingAfterSeal, tail)
			}
			if tail.Content != tc.wantContentAfterSeal {
				t.Errorf("tail Content = %q, want %q", tail.Content, tc.wantContentAfterSeal)
			}

			// The next assistant message is where the two behaviours diverge:
			// appended after a sealed draft, or stored in the reused placeholder
			// slot. Judging by history shape keeps the assertion independent of
			// how many rows the guard touched.
			sm.AddFullMessage(key, providers.Message{Role: "assistant", Content: "❌ Error", ErrorDetail: "boom"})

			final := sm.GetHistory(key)
			if len(final) != tc.wantMsgCount {
				t.Fatalf("history has %d messages, want %d: %+v", len(final), tc.wantMsgCount, final)
			}
			got := final[tc.wantAppendedIndex]
			if got.ErrorDetail != "boom" {
				t.Errorf("message %d ErrorDetail = %q, want %q: %+v", tc.wantAppendedIndex, got.ErrorDetail, "boom", final)
			}
			if got.Streaming {
				t.Errorf("the stored failure must not stay streaming: %+v", got)
			}
		})
	}
}

// TestSealStreamingTail_UnknownSessionDoesNotCreateMessages pins the missing
// tail: sealing a session that was never written must not allocate one, so a
// late failure on an unknown key cannot invent an empty history.
func TestSealStreamingTail_UnknownSessionDoesNotCreateMessages(t *testing.T) {
	sm := NewSessionManager()
	const key = "test:seal:unknown"

	sm.SealStreamingTail(key)

	if h := sm.GetHistory(key); len(h) != 0 {
		t.Fatalf("seal on an unknown session created messages: %+v", h)
	}
}
