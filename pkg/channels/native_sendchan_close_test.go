package channels

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// Tests for SURV-03: a client abandoned mid-broadcast (QueueSend timeout on a
// full SendChan) must be torn down so its wsWriteLoop goroutine EXITS instead
// of hanging for its full 90s write deadline — a goroutine leak per abandoned
// client before the fix.
//
// Signal discipline being tested: teardown closes `done` and sets `closed`
// (it must NOT close SendChan — a concurrent QueueSend parked on a full
// SendChan would panic on "send on closed channel"). wsWriteLoop exits via
// the done case; QueueSend returns immediately via its done case.

// mustEvict wedges the client's SendChan and then routes one message through
// the broadcast path so the cleanup runs, exactly like a stalled writer would
// be caught in production. Uses a short wsQueueSendTimeout so the timeout path
// fires in milliseconds instead of the 5s production default.
func mustEvict(t *testing.T, ch *NativeChannel, client *WSClient) {
	t.Helper()
	oldTimeout := wsQueueSendTimeout
	wsQueueSendTimeout = 100 * time.Millisecond
	t.Cleanup(func() { wsQueueSendTimeout = oldTimeout })

	payload := []byte(`{"v":1,"event":"x"}`)
	for i := 0; i < wsSendChanSize+2; i++ {
		_ = client.QueueSend(payload)
	}
	// Route one more message through the broadcast path so the cleanup runs.
	ch.broadcastAll("test.evict", bus.OutboundMessage{})
}

func TestBroadcastAll_ClosesAbandonedSendChan(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-abandon-all")

	mustEvict(t, f.channel, client)

	// The client must be gone from the map.
	f.channel.mu.RLock()
	_, stillThere := f.channel.wsClients[client.ID]
	f.channel.mu.RUnlock()
	if stillThere {
		t.Fatalf("abandoned client still registered after broadcastAll")
	}

	// The done channel must be CLOSED so wsWriteLoop's select exits promptly.
	client.mu.Lock()
	doneClosed := client.doneClosed()
	client.mu.Unlock()
	if !doneClosed {
		t.Fatalf("done channel was never closed: stale wsWriteLoop would hang for its write deadline")
	}
}

func TestBroadcastToSession_ClosesAbandonedSendChan(t *testing.T) {
	f := newLifecycleFixture(t)
	// Two clients on the same session: one healthy, one abandoned. The
	// healthy one must still be registered; only the wedged one gets torn
	// down.
	healthy := f.newClient(t, "sess-mixed")
	wedged := f.newClient(t, "sess-mixed")

	// Wedge only the second client.
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

	wedged.mu.Lock()
	doneClosed := wedged.doneClosed()
	wedged.mu.Unlock()
	if !doneClosed {
		t.Fatalf("wedged client's done channel was never closed")
	}
}

// TestAbandonedClientWriterLoopExits is the end-to-end SURV-03 assertion:
// after a client is torn down, its live wsWriteLoop goroutine must terminate
// promptly instead of lingering. This is the exact leak: an idle writer with
// a healthy conn used to select forever on an open SendChan while its ping
// ticker kept the connection (and the goroutine) alive — done is now closed
// by teardown, so the select's done case fires and the loop returns.
// The wedge→cleanup path itself is covered by the two broadcast tests above.
func TestAbandonedClientWriterLoopExits(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-writer-exit")

	// Start a writer loop the way handleWSClient does.
	go f.channel.wsWriteLoop(client)

	// Wait for it to actually enter the loop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && client.activeWriteLoops.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	if client.activeWriteLoops.Load() == 0 {
		t.Fatalf("wsWriteLoop never started")
	}

	f.channel.removeWSClient(client.ID)

	// The writer must exit promptly now that done is closed.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && client.activeWriteLoops.Load() != 0 {
		time.Sleep(time.Millisecond)
	}
	if got := client.activeWriteLoops.Load(); got != 0 {
		t.Fatalf("wsWriteLoop still running after abandoned-client cleanup (leak, SURV-03): %d loops", got)
	}
}

// TestRemoveWSClientIsIdempotent guards the close-once property: removing an
// already-removed client must not panic on double close.
func TestRemoveWSClientIsIdempotent(t *testing.T) {
	f := newLifecycleFixture(t)
	client := f.newClient(t, "sess-twice")

	f.channel.removeWSClient(client.ID)
	f.channel.removeWSClient(client.ID) // must not panic
}

// TestStopClosesAllSendChans verifies the shutdown path tears down every
// registered client: removed from the map with done closed so both loops exit.
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
		c.mu.Lock()
		doneClosed := c.doneClosed()
		c.mu.Unlock()
		if !doneClosed {
			t.Fatalf("client %s done channel never closed after Stop", c.ID)
		}
	}
}

// TestQueueSendReturnsOnDone ensures QueueSend does not block for its full
// timeout once the client has been torn down (done closed) — e.g. a broadcast
// racing the cleanup. Before the done case existed, QueueSend parked for the
// entire timeout window on a full SendChan.
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
