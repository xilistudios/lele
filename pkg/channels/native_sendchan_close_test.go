package channels

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

func mustEvict(t *testing.T, ch *NativeChannel, client *WSClient) {
	t.Helper()
	oldTimeout := wsQueueSendTimeout
	wsQueueSendTimeout = 100 * time.Millisecond
	t.Cleanup(func() { wsQueueSendTimeout = oldTimeout })

	payload := []byte(`{"v":1,"event":"x"}`)
	for i := 0; i < wsSendChanSize+2; i++ {
		_ = client.QueueSend(payload)
	}
	ch.broadcastAll("test.evict", bus.OutboundMessage{})
}

func TestBroadcastAll_ClosesAbandonedSendChan(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-abandon-all")

	mustEvict(t, f.channel, client)

	f.channel.mu.RLock()
	_, stillThere := f.channel.wsClients[client.ID]
	f.channel.mu.RUnlock()
	if stillThere {
		t.Fatalf("abandoned client still registered after broadcastAll")
	}

	doneClosed := client.doneClosed()
	if !doneClosed {
		t.Fatalf("done channel was never closed: stale wsWriteLoop would hang for its write deadline")
	}
}

func TestBroadcastToSession_ClosesAbandonedSendChan(t *testing.T) {
	f := newLifecycleFixture(t)
	healthy := f.newClient(t, "sess-mixed")
	wedged := f.newClient(t, "sess-mixed")

	oldTimeout := wsQueueSendTimeout
	wsQueueSendTimeout = 100 * time.Millisecond
	t.Cleanup(func() { wsQueueSendTimeout = oldTimeout })
	payload := []byte(`{"v":1,"event":"x"}`)
	for i := 0; i < wsSendChanSize+2; i++ {
		_ = wedged.QueueSend(payload)
	}

	f.channel.broadcastToSession("sess-mixed", "test.evict", bus.OutboundMessage{})

	f.channel.mu.RLock()
	_, healthyThere := f.channel.wsClients[healthy.ID]
	_, wedgedThere := f.channel.wsClients[wedged.ID]
	f.channel.mu.RUnlock()

	if !healthyThere {
		t.Fatalf("healthy client was wrongly removed")
	}
	if wedgedThere {
		t.Fatalf("wedged client not removed by broadcastToSession cleanup")
	}

	doneClosed := wedged.doneClosed()
	if !doneClosed {
		t.Fatalf("wedged client's done channel was never closed")
	}
}

func TestAbandonedClientWriterLoopExits(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-writer-exit")

	go f.channel.wsWriteLoop(client)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && client.activeWriteLoops.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if client.activeWriteLoops.Load() == 0 {
		t.Fatalf("wsWriteLoop never started")
	}

	f.channel.removeWSClient(client.ID)

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && client.activeWriteLoops.Load() != 0 {
		time.Sleep(time.Millisecond)
	}
	if got := client.activeWriteLoops.Load(); got != 0 {
		t.Fatalf("wsWriteLoop still running after abandoned-client cleanup (leak, SURV-03): %d loops", got)
	}
}

func TestRemoveWSClientIsIdempotent(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-twice")

	f.channel.removeWSClient(client.ID)
	f.channel.removeWSClient(client.ID)
}

func TestStopClosesAllSendChans(t *testing.T) {
	f := newLifecycleFixture(t)
	c1 := f.newClient(t, "sess-stop-1")
	c2 := f.newClient(t, "sess-stop-2")

	f.channel.running = true
	if err := f.channel.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	f.channel.mu.RLock()
	remaining := len(f.channel.wsClients)
	f.channel.mu.RUnlock()
	if remaining != 0 {
		t.Fatalf("%d clients still registered after Stop", remaining)
	}

	for _, c := range []*WSClient{c1, c2} {
		doneClosed := c.doneClosed()
		if !doneClosed {
			t.Fatalf("client %s done channel never closed after Stop", c.ID)
		}
	}
}

func TestQueueSendReturnsOnDone(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-queuedone")

	f.channel.removeWSClient(client.ID)

	start := time.Now()
	err := client.QueueSend([]byte(`{}`))
	if err == nil {
		t.Fatalf("expected error queueing to removed client")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("QueueSend blocked %v after removal; done-signal case missing", elapsed)
	}
}
