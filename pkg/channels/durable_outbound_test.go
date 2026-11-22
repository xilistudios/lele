package channels

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

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

func (f *fakeCompleter) invocations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.invocs...)
}

func (f *fakeCompleter) idsFor(op string) []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]int64(nil), f.ids[op]...)
}

func (f *fakeCompleter) reasonFor(id int64) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.forgets[id]
	return r, ok
}

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

func (c *spooledChannel) sendCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.attempts
}

func (c *spooledChannel) sentChunks() []bus.OutboundMessage {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]bus.OutboundMessage(nil), c.sent...)
}

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

type outboundFixture struct {
	mgr   *Manager
	ch    Channel
	queue chan bus.OutboundMessage
	sp    *fakeCompleter
}

func newOutboundFixture(t *testing.T, messageBus *bus.MessageBus, ch Channel, startDispatcher bool, sp *fakeCompleter) *outboundFixture {
	t.Helper()

	mgr, err := NewManager(config.DefaultConfig(), messageBus, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	mgr.RegisterChannel(ch.Name(), ch)

	queue := make(chan bus.OutboundMessage, 200)
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

func waitSpool(t *testing.T, sp *fakeCompleter, op string, want int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(sp.idsFor(op)) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("spooler rows for %s = %v, want at least %d (all invocations: %v)",
		op, sp.idsFor(op), want, sp.invocations())
}

func waitBusDrained(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, outLen, _, _ := messageBus.Stats(); outLen == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("outbound messages were never consumed by dispatchOutbound")
}

func longTelegramContent(t *testing.T) string {
	t.Helper()

	content := strings.Repeat("a b ", telegramTextChunkMaxLen/4+1)
	got := splitOutboundMessage(bus.OutboundMessage{Channel: "telegram", Content: content})
	if len(got) != 2 {
		t.Fatalf("longTelegramContent splits into %d chunks, want exactly 2 (len %d)", len(got), len(content))
	}
	return content
}

func waitAttempts(t *testing.T, ch *spooledChannel, want int) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ch.sendCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("channel sends = %d, want at least %d", ch.sendCount(), want)
}

func chunkContents(chunks []bus.OutboundMessage) []string {
	out := make([]string, len(chunks))
	for i, chunk := range chunks {
		out[i] = chunk.Content
	}
	return out
}

func waitSentChunk(t *testing.T, ch *spooledChannel, content string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sent := ch.sentChunks()
		for _, chunk := range sent {
			if chunk.Content == content {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("channel never accepted a chunk %q (accepted: %v)", content, chunkContents(ch.sentChunks()))
}

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

func (r *routingFixture) fill(name string) {
	queue := r.queues[name]
	for i := 0; i < cap(queue); i++ {
		queue <- bus.OutboundMessage{Channel: name, ChatID: "chat1", Content: "filler"}
	}
}

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

func TestNoSpoolIDNoCalls(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	sp := newFakeCompleter()

	ch := newSpooledChannel("telegram")
	fx := newOutboundFixture(t, messageBus, ch, true, sp)
	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi"}
	fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, ch, 2)

	failing := newSpooledChannel("telegram", errors.New("boom"))
	fx2 := newOutboundFixture(t, messageBus, failing, true, sp)
	fx2.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi"}
	fx2.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, failing, 2)

	partial := newSpooledChannel("telegram", nil, errors.New("boom"))
	fx3 := newOutboundFixture(t, messageBus, partial, true, sp)
	fx3.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: longTelegramContent(t)}
	fx3.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "barrier"}
	waitAttempts(t, partial, 3)

	rx := newRoutingFixture(t, messageBus, sp, "telegram")
	rx.fill("telegram")
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "overflow"})
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "gone", ChatID: "chat1", Content: "no queue"})
	messageBus.PublishOutbound(bus.OutboundMessage{Channel: "cli", ChatID: "chat1", Content: "internal"})
	waitBusDrained(t, messageBus)

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

	ok := fx.mgr.SendToPeer(context.Background(), "telegram", msg)
	if !ok.Delivered || ok.Err != nil {
		t.Errorf("SendToPeer on a known channel = %+v, want Delivered", ok)
	}
}

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

	bare := &Manager{channels: map[string]Channel{"telegram": plain}}
	bare.SetOutboundSpooler(newFakeCompleter(), &fakeFlusher{})
	if bare.peerFlusher == nil {
		t.Error("peerFlusher not stored when no channel can receive it")
	}
}

func TestNoSpoolerWiredSendsNormally(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	cases := []struct {
		name       string
		message    bus.OutboundMessage
		responses  []error
		wantChunks []string
	}{
		{
			name:       "delivered",
			message:    bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7},
			wantChunks: []string{"hi"},
		},
		{
			name:    "send fails",
			message: bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SpoolID: 7},
			responses: []error{
				errors.New("temporary network hiccup"),
				errors.New("temporary network hiccup"),
				errors.New("temporary network hiccup"),
			},
			wantChunks: nil,
		},
		{
			name:       "content-less signal",
			message:    bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Event: "turn.end", SpoolID: 7},
			wantChunks: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := newSpooledChannel("telegram", tc.responses...)
			fx := newOutboundFixture(t, messageBus, ch, true, nil)

			barrier := "barrier-" + strings.ReplaceAll(tc.name, " ", "-")
			fx.queue <- tc.message
			fx.queue <- bus.OutboundMessage{Channel: "telegram", ChatID: "chat1", Content: barrier, SpoolID: 7}
			waitSentChunk(t, ch, barrier)

			var got []string
			for _, chunk := range ch.sentChunks() {
				if chunk.Content == barrier {
					break
				}
				got = append(got, chunk.Content)
			}
			if !reflect.DeepEqual(got, tc.wantChunks) {
				t.Errorf("chunks accepted before the barrier = %v, want %v", got, tc.wantChunks)
			}
		})
	}

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
