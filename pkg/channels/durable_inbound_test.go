// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

// Durable inbound, publisher side.
//
// pkg/durable owns the spool row; this package decides WHEN it is written. The
// rule these tests pin is that a channel may publish a message only after the
// spooler has been offered the chance to back it, and that the spooler stays
// invisible to the rest of the flow:
//
//	spooler wired + write succeeds -> row exists (claimed) before the bus sees it
//	spooler wired + write fails    -> message is published anyway (availability)
//	spooler wired + bus rejects    -> rollback hook fires AND that one row is
//	                                  released back to pending for the pump
//	spooler nil                    -> behaviour identical to before the feature
//
// Nobody here completes or deletes a row: Finish belongs to the consumer (the
// agent loop), and after a rejected publish the row is the only copy of the
// message left, so the most the publisher may do is hand it back to pending.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test double
// ──────────────────────────────────────────────────────────────────────────────

// fakeSpooler stands in for *durable.Inbound. It tags SpoolID/DedupeID in place
// exactly like the real Enqueue and keeps a snapshot of every row it accepted,
// so a test can check that nothing deletes it. Release is the other half of the
// real protocol: it marks exactly the row it is handed as handed back to the
// pump, which is what a rejected publish must trigger. messageBus is optional:
// when set, Enqueue also samples how deep the inbound queue is at that instant,
// which is what proves the write happened before the publish.
type fakeSpooler struct {
	mu       sync.Mutex
	messageB *bus.MessageBus
	nextID   int64
	fail     bool

	calls   int
	rows    []bus.InboundMessage
	depths  []int64
	release []int64
}

func newFakeSpooler(messageBus *bus.MessageBus) *fakeSpooler {
	return &fakeSpooler{messageB: messageBus, nextID: 100}
}

// Enqueue mirrors durable.Inbound.Enqueue: identity in place, true only when
// the row was written, and never a publish.
func (f *fakeSpooler) Enqueue(msg *bus.InboundMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.messageB != nil {
		inLen, _, _, _, _, _ := f.messageB.Stats()
		f.depths = append(f.depths, inLen)
	}

	if f.fail || msg == nil {
		return false
	}

	if msg.DedupeID == "" {
		if id := msg.Metadata["message_id"]; id != "" {
			msg.DedupeID = id
		} else {
			msg.DedupeID = "synth-" + msg.Content
		}
	}
	msg.SpoolID = f.nextID
	f.nextID++
	f.rows = append(f.rows, *msg)
	return true
}

// Release mirrors durable.Inbound.Release: only a message Enqueue actually
// backed (non-zero SpoolID) has a row to hand back, and the call is scoped to
// exactly that one id.
func (f *fakeSpooler) Release(msg *bus.InboundMessage) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	if msg == nil || msg.SpoolID == 0 {
		return false
	}
	f.release = append(f.release, msg.SpoolID)
	return true
}

// released returns the ids Release was asked to hand back, under the lock.
func (f *fakeSpooler) released() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.release...)
}

// snapshot returns the recorded state under the lock.
func (f *fakeSpooler) snapshot() (int, []bus.InboundMessage, []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls,
		append([]bus.InboundMessage(nil), f.rows...),
		append([]int64(nil), f.depths...)
}

// setFail scripts a spool write failure, as a closed database would cause.
func (f *fakeSpooler) setFail(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = v
}

// waitInbound reads one message from the bus, reporting false on timeout.
func waitInbound(t *testing.T, messageBus *bus.MessageBus, within time.Duration) (bus.InboundMessage, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	return messageBus.ConsumeInbound(ctx)
}

// ──────────────────────────────────────────────────────────────────────────────
// BaseChannel.publishInbound
// ──────────────────────────────────────────────────────────────────────────────

