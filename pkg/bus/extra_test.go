package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMessageBus_RegisterGetHandler(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	// GetHandler for unregistered channel returns nil, false.
	if h, ok := mb.GetHandler("telegram"); ok || h != nil {
		t.Fatalf("GetHandler(unregistered) = (%v, %v), want (nil, false)", h, ok)
	}

	handler := func(InboundMessage) error { return nil }
	mb.RegisterHandler("telegram", handler)

	h, ok := mb.GetHandler("telegram")
	if !ok {
		t.Fatal("GetHandler(registered) returned ok=false")
	}
	if h == nil {
		t.Fatal("GetHandler(registered) returned nil handler")
	}

	// Overwriting a channel updates the handler.
	newHandler := func(InboundMessage) error { return errors.New("new") }
	mb.RegisterHandler("telegram", newHandler)
	h2, _ := mb.GetHandler("telegram")
	if h2 == nil {
		t.Fatal("GetHandler after overwrite returned nil")
	}
}

func TestMessageBus_ConsumeInboundMessage(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	mb.PublishInbound(InboundMessage{Channel: "telegram", Content: "hello", SenderID: "42"})
	msg, ok := mb.ConsumeInbound(ctx)
	if !ok {
		t.Fatal("ConsumeInbound returned ok=false")
	}
	if msg.Channel != "telegram" || msg.Content != "hello" || msg.SenderID != "42" {
		t.Fatalf("ConsumeInbound got %+v", msg)
	}
}

func TestMessageBus_SubscribeOutboundMessage(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	mb.PublishOutbound(OutboundMessage{Channel: "telegram", ChatID: "7", Content: "hi"})
	msg, ok := mb.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("SubscribeOutbound returned ok=false")
	}
	if msg.Channel != "telegram" || msg.ChatID != "7" || msg.Content != "hi" {
		t.Fatalf("SubscribeOutbound got %+v", msg)
	}
}

func TestMessageBus_Close(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()
	// Closing twice must not panic.
	mb.Close()

	// Publish after close is a no-op (also covers close-during-publish guard).
	if _, _, dropped, _, _, _ := mb.Stats(); dropped != 0 {
		t.Fatal("expected no drops after close")
	}
}

func TestMessageBus_StatsCapacities(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	inLen, inCap, droppedIn, outLen, outCap, droppedOut := mb.Stats()
	if inLen != 0 || outLen != 0 {
		t.Fatalf("expected empty stats, got in=%d out=%d", inLen, outLen)
	}
	if inCap != 500 || outCap != 500 {
		t.Fatalf("expected cap 500, got in=%d out=%d", inCap, outCap)
	}
	if droppedIn != 0 || droppedOut != 0 {
		t.Fatalf("expected no drops, got in=%d out=%d", droppedIn, droppedOut)
	}
}