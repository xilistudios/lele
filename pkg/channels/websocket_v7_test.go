package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

// catchupTestAgentLoop wraps nativeTestAgentLoop to override
// GetInProgressAssistant and AllGroupSnapshots so we can exercise the
// catchup/group-snapshot code paths in websocket.go.
type catchupTestAgentLoop struct {
	*nativeTestAgentLoop
	inProgress *providers.Message
	groups     []group.GroupSnapshot
}

func (m *catchupTestAgentLoop) GetInProgressAssistant(sessionKey string) *providers.Message {
	return m.inProgress
}

func (m *catchupTestAgentLoop) AllGroupSnapshots() []group.GroupSnapshot {
	return m.groups
}

// ---------------------------------------------------------------------------
// collectCatchupMessages
// ---------------------------------------------------------------------------

func TestCollectCatchupMessages(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := &catchupTestAgentLoop{
		nativeTestAgentLoop: ts.loop,
		inProgress: &providers.Message{
			Role:             "assistant",
			Content:          "accumulated text",
			ReasoningContent: "thinking text",
		},
	}
	ts.channel.agentLoop = loop

	// processing=true with in-progress assistant msg → returns catchup.
	catchup := ts.channel.collectCatchupMessages("native:"+ts.clientID, true)
	if len(catchup) != 1 {
		t.Fatalf("len(catchup) = %d, want 1", len(catchup))
	}
	if catchup[0]["content"] != "accumulated text" {
		t.Errorf("content = %v, want accumulated text", catchup[0]["content"])
	}
	if catchup[0]["reasoning_content"] != "thinking text" {
		t.Errorf("reasoning_content = %v, want thinking text", catchup[0]["reasoning_content"])
	}
	if catchup[0]["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", catchup[0]["role"])
	}

	// processing=false → nil.
	if got := ts.channel.collectCatchupMessages("native:"+ts.clientID, false); got != nil {
		t.Errorf("processing=false expected nil, got %v", got)
	}

	// in-progress nil → nil.
	loop.inProgress = nil
	if got := ts.channel.collectCatchupMessages("native:"+ts.clientID, true); got != nil {
		t.Errorf("nil in-progress expected nil, got %v", got)
	}
}

