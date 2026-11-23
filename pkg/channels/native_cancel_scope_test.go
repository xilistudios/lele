package channels

import (
	"encoding/json"
	"testing"
	"time"
)

type cancelScopeRecorder struct {
	AgentProvidable
	stopped     []string
	interrupted []string
}

func (r *cancelScopeRecorder) StopAgent(sessionKey string) string {
	r.stopped = append(r.stopped, sessionKey)
	return "stopped"
}

func (r *cancelScopeRecorder) InterruptAgent(sessionKey string) string {
	r.interrupted = append(r.interrupted, sessionKey)
	return "interrupted"
}

func newCancelScopeChannel(t *testing.T, sessionKey string) (*NativeChannel, *WSClient, *cancelScopeRecorder) {
	t.Helper()

	recorder := &cancelScopeRecorder{}
	client := &WSClient{
		ID:            "cancel-scope-client",
		SessionKey:    sessionKey,
		ClientInfo:    &ClientInfo{ClientID: "cancel-scope-client"},
		Subscriptions: map[string]bool{},
		SendChan:      make(chan []byte, 16),
		done:          make(chan struct{}),
	}
	channel := &NativeChannel{
		agentLoop: recorder,
		wsClients: map[string]*WSClient{client.ID: client},
	}
	return channel, client, recorder
}

func readCancelFrames(t *testing.T, client *WSClient) []WSMessage {
	t.Helper()

	var frames []WSMessage
	for {
		select {
		case raw := <-client.SendChan:
			var msg WSMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Fatalf("Unmarshal(frame) error = %v", err)
			}
			frames = append(frames, msg)
		case <-time.After(200 * time.Millisecond):
			return frames
		}
	}
}

func cancelFrame(t *testing.T, data string) WSMessage {
	t.Helper()
	return WSMessage{Version: WSProtocolVersion, Event: "cancel", Data: json.RawMessage(data)}
}

func TestCancelWithoutScopeIsAHardStop(t *testing.T) {
	sessionKey := "native:cancel-scope-session"
	channel, client, recorder := newCancelScopeChannel(t, sessionKey)

	channel.handleWSMessage(client, cancelFrame(t, `{}`))

	if len(recorder.interrupted) != 0 {
		t.Errorf("InterruptAgent calls = %v, want none", recorder.interrupted)
	}
	if len(recorder.stopped) != 1 || recorder.stopped[0] != sessionKey {
		t.Fatalf("StopAgent calls = %v, want [%s]", recorder.stopped, sessionKey)
	}

	for _, frame := range readCancelFrames(t, client) {
		if frame.Event != "cancel.ack" {
			continue
		}
		var ack struct {
			Scope string `json:"scope"`
		}
		decodeWSData(t, frame.Data, &ack)
		if ack.Scope != CancelScopeSession {
			t.Errorf("cancel.ack scope = %q, want %q", ack.Scope, CancelScopeSession)
		}
	}
}

func TestCancelTurnScopeOnlyInterruptsTheTurn(t *testing.T) {
	sessionKey := "native:cancel-scope-turn"
	channel, client, recorder := newCancelScopeChannel(t, sessionKey)

	channel.handleWSMessage(client, cancelFrame(t, `{"session_key":"native:someone-else","scope":"turn"}`))

	if len(recorder.stopped) != 0 {
		t.Errorf("StopAgent calls = %v, want none (turn scope must not stop detached work)", recorder.stopped)
	}
	if len(recorder.interrupted) != 1 || recorder.interrupted[0] != sessionKey {
		t.Fatalf("InterruptAgent calls = %v, want [%s] (ownership comes from the client, not the payload)", recorder.interrupted, sessionKey)
	}

	acked := false
	for _, frame := range readCancelFrames(t, client) {
		if frame.Event != "cancel.ack" {
			continue
		}
		var ack struct {
			Scope string `json:"scope"`
		}
		decodeWSData(t, frame.Data, &ack)
		if ack.Scope != CancelScopeTurn {
			t.Errorf("cancel.ack scope = %q, want %q", ack.Scope, CancelScopeTurn)
		}
		acked = true
	}
	if !acked {
		t.Fatal("no cancel.ack frame for a turn-scoped cancel")
	}
}

func TestCancelBogusScopeIsRejected(t *testing.T) {
	channel, client, recorder := newCancelScopeChannel(t, "native:cancel-scope-bogus")

	channel.handleWSMessage(client, cancelFrame(t, `{"scope":"bogus"}`))

	if len(recorder.stopped) != 0 || len(recorder.interrupted) != 0 {
		t.Fatalf("agent calls = stopped %v / interrupted %v, want none", recorder.stopped, recorder.interrupted)
	}

	severity := false
	for _, frame := range readCancelFrames(t, client) {
		if frame.Event != "error" {
			continue
		}
		var payload WSErrorPayload
		decodeWSData(t, frame.Data, &payload)
		if payload.Code != "payload_error" {
			t.Errorf("error code = %q, want payload_error", payload.Code)
		}
		severity = true
	}
	if !severity {
		t.Fatal("no error frame for a bogus cancel scope")
	}
}

func TestCancelMalformedPayloadIsRejected(t *testing.T) {
	channel, client, recorder := newCancelScopeChannel(t, "native:cancel-scope-malformed")

	channel.handleWSMessage(client, cancelFrame(t, `"not-an-object"`))

	if len(recorder.stopped) != 0 || len(recorder.interrupted) != 0 {
		t.Fatalf("agent calls = stopped %v / interrupted %v, want none", recorder.stopped, recorder.interrupted)
	}

	found := false
	for _, frame := range readCancelFrames(t, client) {
		if frame.Event != "error" {
			continue
		}
		var payload WSErrorPayload
		decodeWSData(t, frame.Data, &payload)
		if payload.Code == "payload_error" {
			found = true
		}
	}
	if !found {
		t.Fatal("no payload_error frame for a malformed cancel payload")
	}
}
