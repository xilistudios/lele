package session

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

type tailSeed struct {
	name string
	seed func(sm *SessionManager, key string)
}

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

func TestSealStreamingTail_UnknownSessionDoesNotCreateMessages(t *testing.T) {
	sm := NewSessionManager()
	const key = "test:seal:unknown"

	sm.SealStreamingTail(key)

	if h := sm.GetHistory(key); len(h) != 0 {
		t.Fatalf("seal on an unknown session created messages: %+v", h)
	}
}
