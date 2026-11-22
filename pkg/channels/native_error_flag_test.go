package channels

import (
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

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