// TestPublishInboundSpoolsBeforePublish is the core ordering guarantee: the row
// must exist before the message can be observed on the bus, or a crash between
// the two silently loses a message the channel already accepted.
func TestPublishInboundSpoolsBeforePublish(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	spooler := newFakeSpooler(messageBus)
	ch := NewBaseChannel("telegram", nil, messageBus, nil)
	ch.SetInboundSpooler(spooler)

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SessionKey: "telegram:chat1"}
	ch.publishInbound(&msg)

	calls, rows, depths := spooler.snapshot()
	if calls != 1 || len(rows) != 1 {
		t.Fatalf("Enqueue called %d times with %d rows, want 1 and 1", calls, len(rows))
	}
	if len(depths) != 1 || depths[0] != 0 {
		t.Errorf("inbound queue depth at Enqueue = %v, want [0]: nothing may be published yet", depths)
	}

	got, ok := waitInbound(t, messageBus, time.Second)
	if !ok {
		t.Fatal("no inbound message published")
	}
	if got.SpoolID == 0 || got.DedupeID == "" {
		t.Errorf("published identity = (%d, %q), want the spooler's tags", got.SpoolID, got.DedupeID)
	}
	if got.SpoolID != msg.SpoolID || got.DedupeID != msg.DedupeID {
		t.Errorf("caller identity = (%d, %q), published (%d, %q): Enqueue must tag the caller's message",
			msg.SpoolID, msg.DedupeID, got.SpoolID, got.DedupeID)
	}
}

// Without a spooler the path must be exactly what it was before the feature:
// the message is published and nothing panics.
func TestPublishInboundWithoutSpoolerIsUnchanged(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	ch := NewBaseChannel("telegram", nil, messageBus, nil)
	if ch.InboundSpooler != nil {
		t.Fatal("InboundSpooler set on a fresh channel, want nil by default")
	}

	var dropped int
	ch.InboundDroppedHook = func(bus.InboundMessage) { dropped++ }

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SessionKey: "telegram:chat1"}
	ch.publishInbound(&msg)

	if dropped != 0 {
		t.Errorf("rollback hook fired %d times, want 0", dropped)
	}
	got, ok := waitInbound(t, messageBus, time.Second)
	if !ok {
		t.Fatal("no inbound message published")
	}
	if got.SpoolID != 0 || got.DedupeID != "" {
		t.Errorf("published identity = (%d, %q), want untouched without a spooler", got.SpoolID, got.DedupeID)
	}
}

// A spool write that fails is not a reason to drop a live message: durability
// is best-effort and availability wins.
func TestPublishInboundStillPublishesWhenSpoolFails(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	spooler := newFakeSpooler(messageBus)
	spooler.setFail(true)

	ch := NewBaseChannel("telegram", nil, messageBus, nil)
	ch.SetInboundSpooler(spooler)
	ch.InboundDroppedHook = func(bus.InboundMessage) {
		t.Error("rollback hook fired for a message the bus accepted")
	}

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SessionKey: "telegram:chat1"}
	ch.publishInbound(&msg)

	calls, rows, _ := spooler.snapshot()
	if calls != 1 || len(rows) != 0 {
		t.Errorf("Enqueue called %d times with %d rows, want 1 call and 0 rows", calls, len(rows))
	}

	got, ok := waitInbound(t, messageBus, time.Second)
	if !ok {
		t.Fatal("a failed spool write must not stop the publish")
	}
	if got.Content != "hi" {
		t.Errorf("published content = %q, want %q", got.Content, "hi")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d, want 0: a declined write tags nothing", got.SpoolID)
	}
}

// The interesting combination: the row was written (and claimed), then the bus
// rejected the publish. The rollback hook must fire (the user may already see a
// typing indicator) and publishInbound must hand exactly that one row back to
// the pump with Release: it is the only copy of the message, so deleting it
// would lose it and leaving it claimed would stall it until the stale-claim
// timeout.
func TestPublishInboundRejectedReleasesSpoolRowForPump(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	// Fill the queue so the next publish is rejected.
	inLen, inCap, _, _, _, _ := messageBus.Stats()
	for i := inLen; i < inCap; i++ {
		if !messageBus.PublishInbound(bus.InboundMessage{Channel: "filler"}) {
			t.Fatalf("filler publish %d returned false before the queue was full", i)
		}
	}

	spooler := newFakeSpooler(messageBus)
	ch := NewBaseChannel("telegram", nil, messageBus, nil)
	ch.SetInboundSpooler(spooler)

	var dropped []bus.InboundMessage
	ch.InboundDroppedHook = func(msg bus.InboundMessage) { dropped = append(dropped, msg) }

	msg := bus.InboundMessage{Channel: "telegram", ChatID: "chat1", Content: "hi", SessionKey: "telegram:chat1"}
	ch.publishInbound(&msg)

	if len(dropped) != 1 {
		t.Fatalf("rollback hook fired %d times, want 1", len(dropped))
	}
	if dropped[0].Content != "hi" {
		t.Errorf("hook received %q, want the rejected message", dropped[0].Content)
	}

	calls, rows, _ := spooler.snapshot()
	if calls != 1 {
		t.Errorf("Enqueue called %d times, want 1: a rejected publish must not retry the spool", calls)
	}
	if len(rows) != 1 || rows[0].SpoolID != msg.SpoolID {
		t.Fatalf("spool rows = %+v, want the row kept (id %d) for the pump to replay", rows, msg.SpoolID)
	}
	if msg.SpoolID == 0 {
		t.Error("caller SpoolID = 0, want the row id the spooler assigned")
	}

	// The rollback is scoped to exactly that row: no other id, no second call.
	if got := spooler.released(); len(got) != 1 || got[0] != msg.SpoolID {
		t.Errorf("Release called for %v, want exactly [%d]", got, msg.SpoolID)
	}
}

