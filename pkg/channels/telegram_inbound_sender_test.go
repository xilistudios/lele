package channels

import (
	"context"
	"testing"
	"time"

	"github.com/mymmrac/telego"
)

func TestTelegramInbound_SenderIDIsTheNumericUserID(t *testing.T) {
	recorder := &botAPIRecorder{}
	channel := newTelegramTestChannel(t, []string{"111|alice"}, recorder)
	channel.agentLoop = newNativeTestAgentLoop(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	message := &telego.Message{
		MessageID: 41,
		From:      &telego.User{ID: 111, Username: "alice"},
		Chat:      telego.Chat{ID: -100123, Type: "group"},
		Text:      "corre el comando",
	}

	if err := channel.handleMessage(ctx, message); err != nil {
		t.Fatalf("handleMessage() error = %v", err)
	}

	inbound, ok := channel.bus.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("handleMessage published no inbound message")
	}
	if inbound.SenderID != "111" {
		t.Fatalf("inbound SenderID = %q, want the bare numeric user id %q", inbound.SenderID, "111")
	}
	if got := ApprovalRequesterForTurn(inbound.Channel, inbound.SenderID); got != "111" {
		t.Fatalf("ApprovalRequesterForTurn(%q, %q) = %q, want %q", inbound.Channel, inbound.SenderID, got, "111")
	}
	if inbound.Channel != "telegram" {
		t.Fatalf("inbound Channel = %q, want %q", inbound.Channel, "telegram")
	}
	if inbound.SessionKey != productSessionKey(t, "group", "-100123") {
		t.Fatalf("inbound SessionKey = %q, want the product group key", inbound.SessionKey)
	}
	if inbound.ChatID != "-100123" {
		t.Fatalf("inbound ChatID = %q, want %q", inbound.ChatID, "-100123")
	}
}

func TestTelegramInbound_OutsideAllowlistNeverReachesTheBus(t *testing.T) {
	recorder := &botAPIRecorder{}
	channel := newTelegramTestChannel(t, []string{"111|alice"}, recorder)
	channel.agentLoop = newNativeTestAgentLoop(nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	message := &telego.Message{
		MessageID: 42,
		From:      &telego.User{ID: 999, Username: "mallory"},
		Chat:      telego.Chat{ID: -100123, Type: "group"},
		Text:      "corre el comando",
	}

	if err := channel.handleMessage(ctx, message); err != nil {
		t.Fatalf("handleMessage() error = %v", err)
	}

	if inbound, ok := channel.bus.ConsumeInbound(ctx); ok {
		t.Fatalf("a sender outside the allowlist must not reach the bus, got %#v", inbound)
	}
}
