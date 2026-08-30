package channels

// Tests for the empty-content completion signal in dispatchOutboundMessage.
//
// Bug: when the agent finished a turn with empty final content and no
// attachments (e.g. finalContent=="" after silent tool calls), the non-streamed
// branch of dispatchOutboundMessage returned early WITHOUT emitting
// message.complete / history.updated. The frontend had already registered the
// turn as processing via message.ack and created an assistant placeholder with
// streaming:true, so the WebUI loading spinner stayed stuck until the HTTP
// polling safety-net (and the placeholder could go stale if the poll missed it).
//
// Contract pinned here: a turn that reaches the non-streamed finalization
// branch ALWAYS emits message.complete + history.updated, even with empty
// content; message.stream is still suppressed when there is nothing to show.

import (
	"encoding/json"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// registerFakeWSClient subscribes a fake WS client (no real connection —
// QueueSend only touches the buffered SendChan) to the given session key and
// removes it on cleanup.
func registerFakeWSClient(t *testing.T, ts *nativeTestServer, sessionKey string) *WSClient {
	t.Helper()
	client := &WSClient{
		ID:         "fake-empty-complete-" + sessionKey,
		SessionKey: sessionKey,
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 64),
	}
	ts.channel.addWSClient(client)
	t.Cleanup(func() { ts.channel.removeWSClient(client.ID) })
	return client
}

// drainWSEvents non-blockingly reads everything currently queued on a fake
// client, preserving delivery order.
func drainWSEvents(t *testing.T, client *WSClient) []WSMessage {
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

// findWSEvent returns the first event with the given name, or nil.
func findWSEvent(events []WSMessage, name string) *WSMessage {
	for i := range events {
		if events[i].Event == name {
			return &events[i]
		}
	}
	return nil
}

// TestDispatchOutboundEmptyContentStillCompletes pins the fix: an outbound
// message with empty content and no attachments must still emit
// message.complete (content "") + history.updated so the frontend can end the
// turn, and must NOT emit message.stream.
func TestDispatchOutboundEmptyContentStillCompletes(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-empty"
	client := registerFakeWSClient(t, ts, sessionKey)
	ts.loop.sessionNames[sessionKey] = "empty turn session"

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "",
		MessageID: "msg-empty-1",
	})

	events := drainWSEvents(t, client)

	if s := findWSEvent(events, "message.stream"); s != nil {
		t.Error("message.stream must NOT be emitted for empty content")
	}

	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message.complete must be emitted even for empty content; got events: %v", eventNames(events))
	}
	var completeData WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completeData)
	if completeData.MessageID != "msg-empty-1" {
		t.Errorf("complete message_id = %q, want msg-empty-1", completeData.MessageID)
	}
	if completeData.SessionKey != sessionKey {
		t.Errorf("complete session_key = %q, want %q", completeData.SessionKey, sessionKey)
	}
	if completeData.Content != "" {
		t.Errorf("complete content = %q, want empty", completeData.Content)
	}
	if len(completeData.Attachments) != 0 {
		t.Errorf("complete attachments = %v, want none", completeData.Attachments)
	}

	history := findWSEvent(events, "history.updated")
	if history == nil {
		t.Fatalf("history.updated must be emitted alongside message.complete; got events: %v", eventNames(events))
	}
	var historyData struct {
		SessionKey string `json:"session_key"`
		Name       string `json:"name"`
	}
	decodeWSData(t, history.Data, &historyData)
	if historyData.SessionKey != sessionKey {
		t.Errorf("history session_key = %q, want %q", historyData.SessionKey, sessionKey)
	}
	if historyData.Name != "empty turn session" {
		t.Errorf("history name = %q, want %q", historyData.Name, "empty turn session")
	}

	// Ordering: completion must arrive before the history signal so the
	// frontend finalizes the message before refetching canonical history.
	if indexOfEvent(events, "message.complete") > indexOfEvent(events, "history.updated") {
		t.Error("message.complete must be emitted before history.updated")
	}
}

// TestDispatchOutboundEmptyContentWithAttachmentsCompletes is the
// no-regression case: empty content WITH attachments must emit exactly one
// message.complete carrying the attachments (and still no message.stream).
func TestDispatchOutboundEmptyContentWithAttachmentsCompletes(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-att"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "",
		MessageID: "msg-att-1",
		Attachments: []bus.FileAttachment{{
			Name:     "photo.png",
			Path:     "/tmp/photo.png",
			MIMEType: "image/png",
			Kind:     "image",
		}},
	})

	events := drainWSEvents(t, client)

	if s := findWSEvent(events, "message.stream"); s != nil {
		t.Error("message.stream must NOT be emitted for empty content even with attachments")
	}

	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message.complete must be emitted with attachments; got events: %v", eventNames(events))
	}
	if n := countEvents(events, "message.complete"); n != 1 {
		t.Errorf("message.complete emitted %d times, want exactly 1", n)
	}
	var completeData WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completeData)
	if completeData.MessageID != "msg-att-1" {
		t.Errorf("complete message_id = %q, want msg-att-1", completeData.MessageID)
	}
	if len(completeData.Attachments) != 1 {
		t.Fatalf("complete attachments = %v, want 1 entry", completeData.Attachments)
	}
	if got := completeData.Attachments[0]["name"]; got != "photo.png" {
		t.Errorf("attachment name = %v, want photo.png", got)
	}
	if got := completeData.Attachments[0]["mime_type"]; got != "image/png" {
		t.Errorf("attachment mime_type = %v, want image/png", got)
	}

	if findWSEvent(events, "history.updated") == nil {
		t.Error("history.updated must be emitted with attachments path")
	}
}

// TestDispatchOutboundNonEmptyContentEmitsStreamAndComplete guards the
// untouched happy path: non-empty content still produces message.stream
// (Done=true) followed by message.complete with that content.
func TestDispatchOutboundNonEmptyContentEmitsStreamAndComplete(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-full"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "hola mundo",
		MessageID: "msg-full-1",
	})

	events := drainWSEvents(t, client)

	stream := findWSEvent(events, "message.stream")
	if stream == nil {
		t.Fatalf("message.stream must be emitted for non-empty content; got events: %v", eventNames(events))
	}
	var streamData WSStreamPayload
	decodeWSData(t, stream.Data, &streamData)
	if streamData.Chunk != "hola mundo" || !streamData.Done {
		t.Errorf("stream payload = %+v, want chunk %q done=true", streamData, "hola mundo")
	}

	complete := findWSEvent(events, "message.complete")
	if complete == nil {
		t.Fatalf("message.complete must be emitted for non-empty content; got events: %v", eventNames(events))
	}
	var completeData WSMessageCompletePayload
	decodeWSData(t, complete.Data, &completeData)
	if completeData.Content != "hola mundo" {
		t.Errorf("complete content = %q, want %q", completeData.Content, "hola mundo")
	}
}

func eventNames(events []WSMessage) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.Event)
	}
	return names
}

func indexOfEvent(events []WSMessage, name string) int {
	for i := range events {
		if events[i].Event == name {
			return i
		}
	}
	return -1
}

func countEvents(events []WSMessage, name string) int {
	n := 0
	for i := range events {
		if events[i].Event == name {
			n++
		}
	}
	return n
}