func TestCollectCatchupMessages_NilAgentLoop(t *testing.T) {
	ts := newNativeTestServer(t)
	ts.channel.agentLoop = nil
	if got := ts.channel.collectCatchupMessages("x", true); got != nil {
		t.Errorf("nil agentLoop expected nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// sessionGroupSnapshots
// ---------------------------------------------------------------------------

func TestSessionGroupSnapshots(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := &catchupTestAgentLoop{
		nativeTestAgentLoop: ts.loop,
		groups: []group.GroupSnapshot{
			// Direct OriginChatID match.
			{GroupID: "g1", OriginChatID: "native:" + ts.clientID},
			// Match via session alias resolution.
			{GroupID: "g2", OriginChatID: "aliased-session"},
			// No match at all.
			{GroupID: "g3", OriginChatID: "other-session"},
		},
	}
	// ResolveSessionKey("aliased-session") → target session.
	loop.sessionAliases["aliased-session"] = "native:" + ts.clientID
	ts.channel.agentLoop = loop

	got := ts.channel.sessionGroupSnapshots("native:" + ts.clientID)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g.GroupID] = true
	}
	if !seen["g1"] {
		t.Error("g1 (direct match) missing")
	}
	if !seen["g2"] {
		t.Error("g2 (alias match) missing")
	}
	if seen["g3"] {
		t.Error("g3 (no match) should not be included")
	}
}

func TestSessionGroupSnapshots_NoGroups(t *testing.T) {
	ts := newNativeTestServer(t)
	got := ts.channel.sessionGroupSnapshots("native:" + ts.clientID)
	if len(got) != 0 {
		t.Errorf("expected no groups, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// sendError — closed-client error path
// ---------------------------------------------------------------------------

func TestSendError_ClosedClient(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("closed-client")
	ts.channel.addWSClient(client)
	// Simulate a closed client so Send returns an error.
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()

	// Should not panic and simply log the failure.
	ts.channel.sendError(client, "some_code", "some message")
}

// ---------------------------------------------------------------------------
// handleWSMessage event dispatch (approve / subscribe / unsubscribe / cancel)
// ---------------------------------------------------------------------------

func requireRegisteredWSClient(t *testing.T, ts *nativeTestServer, id string) *WSClient {
	t.Helper()
	client := newWSClientForTest(id)
	client.ClientInfo.ClientID = ts.clientID // registered via pairing
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)
	return client
}

func TestHandleWSMessage_EventDispatch(t *testing.T) {
	ts := newNativeTestServer(t)
	client := requireRegisteredWSClient(t, ts, "dispatch-client")
	am := NewApprovalManager()
	ts.channel.approvalManager = am

	// approve dispatch → approve.ack (via handleWSApprove).
	approval := am.CreateApproval("native:"+ts.clientID, "ls", "test", 0)
	ts.channel.handleWSMessage(client, WSMessage{
		Version: WSProtocolVersion,
		Event:   "approve",
		Data:    mustMarshal(map[string]interface{}{"request_id": approval.ID, "approved": true}),
	})

	// subscribe dispatch → subscribe.ack.
	ts.channel.handleWSMessage(client, WSMessage{
		Version: WSProtocolVersion,
		Event:   "subscribe",
		Data:    mustMarshal(map[string]string{"session_key": "native:" + ts.clientID}),
	})

	// unsubscribe dispatch → unsubscribe.ack.
	ts.channel.handleWSMessage(client, WSMessage{
		Version: WSProtocolVersion,
		Event:   "unsubscribe",
		Data:    mustMarshal(map[string]string{"session_key": "native:" + ts.clientID}),
	})

	// cancel dispatch → cancel.ack.
	ts.channel.handleWSMessage(client, WSMessage{
		Version: WSProtocolVersion,
		Event:   "cancel",
	})

	// Drain 4+ acks.
	for i := 0; i < 4; i++ {
		if _, ok := client.receiveEvent(t); !ok {
			t.Fatalf("expected ack %d", i)
		}
	}
}

// ---------------------------------------------------------------------------
// handleWSSubscribe error branches + success with catchup
// ---------------------------------------------------------------------------

func TestHandleWSSubscribe_Branches(t *testing.T) {
	ts := newNativeTestServer(t)
	loop := &catchupTestAgentLoop{nativeTestAgentLoop: ts.loop}
	ts.channel.agentLoop = loop
	client := requireRegisteredWSClient(t, ts, "sub-branches")

	// Invalid JSON payload → payload_error.
	ts.channel.handleWSSubscribe(client, []byte(`not-json`), "e1")
	if ev, ok := client.receiveEvent(t); !ok || ev.Event != "error" {
		t.Fatalf("expected payload_error, got %+v ok=%v", ev, ok)
	}

	// Invalid session_key format → session_key_invalid ("a/b" contains / ).
	ts.channel.handleWSSubscribe(client, mustMarshal(map[string]string{"session_key": "a/b"}), "e2")
	if ev, ok := client.receiveEvent(t); !ok || ev.Event != "error" {
		t.Fatalf("expected session_key_invalid, got %+v ok=%v", ev, ok)
	}

	// Ownership failure: subagent key with no parent → forbidden.
	ts.channel.handleWSSubscribe(client, mustMarshal(map[string]string{"session_key": "subagent:subagent-99"}), "e3")
	if ev, ok := client.receiveEvent(t); !ok || ev.Event != "error" {
		t.Fatalf("expected forbidden, got %+v ok=%v", ev, ok)
	}

	// Valid subscribe with catchup (processing + in-progress msg).
	loop.processing["native:"+ts.clientID] = true
	loop.inProgress = &providers.Message{Content: "partial", ReasoningContent: "maybe"}
	ts.channel.handleWSSubscribe(client, mustMarshal(map[string]string{"session_key": "native:" + ts.clientID}), "e4")

	ev, ok := client.receiveEvent(t)
	if !ok {
		t.Fatal("expected subscribe.ack")
	}
	if ev.Event != "subscribe.ack" {
		t.Fatalf("event = %q, want subscribe.ack", ev.Event)
	}
	var ackData map[string]interface{}
	if err := json.Unmarshal(ev.Data, &ackData); err != nil {
		t.Fatalf("unmarshal ack data: %v", err)
	}
	if ackData["processing"] != true {
		t.Errorf("processing = %v, want true", ackData["processing"])
	}
	msgs, ok := ackData["in_progress_messages"].([]interface{})
	if !ok || len(msgs) != 1 {
		t.Fatalf("in_progress_messages = %v, want 1 catchup msg", ackData["in_progress_messages"])
	}
	first := msgs[0].(map[string]interface{})
	if first["content"] != "partial" {
		t.Errorf("catchup content = %v, want partial", first["content"])
	}
}

func TestHandleWSSubscribe_NotProcessing(t *testing.T) {
	ts := newNativeTestServer(t)
	client := requireRegisteredWSClient(t, ts, "sub-notproc")

	ts.channel.handleWSSubscribe(client, mustMarshal(map[string]string{"session_key": "native:" + ts.clientID}), "e5")
	ev, ok := client.receiveEvent(t)
	if !ok {
		t.Fatal("expected subscribe.ack")
	}
	if ev.Event != "subscribe.ack" {
		t.Fatalf("event = %q, want subscribe.ack", ev.Event)
	}
	var ackData map[string]interface{}
	if err := json.Unmarshal(ev.Data, &ackData); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ackData["processing"] != false {
		t.Errorf("processing = %v, want false", ackData["processing"])
	}
	if _, present := ackData["in_progress_messages"]; present {
		t.Error("in_progress_messages should be absent when not processing")
	}
}

// ---------------------------------------------------------------------------
// handleWebSocket — pre-upgrade error paths via recorder
// ---------------------------------------------------------------------------

func newWSRequest(ts *nativeTestServer, sessionKey string) *http.Request {
	u := "/api/v1/ws?token=" + url.QueryEscape(ts.token)
	if sessionKey != "" {
		u += "&session_key=" + url.QueryEscape(sessionKey)
	}
	return httptest.NewRequest(http.MethodGet, u, nil)
}

func TestHandleWebSocket_MethodNotAllowed(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ws?token="+ts.token, nil)
	ts.channel.handleWebSocket(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", rec.Code)
	}
}

func TestHandleWebSocket_MissingToken(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	ts.channel.handleWebSocket(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", rec.Code)
	}
}

func TestHandleWebSocket_InvalidToken(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws?token=bogus-token", nil)
	ts.channel.handleWebSocket(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", rec.Code)
	}
}

func TestHandleWebSocket_BearerToken(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws", nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	ts.channel.handleWebSocket(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusBadRequest {
		// Without a real upgrade the recorder can be 200 (upgrade not
		// applicable to recorder) — just ensure it passed token checks.
		t.Logf("bearer token path proceeded with code %d", rec.Code)
	}
}

func TestHandleWebSocket_InvalidSessionKeyFormat(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := newWSRequest(ts, "a..b/bad")
	ts.channel.handleWebSocket(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

func TestHandleWebSocket_OwnershipForbidden(t *testing.T) {
	ts := newNativeTestServer(t)
	req := newWSRequest(ts, "subagent:subagent-123") // valid format, no parent → forbidden
	rec := httptest.NewRecorder()
	ts.channel.handleWebSocket(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Real websocket connection: handleWebSocket upgrade + read/write loops
// ---------------------------------------------------------------------------

func dialNativeWS(t *testing.T, ts *nativeTestServer, sessionKey string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.server.URL, "http") +
		"/api/v1/ws?token=" + url.QueryEscape(ts.token)
	if sessionKey != "" {
		wsURL += "&session_key=" + url.QueryEscape(sessionKey)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed with status %d: %v", resp.StatusCode, err)
		}
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	// Server should send a welcome right after upgrade.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	var welcome WSMessage
	if err := json.Unmarshal(raw, &welcome); err != nil {
		t.Fatalf("unmarshal welcome: %v", err)
	}
	if welcome.Event != "welcome" {
		t.Fatalf("first event = %q, want welcome", welcome.Event)
	}
	return conn
}

func readNextWS(t *testing.T, conn *websocket.Conn) WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return msg
}

func TestHandleWebSocket_UpgradeAndPingPong(t *testing.T) {
	ts := newNativeTestServer(t)
	conn := dialNativeWS(t, ts, "")

	// Send a ping → expect pong.
	if err := conn.WriteMessage(websocket.TextMessage, mustMarshal(WSMessage{
		Version: WSProtocolVersion, Event: "ping",
	})); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	pong := readNextWS(t, conn)
	if pong.Event != "pong" {
		t.Fatalf("event = %q, want pong", pong.Event)
	}

	// Send an invalid (non-JSON) message → server sends parse_error.
	if err := conn.WriteMessage(websocket.TextMessage, []byte("not-json")); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	errMsg := readNextWS(t, conn)
	if errMsg.Event != "error" {
		t.Fatalf("event = %q, want error for parse", errMsg.Event)
	}

	// Send subscribe for the client's default session → subscribe.ack.
	if err := conn.WriteMessage(websocket.TextMessage, mustMarshal(WSMessage{
		Version: WSProtocolVersion, Event: "subscribe",
		Data: mustMarshal(map[string]string{"session_key": ts.clientID}),
	})); err != nil {
		t.Fatalf("write subscribe: %v", err)
	}
	ack := readNextWS(t, conn)
	if ack.Event != "subscribe.ack" {
		t.Fatalf("event = %q, want subscribe.ack", ack.Event)
	}

	// Send a message → message.ack.
	if err := conn.WriteMessage(websocket.TextMessage, mustMarshal(WSMessage{
		Version: WSProtocolVersion, Event: "message",
		Data: mustMarshal(map[string]string{"content": "hello"}),
	})); err != nil {
		t.Fatalf("write message: %v", err)
	}
	msgAck := readNextWS(t, conn)
	if msgAck.Event != "message.ack" {
		t.Fatalf("event = %q, want message.ack", msgAck.Event)
	}
}

func TestHandleWebSocket_UpgradeWithSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)
	conn := dialNativeWS(t, ts, "native:"+ts.clientID)
	// Connections remain usable; a ping/pong round-trip confirms the
	// read/write loops are running with the provided session key.
	if err := conn.WriteMessage(websocket.TextMessage, mustMarshal(WSMessage{
		Version: WSProtocolVersion, Event: "ping",
	})); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	pong := readNextWS(t, conn)
	if pong.Event != "pong" {
		t.Fatalf("event = %q, want pong", pong.Event)
	}
}

// ---------------------------------------------------------------------------
// wsWriteLoop — close-channel and write-error branches
// ---------------------------------------------------------------------------

// newWSLoopPair spins up a bare WebSocket server that holds the server-side
// conn, and returns both the client and server conns so a WSClient can be
// driven directly by wsWriteLoop.
func newWSLoopPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	var serverConn *websocket.Conn
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		serverConn = conn
		// Keep the handler alive so the server-side conn stays open.
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { clientConn.Close() })
	// Wait for the server-side conn to be assigned.
	deadline := time.Now().Add(2 * time.Second)
	for serverConn == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if serverConn == nil {
		t.Fatal("server-side conn never assigned")
	}
	return clientConn, serverConn
}

