// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

// Durable outbound, consumer side.
//
// pkg/durable owns the spool row; this package decides what happens to it once a
// message has been routed and sent. The rules these tests pin are the whole
// outbound protocol as seen from the dispatcher:
//
//	every chunk delivered         -> Complete (row deleted, first action after success)
//	failed before chunk 1         -> Release  (pump replays the message whole)
//	failed after chunk k>=1       -> Forget   (dead-letter; a replay would re-send k chunks)
//	dropped by routing            -> Release  (nothing reached the wire, and a channel
//	                                           that is off today may come back)
//	msg.SpoolID == 0 / no spooler -> no call at all: the flag-off path is the old code
//
// Nobody here writes a row: Enqueue belongs to the bus hook (see pkg/bus), and
// the replay pump calls the same SendNow path these tests exercise, so live
// traffic and replay cannot diverge.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test doubles
// ──────────────────────────────────────────────────────────────────────────────

// fakeCompleter stands in for *durable.Outbound. It records every invocation,
// including ones it would refuse, and the row id each one named. The unfiltered
// counter is what makes the flag-off test genuine: the real methods return false
// for SpoolID == 0, so a test that only counted accepted rows could not tell a
// dispatcher that never calls from one that calls and gets refused.
type fakeCompleter struct {
	mu      sync.Mutex
	invocs  []string
	ids     map[string][]int64
	forgets map[int64]string
}

func newFakeCompleter() *fakeCompleter {
	return &fakeCompleter{
		ids:     map[string][]int64{},
		forgets: map[int64]string{},
	}
}

func (f *fakeCompleter) Complete(msg *bus.OutboundMessage) bool { return f.record("complete", msg) }

func (f *fakeCompleter) Release(msg *bus.OutboundMessage) bool { return f.record("release", msg) }

func (f *fakeCompleter) Forget(msg *bus.OutboundMessage, reason string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.invocs = append(f.invocs, "forget")
	if msg == nil || msg.SpoolID == 0 {
		return false
	}
	f.ids["forget"] = append(f.ids["forget"], msg.SpoolID)
	f.forgets[msg.SpoolID] = reason
	return true
}

// record mirrors the real methods' guard while still counting the call.
func (f *fakeCompleter) record(op string, msg *bus.OutboundMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.invocs = append(f.invocs, op)
	if msg == nil || msg.SpoolID == 0 {
		return false
	}
	f.ids[op] = append(f.ids[op], msg.SpoolID)
	return true
}

// invocations returns every method name the dispatcher used, accepted or not.
func (f *fakeCompleter) invocations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.invocs...)
}

// idsFor returns the row ids recorded for one method.
func (f *fakeCompleter) idsFor(op string) []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]int64(nil), f.ids[op]...)
}

// reasonFor returns the reason Forget was given for one row.
func (f *fakeCompleter) reasonFor(id int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.forgets[id]
	return r, ok
}

// spooledChannel scripts Channel.Send per chunk: attempts counts Send calls, so
// a test can let chunk 1 through and fail chunk 2 on the next one. Responses are
// consumed in order; past the end, Send succeeds.
type spooledChannel struct {
	mu        sync.Mutex
	name      string
	responses []error
	attempts  int
	sent      []bus.OutboundMessage
}

func newSpooledChannel(name string, responses ...error) *spooledChannel {
	return &spooledChannel{name: name, responses: responses}
}

func (c *spooledChannel) Name() string                { return c.name }
func (c *spooledChannel) IsRunning() bool             { return true }
func (c *spooledChannel) IsAllowed(string) bool       { return true }
func (c *spooledChannel) Start(context.Context) error { return nil }
func (c *spooledChannel) Stop(context.Context) error  { return nil }

func (c *spooledChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.attempts++
	if c.attempts <= len(c.responses) {
		if err := c.responses[c.attempts-1]; err != nil {
			return err
		}
	}
	c.sent = append(c.sent, msg)
	return nil
}

// sendCount reports how many Sends reached the channel, under the lock.
func (c *spooledChannel) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.attempts
}

