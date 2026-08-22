//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// ---------------------------------------------------------------------------
// feishu_64.go
// ---------------------------------------------------------------------------

func TestFeishu_Stop(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)

	// Stop with nil cancel is a no-op.
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop with nil cancel: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after stop")
	}

	// Now with a non-nil cancel set.
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.mu.Lock()
	ch.cancel = cancel
	ch.mu.Unlock()
	ch.setRunning(true)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop with cancel: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after stop with cancel")
	}
	ch.mu.Lock()
	ws := ch.wsClient
	ch.mu.Unlock()
	if ws != nil {
		t.Error("wsClient should be nil after stop")
	}
}

// ---------------------------------------------------------------------------
// maixcam.go
// ---------------------------------------------------------------------------

func TestMaixCam_StartAndAcceptConnections(t *testing.T) {
	mb := bus.NewMessageBus()
	// Use an ephemeral port.
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{Host: "127.0.0.1", Port: 0}, mb)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen on a temp free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	ln.Close()

	ch.config.Port = addr.Port
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("channel should be running after Start")
	}

	// Connect a client and send a person_detected + heartbeat message.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", addr.Port))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(5 * time.Second)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			if _, ok := ch.clients[conn]; ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	wg.Wait()

	// Send a heartbeat (no panic / no inbound) and confirm it does not error.
	enc := json.NewEncoder(conn)
	_ = enc.Encode(map[string]interface{}{"type": "heartbeat"})

	// Send a person_detected message.
	_ = enc.Encode(map[string]interface{}{
		"type":     "person_detected",
		"image":    "data:base64,...",
		"location": "door",
	})
	_ = enc.Encode(map[string]interface{}{
		"type":    "status",
		"running": true,
	})

	// Give the decoder time to process.
	time.Sleep(200 * time.Millisecond)

	// Stop the channel: acceptConnections and handleConnection should return.
	cancel()
	time.Sleep(200 * time.Millisecond)
}

func TestMaixCam_Start_ListenError(t *testing.T) {
	mb := bus.NewMessageBus()
	// Grab a port and keep it open so the second bind fails.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	ch, _ := NewMaixCamChannel(config.MaixCamConfig{Host: "127.0.0.1", Port: port}, mb)
	if err := ch.Start(context.Background()); err == nil {
		t.Error("expected error when port is in use")
	}
}

// ---------------------------------------------------------------------------
// rest_secrets.go — nil keyring service paths
// ---------------------------------------------------------------------------

