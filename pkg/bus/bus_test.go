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

// ──────────────────────────────────────────────────────────────────────────────
// Outbound spooler seam (B1-B4)
// ──────────────────────────────────────────────────────────────────────────────

// fakeOutboundSpooler records every call the bus makes on the seam. tag is
// the stand-in for "this implementation wrote a row": when it is true Enqueue
// assigns a fresh SpoolID, when it is false the message is left untouched
// exactly as an ineligible or durability-off Enqueue behaves.
type fakeOutboundSpooler struct {
	mu       sync.Mutex
	tag      bool
	enqueued []OutboundMessage
	released []int64
	nextID   int64
	// enqueueOrder, when set, is called while Enqueue runs so a test can
	// observe the bus state at that instant.
	enqueueOrder func()
}

func (f *fakeOutboundSpooler) Enqueue(msg *OutboundMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueued = append(f.enqueued, *msg)
	if f.enqueueOrder != nil {
		f.enqueueOrder()
	}
	if !f.tag {
		return false
	}
	f.nextID++
	msg.SpoolID = f.nextID
	return true
}

func (f *fakeOutboundSpooler) Release(msg *OutboundMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, msg.SpoolID)
	return true
}

func (f *fakeOutboundSpooler) calls() (enq, rel int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.enqueued), len(f.released)
}

func (f *fakeOutboundSpooler) releasedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.released...)
}

// B1: without a spooler the outbound path is exactly what it always was.
func TestMessageBus_PublishOutboundWithoutSpoolerUnchanged(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "a"})
	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "b", Event: "stream_end"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first, ok := mb.SubscribeOutbound(ctx)
	if !ok || first.Content != "a" {
		t.Fatalf("SubscribeOutbound() = (%+v, %v), want the first message", first, ok)
	}
	second, ok := mb.SubscribeOutbound(ctx)
	if !ok || second.Content != "b" {
		t.Fatalf("SubscribeOutbound() = (%+v, %v), want the second message", second, ok)
	}
	if second.SpoolID != 0 {
		t.Errorf("SpoolID = %d without a spooler, want 0", second.SpoolID)
	}
	if _, _, _, _, _, dropped := mb.Stats(); dropped != 0 {
		t.Errorf("droppedOutbound = %d, want 0", dropped)
	}
}

// B2: Enqueue runs before the channel handoff and the SpoolID it tags reaches
// the consumer, because msg is a value parameter and the tagged copy is what
// goes into the queue.
func TestMessageBus_PublishOutboundEnqueuesBeforeChannel(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	sp := &fakeOutboundSpooler{tag: true, enqueueOrder: func() {
		// At this instant nothing has been queued yet.
		if _, _, _, outLen, _, _ := mb.Stats(); outLen != 0 {
			t.Errorf("outbound queue length = %d during Enqueue, want 0", outLen)
		}
	}}
	mb.SetOutboundSpooler(sp)

	mb.PublishOutbound(OutboundMessage{Channel: "test", ChatID: "c1", Content: "hello"})

	if enq, _ := sp.calls(); enq != 1 {
		t.Fatalf("Enqueue called %d times, want 1", enq)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, ok := mb.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("SubscribeOutbound() = false, want the message")
	}
	if got.SpoolID != 1 {
		t.Errorf("consumer saw SpoolID = %d, want 1 (the tagged copy must be queued)", got.SpoolID)
	}
}

// B3: a full queue releases the row the spooler wrote, so the replay pump -
// not the silent drop counter - owns the message.
func TestMessageBus_PublishOutboundReleasesOnFullQueue(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	sp := &fakeOutboundSpooler{tag: true}
	mb.SetOutboundSpooler(sp)

	for i := 0; i < cap(mb.outbound); i++ {
		mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "fill"})
	}
	_, _, _, outLen, outCap, _ := mb.Stats()
	if outLen != outCap {
		t.Fatalf("queue = %d/%d, want it full before the overflow publish", outLen, outCap)
	}

	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "overflow"})

	if _, rel := sp.calls(); rel != 1 {
		t.Fatalf("Release called %d times, want 1 for the dropped message", rel)
	}
	ids := sp.releasedIDs()
	if len(ids) != 1 || ids[0] == 0 {
		t.Errorf("released SpoolIDs = %v, want exactly the non-zero id of the dropped row", ids)
	}
	if _, _, _, _, _, dropped := mb.Stats(); dropped != 1 {
		t.Errorf("droppedOutbound = %d, want 1", dropped)
	}
}

