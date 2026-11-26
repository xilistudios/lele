package durable

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func TestSpoolPumpIntervalsAreAtRestRate(t *testing.T) {
	if PumpInterval < time.Second {
		t.Fatalf("PumpInterval = %v, want >= 1s: the inbound pump polls the spool even when nothing is queued, so a sub-second ticker keeps the database busy 4x a second at rest", PumpInterval)
	}
	if OutboundPumpInterval < time.Second {
		t.Fatalf("OutboundPumpInterval = %v, want >= 1s: the outbound pump polls the spool even when nothing is queued, so a sub-second ticker keeps the database busy 4x a second at rest", OutboundPumpInterval)
	}
}

func TestSpool_KickProcessesPromptly(t *testing.T) {
	fixture := openSpool(t)
	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("StartPump() did not return after cancellation")
		}
	})

	if _, err := fixture.repo.Enqueue(store.SpoolOutbound, "telegram", "chat-1", "chat-1", "", outPayload(t, outMsg("hi"))); err != nil {
		t.Fatalf("Enqueue(outbound) failed: %v", err)
	}

	start := time.Now()
	d.FlushPeer("telegram", "chat-1")

	deadline := start.Add(OutboundPumpInterval * 3 / 4)
	for time.Now().Before(deadline) {
		if box.callCount() == 1 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}

	t.Fatalf("a kick did not deliver the queued row within %v (ticker is %v): the wake-up path is no longer immediate", deadline.Sub(start), OutboundPumpInterval)
}