// sentChunks returns the messages the channel actually accepted, under the lock.
// Unlike sendCount this excludes refused attempts, which is what tells a delivered
// chunk from a Send call that ended in error.
func (c *spooledChannel) sentChunks() []bus.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]bus.OutboundMessage(nil), c.sent...)
}

// flushSetterChannel is the shape NativeChannel will have after Task C: a channel
// that can be handed the pump's wake-up signal. Declaring it here keeps the
// propagation contract testable without touching native.go.
type flushSetterChannel struct {
	*mockChannel
	mu   sync.Mutex
	flux []PeerFlusher
}

func (c *flushSetterChannel) SetOutboundFlusher(f PeerFlusher) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.flux = append(c.flux, f)
}

func (c *flushSetterChannel) lastFlusher() PeerFlusher {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.flux) == 0 {
		return nil
	}
	return c.flux[len(c.flux)-1]
}

// fakeFlusher is the PeerFlusher handed to SetOutboundSpooler.
type fakeFlusher struct {
	mu    sync.Mutex
	kicks []string
}

func (f *fakeFlusher) FlushPeer(channel, peer string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.kicks = append(f.kicks, channel+"/"+peer)
}

func (f *fakeFlusher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.kicks)
}

// ──────────────────────────────────────────────────────────────────────────────
// Fixtures
// ──────────────────────────────────────────────────────────────────────────────

// outboundFixture is a Manager with one channel registered and, when asked, the
// dispatcher for it running. It is built the way multi_channel_test.go and
// turn_end_test.go build theirs: NewManager on the default config creates no
// channel at all, so the channel under test is added by hand together with the
// queue its dispatcher reads.
type outboundFixture struct {
	mgr   *Manager
	ch    Channel
	queue chan bus.OutboundMessage
	sp    *fakeCompleter
}

// newOutboundFixture wires the spooler and, if startDispatcher, the per-channel
// dispatcher. It never starts dispatchOutbound: pushing straight into fx.queue
// exercises startChannelDispatcher alone, which is where the send and the
// cycle-closing live. Passing startDispatcher false leaves the queue undrained,
// which is how the overflow case is produced.
func newOutboundFixture(t *testing.T, messageBus *bus.MessageBus, ch Channel, startDispatcher bool, sp *fakeCompleter) *outboundFixture {
	t.Helper()

	mgr, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	mgr.RegisterChannel(ch.Name(), ch)

	queue := make(chan bus.OutboundMessage, 200) // same capacity initChannels uses
	mgr.mu.Lock()
	mgr.dispatchQueues[ch.Name()] = queue
	mgr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if sp != nil {
		mgr.SetOutboundSpooler(sp, &fakeFlusher{})
	}
	if startDispatcher {
		go mgr.startChannelDispatcher(ctx, ch.Name(), ch, queue)
	}

	return &outboundFixture{mgr: mgr, ch: ch, queue: queue, sp: sp}
}

// waitSpool polls until the completer reports the wanted number of accepted rows
// for one method. The dispatcher is a goroutine, so a spool call cannot be
// asserted right after a queue push; this is the same "poll until stable" shape
// turn_end_test.go uses for sends.
func waitSpool(t *testing.T, sp *fakeCompleter, op string, want int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sp.idsFor(op)) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("spooler rows for %s = %v, want at least %d (all invocations: %v)",
		op, sp.idsFor(op), want, sp.invocations())
}

// waitBusDrained blocks until every published outbound message has been consumed
// by dispatchOutbound, which is how a routing-exit test knows its message was
// actually routed rather than still sitting in the bus buffer.
func waitBusDrained(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, outLen, _, _ := messageBus.Stats(); outLen == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("outbound messages were never consumed by dispatchOutbound")
}

// longTelegramContent returns content that splitOutboundMessage cuts into
// EXACTLY two text chunks for the "telegram" channel name. The chunk limit is
// keyed on the channel NAME, not on the Channel implementation, so a fake called
// telegram splits like the real one - which is what lets the partial case be
// reached with no production hook of its own.
//
// It checks its own premise: a third chunk would make the barrier arithmetic
// below wrong, and one chunk would silently turn the partial case into a plain
// failure. Both would be confusing test failures rather than this one line.
func longTelegramContent(t *testing.T) string {
	t.Helper()

	content := strings.Repeat("a b ", telegramTextChunkMaxLen/4+1)
	got := splitOutboundMessage(bus.OutboundMessage{Channel: "telegram", Content: content})
	if len(got) != 2 {
		t.Fatalf("longTelegramContent splits into %d chunks, want exactly 2 (len %d)", len(got), len(content))
	}
	return content
}

