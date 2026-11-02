package channels

// Tests for the message.discard event on the native channel.
//
// The agent streams every LLM iteration under its own message id and, when it
// throws an iteration away (empty-response retry, plain-text tool call, invalid
// tool calls), publishes message.discard carrying that iteration's id. The
// native channel has exactly one job with it: forward it to the clients of the
// session, with the id they already know, so they can drop the bubble that
// history will never confirm.
//
// Two properties are pinned here: the wire payload ({"event":"message.discard",
// "data":{"message_id":...,"session_key":...,"reason":...}}) and the fact that
// the event does NOT touch the session — nothing of a discarded iteration may be
// appended or finalized on its way out.

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// TestDispatchOutboundDiscardForwardsToClient is the core contract test: a
// message.discard outbound message reaches the WebSocket client of the session
// as a single frame with the exact payload shape the frontend parses.
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

	// Freeze the serialized shape: the web client reads these three snake_case
	// keys, and message_id is the only one it needs to act on.
	want := `{"message_id":"` + discardID + `","session_key":"` + sessionKey + `","reason":"empty_response"}`
	if string(discard.Data) != want {
		t.Errorf("payload JSON = %s\nwant            %s", discard.Data, want)
	}
	// The frame is the protocol envelope, not a bare payload.
	if discard.Version != WSProtocolVersion {
		t.Errorf("frame version = %d, want %d", discard.Version, WSProtocolVersion)
	}
}

// TestDispatchOutboundDiscardLeavesSessionUntouched pins the "no session side
// effect" half: unlike message.stream (append/finalize) and message.thinking
// (append reasoning), a discard must not write anything. A bug that routed it
// through the streaming path would resurrect the very bubble it drops.
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

	// And it must still be forwarded (the checks above are the side-effect
	// half, not an early return that swallows the event).
	if findWSEvent(drainWSEvents(t, client), "message.discard") == nil {
		t.Fatal("message.discard was not forwarded to the client")
	}
}

// TestDispatchOutboundDiscardWithoutReasonKeepsShape covers a producer that
// forgot the metadata: the key is still present (empty) instead of vanishing, so
// the client parser never has to guess a shape.
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
