package channels

import (
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

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

// TestBaseChannelHandleMessageInvokesInboundDroppedHook pins the rollback
// contract of HandleMessageWithAttachments: when the bus rejects the message
// (here: closed bus), the configured InboundDroppedHook receives the exact
// message that was dropped.
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

// TestBaseChannelHandleMessageFiresHookOnFullQueue covers the other rejection
// branch: queue full (not just closed bus).
func TestBaseChannelHandleMessageFiresHookOnFullQueue(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	// Fill the inbound queue without any consumer.
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