// waitAttempts blocks until the channel has been asked to send `want` times.
//
// It is also the barrier for "the dispatcher finished the message before this
// one": startChannelDispatcher is a single goroutine working through one queue in
// order, so a second message whose send is observed proves the first one ran to
// the end - including the spool call that follows its send. That is why no test
// below sleeps or reads from a dispatcher's queue: reading the queue would steal
// the message the dispatcher is supposed to deliver.
func waitAttempts(t *testing.T, ch *spooledChannel, want int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ch.sendCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("channel sends = %d, want at least %d", ch.sendCount(), want)
}

// ──────────────────────────────────────────────────────────────────────────────
// C1-C3: startChannelDispatcher, the three arms of the send result
// ──────────────────────────────────────────────────────────────────────────────

// C1: every chunk out means the row is deleted - and deleted for the row that
// backed THIS message, not for some other id the spooler might remember.
func TestDispatcherCompletesOnDelivered(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	ch := newSpooledChannel("telegram")
	sp := newFakeCompleter()
	fx := newOutboundFixture(t, messageBus, ch, true, sp)

	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7}

	waitSpool(t, sp, "complete", 1)
	if got := sp.invocations(); len(got) != 1 || got[0] != "complete" {
		t.Errorf("spooler invocations = %v, want exactly [complete]", got)
	}
	if got := sp.idsFor("complete"); len(got) != 1 || got[0] != 7 {
		t.Errorf("Complete row ids = %v, want [7]", got)
	}
	if n := ch.sendCount(); n != 1 {
		t.Errorf("channel sends = %d, want 1", n)
	}
}

// C2: a failure on the first chunk means nothing reached the channel, so the row
// goes back to pending and the pump replays the message whole.
func TestDispatcherReleasesOnFailure(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	ch := newSpooledChannel("telegram", errors.New("boom"))
	sp := newFakeCompleter()
	fx := newOutboundFixture(t, messageBus, ch, true, sp)

	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7}

	waitSpool(t, sp, "release", 1)
	if got := sp.invocations(); len(got) != 1 || got[0] != "release" {
		t.Errorf("spooler invocations = %v, want exactly [release]: a message that never "+
			"started must be replayed, never completed or forgotten", got)
	}
	if got := sp.idsFor("release"); len(got) != 1 || got[0] != 7 {
		t.Errorf("Release row ids = %v, want [7]", got)
	}
}

