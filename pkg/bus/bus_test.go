package bus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMessageBus_PublishInboundNonBlocking(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	accepted := 0
	for i := 0; i < cap(mb.inbound)+50; i++ {
		if mb.PublishInbound(InboundMessage{Channel: "test", Content: "msg"}) {
			accepted++
		}
	}

	// The first cap(inbound) publishes must be accepted, the rest rejected.
	if accepted != cap(mb.inbound) {
		t.Errorf("expected %d accepted publishes, got %d", cap(mb.inbound), accepted)
	}

	_, _, dropped, _, _, _ := mb.Stats()
	if dropped == 0 {
		t.Error("expected some inbound messages to be dropped when buffer is full")
	}
}

// TestMessageBus_PublishInboundReturnValues pins down the acceptance
// contract: true when enqueued, false when the queue is full, false after
// Close. Callers rely on false to roll back side effects (typing
// indicators, placeholders).
func TestMessageBus_PublishInboundReturnValues(t *testing.T) {
	t.Run("accepted returns true", func(t *testing.T) {
		mb := NewMessageBus()
		defer mb.Close()

		if !mb.PublishInbound(InboundMessage{Channel: "test", Content: "ok"}) {
			t.Fatal("expected PublishInbound to return true when the queue has room")
		}
		inLen, _, dropped, _, _, _ := mb.Stats()
		if inLen != 1 || dropped != 0 {
			t.Fatalf("expected 1 queued / 0 dropped, got len=%d dropped=%d", inLen, dropped)
		}
	})

	t.Run("full queue returns false", func(t *testing.T) {
		mb := NewMessageBus()
		defer mb.Close()

		for i := 0; i < cap(mb.inbound); i++ {
			if !mb.PublishInbound(InboundMessage{Channel: "test"}) {
				t.Fatalf("publish %d into a not-yet-full queue returned false", i)
			}
		}
		if mb.PublishInbound(InboundMessage{Channel: "test", Content: "overflow"}) {
			t.Fatal("expected PublishInbound to return false when the queue is full")
		}
		_, _, dropped, _, _, _ := mb.Stats()
		if dropped != 1 {
			t.Fatalf("expected droppedInbound=1, got %d", dropped)
		}
	})

	t.Run("closed bus returns false", func(t *testing.T) {
		mb := NewMessageBus()
		mb.Close()

		if mb.PublishInbound(InboundMessage{Channel: "test"}) {
			t.Fatal("expected PublishInbound to return false after Close")
		}
	})
}

func TestMessageBus_PublishOutboundNonBlocking(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	for i := 0; i < cap(mb.outbound)+50; i++ {
		mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "msg"})
	}

	_, _, _, _, _, dropped := mb.Stats()
	if dropped == 0 {
		t.Error("expected some outbound messages to be dropped when buffer is full")
	}
}

func TestMessageBus_ConcurrentPublishDoesNotBlock(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	var wg sync.WaitGroup
	var blocked atomic.Bool

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := make(chan struct{})
			go func() {
				for j := 0; j < 1000; j++ {
					mb.PublishInbound(InboundMessage{Channel: "test", Content: "msg"})
					mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "msg"})
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				blocked.Store(true)
			}
		}()
	}

	wg.Wait()

	if blocked.Load() {
		t.Error("publish operations blocked under concurrent load")
	}
}

func TestMessageBus_PublishAfterCloseIsNoop(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	mb.PublishInbound(InboundMessage{Channel: "test"})
	mb.PublishOutbound(OutboundMessage{Channel: "test"})
}

func TestMessageBus_Stats(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "msg1"})
	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "msg2"})

	_, inCap, droppedIn, outLen, outCap, droppedOut := mb.Stats()

	if inCap != 500 {
		t.Errorf("expected inbound capacity 500, got %d", inCap)
	}
	if outCap != 500 {
		t.Errorf("expected outbound capacity 500, got %d", outCap)
	}
	if outLen != 2 {
		t.Errorf("expected 2 outbound messages, got %d", outLen)
	}
	if droppedIn != 0 {
		t.Errorf("expected 0 dropped inbound, got %d", droppedIn)
	}
	if droppedOut != 0 {
		t.Errorf("expected 0 dropped outbound, got %d", droppedOut)
	}
}

func TestMessageBus_ConsumeInboundWithContextCancel(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, ok := mb.ConsumeInbound(ctx)
	if ok {
		t.Error("expected ConsumeInbound to return false on cancelled context")
	}
}

func TestMessageBus_SubscribeOutboundWithContextCancel(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, ok := mb.SubscribeOutbound(ctx)
	if ok {
		t.Error("expected SubscribeOutbound to return false on cancelled context")
	}
}
