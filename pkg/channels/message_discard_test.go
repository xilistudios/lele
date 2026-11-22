package channels

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestDispatchOutboundDiscardForwardsToClient(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-discard"
	client := registerFakeWSClient(t, ts, sessionKey)

	const discardID = "msg-discard-2"

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.discard",
		MessageID: discardID,
		Metadata:  map[string]string{"reason": "empty_response"},
	})

	events := drainWSEvents(t, client)
	if len(events) != 1 {
		t.Fatalf("discard must emit exactly one frame (no message.complete, no history.updated); got: %v",
			eventNames(events))
	}

	discard := findWSEvent(events, "message.discard")
	if discard == nil {
		t.Fatalf("message.discard not emitted; got events: %v", eventNames(events))
	}

	var payload WSMessageDiscardPayload
	decodeWSData(t, discard.Data, &payload)
	if payload.MessageID != discardID {
		t.Errorf("message_id = %q, want %q", payload.MessageID, discardID)
	}
	if payload.SessionKey != sessionKey {
		t.Errorf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	if payload.Reason != "empty_response" {
		t.Errorf("reason = %q, want empty_response", payload.Reason)
	}

	want := `{"message_id":"` + discardID + `","session_key":"` + sessionKey + `","reason":"empty_response"}`
	if string(discard.Data) != want {
		t.Errorf("payload JSON = %s\nwant            %s", discard.Data, want)
	}
	if discard.Version != WSProtocolVersion {
		t.Errorf("frame version = %d, want %d", discard.Version, WSProtocolVersion)
	}
}

func TestDispatchOutboundDiscardLeavesSessionUntouched(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-discard-untouched"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.discard",
		MessageID: "msg-discard-3",
		Metadata:  map[string]string{"reason": "plain_text_tool_call"},
	})

	if history := ts.loop.GetSessionHistory(sessionKey); len(history) != 0 {
		t.Errorf("discard must not append to the session, history = %+v", history)
	}
	if ts.loop.HasStreamedContent(sessionKey) {
		t.Error("discard must not register streamed content")
	}
	if ts.loop.GetInProgressAssistant(sessionKey) != nil {
		t.Error("discard must not create an in-progress assistant message")
	}
	if len(ts.loop.attachCalls()) != 0 {
		t.Errorf("discard must not attach files, got %+v", ts.loop.attachCalls())
	}

	if findWSEvent(drainWSEvents(t, client), "message.discard") == nil {
		t.Fatal("message.discard was not forwarded to the client")
	}
}

func TestDispatchOutboundDiscardWithoutReasonKeepsShape(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-discard-noreason"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.discard",
		MessageID: "msg-discard-4",
	})

	events := drainWSEvents(t, client)
	discard := findWSEvent(events, "message.discard")
	if discard == nil {
		t.Fatalf("message.discard not emitted; got events: %v", eventNames(events))
	}
	if !strings.Contains(string(discard.Data), `"reason":""`) {
		t.Errorf("reason key must be present even when empty: %s", discard.Data)
	}
	var payload WSMessageDiscardPayload
	decodeWSData(t, discard.Data, &payload)
	if payload.MessageID != "msg-discard-4" || payload.Reason != "" {
		t.Errorf("payload = %+v, want msg-discard-4 with an empty reason", payload)
	}
}
