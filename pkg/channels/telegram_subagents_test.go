package channels

import (
	"context"
	"testing"
	"time"

	"github.com/mymmrac/telego"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestTelegramHandleCommandWithSession_SubagentsPreservesArguments(t *testing.T) {
	msgBus := bus.NewMessageBus()
	channel := &TelegramChannel{
		BaseChannel: NewBaseChannel("telegram", nil, msgBus, nil),
	}

	message := &telego.Message{
		Text:      "/subagents continue subagent-3 use branch main",
		MessageID: 77,
		Chat: telego.Chat{
			ID: 12345,
		},
		From: &telego.User{ID: 99},
	}

	if err := channel.handleCommandWithSession(context.Background(), message, "subagents"); err != nil {
		t.Fatalf("handleCommandWithSession returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inbound, ok := msgBus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("expected an inbound system message")
	}

	if inbound.Channel != "system" {
		t.Fatalf("expected system channel, got %s", inbound.Channel)
	}
	if inbound.SenderID != "99" {
		t.Fatalf("expected sender 99, got %s", inbound.SenderID)
	}
	if inbound.ChatID != "telegram:12345" {
		t.Fatalf("expected telegram session chat id, got %s", inbound.ChatID)
	}
	if inbound.SessionKey != "telegram:12345" {
		t.Fatalf("expected telegram session key, got %s", inbound.SessionKey)
	}
	if inbound.Content != "/subagents continue subagent-3 use branch main" {
		t.Fatalf("unexpected system content: %s", inbound.Content)
	}
	if inbound.Metadata["message_id"] != "77" {
		t.Fatalf("expected message_id metadata 77, got %s", inbound.Metadata["message_id"])
	}
}
