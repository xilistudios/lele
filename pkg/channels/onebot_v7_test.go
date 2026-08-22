package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// startOneBotWSEchoServer starts a websocket server that the onebot channel
// dials. It captures what the client sends and can push scripted messages back.
// Returns server, a channel build for wsURL, and a channel capturing sent
// payloads.
func startOneBotWSServer(t *testing.T, handler func(conn *websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if handler != nil {
			handler(conn)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestOneBot(wsURL string, cfg config.OneBotConfig) *OneBotChannel {
	cfg.WSUrl = wsURL
	ch, _ := NewOneBotChannel(cfg, bus.NewMessageBus())
	return ch
}

func TestOneBot_Send_SuccessWithEcho(t *testing.T) {
	var mu sync.Mutex
	var sentEcho string
	var sentPayload []byte

	// The server reads the initial "get_login_info" fetchSelfID request, then
	// reads the Send payload and replies with a matching echo.
	srv := startOneBotWSServer(t, func(conn *websocket.Conn) {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req oneBotAPIRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			mu.Lock()
			sentPayload = append(sentPayload[:0], msg...)
			sentEcho = req.Echo
			mu.Unlock()
			// Echo back the API response for the request.
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"ok","echo":"`+req.Echo+`"}`))
		}
	})

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ch := newTestOneBot(wsURL, config.OneBotConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	defer ch.cancel()
	if err := ch.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	ch.setRunning(true)

	// Disable emoji reactions so Send just posts.
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "private:123", Content: "hello"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Wait for the server goroutine to record the payload.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := sentEcho
		pl := len(sentPayload)
		mu.Unlock()
		if got != "" && pl > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Errorf("send request never reached the server (echo=%q payload=%d bytes)", sentEcho, len(sentPayload))
}

func TestOneBot_connect_PingHandlerSet(t *testing.T) {
	// Connecting should succeed and set up pinger + pong handler without panic.
	srv := startOneBotWSServer(t, func(conn *websocket.Conn) {
		// Eat ping frames and reply pong so read deadlines don't kill us.
		for {
			mtype, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if mtype == websocket.PingMessage {
				_ = conn.WriteMessage(websocket.PongMessage, nil)
			}
		}
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ch := newTestOneBot(wsURL, config.OneBotConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	defer ch.cancel()
	if err := ch.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

func TestOneBot_Start_ReconnectDisabledConnectFailure(t *testing.T) {
	// Reconnect disabled + unreachable URL → Start returns error.
	ch := newTestOneBot("ws://127.0.0.1:1", config.OneBotConfig{})
	if err := ch.Start(context.Background()); err == nil {
		t.Error("expected error when reconnect disabled and connect fails")
	}
}

func TestOneBot_Send_MarshalPendingEmoji(t *testing.T) {
	// Send with a group chat that has a stored message id triggers setMsgEmojiLike.
	var mu sync.Mutex
	srv := startOneBotWSServer(t, func(conn *websocket.Conn) {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req oneBotAPIRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}
			mu.Lock()
			_ = req
			mu.Unlock()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"ok","echo":"`+req.Echo+`"}`))
		}
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ch := newTestOneBot(wsURL, config.OneBotConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	defer ch.cancel()
	if err := ch.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	ch.setRunning(true)
	ch.pendingEmojiMsg.Store("group:999", "5000")
	if err := ch.Send(ctx, bus.OutboundMessage{ChatID: "group:999", Content: "hi"}); err != nil {
		t.Fatalf("Send with emoji: %v", err)
	}
}

func TestOneBot_sendAPIRequest_Timeout(t *testing.T) {
	// Server accepts the connection but never replies to API requests.
	srv := startOneBotWSServer(t, func(conn *websocket.Conn) {
		// Just drain without responding.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ch := newTestOneBot(wsURL, config.OneBotConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	defer ch.cancel()
	if err := ch.connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, err := ch.sendAPIRequest("get_login_info", nil, 300*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want timeout", err)
	}
}

func TestOneBot_sendAPIRequest_NotConnectedV7(t *testing.T) {
	ch := newTestOneBot("ws://localhost:1", config.OneBotConfig{})
	_, err := ch.sendAPIRequest("get_login_info", nil, time.Second)
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestOneBot_buildMessageSegments_WithLastMessageID(t *testing.T) {
	ch := newTestOneBot("ws://localhost:1", config.OneBotConfig{})
	ch.lastMessageID.Store("private:1", "reply-msg-id")
	segs := ch.buildMessageSegments("private:1", "hi")
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[0].Type != "reply" || segs[0].Data["id"] != "reply-msg-id" {
		t.Errorf("reply segment = %+v", segs[0])
	}
	segs2 := ch.buildMessageSegments("private:2", "hi")
	if len(segs2) != 1 {
		t.Errorf("no-last-id segments = %d", len(segs2))
	}
}