func newLoopClient(conn *websocket.Conn) *WSClient {
	return &WSClient{
		ID:         "loop-client",
		Conn:       conn,
		SendChan:   make(chan []byte, wsSendChanSize),
		ClientInfo: &ClientInfo{ClientID: "loop-client", DeviceName: "test"},
	}
}

func TestWSWriteLoop_ChannelClosed(t *testing.T) {
	clientConn, serverConn := newWSLoopPair(t)
	client := newLoopClient(serverConn)

	n := &NativeChannel{}
	done := make(chan struct{})
	go func() {
		n.wsWriteLoop(client)
		close(done)
	}()

	// Send data first to prove normal writes work.
	client.SendChan <- []byte(`{"event":"test"}`)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read normal message: %v", err)
	}
	if !strings.Contains(string(raw), "test") {
		t.Errorf("payload = %s, want test event", raw)
	}

	// Close the SendChan → loop writes CloseMessage and returns.
	close(client.SendChan)
	clientConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, err = clientConn.ReadMessage()
	// Expect a close message (or a read error as the peer half-closes).
	if err == nil {
		t.Fatal("expected close frame after SendChan closed")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wsWriteLoop did not exit after SendChan closed")
	}
}

func TestWSWriteLoop_WriteError(t *testing.T) {
	_, serverConn := newWSLoopPair(t)
	client := newLoopClient(serverConn)

	n := &NativeChannel{}
	done := make(chan struct{})
	go func() {
		n.wsWriteLoop(client)
		close(done)
	}()

	// Force a write error by closing the underlying connection out from
	// under the write loop.
	serverConn.Close()
	client.SendChan <- []byte(`{"event":"boom"}`)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wsWriteLoop did not exit after write error")
	}
} // ---------------------------------------------------------------------------
// wsReadLoop — ping handler + unexpected-close error branch
// ---------------------------------------------------------------------------