// HandleMessageWithAttachments is what every real channel calls; the spooler
// must be reached through it, with the identity visible on the wire.
func TestHandleMessageWithAttachmentsCarriesSpoolIdentity(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	spooler := newFakeSpooler(messageBus)
	ch := NewBaseChannel("telegram", nil, messageBus, nil)
	ch.SetInboundSpooler(spooler)

	ch.HandleMessageWithAttachments(
		"sender1", "chat1", "hello",
		[]bus.FileAttachment{{Name: "f.txt", Path: "/tmp/f.txt", Kind: "file"}},
		map[string]string{"message_id": "7"}, "session1",
	)

	if calls, _, _ := spooler.snapshot(); calls != 1 {
		t.Fatalf("Enqueue called %d times, want 1", calls)
	}

	got, ok := waitInbound(t, messageBus, time.Second)
	if !ok {
		t.Fatal("no inbound message published")
	}
	if got.SpoolID == 0 {
		t.Error("inbound SpoolID = 0, want the row id")
	}
	if got.DedupeID != "7" {
		t.Errorf("inbound DedupeID = %q, want the channel message id %q", got.DedupeID, "7")
	}
	if got.Content != "hello" || got.SessionKey != "session1" ||
		len(got.Attachments) != 1 || got.Attachments[0].Path != "/tmp/f.txt" {
		t.Errorf("the spool path altered the payload: %+v", got)
	}
}