// B4: after Close the row is released too. Close() takes mu.Lock, so this also
// pins down that the spooler call never happens under mu.
func TestMessageBus_PublishOutboundReleasesWhenClosed(t *testing.T) {
	mb := NewMessageBus()

	sp := &fakeOutboundSpooler{tag: true}
	mb.SetOutboundSpooler(sp)
	mb.Close()

	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "late"})

	if _, rel := sp.calls(); rel != 1 {
		t.Fatalf("Release called %d times after Close, want 1", rel)
	}
	if ids := sp.releasedIDs(); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("released SpoolIDs = %v, want [1]", ids)
	}
}

// A spooler that declines (durability off, or an ineligible message) leaves
// SpoolID at 0, so even a dropped publish must not call Release: there is no
// row to hand back.
func TestMessageBus_PublishOutboundDeclinedSkipsRelease(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	sp := &fakeOutboundSpooler{tag: false}
	mb.SetOutboundSpooler(sp)

	for i := 0; i < cap(mb.outbound); i++ {
		mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "fill"})
	}
	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "overflow"})

	enq, rel := sp.calls()
	if enq != cap(mb.outbound)+1 {
		t.Fatalf("Enqueue called %d times, want one per publish", enq)
	}
	if rel != 0 {
		t.Errorf("Release called %d times, want 0: no row was ever written", rel)
	}
}

// A nil spooler wired explicitly is the same as wiring nothing.
func TestMessageBus_SetNilSpoolerKeepsOldPath(t *testing.T) {
	mb := NewMessageBus()
	defer mb.Close()

	mb.SetOutboundSpooler(&fakeOutboundSpooler{tag: true})
	mb.SetOutboundSpooler(nil)

	mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "hello"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got, ok := mb.SubscribeOutbound(ctx)
	if !ok {
		t.Fatal("SubscribeOutbound() = false, want the message")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d with a nil spooler, want 0", got.SpoolID)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Closed-channel consumer tests (CORE-05)
// ──────────────────────────────────────────────────────────────────────────────

func TestConsumeInbound_ReturnsFalseAfterClose(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	_, ok := mb.ConsumeInbound(context.Background())
	if ok {
		t.Error("ConsumeInbound returned ok=true after Close(); want false")
	}
}

func TestSubscribeOutbound_ReturnsFalseAfterClose(t *testing.T) {
	mb := NewMessageBus()
	mb.Close()

	_, ok := mb.SubscribeOutbound(context.Background())
	if ok {
		t.Error("SubscribeOutbound returned ok=true after Close(); want false")
	}
}

func TestConsumeInbound_LoopTerminatesAfterClose(t *testing.T) {
	mb := NewMessageBus()

	// Pre-fill the queue so the consumer has messages to read before
	// observing the closed state.
	for i := 0; i < 10; i++ {
		mb.PublishInbound(InboundMessage{Channel: "test", Content: "msg"})
	}
	mb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	count := 0
	for {
		_, ok := mb.ConsumeInbound(ctx)
		if !ok {
			break
		}
		count++
		if count > 200 {
			t.Fatalf("consumer loop did not terminate after 200 iterations (likely busy-spinning on closed channel)")
		}
	}
	// The loop terminated — that's the assertion. A pre-fix bus would
	// busy-spin forever and hit the 200-iteration cap.
}

func TestSubscribeOutbound_LoopTerminatesAfterClose(t *testing.T) {
	mb := NewMessageBus()

	for i := 0; i < 10; i++ {
		mb.PublishOutbound(OutboundMessage{Channel: "test", Content: "msg"})
	}
	mb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	count := 0
	for {
		_, ok := mb.SubscribeOutbound(ctx)
		if !ok {
			break
		}
		count++
		if count > 200 {
			t.Fatalf("consumer loop did not terminate after 200 iterations (likely busy-spinning on closed channel)")
		}
	}
}