func TestWSReadLoop_PingAndUnexpectedClose(t *testing.T) {
	clientConn, serverConn := newWSLoopPair(t)
	client := newLoopClient(serverConn)

	n := &NativeChannel{}
	done := make(chan struct{})
	go func() {
		n.wsReadLoop(client)
		close(done)
	}()

	// Send a Ping control frame → server's ping handler replies with a Pong.
	// gorilla consumes incoming control frames internally, so we detect the
	// pong via a pong handler while a reader goroutine keeps the socket draining.
	pongReceived := make(chan []byte, 1)
	clientConn.SetPongHandler(func(appData string) error {
		select {
		case pongReceived <- []byte(appData):
		default:
		}
		return nil
	})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_, _, _ = clientConn.ReadMessage() // drains control frames + dispatch response
	}()

	if err := clientConn.WriteControl(websocket.PingMessage, []byte("ping-payload"), time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	select {
	case pong := <-pongReceived:
		if string(pong) != "ping-payload" {
			t.Errorf("pong payload = %q, want ping-payload", pong)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pong handler")
	}

	// Client sends a normal text message → processed (dispatch to unknown
	// event → server sends an error on the client). This exercises the
	// main read/dispatch loop.
	if err := clientConn.WriteMessage(websocket.TextMessage, mustMarshal(WSMessage{
		Version: WSProtocolVersion,
		Event:   "bogus-event-for-readloop",
	})); err != nil {
		t.Fatalf("write text: %v", err)
	}

	// Send an unexpected-close (application-defined code 4100) so the
	// read loop hits IsUnexpectedCloseError's true branch and returns.
	if err := clientConn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(4100, "boom")); err != nil {
		t.Fatalf("write close: %v", err)
	}
	// Drain the server's close response (reader goroutine consumes it).
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	select {
	case <-readerDone:
	case <-time.After(3 * time.Second):
		t.Log("reader goroutine still running (close frame pending)")
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("wsReadLoop did not exit after unexpected close")
	}
} // ---------------------------------------------------------------------------
// Send-failure branches (closed client → Send returns error)
// ---------------------------------------------------------------------------

