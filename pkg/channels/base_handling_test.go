package channels

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestBaseChannelHandleMessageWithAttachments(t *testing.T) {
	mb := bus.NewMessageBus()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan bus.InboundMessage, 10)
	go func() {
		for {
			msg, ok := mb.ConsumeInbound(ctx)
			if !ok {
				return
			}
			received <- msg
		}
	}()
	defer func() { cancel(); mb.Close() }()

	ch := NewBaseChannel("test", nil, mb, nil)
	attachments := []bus.FileAttachment{
		{Name: "a.png", Path: "/tmp/a.png", MIMEType: "image/png"},
		{Path: ""}, // empty path should be filtered from Media
	}
	ch.HandleMessageWithAttachments("sender1", "chat1", "hello", attachments, map[string]string{"k": "v"}, "sk1")

	msg := <-received
	if msg.Channel != "test" {
		t.Errorf("Channel = %q", msg.Channel)
	}
	if msg.SenderID != "sender1" || msg.ChatID != "chat1" {
		t.Errorf("sender/chat = %q/%q", msg.SenderID, msg.ChatID)
	}
	if msg.Content != "hello" {
		t.Errorf("Content = %q", msg.Content)
	}
	if msg.SessionKey != "sk1" {
		t.Errorf("SessionKey = %q", msg.SessionKey)
	}
	if msg.Metadata["k"] != "v" {
		t.Errorf("Metadata = %v", msg.Metadata)
	}
	// Only the attachment with a non-empty Path should appear in Media.
	if len(msg.Media) != 1 || msg.Media[0] != "/tmp/a.png" {
		t.Errorf("Media = %v", msg.Media)
	}
	if len(msg.Attachments) != 2 {
		t.Errorf("Attachments len = %d", len(msg.Attachments))
	}
}

func TestBaseChannelHandleMessageWithAttachments_NotAllowed(t *testing.T) {
	mb := bus.NewMessageBus()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan bus.InboundMessage, 10)
	go func() {
		for {
			msg, ok := mb.ConsumeInbound(ctx)
			if !ok {
				return
			}
			received <- msg
		}
	}()
	defer func() { cancel(); mb.Close() }()

	ch := NewBaseChannel("test", nil, mb, []string{"999"})
	ch.HandleMessageWithAttachments("blocked", "chat1", "hi", nil, nil, "")

	select {
	case msg := <-received:
		t.Errorf("expected no inbound message for disallowed sender, got %v", msg)
	case <-time.After(100 * time.Millisecond):
		// OK - nothing received.
	}
}

func TestBaseChannelHandleMessage(t *testing.T) {
	mb := bus.NewMessageBus()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan bus.InboundMessage, 3)
	go func() {
		for {
			msg, ok := mb.ConsumeInbound(ctx)
			if !ok {
				return
			}
			received <- msg
		}
	}()
	defer func() { cancel(); mb.Close() }()

	ch := NewBaseChannel("test", nil, mb, nil)
	// HandleMessage → HandleMessageWithSession → HandleMessageWithAttachments
	media := []string{"/tmp/x.jpg", ""}
	ch.HandleMessage("sender", "chat", "content", media, nil)

	msg := <-received
	if msg.Content != "content" {
		t.Errorf("Content = %q", msg.Content)
	}
	if msg.SessionKey != "" {
		t.Errorf("SessionKey = %q", msg.SessionKey)
	}
	// HandleMessageWithSession routes to attachments with media paths.
	if len(msg.Attachments) != 2 {
		t.Errorf("expected 2 attachments, got %d", len(msg.Attachments))
	}
}

func TestBaseChannelNameAndRunning(t *testing.T) {
	ch := NewBaseChannel("mychan", nil, nil, nil)
	if ch.Name() != "mychan" {
		t.Errorf("Name() = %q", ch.Name())
	}
	if ch.IsRunning() {
		t.Error("IsRunning should be false initially")
	}
	ch.setRunning(true)
	if !ch.IsRunning() {
		t.Error("IsRunning should be true after setRunning(true)")
	}
	ch.setRunning(false)
	if ch.IsRunning() {
		t.Error("IsRunning should be false after setRunning(false)")
	}
}
