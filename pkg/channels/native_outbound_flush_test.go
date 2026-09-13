// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

// Durable outbound, native channel side (Task C: flush-on-reconnect).
//
// The manager owns the spool row; the channel owns the two decisions these
// tests pin:
//
//	a spooled message with no live client  -> refuse BEFORE emitting anything
//	                                        (ErrPeerNotReady, so the dispatcher
//	                                        Releases and the pump replays whole)
//	no flusher wired (durability off)      -> Send behaves exactly as it did
//	                                        before this feature existed
//	a peer that just reconnected           -> exactly one pump wake-up, AFTER the
//	                                        in-memory buffer was flushed
//	a reconnecting client                  -> NOT live: its pendingMsgs buffer
//	                                        dies with the process
//
// The gate is keyed on the flusher being non-nil, not on SpoolID alone, so the
// flag-off path cannot regress: with durability off nobody calls
// SetOutboundFlusher, the gate never runs, and a stale SpoolID on a message is
// as harmless as it was yesterday.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// Compile-time proof that NativeChannel satisfies the seam
// Manager.SetOutboundSpooler propagates the flusher through. If the method
// signature drifts, the manager's type assertion silently stops matching, and
// this is the line that catches it.
var _ outboundFlushSetter = (*NativeChannel)(nil)

// ─────────────────────────────────────────────────────────────────────────────
// Test doubles
// ─────────────────────────────────────────────────────────────────────────────

// recordingFlusher stands in for *durable.Outbound's wake-up signal. Beyond
// counting kicks it samples the client's outbound queue at the instant
// FlushPeer runs, which is what makes the ordering contract observable without
// a real websocket connection: QueueSend hands the payload to SendChan before
// sendReconnected can reach the kick, so a kick that fired too early would see
// an empty queue.
type recordingFlusher struct {
	client *WSClient

	kicks   []string
	chanLen []int
}

func (f *recordingFlusher) FlushPeer(channel, peer string) {
	f.kicks = append(f.kicks, channel+"/"+peer)
	if f.client != nil {
		f.chanLen = append(f.chanLen, len(f.client.SendChan))
	}
}

func (f *recordingFlusher) count() int { return len(f.kicks) }

// drainEvents reads everything currently queued on a fake client without
// blocking, preserving delivery order.
func drainEvents(t *testing.T, client *WSClient) []WSMessage {
	t.Helper()

	var events []WSMessage
	for {
		select {
		case raw := <-client.SendChan:
			var msg WSMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("Unmarshal(WSMessage) error = %v", err)
			}
			events = append(events, msg)
		default:
			return events
		}
	}
}

// newLiveFakeClient registers a connected client (no real connection: QueueSend
// only touches the buffered SendChan, and no write loop drains it) and removes
// it on cleanup.
func newLiveFakeClient(t *testing.T, ts *nativeTestServer, sessionKey string) *WSClient {
	t.Helper()

	client := &WSClient{
		ID:            "fake-live-" + sessionKey,
		SessionKey:    sessionKey,
		ClientInfo:    &ClientInfo{ClientID: ts.clientID, DeviceName: "test"},
		Subscriptions: map[string]bool{sessionKey: true},
		SendChan:      make(chan []byte, 64),
		done:          make(chan struct{}),
	}
	ts.channel.addWSClient(client)
	t.Cleanup(func() { ts.channel.removeWSClient(client.ID) })
	return client
}

// streamSpy subscribes to the REST stream fan-out for a session, which sees
// every emitNativeEvent regardless of WebSocket clients - the cleanest way to
// assert "nothing was emitted" when there is no connection to read from.
func streamSpy(t *testing.T, ts *nativeTestServer, sessionKey string) *restStreamSubscriber {
	t.Helper()

	sub := ts.channel.registerRESTStreamSubscriber(sessionKey, "")
	t.Cleanup(func() { ts.channel.unregisterRESTStreamSubscriber(sub.id) })
	return sub
}

// spyHasEvents reports whether the stream subscriber holds any queued event.
func spyHasEvents(sub *restStreamSubscriber) bool {
	select {
	case <-sub.ch:
		return true
	default:
		return false
	}
}

