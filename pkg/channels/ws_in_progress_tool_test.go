package channels

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/session"
)

func dialWS(t *testing.T, ts *nativeTestServer, sessionKey string) (*websocket.Conn, map[string]interface{}) {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") + "/api/v1/ws?token=" + url.QueryEscape(ts.token)
	if sessionKey != "" {
		wsURL += "&session_key=" + url.QueryEscape(sessionKey)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	welcome := readWSMessage(t, conn)
	if welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}
	var payload map[string]interface{}
	decodeWSData(t, welcome.Data, &payload)
	return conn, payload
}

func subscribeWS(t *testing.T, conn *websocket.Conn, sessionKey string) map[string]interface{} {
	t.Helper()

	if err := conn.WriteJSON(map[string]interface{}{
		"event": "subscribe",
		"data":  map[string]interface{}{"session_key": sessionKey},
	}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}

	ack := readWSMessage(t, conn)
	if ack.Event != "subscribe.ack" {
		t.Fatalf("ack event = %q, want subscribe.ack", ack.Event)
	}
	var payload map[string]interface{}
	decodeWSData(t, ack.Data, &payload)
	return payload
}

func TestSubscribeAck_IncludesInProgressTool(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.processing[sessionKey] = true
	ts.loop.inProgressTools = map[string]*session.InProgressTool{
		sessionKey: {
			Tool:       "wait_for_subagent",
			Action:     "wait_for_subagent: task_id=subagent-3",
			Arguments:  `{"task_id":"subagent-3","timeout_seconds":600}`,
			ToolCallID: "call_wait_1",
		},
	}

	conn, _ := dialWS(t, ts, "")
	ack := subscribeWS(t, conn, sessionKey)

	if ack["processing"] != true {
		t.Fatalf("processing = %v, want true", ack["processing"])
	}

	raw, ok := ack["in_progress_tool"]
	if !ok {
		t.Fatalf("subscribe.ack is missing in_progress_tool: %#v", ack)
	}
	var payload WSToolExecutingPayload
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload.Tool != "wait_for_subagent" || payload.Action != "wait_for_subagent: task_id=subagent-3" {
		t.Fatalf("payload = %#v, want wait_for_subagent", payload)
	}
	if payload.ToolCallID != "call_wait_1" || payload.SessionKey != sessionKey {
		t.Fatalf("payload identity = %#v", payload)
	}
	if payload.Arguments["task_id"] != "subagent-3" {
		t.Fatalf("arguments = %#v, want the parsed JSON object", payload.Arguments)
	}
}

func TestSubscribeAck_OmitsInProgressToolWhenNotProcessing(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.processing[sessionKey] = false
	ts.loop.inProgressTools = map[string]*session.InProgressTool{
		sessionKey: {Tool: "sleep", Action: "sleep: 600s"},
	}

	conn, _ := dialWS(t, ts, "")
	ack := subscribeWS(t, conn, sessionKey)

	if _, ok := ack["in_progress_tool"]; ok {
		t.Fatalf("stale tool reported while not processing: %#v", ack)
	}
}

func TestWelcome_IncludesInProgressTool(t *testing.T) {
	ts := newNativeTestServer(t)

	baseKey := "native:" + ts.clientID
	ts.loop.processing[baseKey] = true
	ts.loop.inProgressTools = map[string]*session.InProgressTool{
		baseKey: {Tool: "exec", Action: "exec: sleep 600", ToolCallID: "call_exec_1"},
	}

	_, welcome := dialWS(t, ts, baseKey)

	raw, ok := welcome["in_progress_tool"]
	if !ok {
		t.Fatalf("welcome is missing in_progress_tool: %#v", welcome)
	}
	var payload WSToolExecutingPayload
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if payload.Tool != "exec" || payload.ToolCallID != "call_exec_1" {
		t.Fatalf("welcome payload = %#v", payload)
	}
}

func TestWelcome_OmitsInProgressToolWhenIdle(t *testing.T) {
	ts := newNativeTestServer(t)

	_, welcome := dialWS(t, ts, "")

	if _, ok := welcome["in_progress_tool"]; ok {
		t.Fatalf("idle welcome carried in_progress_tool: %#v", welcome)
	}
}
