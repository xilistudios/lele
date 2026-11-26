package channels

import (
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestIsAllowed_EmptyWarns(t *testing.T) {
	empty := NewBaseChannel("telegram", nil, nil, nil)
	out := captureLogs(t, func() {
		for i := 0; i < 5; i++ {
			if !empty.IsAllowed("123456789") {
				t.Fatal("an empty allowlist still allows every sender: the behavior is kept on purpose")
			}
		}
	})
	if !strings.Contains(out, "allowlist is empty") {
		t.Fatalf("an empty allowlist must be announced in the log, got %q", out)
	}
	if got := strings.Count(out, "allowlist is empty"); got != 1 {
		t.Fatalf("the empty-allowlist warning must be emitted once per channel, got %d: %q", got, out)
	}
	if !strings.Contains(out, "telegram") {
		t.Fatalf("the warning must name the channel, got %q", out)
	}

	restricted := NewBaseChannel("telegram", nil, nil, []string{"123456789"})
	out = captureLogs(t, func() {
		restricted.IsAllowed("123456789")
		restricted.IsAllowed("999")
	})
	if strings.Contains(out, "allowlist is empty") {
		t.Fatalf("a configured allowlist must not warn, got %q", out)
	}
}

func TestBaseChannelIsAllowed(t *testing.T) {
	tests := []struct {
		name      string
		allowList []string
		senderID  string
		want      bool
	}{
		{
			name:      "empty allowlist allows all",
			allowList: nil,
			senderID:  "anyone",
			want:      true,
		},
		{
			name:      "compound sender matches numeric allowlist",
			allowList: []string{"123456"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "compound sender matches username allowlist",
			allowList: []string{"@alice"},
			senderID:  "123456|alice",
			want:      true,
		},
		{
			name:      "numeric sender matches legacy compound allowlist",
			allowList: []string{"123456|alice"},
			senderID:  "123456",
			want:      true,
		},
		{
			name:      "non matching sender is denied",
			allowList: []string{"123456"},
			senderID:  "654321|bob",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := NewBaseChannel("test", nil, nil, tt.allowList)
			if got := ch.IsAllowed(tt.senderID); got != tt.want {
				t.Fatalf("IsAllowed(%q) = %v, want %v", tt.senderID, got, tt.want)
			}
		})
	}
}

func TestBaseChannelHandleMessageInvokesInboundDroppedHook(t *testing.T) {
	tests := []struct {
		name     string
		closeBus bool
		setHook  bool
		wantHook bool
	}{
		{name: "closed bus fires hook", closeBus: true, setHook: true, wantHook: true},
		{name: "accepted message does not fire hook", closeBus: false, setHook: true, wantHook: false},
		{name: "no hook configured is nil-safe", closeBus: true, setHook: false, wantHook: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messageBus := bus.NewMessageBus()
			if tt.closeBus {
				messageBus.Close()
			} else {
				defer messageBus.Close()
			}

			ch := NewBaseChannel("test", nil, messageBus, nil)
			var mu sync.Mutex
			var dropped []bus.InboundMessage
			if tt.setHook {
				ch.InboundDroppedHook = func(msg bus.InboundMessage) {
					mu.Lock()
					defer mu.Unlock()
					dropped = append(dropped, msg)
				}
			}

			ch.HandleMessageWithAttachments(
				"sender1", "chat1", "hello",
				[]bus.FileAttachment{{Name: "f.txt", Path: "/tmp/f.txt", Kind: "file"}},
				map[string]string{"message_id": "7"}, "session1",
			)

			mu.Lock()
			defer mu.Unlock()
			if len(dropped) != 0 && !tt.wantHook {
				t.Fatalf("hook fired unexpectedly: %+v", dropped)
			}
			if !tt.wantHook {
				return
			}
			if len(dropped) != 1 {
				t.Fatalf("expected exactly 1 hook call, got %d", len(dropped))
			}
			got := dropped[0]
			if got.ChatID != "chat1" || got.SenderID != "sender1" || got.Content != "hello" || got.SessionKey != "session1" {
				t.Fatalf("hook received wrong message: %+v", got)
			}
			if got.Metadata["message_id"] != "7" {
				t.Fatalf("hook lost metadata: %+v", got.Metadata)
			}
			if len(got.Attachments) != 1 || got.Attachments[0].Path != "/tmp/f.txt" {
				t.Fatalf("hook lost attachments: %+v", got.Attachments)
			}
		})
	}
}

func TestBaseChannelHandleMessageFiresHookOnFullQueue(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	inLen, inCap, _, _, _, _ := messageBus.Stats()
	for i := inLen; i < inCap; i++ {
		if !messageBus.PublishInbound(bus.InboundMessage{Channel: "test"}) {
			t.Fatalf("publish %d into a not-yet-full queue returned false", i)
		}
	}

	ch := NewBaseChannel("test", nil, messageBus, nil)
	var fired int
	ch.InboundDroppedHook = func(msg bus.InboundMessage) { fired++ }

	ch.HandleMessage("sender1", "chat1", "overflow", nil, nil)
	if fired != 1 {
		t.Fatalf("expected hook to fire once on full queue, got %d", fired)
	}
}