// spooledMessage is a final reply as the dispatcher hands it to the channel
// when durability tagged it: real content, no event, non-zero row id.
func spooledMessage(chatID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    chatID,
		Content:   "durable reply",
		MessageID: "msg-durable-1",
		SpoolID:   4242,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// N1: no live client -> refuse, emit nothing
// ─────────────────────────────────────────────────────────────────────────────

func TestNativeSendDefersWithoutLiveClient(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-no-live"
	flusher := &recordingFlusher{}
	ts.channel.SetOutboundFlusher(flusher)

	spy := streamSpy(t, ts, sessionKey)

	// Zero clients connected: the row must be handed back to the pump, not
	// half-delivered to nobody.
	err := ts.channel.Send(context.Background(), spooledMessage(sessionKey))
	if !errors.Is(err, ErrPeerNotReady) {
		t.Fatalf("Send() error = %v, want ErrPeerNotReady", err)
	}
	if spyHasEvents(spy) {
		t.Error("Send emitted events after refusing: the row would be half-out and a replay would duplicate it")
	}
	if flusher.count() != 0 {
		t.Errorf("FlushPeer calls = %d, want 0: Send only refuses, waking the pump is the reconnect path's job", flusher.count())
	}

	// Positive control: the same message with a live client must go through,
	// which is what proves the gate keys on liveness and not on SpoolID alone.
	client := newLiveFakeClient(t, ts, sessionKey)
	if err := ts.channel.Send(context.Background(), spooledMessage(sessionKey)); err != nil {
		t.Fatalf("Send() with a live client error = %v, want nil", err)
	}
	events := drainEvents(t, client)
	if len(events) == 0 {
		t.Fatal("live-client Send emitted nothing")
	}
	if events[0].Event != "message.stream" {
		t.Errorf("first event = %q, want message.stream", events[0].Event)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// N2: no flusher wired -> the old behaviour, byte for byte
// ─────────────────────────────────────────────────────────────────────────────

func TestNativeSendUnaffectedWithoutFlusher(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-no-flusher"
	// SetOutboundFlusher is never called: outboundFlusher stays nil, which is
	// the flag-off state and the whole point of this test.
	if ts.channel.hasOutboundFlusher() {
		t.Fatal("hasOutboundFlusher() = true on a fresh channel, want false")
	}
	if ts.channel.hasLiveClientFor(sessionKey) {
		t.Fatal("hasLiveClientFor() = true with zero clients registered")
	}

	spy := streamSpy(t, ts, sessionKey)

	if err := ts.channel.Send(context.Background(), spooledMessage(sessionKey)); err != nil {
		t.Fatalf("Send() error = %v, want nil: with durability off the gate must not run", err)
	}
	if !spyHasEvents(spy) {
		t.Error("Send emitted nothing: the flag-off path must dispatch as it always did")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// N3: reconnect -> exactly one wake-up, after the in-memory buffer
// ─────────────────────────────────────────────────────────────────────────────

// bufferedEvent builds one entry of a client's reconnect buffer exactly as
// QueueSend would have stored it.
func bufferedEvent(t *testing.T, event string) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   event,
		Data:    mustMarshal(map[string]string{"payload": event}),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return raw
}

func TestReconnectKicksFlush(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-reconnect"
	client := newLiveFakeClient(t, ts, sessionKey)

	flusher := &recordingFlusher{client: client}
	ts.channel.SetOutboundFlusher(flusher)

	// Two replayable events plus a stream chunk, which sendReconnected skips by
	// design (its content already rides in the reconnected payload).
	buffered := []json.RawMessage{
		bufferedEvent(t, "tool.executing"),
		bufferedEvent(t, "message.stream"),
		bufferedEvent(t, "history.updated"),
	}

	ts.channel.sendReconnected(client, buffered)

	if flusher.count() != 1 {
		t.Fatalf("FlushPeer calls = %d, want exactly 1", flusher.count())
	}
	if got, want := flusher.kicks[0], ChannelName+"/"+sessionKey; got != want {
		t.Errorf("FlushPeer args = %q, want %q", got, want)
	}

	// Ordering: at the instant of the kick the reconnected envelope and both
	// non-skipped buffered events were already queued to the client. A kick
	// placed before the flush would have seen an empty queue, and durable rows
	// would overtake the in-memory buffer.
	if got, want := flusher.chanLen[0], 3; got != want {
		t.Errorf("queued events at kick time = %d, want %d (reconnected + 2 flushed, stream skipped)", got, want)
	}

	events := drainEvents(t, client)
	if len(events) != 3 {
		t.Fatalf("client events = %d, want 3", len(events))
	}
	wantEvents := []string{"reconnected", "tool.executing", "history.updated"}
	for i, want := range wantEvents {
		if events[i].Event != want {
			t.Errorf("event %d = %q, want %q", i, events[i].Event, want)
		}
	}
}

// With durability off the reconnect path must stay silent: no wake-up exists to
// make, and calling a nil flusher would panic.
func TestReconnectWithoutFlusherDoesNotKick(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-reconnect-off"
	client := newLiveFakeClient(t, ts, sessionKey)

	ts.channel.sendReconnected(client, []json.RawMessage{bufferedEvent(t, "history.updated")})

	if n := len(client.SendChan); n != 2 {
		t.Errorf("queued events = %d, want 2 (reconnected + flushed)", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// N4: reconnecting / closed clients are not live
// ─────────────────────────────────────────────────────────────────────────────

func TestReconnectingClientIsNotLive(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-reconnecting"
	client := newLiveFakeClient(t, ts, sessionKey)

	// Connected: live.
	if !ts.channel.hasLiveClientFor(sessionKey) {
		t.Fatal("hasLiveClientFor() = false for a connected client")
	}

	// The reconnect window: still registered, still buffering, but the buffer is
	// memory-only and dies with the process, so a spooled message must not be
	// spent on it.
	client.mu.Lock()
	client.reconnecting = true
	client.mu.Unlock()
	if ts.channel.hasLiveClientFor(sessionKey) {
		t.Error("hasLiveClientFor() = true for a reconnecting client, want false")
	}

	// Back online: live again.
	client.mu.Lock()
	client.reconnecting = false
	client.mu.Unlock()
	if !ts.channel.hasLiveClientFor(sessionKey) {
		t.Error("hasLiveClientFor() = false after reconnecting cleared, want true")
	}

	// Closed is terminal, even if the client has not been removed from the map
	// yet (removeWSClient closes the entry after deleting it; the cleanup sweep
	// in broadcastToSession marks closed before removing).
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
	if ts.channel.hasLiveClientFor(sessionKey) {
		t.Error("hasLiveClientFor() = true for a closed client, want false")
	}
}

// Targeting must agree with broadcastToSession: a client that only holds the
// session in Subscriptions, or whose own key resolves to the target through an
// alias, counts as live.
func TestHasLiveClientForMatchesBroadcastTargets(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID + "-alias"
	alias := base + ":chat:7"
	setLoopAlias(ts.loop, base, alias)

	// Registered under the base key, subscribed to nothing else: the alias is
	// reachable only through the ResolveSessionKey fallback.
	client := newLiveFakeClient(t, ts, base)
	client.mu.Lock()
	client.Subscriptions = nil
	client.mu.Unlock()

	if !ts.channel.hasLiveClientFor(alias) {
		t.Errorf("hasLiveClientFor(%q) = false, want true via ResolveSessionKey of %q", alias, base)
	}

	// A subscription-only match (the client's own key is elsewhere) counts too.
	other := newLiveFakeClient(t, ts, "native:"+ts.clientID+"-elsewhere")
	other.Subscriptions[alias] = true
	if !ts.channel.hasLiveClientFor(alias) {
		t.Error("hasLiveClientFor() = false for a subscription-only match, want true")
	}

	// And an unrelated session stays not-live: the gate must not be a no-op that
	// any registered client satisfies.
	if ts.channel.hasLiveClientFor("native:nobody-at-all") {
		t.Error("hasLiveClientFor() = true for an unsubscribed session, want false")
	}
}