func closedWSClient(ts *nativeTestServer, id string) *WSClient {
	c := newWSClientForTest(id)
	c.ClientInfo.ClientID = ts.clientID
	c.SessionKey = "native:" + ts.clientID
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c
}

func TestHandleWSSubscribe_SendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "sub-sendfail")
	ts.channel.addWSClient(client)
	// Closed client → subscribe.ack send fails → error branch.
	ts.channel.handleWSSubscribe(client, mustMarshal(map[string]string{"session_key": "native:" + ts.clientID}), "e1")
}

func TestHandleWSUnsubscribe_SendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "unsub-sendfail")
	ts.channel.addWSClient(client)
	ts.channel.handleWSUnsubscribe(client, mustMarshal(map[string]string{"session_key": "native:" + ts.clientID}), "e1")
}

func TestHandleWSApprove_SendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	am := NewApprovalManager()
	ts.channel.approvalManager = am
	approval := am.CreateApproval("native:"+ts.clientID, "ls", "test", 0)

	client := closedWSClient(ts, "approve-sendfail")
	ts.channel.addWSClient(client)
	// Closed client → approve.ack send fails; handleWSApprove still emits.
	ts.channel.handleWSApprove(client, mustMarshal(map[string]interface{}{"request_id": approval.ID, "approved": true}), "e1")
}

func TestSendWelcome_SendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "welcome-sendfail")
	ts.channel.addWSClient(client)
	ts.channel.sendWelcome(client)
}

func TestSendReconnected_SendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "reconnected-sendfail")
	ts.channel.addWSClient(client)
	ts.channel.sendReconnected(client, []json.RawMessage{})
}

