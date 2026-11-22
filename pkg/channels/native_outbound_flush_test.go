package channels

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

var _ outboundFlushSetter = (*NativeChannel)(nil)

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

func streamSpy(t *testing.T, ts *nativeTestServer, sessionKey string) *restStreamSubscriber {
	t.Helper()

	sub := ts.channel.registerRESTStreamSubscriber(sessionKey, "")
	t.Cleanup(func() { ts.channel.unregisterRESTStreamSubscriber(sub.id) })
	return sub
}

func spyHasEvents(sub *restStreamSubscriber) bool {
	select {
	case <-sub.ch:
		return true
	default:
		return false
	}
}

func spooledMessage(chatID string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    chatID,
		Content:   "durable reply",
		MessageID: "msg-durable-1",
		SpoolID:   4242,
	}
}

func TestNativeSendDefersWithoutLiveClient(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-no-live"
	flusher := &recordingFlusher{}
	ts.channel.SetOutboundFlusher(flusher)

	spy := streamSpy(t, ts, sessionKey)

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

func TestNativeSendUnaffectedWithoutFlusher(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-no-flusher"
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

func TestReconnectWithoutFlusherDoesNotKick(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-reconnect-off"
	client := newLiveFakeClient(t, ts, sessionKey)

	ts.channel.sendReconnected(client, []json.RawMessage{bufferedEvent(t, "history.updated")})

	if n := len(client.SendChan); n != 2 {
		t.Errorf("queued events = %d, want 2 (reconnected + flushed)", n)
	}
}

func TestReconnectingClientIsNotLive(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-reconnecting"
	client := newLiveFakeClient(t, ts, sessionKey)

	if !ts.channel.hasLiveClientFor(sessionKey) {
		t.Fatal("hasLiveClientFor() = false for a connected client")
	}

	client.mu.Lock()
	client.reconnecting = true
	client.mu.Unlock()
	if ts.channel.hasLiveClientFor(sessionKey) {
		t.Error("hasLiveClientFor() = true for a reconnecting client, want false")
	}

	client.mu.Lock()
	client.reconnecting = false
	client.mu.Unlock()
	if !ts.channel.hasLiveClientFor(sessionKey) {
		t.Error("hasLiveClientFor() = false after reconnecting cleared, want true")
	}

	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
	if ts.channel.hasLiveClientFor(sessionKey) {
		t.Error("hasLiveClientFor() = true for a closed client, want false")
	}
}

func TestHasLiveClientForMatchesBroadcastTargets(t *testing.T) {
	ts := newNativeTestServer(t)

	base := "native:" + ts.clientID + "-alias"
	alias := base + ":chat:7"
	setLoopAlias(ts.loop, base, alias)

	client := newLiveFakeClient(t, ts, base)
	client.mu.Lock()
	client.Subscriptions = nil
	client.mu.Unlock()

	if !ts.channel.hasLiveClientFor(alias) {
		t.Errorf("hasLiveClientFor(%q) = false, want true via ResolveSessionKey of %q", alias, base)
	}

	other := newLiveFakeClient(t, ts, "native:"+ts.clientID+"-elsewhere")
	other.Subscriptions[alias] = true
	if !ts.channel.hasLiveClientFor(alias) {
		t.Error("hasLiveClientFor() = false for a subscription-only match, want true")
	}

	if ts.channel.hasLiveClientFor("native:nobody-at-all") {
		t.Error("hasLiveClientFor() = true for an unsubscribed session, want false")
	}
}
