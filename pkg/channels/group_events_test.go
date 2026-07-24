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

// TestGroupEventsBridge verifies that group.status, group.turn, and
// group.complete bus events are bridged to WebSocket clients with the
// correct payload structure and field values.
func TestGroupEventsBridge(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Port = 0

	tmpDir := t.TempDir()
	cfg.Channels.Native.LeleDir = tmpDir

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	loop := newNativeTestAgentLoop(cfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(cfg, msgBus, loop, approvalMgr)
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := native.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer native.Stop(ctx)

	// Set up HTTP test server with WebSocket support.
	mux := http.NewServeMux()
	native.RegisterRoutes(mux)
	handler := native.corsMiddleware(native.securityHeadersMiddleware(mux))
	server := httptest.NewServer(handler)
	defer server.Close()

	// Authenticate and get token.
	pin, err := native.auth.GeneratePIN("GroupTestDevice")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}
	client, token, _, err := native.auth.PairWithPIN(pin.PIN, "GroupTestDevice")
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}

	// Connect via WebSocket.
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Authorization": []string{"Bearer " + token},
	})
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer wsConn.Close()

	// Consume the welcome event.
	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage(welcome) error = %v", err)
	}

	// Start a bus consumer that routes outbound messages to the native channel
	// (same pattern used by TestWebSocketRapidMessagesUnderLoad).
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, ok := msgBus.SubscribeOutbound(ctx)
				if !ok {
					continue
				}
				if msg.Channel == "native" {
					native.dispatchOutboundMessage(msg)
				}
			}
		}
	}()

	sessionKey := client.ClientID

	// Collect received WS events.
	type wsEvent struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	received := make(chan wsEvent, 50)
	go func() {
		for {
			wsConn.SetReadDeadline(time.Now().Add(10 * time.Second))
			_, raw, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			var msg wsEvent
			if json.Unmarshal(raw, &msg) == nil {
				received <- msg
			}
		}
	}()

	// Give the goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	// --- Test 1: group.status ---
	msgBus.PublishOutbound(bus.OutboundMessage{
		Channel:        "native",
		ChatID:         sessionKey,
		Event:          "group.status",
		IsIntermediate: true,
		Metadata: map[string]string{
			"group_id":     "grp-1",
			"status":       "started",
			"participants": "agent-a,agent-b",
		},
	})

	select {
	case evt := <-received:
		if evt.Event != "group.status" {
			t.Errorf("expected event group.status, got %q", evt.Event)
		}
		var payload WSGroupStatusPayload
		if err := json.Unmarshal(evt.Data, &payload); err != nil {
			t.Fatalf("unmarshal group.status payload: %v", err)
		}
		if payload.GroupID != "grp-1" {
			t.Errorf("group_id = %q, want %q", payload.GroupID, "grp-1")
		}
		if payload.Status != "started" {
			t.Errorf("status = %q, want %q", payload.Status, "started")
		}
		if payload.Participants != "agent-a,agent-b" {
			t.Errorf("participants = %q, want %q", payload.Participants, "agent-a,agent-b")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for group.status event")
	}

	// --- Test 2: group.turn ---
	msgBus.PublishOutbound(bus.OutboundMessage{
		Channel:        "native",
		ChatID:         sessionKey,
		Event:          "group.turn",
		Content:        "I propose we use approach X.",
		IsIntermediate: true,
		Metadata: map[string]string{
			"group_id":   "grp-1",
			"speaker":    "agent-a",
			"label":      "Agent Alpha",
			"role":       "proposer",
			"layer":      "0",
			"turn_index": "3",
		},
	})

	select {
	case evt := <-received:
		if evt.Event != "group.turn" {
			t.Errorf("expected event group.turn, got %q", evt.Event)
		}
		var payload WSGroupTurnPayload
		if err := json.Unmarshal(evt.Data, &payload); err != nil {
			t.Fatalf("unmarshal group.turn payload: %v", err)
		}
		if payload.GroupID != "grp-1" {
			t.Errorf("group_id = %q, want %q", payload.GroupID, "grp-1")
		}
		if payload.Speaker != "agent-a" {
			t.Errorf("speaker = %q, want %q", payload.Speaker, "agent-a")
		}
		if payload.Label != "Agent Alpha" {
			t.Errorf("label = %q, want %q", payload.Label, "Agent Alpha")
		}
		if payload.Role != "proposer" {
			t.Errorf("role = %q, want %q", payload.Role, "proposer")
		}
		if payload.Layer != 0 {
			t.Errorf("layer = %d, want %d", payload.Layer, 0)
		}
		if payload.TurnIndex != 3 {
			t.Errorf("turn_index = %d, want %d", payload.TurnIndex, 3)
		}
		if payload.Content != "I propose we use approach X." {
			t.Errorf("content = %q, want %q", payload.Content, "I propose we use approach X.")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for group.turn event")
	}

	// --- Test 3: group.complete ---
	msgBus.PublishOutbound(bus.OutboundMessage{
		Channel:        "native",
		ChatID:         sessionKey,
		Event:          "group.complete",
		Content:        "Final synthesis: approach X is best.",
		IsIntermediate: false,
		Metadata: map[string]string{
			"group_id":     "grp-1",
			"strategy":     "moa",
			"layers":       "3",
			"total_tokens": "1500",
		},
	})

	select {
	case evt := <-received:
		if evt.Event != "group.complete" {
			t.Errorf("expected event group.complete, got %q", evt.Event)
		}
		var payload WSGroupCompletePayload
		if err := json.Unmarshal(evt.Data, &payload); err != nil {
			t.Fatalf("unmarshal group.complete payload: %v", err)
		}
		if payload.GroupID != "grp-1" {
			t.Errorf("group_id = %q, want %q", payload.GroupID, "grp-1")
		}
		if payload.Strategy != "moa" {
			t.Errorf("strategy = %q, want %q", payload.Strategy, "moa")
		}
		if payload.Layers != 3 {
			t.Errorf("layers = %d, want %d", payload.Layers, 3)
		}
		if payload.TotalTokens != 1500 {
			t.Errorf("total_tokens = %d, want %d", payload.TotalTokens, 1500)
		}
		if payload.Content != "Final synthesis: approach X is best." {
			t.Errorf("content = %q, want %q", payload.Content, "Final synthesis: approach X is best.")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for group.complete event")
	}

	cancel()
	wg.Wait()
}