func TestSecrets_NilKeyringService(t *testing.T) {
	ts := newNativeTestServer(t)
	// Ensure no keyring service is attached.
	ts.channel.SetKeyringService(nil)

	// handleSecretsList
	rec := httptest.NewRecorder()
	ts.channel.handleSecretsList(rec, authenticatedRequest(t, ts, "/api/v1/secrets"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secrets list w/o keyring = %d, want 503", rec.Code)
	}

	// handleSecretGet
	rec = httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/secrets/foo")
	req.SetPathValue("name", "foo")
	ts.channel.handleSecretGet(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secret get w/o keyring = %d, want 503", rec.Code)
	}

	// handleSecretDelete
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/secrets/foo")
	req.SetPathValue("name", "foo")
	ts.channel.handleSecretDelete(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secret del w/o keyring = %d, want 503", rec.Code)
	}

	// handleSecretsStatus & audit
	rec = httptest.NewRecorder()
	ts.channel.handleSecretsStatus(rec, authenticatedRequest(t, ts, "/api/v1/secrets/status"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secrets status w/o keyring = %d, want 503", rec.Code)
	}
	rec = httptest.NewRecorder()
	ts.channel.handleSecretsAudit(rec, authenticatedRequest(t, ts, "/api/v1/secrets/audit"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secrets audit w/o keyring = %d, want 503", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// rest_agent.go — handleAgentStatus edge cases
// ---------------------------------------------------------------------------

func TestAgentStatus_MissingAndNotFound(t *testing.T) {
	ts := newNativeTestServer(t)

	// Missing agentID.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/agents//status")
	ts.channel.handleAgentStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing agent id = %d, want 400", rec.Code)
	}

	// Unknown agent.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/agents/nope/status")
	req.SetPathValue("agentID", "nope")
	ts.channel.handleAgentStatus(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown agent = %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// rest_stream.go — writeSSE
// ---------------------------------------------------------------------------

func TestWriteSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := writeSSE(rec, "message.chunk", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("writeSSE: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: message.chunk") {
		t.Errorf("missing event line: %q", body)
	}
	if !strings.Contains(body, `"k":"v"`) {
		t.Errorf("missing data payload: %q", body)
	}

	// Data that fails to marshal (channel) → error.
	rec = httptest.NewRecorder()
	if err := writeSSE(rec, "e", make(chan int)); err == nil {
		t.Error("expected error marshalling channel data")
	}
}

func TestFanoutRESTStream(t *testing.T) {
	ts := newNativeTestServer(t)
	n := ts.channel

	sub := n.registerRESTStreamSubscriber("sess1", "")
	defer n.unregisterRESTStreamSubscriber(sub.id)
	sub2 := n.registerRESTStreamSubscriber("sess1", "")
	defer n.unregisterRESTStreamSubscriber(sub2.id)
	_ = n.registerRESTStreamSubscriber("other", "")

	n.fanoutRESTStream("sess1", "evt", map[string]string{"a": "1"}, "msg1")

	// Both sess1 subscribers should receive the event.
	for i, s := range []*restStreamSubscriber{sub, sub2} {
		select {
		case e := <-s.ch:
			if e.event != "evt" {
				t.Errorf("sub %d event = %q, want evt", i, e.event)
			}
		case <-time.After(time.Second):
			t.Errorf("sub %d did not receive event", i)
		}
	}
}

func TestRegisterUnregisterRESTStream(t *testing.T) {
	ts := newNativeTestServer(t)
	n := ts.channel

	sub := n.registerRESTStreamSubscriber("s", "mid")
	if sub.messageID != "mid" {
		t.Errorf("messageID = %q", sub.messageID)
	}
	// Unregister closes the channel.
	n.unregisterRESTStreamSubscriber(sub.id)
	if _, ok := <-sub.ch; ok {
		t.Error("channel should be closed")
	}
	// Unregister non-existent is a no-op.
	n.unregisterRESTStreamSubscriber("nope")
}

// ---------------------------------------------------------------------------
// rest_system.go — handleSkillsAvailable error path (no network), handleSkillToggle
// ---------------------------------------------------------------------------

func TestHandleSkillInstall_InvalidJSON(t *testing.T) {
	ts := newNativeTestServer(t)

	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/skills")
	req.Body = http.NoBody
	ts.channel.handleSkillInstall(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body = %d, want 400", rec.Code)
	}

	// Missing URL.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/skills")
	req.Body = mkBody(`{"url":""}`)
	ts.channel.handleSkillInstall(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing url = %d, want 400", rec.Code)
	}
}

func TestHandleSkillToggle_NoConfigMgrGetter(t *testing.T) {
	ts := newNativeTestServer(t)
	// skillsLoader has nil config manager in the fresh server (SkillsLoader{}),
	// so GetConfigManager returns nil.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/skills/foo/toggle")
	req.SetPathValue("name", "foo")
	req.Body = mkBody(`{"enabled":true}`)
	ts.channel.handleSkillToggle(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("no config mgr = %d, want 500", rec.Code)
	}

	// Missing name.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/skills//toggle")
	ts.channel.handleSkillToggle(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", rec.Code)
	}

	// Invalid JSON body.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/skills/foo/toggle")
	req.SetPathValue("name", "foo")
	req.Body = http.NoBody
	ts.channel.handleSkillToggle(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body = %d, want 400", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// native.go — findReconnectingClient
// ---------------------------------------------------------------------------

func TestFindReconnectingClient(t *testing.T) {
	ts := newNativeTestServer(t)
	n := ts.channel

	conn := &websocket.Conn{}
	active := &WSClient{ID: "u1", Conn: conn}
	reconnecting := &WSClient{ID: "u2", Conn: conn, reconnecting: true}
	reconnecting.ClientInfo = &ClientInfo{ClientID: "recon-user"}

	n.wsClients["a"] = active
	n.wsClients["b"] = reconnecting

	if got := n.findReconnectingClient("recon-user"); got != reconnecting {
		t.Errorf("findReconnectingClient = %v, want reconnecting client", got)
	}
	if got := n.findReconnectingClient("other"); got != nil {
		t.Errorf("expected nil for non-reconnecting user")
	}
}

// ---------------------------------------------------------------------------
// manager.go — startChannelDispatcher
// ---------------------------------------------------------------------------

func TestStartChannelDispatcher(t *testing.T) {
	mb := bus.NewMessageBus()
	mgr, err := NewManager(config.DefaultConfig(), mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ch := &collectChannel{out: make(chan bus.OutboundMessage, 10)}
	queue := make(chan bus.OutboundMessage)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go mgr.startChannelDispatcher(ctx, "collect", ch, queue)

	// Send a message; dispatcher forwards it to the channel.
	queue <- bus.OutboundMessage{Content: "hello"}
	select {
	case got := <-ch.out:
		if got.Content != "hello" {
			t.Errorf("got %q", got.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not forward message")
	}

	// Cancel stops the dispatcher loop.
	cancel()
}

// ---------------------------------------------------------------------------
// slack.go — SetTranscriber
// ---------------------------------------------------------------------------

func TestSlack_SetTranscriber_Nil(t *testing.T) {
	mb := bus.NewMessageBus()
	cfg := config.DefaultConfig()
	// Constructor requires a token; build channel directly to avoid network.
	base := NewBaseChannel("slack", &cfg.Channels.Slack, mb, nil)
	ch := &SlackChannel{BaseChannel: base}
	ch.SetTranscriber(nil)
	if ch.transcriber != nil {
		t.Error("transcriber should be nil")
	}
}

// collectChannel is a minimal Channel for dispatcher tests.
type collectChannel struct {
	out chan bus.OutboundMessage
}

func (c *collectChannel) Name() string                 { return "collect" }
func (c *collectChannel) Start(ctx context.Context) error { return nil }
func (c *collectChannel) Stop(ctx context.Context) error  { return nil }
func (c *collectChannel) IsRunning() bool                  { return true }
func (c *collectChannel) IsAllowed(senderID string) bool   { return true }
func (c *collectChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	select {
	case c.out <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- test helpers ---
func authenticatedRequest(t *testing.T, ts *nativeTestServer, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	req.Header.Set("X-Client-Id", ts.clientID)
	return req
}

func mkBody(s string) io.ReadCloser {
	return io.NopCloser(strings.NewReader(s))
}// ---------------------------------------------------------------------------
// dingtalk.go — Stop (no network)
// ---------------------------------------------------------------------------

func TestDingTalk_Stop(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewDingTalkChannel(config.DingTalkConfig{ClientID: "id", ClientSecret: "sec"}, mb)

	// Stop with nil cancel and nil streamClient is a no-op.
	ch.setRunning(true)
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop (nil cancel): %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop")
	}

	// With a non-nil cancel.
	_, cancel := context.WithCancel(context.Background())
	ch.cancel = cancel
	ch.setRunning(true)
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop (with cancel): %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop with cancel")
	}
}

// ---------------------------------------------------------------------------
// onebot.go — raw event dispatch (no network)
// ---------------------------------------------------------------------------

func TestOneBot_handleRawEvent_Dispatch(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:3000"}, mb)

	// meta_event → handleMetaEvent, no error.
	ch.handleRawEvent(&oneBotRawEvent{PostType: "meta_event"})

	// notice → handleNoticeEvent, no error.
	ch.handleRawEvent(&oneBotRawEvent{PostType: "notice"})

	// message_sent → ignore.
	ch.handleRawEvent(&oneBotRawEvent{PostType: "message_sent"})

	// request → ignore.
	ch.handleRawEvent(&oneBotRawEvent{PostType: "request"})

	// API response (empty post type) → ignored.
	ch.handleRawEvent(&oneBotRawEvent{PostType: ""})

	// Unknown → ignored.
	ch.handleRawEvent(&oneBotRawEvent{PostType: "weird"})

	// handleMetaEvent and handleNoticeEvent are directly callable no-ops.
	ch.handleMetaEvent(&oneBotRawEvent{})
	ch.handleNoticeEvent(&oneBotRawEvent{})
}// ---------------------------------------------------------------------------
// onebot.go — connect/listen/pinger with a real websocket server
// ---------------------------------------------------------------------------

func TestOneBot_ConnectAndListen(t *testing.T) {
	// Start a websocket echo/capture server.
	var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Read once and respond with a ping/pong lifecycle-like event.
		_, _, err = conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"post_type":"meta_event","meta_event_type":"lifecycle"}`))
		}
		// Keep the connection open briefly.
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				break
			}
		}
		conn.Close()
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: wsURL}, mb)
	ctx, cancel := context.WithCancel(context.Background())
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	defer cancel()

	if err := ch.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	ch.mu.Lock()
	conn := ch.conn
	ch.mu.Unlock()
	if conn == nil {
		t.Fatal("conn should be set after connect")
	}

	// Start listen in a goroutine; it should read the meta_event and return
	// when the server closes (or context cancels).
	done := make(chan struct{})
	go func() {
		ch.listen()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// listen may still be blocked on ReadMessage; cancel context.
		ch.cancel()
	}

	// pinger: exercise the ping loop with a short deadline (may send one ping
	// then exit only on ctx cancel). Just ensure it exits on ctx cancel.
	pctx, pcancel := context.WithCancel(context.Background())
	defer pcancel()
	ch.mu.Lock()
	pconn := ch.conn
	ch.mu.Unlock()
	if pconn != nil {
		pdone := make(chan struct{})
		go func() {
			c2 := &OneBotChannel{ctx: pctx}
			c2.writeMu = sync.Mutex{}
			c2.pinger(pconn)
			close(pdone)
		}()
		pcancel()
		select {
		case <-pdone:
		case <-time.After(2 * time.Second):
			t.Error("pinger did not exit on ctx cancel")
		}
	}
}

func TestOneBot_Send_NotConnected(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:1"}, mb)
	ch.setRunning(true)
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "group:123", Content: "hi"}); err == nil {
		t.Error("expected error when not connected")
	}
}

func TestOneBot_sendAPIRequest_NotConnected(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:1"}, mb)
	if _, err := ch.sendAPIRequest("get_status", nil, time.Second); err == nil {
		t.Error("expected error when not connected")
	}
}// ---------------------------------------------------------------------------
// native.go — handleBackgroundExecStream error path
// ---------------------------------------------------------------------------

func TestBackgroundExecStream_NoID(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/background-exec/x/stream")
	ts.channel.handleBackgroundExecStream(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing id = %d, want 400", rec.Code)
	}
}

func TestBackgroundExecStream_NotFound(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/background-exec/nope/stream")
	req.SetPathValue("id", "nope")
	ts.channel.handleBackgroundExecStream(rec, req)
	// The test agent loop returns "not implemented" → handler writes an error
	// event and returns with 200 (streaming response).
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not implemented") {
		t.Errorf("expected error payload, got %q", rec.Body.String())
	}
}// ---------------------------------------------------------------------------
// websocket.go — handleWSMessage / handleWSClientMessage branches
// ---------------------------------------------------------------------------

func TestHandleWSMessage_VersionAndPing(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("ws-v1")
	ts.channel.addWSClient(client)

	// Unsupported version → error event.
	ts.channel.handleWSMessage(client, WSMessage{Version: 999, Event: "msg"})
	ev, ok := client.receiveEvent(t)
	if !ok {
		t.Fatal("expected version error event")
	}
	if ev.Event != "error" {
		t.Errorf("event = %q, want error", ev.Event)
	}

	// Unknown event → error.
	ts.channel.handleWSMessage(client, WSMessage{Version: WSProtocolVersion, Event: "bogus"})
	ev, ok = client.receiveEvent(t)
	if !ok {
		t.Fatal("expected unknown event error")
	}
	if ev.Event != "error" {
		t.Errorf("event = %q, want error", ev.Event)
	}

	// Ping → pong.
	ts.channel.handleWSMessage(client, WSMessage{Version: WSProtocolVersion, Event: "ping"})
	ev, ok = client.receiveEvent(t)
	if !ok {
		t.Fatal("expected pong")
	}
	if ev.Event != "pong" {
		t.Errorf("event = %q, want pong", ev.Event)
	}
}

func TestHandleWSClientMessage_Branches(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("ws-cm1")
	ts.channel.addWSClient(client)

	// Invalid payload JSON.
	ts.channel.handleWSClientMessage(client, []byte(`not-json`), "e1")
	ev, ok := client.receiveEvent(t)
	if !ok || ev.Data == nil {
		t.Fatal("expected payload_error event")
	}

	// Empty content.
	ts.channel.handleWSClientMessage(client, []byte(`{"content":""}`), "e2")
	ev, _ = client.receiveEvent(t)
	if ev.Event != "error" {
		t.Errorf("expected error for empty content, got %q", ev.Event)
	}

	// Invalid session key format.
	ts.channel.handleWSClientMessage(client, []byte(`{"content":"hi","session_key":"bad format!"}`), "e3")
	ev, _ = client.receiveEvent(t)
	if ev.Event != "error" {
		t.Errorf("expected error for invalid session key, got %q", ev.Event)
	}

	// Valid content → message.ack.
	client.SessionKey = "native:" + ts.clientID
	ts.channel.handleWSClientMessage(client, []byte(`{"content":"hello world"}`), "e4")
	ev, ok = client.receiveEvent(t)
	if !ok {
		t.Fatal("expected ack")
	}
	if ev.Event != "message.ack" {
		t.Errorf("event = %q, want message.ack", ev.Event)
	}
	if ev.ID != "e4" {
		t.Errorf("id = %q, want e4", ev.ID)
	}
}

func TestHandleWSMessage_UnknownEventVersionZero(t *testing.T) {
	ts := newNativeTestServer(t)
	client := newWSClientForTest("ws-v0")
	ts.channel.addWSClient(client)

	// version 0 → treated as not set, accepts.
	ts.channel.handleWSMessage(client, WSMessage{Version: 0, Event: "typing", Data: mustMarshal(map[string]string{"session_key": "agent:main:main"})})
}// ---------------------------------------------------------------------------
// onebot.go — Stop / fetchSelfID / reconnectLoop
// ---------------------------------------------------------------------------

func TestOneBot_Stop(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:1"}, mb)

	// With pending requests.
	ch.pending["echo1"] = make(chan json.RawMessage, 1)
	ch.pendingMu = sync.Mutex{}

	_, cancel := context.WithCancel(context.Background())
	ch.cancel = cancel
	ch.setRunning(true)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after stop")
	}
	if _, ok := ch.pending["echo1"]; ok {
		t.Error("pending should be cleared")
	}
}

func TestOneBot_fetchSelfID_NoConn(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:1"}, mb)
	// sendAPIRequest returns an error when not connected → fetchSelfID returns.
	ch.fetchSelfID()
	if ch.selfID != 0 {
		t.Errorf("selfID = %d, want 0", ch.selfID)
	}
}

func TestOneBot_reconnectLoop_Cancels(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://localhost:1", ReconnectInterval: 3600}, mb)
	ctx, cancel := context.WithCancel(context.Background())
	ch.ctx = ctx
	ch.cancel = cancel

	done := make(chan struct{})
	go func() {
		ch.reconnectLoop()
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("reconnectLoop did not exit on cancel")
	}
}

// ---------------------------------------------------------------------------
// qq.go — Stop
// ---------------------------------------------------------------------------

func TestQQ_Stop(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewQQChannel(config.QQConfig{}, mb)
	ch.setRunning(true)
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop")
	}
}// ---------------------------------------------------------------------------
// onebot.go — Start success path with a live websocket server
// ---------------------------------------------------------------------------

func TestOneBot_Start_Success(t *testing.T) {
	var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Handle API requests (echo) responding to get_login_info.
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mt == websocket.TextMessage && len(msg) > 0 {
				var req struct {
					Action string `json:"action"`
					Echo   string `json:"echo"`
				}
				if json.Unmarshal(msg, &req) == nil && req.Echo != "" {
					resp := map[string]interface{}{
						"status": "ok",
						"echo":   req.Echo,
						"data": map[string]interface{}{
							"user_id":  12345,
							"nickname": "bot",
						},
					}
					_ = conn.WriteMessage(websocket.TextMessage, mustMarshal(resp))
				}
			}
		}
	}))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")

	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: wsURL, ReconnectInterval: 0}, mb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !ch.IsRunning() {
		t.Fatal("channel should be running")
	}
	if atomic.LoadInt64(&ch.selfID) != 12345 {
		t.Errorf("selfID = %d, want 12345", atomic.LoadInt64(&ch.selfID))
	}

	// Stop to clean up.
	_ = ch.Stop(ctx)
}

func TestOneBot_Start_EmptyURL(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewOneBotChannel(config.OneBotConfig{}, mb)
	if err := ch.Start(context.Background()); err == nil {
		t.Error("expected error for empty ws_url")
	}
}

func TestOneBot_Start_NoReconnectFail(t *testing.T) {
	mb := bus.NewMessageBus()
	// Unreachable URL and reconnect disabled → Start returns error.
	ch, _ := NewOneBotChannel(config.OneBotConfig{WSUrl: "ws://127.0.0.1:1", ReconnectInterval: 0}, mb)
	if err := ch.Start(context.Background()); err == nil {
		t.Error("expected error when connect fails and reconnect disabled")
	}
}// ---------------------------------------------------------------------------
// rest_agent.go — file read/save edge cases
// ---------------------------------------------------------------------------

func TestHandleAgentFile_ReadMissingFile(t *testing.T) {
	ts := newNativeTestServer(t)
	tmpDir := t.TempDir()
	ts.loop.workspace = tmpDir

	// Replace AGENT.md with a directory so ReadFile returns a non-IsNotExist
	// error → 500 read_error.
	if err := os.Remove(filepath.Join(tmpDir, "AGENT.md")); err != nil {
		// may not exist if InitializeWorkspace skipped mem; ignore
		_ = err
	}
	_ = os.MkdirAll(filepath.Join(tmpDir, "AGENT.md"), 0755)

	req := authenticatedRequest(t, ts, "/api/v1/agents/main/files/AGENT.md")
	req.SetPathValue("agentID", "main")
	req.SetPathValue("fileName", "AGENT.md")
	rec := httptest.NewRecorder()
	ts.channel.handleAgentFileRead(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("read dir status = %d, want 500", rec.Code)
	}
}

func TestHandleAgentFile_ReadMissingParams(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/agents//files/")
	ts.channel.handleAgentFileRead(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing params status = %d, want 400", rec.Code)
	}
}

func TestHandleAgentFile_SaveMissingParams(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/agents//files/")
	ts.channel.handleAgentFileSave(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing params status = %d, want 400", rec.Code)
	}
}

func TestHandleAgentFile_ReadUnknownAgent(t *testing.T) {
	ts := newNativeTestServer(t)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/agents/nope/files/x")
	req.SetPathValue("agentID", "nope")
	req.SetPathValue("fileName", "AGENT.md")
	ts.channel.handleAgentFileRead(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown agent status = %d, want 404", rec.Code)
	}
}