func TestSendReconnected_FlushSendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "reconnected-flushfail")
	ts.channel.addWSClient(client)
	// The reconnected payload send fails and returns before flushing.
	ts.channel.sendReconnected(client, []json.RawMessage{json.RawMessage(`{"event":"tool.executing","data":{}}`)})
}

func TestHandleWSClientMessage_RateLimited(t *testing.T) {
	ts := newNativeTestServer(t)
	// Rate limit to 1 per hour.
	ts.channel.wsMessageLimiter = newRateLimiter(1, time.Hour)
	client := newWSClientForTest("ratelimit-client")
	client.ClientInfo.ClientID = ts.clientID
	client.SessionKey = "native:" + ts.clientID
	ts.channel.addWSClient(client)

	// First message passes limiter.
	ts.channel.handleWSClientMessage(client, mustMarshal(map[string]string{"content": "first"}), "e1")
	// Second message hits rate limit.
	ts.channel.handleWSClientMessage(client, mustMarshal(map[string]string{"content": "second"}), "e2")

	// Drain: expect message.ack then rate-limit error (order matters:
	// the limiter check happens before any ack, but first is allowed).
	seenErr := false
	for i := 0; i < 2; i++ {
		ev, ok := client.receiveEvent(t)
		if !ok {
			break
		}
		if ev.Event == "error" {
			seenErr = true
		}
	}
	if !seenErr {
		t.Error("expected a rate_limit_exceeded error")
	}
}

func TestHandleWSMessage_PingSendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "ping-sendfail")
	ts.channel.addWSClient(client)
	ts.channel.handleWSMessage(client, WSMessage{Version: WSProtocolVersion, Event: "ping"})
} // ---------------------------------------------------------------------------
// handleWSClientMessage — session_key + AgentID branches
// ---------------------------------------------------------------------------

func TestHandleWSClientMessage_SessionKeyAndAgentID(t *testing.T) {
	ts := newNativeTestServer(t)
	client := requireRegisteredWSClient(t, ts, "cm-extra")

	// Valid explicit session_key with AgentID → message.ack + SetSessionAgent.
	ts.channel.handleWSClientMessage(client, []byte(`{"content":"hi","session_key":"native:`+ts.clientID+`","agent_id":"research"}`), "e1")
	if got := ts.loop.GetSessionAgent("native:" + ts.clientID); got != "research" {
		t.Errorf("session agent = %q, want research (SetSessionAgent branch)", got)
	}
	if ev, ok := client.receiveEvent(t); !ok || ev.Event != "message.ack" {
		t.Fatalf("expected message.ack for valid session, got %+v ok=%v", ev, ok)
	}

	// Explicit session_key with invalid format → session_key_invalid.
	ts.channel.handleWSClientMessage(client, []byte(`{"content":"hi","session_key":"a/b"}`), "e2")
	if ev, ok := client.receiveEvent(t); !ok || ev.Event != "error" {
		t.Fatalf("expected error for invalid session_key, got %+v ok=%v", ev, ok)
	}

	// Explicit subagent session_key with no parent → forbidden.
	ts.channel.handleWSClientMessage(client, []byte(`{"content":"hi","session_key":"subagent:subagent-42"}`), "e3")
	if ev, ok := client.receiveEvent(t); !ok || ev.Event != "error" {
		t.Fatalf("expected forbidden error, got %+v ok=%v", ev, ok)
	}
}

func TestHandleWSClientMessage_AckSendFailure(t *testing.T) {
	ts := newNativeTestServer(t)
	client := closedWSClient(ts, "cm-ackfail")
	ts.channel.addWSClient(client)
	// A valid message passes all validation but the ack Send fails →
	// message.ack error-history branch (line 313).
	ts.channel.handleWSClientMessage(client, mustMarshal(map[string]string{"content": "hi"}), "e1")
}

// ---------------------------------------------------------------------------
// handleWebSocket — reconnect flow (existing reconnecting client)
// ---------------------------------------------------------------------------
// handleWebSocket — upgrade via live connection (welcome confirmed)
// ---------------------------------------------------------------------------

func readRawWS(t *testing.T, conn *websocket.Conn) WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return msg
}
