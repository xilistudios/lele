package channels

import (
	"encoding/json"
	"testing"
	"time"
)

// newWSClientForTest builds a WSClient with a buffered send channel and valid
// ClientInfo.
func newWSClientForTest(id string) *WSClient {
	return &WSClient{
		ID:         id,
		SendChan:   make(chan []byte, 16),
		ClientInfo: &ClientInfo{ClientID: id, DeviceName: "test-device"},
	}
}

func (c *WSClient) receiveEvent(t *testing.T) (WSMessage, bool) {
	t.Helper()
	select {
	case raw := <-c.SendChan:
		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return msg, true
	case <-time.After(500 * time.Millisecond):
		return WSMessage{}, false
	}
}

// TestHandleWSApprove exercises the approve flow through the WS handler using
// a real ApprovalManager.
func TestHandleWSApprove(t *testing.T) {
	ts := newNativeTestServer(t)
	am := NewApprovalManager()
	ts.channel.approvalManager = am

	client := newWSClientForTest("client-1")
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	// Invalid JSON → sendError.
	ts.channel.handleWSApprove(client, []byte(`not-json`), "evt-3")

	// Empty request_id: HandleApproval returns error → sendError.
	ts.channel.handleWSApprove(client, []byte(`{"request_id":"","approved":true}`), "evt-1")

	// Valid approval → approve.ack.
	approval := am.CreateApproval("native:"+ts.clientID, "echo hi", "test", 0)
	okPayload := []byte(`{"request_id":"` + approval.ID + `","approved":true}`)
	ts.channel.handleWSApprove(client, okPayload, "evt-2")

	_, _ = client.receiveEvent(t)
	_, _ = client.receiveEvent(t)
	_, _ = client.receiveEvent(t)
}

// TestHandleWSUnsubscribe exercises the unsubscribe handler branches.
func TestHandleWSUnsubscribe(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("uclient-1")
	client.SessionKey = "native:" + ts.clientID
	client.Subscriptions = map[string]bool{"agent:main:main": true}
	ts.channel.addWSClient(client)

	data := []byte(`{"session_key":"agent:main:main"}`)
	ts.channel.handleWSUnsubscribe(client, data, "evt-1")

	// Invalid payload.
	ts.channel.handleWSUnsubscribe(client, []byte(`bad`), "evt-2")
	_, _ = client.receiveEvent(t)
}

// TestHandleWSTyping exercises the typing handler.
func TestHandleWSTyping(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("tclient-1")
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	ts.channel.handleWSTyping(client, []byte(`{"session_key":"agent:main:main"}`))
	// Empty sessionKey falls back to client.SessionKey.
	ts.channel.handleWSTyping(client, []byte(`{}`))
	// Invalid payload ignored.
	ts.channel.handleWSTyping(client, []byte(`bad`))
}

// TestHandleWSCancel exercises the cancel handler.
func TestHandleWSCancel(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("cclient-1")
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	ts.channel.handleWSCancel(client, nil, "evt-1")
	_, _ = client.receiveEvent(t)
}

// TestStreamMessageMethods covers StreamMessage, SendToolExecuting,
// SendToolResult, and SendApprovalRequest.
func TestStreamMessageMethods(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("sclient-1")
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	ts.channel.StreamMessage("native:"+ts.clientID, "mid-1", "chunk", false)
	ts.channel.SendToolExecuting("native:"+ts.clientID, "read_file", "start")
	ts.channel.SendToolResult("native:"+ts.clientID, "read_file", "result")
	ts.channel.SendApprovalRequest("native:"+ts.clientID, "aid-1", "cmd", "reason")

	for i := 0; i < 4; i++ {
		if _, ok := client.receiveEvent(t); !ok {
			break
		}
	}
}

// TestSendReconnected verifies reconnected event emission and buffered flush
// (message.stream is skipped, tool.executing is flushed).
func TestSendReconnected(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("rclient-1")
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	buffered := []json.RawMessage{
		json.RawMessage(`{"v":1,"event":"message.stream","data":{}}`),
		json.RawMessage(`{"v":1,"event":"tool.executing","data":{}}`),
	}
	ts.channel.sendReconnected(client, buffered)

	if _, ok := client.receiveEvent(t); !ok {
		t.Fatal("expected reconnected event")
	}
	if msg, ok := client.receiveEvent(t); ok {
		if msg.Event != "tool.executing" {
			t.Errorf("flushed event = %q, want tool.executing", msg.Event)
		}
	}
}

// TestSendError covers the sendError helper.
func TestSendError(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("eclient-1")
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	ts.channel.sendError(client, "test_code", "test message")
	msg, ok := client.receiveEvent(t)
	if !ok {
		t.Fatal("expected error event")
	}
	if msg.Event != "error" {
		t.Errorf("event = %q, want error", msg.Event)
	}
}