// A sender the allowlist rejects must never reach the spool: a row for a
// message nobody wanted would be replayed by the pump on every restart.
func TestHandleMessageRejectedByAllowlistIsNotSpooled(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	spooler := newFakeSpooler(messageBus)
	ch := NewBaseChannel("telegram", nil, messageBus, []string{"123456"})
	ch.SetInboundSpooler(spooler)

	ch.HandleMessage("999999", "chat1", "hello", nil, nil)

	if calls, rows, _ := spooler.snapshot(); calls != 0 || len(rows) != 0 {
		t.Errorf("Enqueue called %d times with %d rows, want none for a denied sender", calls, len(rows))
	}
	if _, ok := waitInbound(t, messageBus, 100*time.Millisecond); ok {
		t.Error("a denied sender's message reached the bus")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Manager propagation
// ──────────────────────────────────────────────────────────────────────────────

// embeddedChannel is a channel in the shape most of them have: *BaseChannel
// embedded, so BaseChannel's setter is promoted to it.
type embeddedChannel struct {
	*BaseChannel
}

func (embeddedChannel) Start(context.Context) error                     { return nil }
func (embeddedChannel) Stop(context.Context) error                      { return nil }
func (embeddedChannel) Send(context.Context, bus.OutboundMessage) error { return nil }

// plainChannel knows nothing about BaseChannel at all: the case the manager has
// to skip without breaking the wiring of everything else.
type plainChannel struct{}

func (plainChannel) Name() string                                    { return "plain" }
func (plainChannel) Start(context.Context) error                     { return nil }
func (plainChannel) Stop(context.Context) error                      { return nil }
func (plainChannel) Send(context.Context, bus.OutboundMessage) error { return nil }
func (plainChannel) IsRunning() bool                                 { return false }
func (plainChannel) IsAllowed(string) bool                           { return true }

// TestManagerSetInboundSpoolerReachesEveryChannel pins the wiring contract: one
// call on the manager covers the channels that embed BaseChannel (setter
// promoted), the one that keeps the base in a named field (NativeChannel, via
// its forwarder), and leaves a channel that supports neither alone.
func TestManagerSetInboundSpoolerReachesEveryChannel(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	embedded := &embeddedChannel{BaseChannel: NewBaseChannel("embedded", nil, messageBus, nil)}
	telegram := &TelegramChannel{BaseChannel: NewBaseChannel("telegram", nil, messageBus, nil)}
	discord := &DiscordChannel{BaseChannel: NewBaseChannel("discord", nil, messageBus, nil)}
	native := &NativeChannel{
		base:      NewBaseChannel(ChannelName, config.NativeConfig{}, messageBus, nil),
		bus:       messageBus,
		wsClients: make(map[string]*WSClient),
	}
	foreign := plainChannel{}

	m := &Manager{channels: map[string]Channel{
		"embedded": embedded,
		"telegram": telegram,
		"discord":  discord,
		"native":   native,
		"foreign":  foreign,
	}}

	spooler := newFakeSpooler(messageBus)
	m.SetInboundSpooler(spooler)

	for name, base := range map[string]*BaseChannel{
		"embedded": embedded.BaseChannel,
		"telegram": telegram.BaseChannel,
		"discord":  discord.BaseChannel,
		"native":   native.base,
	} {
		if _, ok := m.GetChannel(name); !ok {
			t.Fatalf("channel %q missing from the manager", name)
		}
		if base.InboundSpooler == nil {
			t.Errorf("channel %q has no spooler after SetInboundSpooler", name)
		}
	}

	// The unsupported channel is skipped silently rather than panicking, and it
	// really is unsupported: if that ever stops being true this test stops
	// covering the skip path.
	if _, ok := Channel(foreign).(spoolerSetter); ok {
		t.Error("plainChannel unexpectedly satisfies spoolerSetter; the skip path is no longer covered")
	}

	// Clearing propagates the same way: nil is the "durability off" wiring.
	m.SetInboundSpooler(nil)
	for name, base := range map[string]*BaseChannel{
		"embedded": embedded.BaseChannel,
		"telegram": telegram.BaseChannel,
		"discord":  discord.BaseChannel,
		"native":   native.base,
	} {
		if base.InboundSpooler != nil {
			t.Errorf("channel %q still holds a spooler after SetInboundSpooler(nil)", name)
		}
	}
}

// TestEveryChannelTypeExposesTheSetter guards the assumption behind
// Manager.SetInboundSpooler: channels are wired by capability, not by name, so
// a channel that could not be reached would be easy to forget. Build every real
// channel and check each one answers to the setter and stores what it is given.
func TestEveryChannelTypeExposesTheSetter(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	cfg := config.DefaultConfig()

	channels := []Channel{
		&TelegramChannel{BaseChannel: NewBaseChannel("telegram", nil, messageBus, nil)},
		&DiscordChannel{BaseChannel: NewBaseChannel("discord", nil, messageBus, nil)},
		&SlackChannel{BaseChannel: NewBaseChannel("slack", nil, messageBus, nil)},
		&OneBotChannel{BaseChannel: NewBaseChannel("onebot", nil, messageBus, nil)},
		&LINEChannel{BaseChannel: NewBaseChannel("line", nil, messageBus, nil)},
		&QQChannel{BaseChannel: NewBaseChannel("qq", nil, messageBus, nil)},
		&DingTalkChannel{BaseChannel: NewBaseChannel("dingtalk", nil, messageBus, nil)},
		&MaixCamChannel{BaseChannel: NewBaseChannel("maixcam", nil, messageBus, nil)},
		&WhatsAppChannel{BaseChannel: NewBaseChannel("whatsapp", nil, messageBus, nil)},
		&FeishuChannel{BaseChannel: NewBaseChannel("feishu", nil, messageBus, nil)},
		&NativeChannel{base: NewBaseChannel(ChannelName, cfg.Channels.Native, messageBus, nil)},
	}

	for _, ch := range channels {
		setter, ok := ch.(spoolerSetter)
		if !ok {
			t.Errorf("%T does not expose SetInboundSpooler; its inbound would stay unpersisted", ch)
			continue
		}
		setter.SetInboundSpooler(newFakeSpooler(nil))
		if base := baseOf(ch); base == nil || base.InboundSpooler == nil {
			t.Errorf("%T: SetInboundSpooler did not reach its BaseChannel", ch)
		}
	}
}

// baseOf returns the BaseChannel a concrete channel writes through.
func baseOf(ch Channel) *BaseChannel {
	switch c := ch.(type) {
	case *TelegramChannel:
		return c.BaseChannel
	case *DiscordChannel:
		return c.BaseChannel
	case *SlackChannel:
		return c.BaseChannel
	case *OneBotChannel:
		return c.BaseChannel
	case *LINEChannel:
		return c.BaseChannel
	case *QQChannel:
		return c.BaseChannel
	case *DingTalkChannel:
		return c.BaseChannel
	case *MaixCamChannel:
		return c.BaseChannel
	case *WhatsAppChannel:
		return c.BaseChannel
	case *FeishuChannel:
		return c.BaseChannel
	case *NativeChannel:
		return c.base
	default:
		return nil
	}
}

// The manager hands the setter to whatever is registered, including a channel
// that was only half built (the test fixtures do exactly that). Forwarding must
// be a no-op rather than a panic.
func TestNativeSetInboundSpoolerToleratesAMissingBase(t *testing.T) {
	var nilChannel *NativeChannel
	nilChannel.SetInboundSpooler(newFakeSpooler(nil)) //nolint:staticcheck // the nil receiver is the point

	bare := &NativeChannel{}
	bare.SetInboundSpooler(newFakeSpooler(nil))
	if bare.base != nil {
		t.Error("SetInboundSpooler invented a BaseChannel, want the call ignored")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Native publishers
// ──────────────────────────────────────────────────────────────────────────────

// The REST chat publisher used to write straight to the bus. It has to go
// through the same chokepoint as everything else, or web/desktop/REST messages
// stay the one class of inbound a crash can lose.
func TestNativeChatSendRoutesThroughTheSpooler(t *testing.T) {
	ts := newNativeTestServer(t)

	spooler := newFakeSpooler(ts.bus)
	ts.channel.SetInboundSpooler(spooler)

	body, err := json.Marshal(ChatSendRequest{Content: "hello durable"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/send", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}

	if calls, _, _ := spooler.snapshot(); calls != 1 {
		t.Fatalf("Enqueue called %d times, want 1: POST /chat/send must spool before publishing", calls)
	}

	got, ok := waitInbound(t, ts.bus, 2*time.Second)
	if !ok {
		t.Fatal("no inbound message published by /chat/send")
	}
	if got.SpoolID == 0 {
		t.Error("inbound SpoolID = 0, want the row id assigned before the publish")
	}
	if got.DedupeID == "" {
		t.Error("inbound DedupeID is empty, want the generated message id")
	}
	if got.Content != "hello durable" || got.Channel != ChannelName {
		t.Errorf("inbound = (%q, %q), want the message the client sent on %q",
			got.Channel, got.Content, ChannelName)
	}

	// The response must still report the id the client can pair events with:
	// spooling may not eat the caller's metadata.
	var payload ChatSendResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if payload.MessageID != got.DedupeID {
		t.Errorf("ack message_id = %q, inbound DedupeID %q: they must be the same id",
			payload.MessageID, got.DedupeID)
	}
}

// The native publishers share one helper, so the WebSocket path is covered by
// the same code. This drives it directly (no real connection needed) to pin
// that a spool failure cannot stop a WS message either.
func TestNativeWSMessageStillPublishesWhenSpoolFails(t *testing.T) {
	ts := newNativeTestServer(t)

	spooler := newFakeSpooler(ts.bus)
	spooler.setFail(true)
	ts.channel.SetInboundSpooler(spooler)

	client := &WSClient{
		ID:         "fake-client",
		SessionKey: "native:spool-fail",
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 16),
	}

	body, err := json.Marshal(WSMessagePayload{Content: "still delivered", SessionKey: "native:spool-fail"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ts.channel.handleWSClientMessage(client, body, "evt-spool-fail")
	}()

	got, ok := waitInbound(t, ts.bus, 2*time.Second)
	if !ok {
		t.Fatal("a failed spool write must not stop a WebSocket message")
	}
	if got.Content != "still delivered" {
		t.Errorf("published content = %q, want %q", got.Content, "still delivered")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d, want 0 after a declined write", got.SpoolID)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handleWSClientMessage did not return")
	}
}
