package channels

// Tests for the failure marker on the final completion event.
//
// A turn that ended in a provider/tool failure is published by pkg/agent/loop.go
// as a normal final answer (Event ""), so it reaches dispatchOutboundMessage
// through the legacy full-message path, but with OutboundMessage.Metadata
// {"error": "true"}. That marker is mirrored onto message.complete as
// `error: true` so the WebUI can paint the failure as an error box right away
// instead of showing a plain assistant bubble that the history refetch deletes.
//
// Contract pinned here: the flag is set only when the marker is present, and it
// never leaks onto a normal completion.

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// TestDispatchOutboundErrorMessageMarksComplete pins the failure path: an
// outbound message carrying the error marker must emit message.complete with
// error=true, and the textual content must still be delivered (plain channels
// and the live bubble both rely on it).
func TestDispatchOutboundErrorMessageMarksComplete(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-llm-error"
	client := registerFakeWSClient(t, ts, sessionKey)

	content := "❌ Error processing message: LLM call failed after retries"
	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   content,
		MessageID: "msg-error-1",
		Metadata:  map[string]string{"error": "true"},
	})

	events := drainWSEvents(t, client)

	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message.complete must be emitted for a failed turn; got events: %v", eventNames(events))
	}
	var completeData WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completeData)
	if !completeData.Error {
		t.Error("complete error = false, want true for a failed turn")
	}
	if completeData.Content != content {
		t.Errorf("complete content = %q, want %q", completeData.Content, content)
	}
	if completeData.SessionKey != sessionKey {
		t.Errorf("complete session_key = %q, want %q", completeData.SessionKey, sessionKey)
	}
}

// TestDispatchOutboundNormalMessageClearsErrorFlag is the no-regression case: a
// completion without the marker must not carry the flag at all (omitempty on the
// payload keeps the wire format unchanged for every successful turn).
func TestDispatchOutboundNormalMessageClearsErrorFlag(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-normal"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "all good",
		MessageID: "msg-normal-1",
	})

	events := drainWSEvents(t, client)

	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message.complete must be emitted; got events: %v", eventNames(events))
	}
	var completeData WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completeData)
	if completeData.Error {
		t.Error("complete error = true, want false on the happy path")
	}
}

// TestDispatchOutboundErrorMessageWithAttachmentsMarksComplete covers the
// attachments branch of dispatchOutboundMessage, which emits its own
// message.complete: the marker must be carried there too, otherwise a failed
// turn that produced attachments would render as a normal bubble.
func TestDispatchOutboundErrorMessageWithAttachmentsMarksComplete(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-llm-error-att"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "❌ Error processing message: boom",
		MessageID: "msg-error-att-1",
		Metadata:  map[string]string{"error": "true"},
		Attachments: []bus.FileAttachment{{
			Name:     "trace.log",
			Path:     "/tmp/trace.log",
			MIMEType: "text/plain",
			Kind:     "file",
		}},
	})

	events := drainWSEvents(t, client)

	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message.complete must be emitted; got events: %v", eventNames(events))
	}
	if n := countEvents(events, "message.complete"); n != 1 {
		t.Errorf("message.complete emitted %d times, want exactly 1", n)
	}
	var completeData WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completeData)
	if !completeData.Error {
		t.Error("complete error = false, want true for a failed turn with attachments")
	}
	if len(completeData.Attachments) != 1 {
		t.Errorf("complete attachments = %v, want 1 entry", completeData.Attachments)
	}
}