// C3: chunk 1 is on the wire when chunk 2 fails. Releasing would re-send chunk 1
// and the user would read it twice, so the row must be dead-lettered instead.
func TestDispatcherForgetsOnPartial(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	content := longTelegramContent(t)
	ch := newSpooledChannel("telegram", nil, errors.New("boom"))
	sp := newFakeCompleter()
	fx := newOutboundFixture(t, messageBus, ch, true, sp)

	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: content, SpoolID: 7}

	waitSpool(t, sp, "forget", 1)
	if got := sp.invocations(); len(got) != 1 || got[0] != "forget" {
		t.Errorf("spooler invocations = %v, want exactly [forget]: releasing would re-send chunk 1", got)
	}
	if n := ch.sendCount(); n != 2 {
		t.Errorf("channel sends = %d, want 2 (one delivered, one failed)", n)
	}
	reason, ok := sp.reasonFor(7)
	if !ok || !strings.Contains(reason, "boom") {
		t.Errorf("Forget reason for row 7 = %q (found %v), want the send error", reason, ok)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// C4 + the other routing exits: dispatchOutbound used to lose the message
// ──────────────────────────────────────────────────────────────────────────────

// routingFixture runs dispatchOutbound over a set of named channels, none of them
// drained by a dispatcher, so every message stops at a routing exit.
type routingFixture struct {
	mgr    *Manager
	queues map[string]chan bus.OutboundMessage
	sp     *fakeCompleter
}

func newRoutingFixture(t *testing.T, messageBus *bus.MessageBus, sp *fakeCompleter, names ...string) *routingFixture {
	t.Helper()

	mgr, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	queues := make(map[string]chan bus.OutboundMessage, len(names))
	mgr.mu.Lock()
	for _, name := range names {
		ch := newSpooledChannel(name)
		mgr.channels[name] = ch
		queues[name] = make(chan bus.OutboundMessage, 200)
		mgr.dispatchQueues[name] = queues[name]
	}
	mgr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	mgr.SetOutboundSpooler(sp, &fakeFlusher{})
	go mgr.dispatchOutbound(ctx)

	return &routingFixture{mgr: mgr, queues: queues, sp: sp}
}

// fill packs a channel queue so the next routed message hits the overflow branch.
func (r *routingFixture) fill(name string) {
	queue := r.queues[name]
	for i := 0; i < cap(queue); i++ {
		queue <- bus.OutboundMessage{Channel: name, ChatID: "chat1", Content: "filler"}
	}
}

// C4: the bus accepted the message and the per-channel queue killed it. Without
// this release, durability is paid for and then thrown away at the last hop.
// This is the gap the bus-side hook cannot close on its own.
func TestDispatcherReleasesWhenQueueFull(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	sp := newFakeCompleter()
	rx := newRoutingFixture(t, messageBus, sp, "telegram")
	rx.fill("telegram")
	if n := len(rx.queues["telegram"]); n != 200 {
		t.Fatalf("queue holds %d messages, want the production capacity 200", n)
	}

	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "overflow", SpoolID: 7})
	waitBusDrained(t, messageBus)

	waitSpool(t, sp, "release", 1)
	if got := sp.invocations(); len(got) != 1 || got[0] != "release" {
		t.Errorf("spooler invocations = %v, want exactly [release]", got)
	}
	if got := sp.idsFor("release"); len(got) != 1 || got[0] != 7 {
		t.Errorf("Release row ids = %v, want [7]", got)
	}
}

