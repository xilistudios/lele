package channels

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// GW-L9 regression, part 2: handleChatSendStream stays open for up to
// restStreamDeadline (5 min). With the server WriteTimeout of 30s still in
// effect, a stream whose chunks arrive past the deadline is killed mid-body.
// Same reproduction as the background-exec test: real http.Server with
// WriteTimeout=2s, chunks pushed at ~1s intervals for ~4s.

func TestSSEChatStreamSurvivesWriteTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.LeleDir = t.TempDir()

	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	loop := newNativeTestAgentLoop(cfg)
	native, err := NewNativeChannel(cfg, messageBus, loop, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewNativeChannel: %v", err)
	}

	pin, err := native.auth.GeneratePIN("WriteTimeoutTest")
	if err != nil {
		t.Fatalf("GeneratePIN: %v", err)
	}
	_, token, _, err := native.auth.PairWithPIN(pin.PIN, "WriteTimeoutTest")
	if err != nil {
		t.Fatalf("PairWithPIN: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/chat/send/stream", native.authMiddleware(http.HandlerFunc(native.handleChatSendStream)).ServeHTTP)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux, WriteTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second}
	go srv.Serve(ln)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer reqCancel()
	body, _ := json.Marshal(map[string]string{"content": "hola", "session_key": "wt-test-session"})
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, "http://"+ln.Addr().String()+"/api/v1/chat/send/stream", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()

	// The handler writes the ack immediately; the subscriber is registered
	// before that. Once we see the ack we can push events for this session.
	scanner := bufio.NewScanner(resp.Body)
	if !scanner.Scan() || !strings.HasPrefix(scanner.Text(), "event: message.ack") {
		t.Fatalf("expected ack as first event, got: %q", scanner.Text())
	}

	// Pump: one chunk per second for ~4s (crossing the 2s WriteTimeout),
	// then the terminal pair the handler waits for.
	go func() {
		time.Sleep(300 * time.Millisecond)
		for i := 0; i < 4; i++ {
			native.emitNativeEvent("wt-test-session", "message.stream", WSStreamPayload{
				SessionKey: "wt-test-session",
				Chunk:      "chunk",
			}, "")
			time.Sleep(time.Second)
		}
		native.emitNativeEvent("wt-test-session", "message.complete", map[string]interface{}{"session_key": "wt-test-session"}, "")
		native.emitNativeEvent("wt-test-session", "history.updated", map[string]interface{}{"session_key": "wt-test-session"}, "")
	}()

	var sawComplete, sawHistory bool
	eventsDone := make(chan struct{})
	go func() {
		defer close(eventsDone)
		for scanner.Scan() {
			line := scanner.Text()
			t.Logf("SSE< %s", line)
			if strings.HasPrefix(line, "event: ") {
				switch strings.TrimPrefix(line, "event: ") {
				case "message.complete":
					sawComplete = true
					return
				case "error":
					return
				}
			}
		}
	}()

	select {
	case <-eventsDone:
	case <-time.After(12 * time.Second):
	}
	if err := scanner.Err(); err != nil {
		t.Logf("scanner error: %v", err)
	}
	if !sawComplete {
		_ = sawHistory
		t.Fatal("chat SSE stream was killed before message.complete (WriteTimeout not cleared — GW-L9)")
	}
}