// The two routing exits that only log: an internal channel and a channel with no
// dispatch queue. Both must Release rather than Forget - nothing reached the wire,
// and a channel that is off today may be on tomorrow, in which case the pump's own
// unknown-channel failure is what climbs attempts and dead-letters the row.
//
// For the internal channel the release is defensive: ShouldSpoolOutbound already
// excludes internal channels, so a real message never gets a SpoolID here. The
// fake counts the invocation anyway, which is what proves the exit closes the cycle
// instead of assuming it cannot be reached.
func TestDispatchOutboundReleasesOnRoutingDrops(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	sp := newFakeCompleter()
	rx := newRoutingFixture(t, messageBus, sp, "telegram")
	if _, ok := rx.queues["gone"]; ok {
		t.Fatal("fixture registered a queue for \"gone\": the unknown-channel exit needs none")
	}

	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "cli", ChatID: "chat1", Content: "internal", SpoolID: 7})
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "gone", ChatID: "chat1", Content: "no queue", SpoolID: 8})
	waitBusDrained(t, messageBus)

	waitSpool(t, sp, "release", 2)
	if got := sp.invocations(); len(got) != 2 {
		t.Errorf("spooler invocations = %v, want exactly 2", got)
	}
	for _, op := range []string{"complete", "forget"} {
		if got := sp.idsFor(op); len(got) != 0 {
			t.Errorf("%s row ids = %v, want none: a routing drop sends nothing", op, got)
		}
	}
	if got := sp.idsFor("release"); len(got) != 2 || got[0] != 7 || got[1] != 8 {
		t.Errorf("Release row ids = %v, want [7 8] in publish order", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// C5: flag off - no row, no calls, no change of behaviour
// ──────────────────────────────────────────────────────────────────────────────

// With durability off every message carries SpoolID == 0, and the helpers must
// then stay invisible: the dispatcher sends exactly as it did before this feature
// and the spooler is never named, not even to be refused.
//
// Each send case is followed by a barrier message on the same dispatcher, so the
// assertion below runs only after the dispatcher has returned from the case under
// test - no sleeps, and no reading the queue behind the dispatcher's back.
func TestNoSpoolIDNoCalls(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	sp := newFakeCompleter()

	// Send path: delivered, failed, and partial - all with no row behind them.
	ch := newSpooledChannel("telegram")
	fx := newOutboundFixture(t, messageBus, ch, true, sp)
	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi"}
	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, ch, 2)

	failing := newSpooledChannel("telegram", errors.New("boom"))
	fx2 := newOutboundFixture(t, messageBus, failing, true, sp)
	fx2.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi"}
	fx2.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, failing, 2) // attempt 1 fails, barrier succeeds

	partial := newSpooledChannel("telegram", nil, errors.New("boom"))
	fx3 := newOutboundFixture(t, messageBus, partial, true, sp)
	fx3.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: longTelegramContent(t)}
	fx3.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, partial, 3) // chunk 1 ok, chunk 2 fails, barrier ok

	// Routing exits: overflow, unknown channel, internal channel.
	rx := newRoutingFixture(t, messageBus, sp, "telegram")
	rx.fill("telegram")
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "overflow"})
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "gone", ChatID: "chat1", Content: "no queue"})
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "cli", ChatID: "chat1", Content: "internal"})
	waitBusDrained(t, messageBus)

	// The dropped event signal is the last forgetOutbound caller. Its barrier is a
	// real message that must reach Send, which also proves the guard did not
	// swallow it.
	dropping := newSpooledChannel("telegram")
	fx4 := newOutboundFixture(t, messageBus, dropping, true, sp)
	fx4.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Event: "turn.end"}
	fx4.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, dropping, 1)
	if n := dropping.sendCount(); n != 1 {
		t.Errorf("channel sends = %d, want 1: only the barrier may be sent, never the signal", n)
	}

	if got := sp.invocations(); len(got) != 0 {
		t.Errorf("spooler invocations = %v, want none when SpoolID == 0", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// C6 + C7: the name-resolving send path, and turning the seam off
// ──────────────────────────────────────────────────────────────────────────────

// An unknown channel is a transient failure, not a partial one: the pump must be
// able to Release the row and retry, so SendToPeer reports it with both flags
// clear. A known channel goes through the same SendNow as live traffic.
func TestSendToPeerUnknownChannel(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	ch := newSpooledChannel("telegram")
	fx := newOutboundFixture(t, messageBus, ch, false, nil)
	msg := bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi"}

	got := fx.mgr.SendToPeer(context.Background(), "no-such-channel", msg)
	if got.Delivered || got.SentAnyChunk {
		t.Errorf("SendToPeer outcome = %+v, want no flags set for an unknown channel", got)
	}
	if got.Err == nil {
		t.Error("SendToPeer error = nil, want the unknown-channel failure")
	}
	if n := ch.sendCount(); n != 0 {
		t.Errorf("channel sends = %d, want 0", n)
	}

	// The same call on a registered channel delivers, which is the whole point of
	// resolving by name: the pump only ever has the name from the row.
	ok := fx.mgr.SendToPeer(context.Background(), "telegram", msg)
	if !ok.Delivered || ok.Err != nil {
		t.Errorf("SendToPeer on a known channel = %+v, want Delivered", ok)
	}
}

// Turning the seam off must leave the manager able to send: every helper reads the
// spooler under the lock and returns on nil, so a nil pair is the old behaviour and
// not a nil-pointer dereference. The flusher is un-forwarded too, so a channel that
// had been wired goes back to silence.
func TestSetOutboundSpoolerNilSafe(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	flushable := &flushSetterChannel{mockChannel: newMockChannel("native", messageBus)}
	ch := newSpooledChannel("telegram")
	fx := newOutboundFixture(t, messageBus, ch, true, nil)
	fx.mgr.RegisterChannel(flushable.Name(), flushable)

	sp := newFakeCompleter()
	fl := &fakeFlusher{}
	fx.mgr.SetOutboundSpooler(sp, fl)
	if got := flushable.lastFlusher(); got != PeerFlusher(fl) {
		t.Errorf("channel flusher = %v, want the one SetOutboundSpooler was given", got)
	}
	if fl.count() != 0 {
		t.Errorf("FlushPeer calls = %d, want 0: wiring must not replay anything", fl.count())
	}

	// Off again: the flusher is un-forwarded, and a message that still carries a
	// stale SpoolID must be sent normally and touch nothing.
	fx.mgr.SetOutboundSpooler(nil, nil)
	if got := flushable.lastFlusher(); got != nil {
		t.Errorf("channel flusher = %v after nil,nil, want nil", got)
	}

	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7}
	waitAttempts(t, ch, 1)
	if got := sp.invocations(); len(got) != 0 {
		t.Errorf("spooler invocations = %v, want none after nil,nil", got)
	}
}

// The propagation contract of SetOutboundSpooler: channels are matched by the
// outboundFlushSetter capability, not by name, so a channel that cannot be reached
// is skipped silently and one that can is covered without editing this loop. Today
// no channel in the package implements the setter - NativeChannel gains it in
// Task C - so the loop is a no-op, and that is the state this test pins: it must
// not start failing the moment the wiring exists.
func TestSetOutboundSpoolerPropagatesByCapability(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	flushable := &flushSetterChannel{mockChannel: newMockChannel("native", messageBus)}
	plain := newSpooledChannel("telegram")

	mgr, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	mgr.RegisterChannel(flushable.Name(), flushable)
	mgr.RegisterChannel(plain.Name(), plain)

	if _, ok := Channel(plain).(outboundFlushSetter); ok {
		t.Error("spooledChannel unexpectedly satisfies outboundFlushSetter; the skip path is no longer covered")
	}

	mgr.SetOutboundSpooler(newFakeCompleter(), &fakeFlusher{})
	if flushable.lastFlusher() == nil {
		t.Error("channel with the setter was not handed the flusher")
	}

	// No channel implements the setter yet: the loop must simply do nothing.
	bare := &Manager{channels: map[string]Channel{"telegram": plain}}
	bare.SetOutboundSpooler(newFakeCompleter(), &fakeFlusher{})
	if bare.peerFlusher == nil {
		t.Error("peerFlusher not stored when no channel can receive it")
	}
}

// The other switch: a manager that was never given a spooler at all. Every
// deployment that leaves the feature off runs this code, so the guards must keep
// it from panicking and from touching anything - and the message must still be
// delivered, which is the part a nil-guard bug would break.
func TestNoSpoolerWiredSendsNormally(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	cases := []struct {
		name    string
		message bus.OutboundMessage
		// wantSends is what the channel must have received; 0 means the message
		// is dropped before any send, as with a content-less event signal.
		wantSends int
	}{
		{
			name:      "delivered",
			message:   bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7},
			wantSends: 1,
		},
		{
			name:      "send fails",
			message:   bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7},
			wantSends: 0,
		},
		{
			name:      "content-less signal",
			message:   bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Event: "turn.end", SpoolID: 7},
			wantSends: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newSpooledChannel("telegram")
			if tc.name == "send fails" {
				ch = newSpooledChannel("telegram", errors.New("boom"))
			}
			fx := newOutboundFixture(t, messageBus, ch, true, nil)

			fx.queue <- tc.message
			fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
			// The barrier is the second send (or the first, for the signal).
			waitAttempts(t, ch, tc.wantSends+1)

			if n := len(ch.sentChunks()); n != tc.wantSends+1 {
				t.Errorf("channel received %d chunks, want %d", n, tc.wantSends+1)
			}
		})
	}

	// Routing exits with no spooler: the message is dropped and nothing panics.
	plain, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	plainQueue := make(chan bus.OutboundMessage, 1)
	plain.mu.Lock()
	plain.channels["telegram"] = newSpooledChannel("telegram")
	plain.dispatchQueues["telegram"] = plainQueue
	plain.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go plain.dispatchOutbound(ctx)

	plainQueue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "filler"}
	messageBus.PublishOutbound(bus.OutboundMessage{
		Channel: "telegram", ChatID: "chat1", Content: "overflow", SpoolID: 7,
	})
	waitBusDrained(t, messageBus)
}
