package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTestWhatsApp(t *testing.T) (*WhatsAppChannel, *bus.MessageBus) {
	t.Helper()
	msgBus := bus.NewMessageBus()
	cfg := config.WhatsAppConfig{
		BridgeURL: "ws://localhost:1",
		AllowFrom: []string{},
	}
	ch, err := NewWhatsAppChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewWhatsAppChannel: %v", err)
	}
	return ch, msgBus
}

func TestWhatsAppNewChannel(t *testing.T) {
	msgBus := bus.NewMessageBus()
	cfg := config.WhatsAppConfig{BridgeURL: "ws://127.0.0.1:19999"}
	ch, err := NewWhatsAppChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewWhatsAppChannel: %v", err)
	}
	if ch.Name() != "whatsapp" {
		t.Errorf("Name() = %q", ch.Name())
	}
	if ch.url != "ws://127.0.0.1:19999" {
		t.Errorf("url = %q", ch.url)
	}
	if !ch.IsAllowed("anyone") {
		t.Error("empty allowlist should allow all")
	}
	if ch.connected {
		t.Error("should not be connected initially")
	}
}

func TestWhatsAppStart_ConnectionFailure(t *testing.T) {
	msgBus := bus.NewMessageBus()
	cfg := config.WhatsAppConfig{BridgeURL: "ws://127.0.0.1:1"}
	ch, err := NewWhatsAppChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewWhatsAppChannel: %v", err)
	}
	// Connection to an unroutable port should fail quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = ch.Start(ctx)
	if err == nil {
		t.Fatal("expected error connecting to invalid bridge")
	}
}

func TestWhatsAppSend_NoConnection(t *testing.T) {
	ch, _ := newTestWhatsApp(t)
	ctx := context.Background()
	err := ch.Send(ctx, bus.OutboundMessage{ChatID: "123", Content: "hi"})
	if err == nil {
		t.Fatal("expected error when no connection established")
	}
}

func TestWhatsAppStop_NoConnection(t *testing.T) {
	ch, _ := newTestWhatsApp(t)
	ctx := context.Background()
	// Stop on a channel that was never started should not panic.
	if err := ch.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.connected {
		t.Error("should not be connected after Stop")
	}
}

func TestWhatsAppAttachmentName(t *testing.T) {
	tests := []struct {
		name string
		att  bus.FileAttachment
		want string
	}{
		{name: "with name", att: bus.FileAttachment{Name: "doc.pdf", Path: "/tmp/doc.pdf"}, want: "doc.pdf"},
		{name: "only path", att: bus.FileAttachment{Path: "/tmp/report.txt"}, want: "report.txt"},
		{name: "empty", att: bus.FileAttachment{}, want: "attachment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := whatsappAttachmentName(tt.att); got != tt.want {
				t.Errorf("whatsappAttachmentName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWhatsAppHandleIncomingMessage(t *testing.T) {
	ch, msgBus := newTestWhatsApp(t)
	ctx := context.Background()

	t.Run("media array paths", func(t *testing.T) {
		msg := map[string]interface{}{
			"from":    "12345",
			"chat":    "12345",
			"content": "hello",
			"id":      "msg-1",
			"media":   []interface{}{"/tmp/a.mp3", "/tmp/b.mp3"},
		}
		ch.handleIncomingMessage(msg)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Content != "hello" {
			t.Errorf("content = %q", inbound.Content)
		}
		if inbound.ChatID != "12345" {
			t.Errorf("chatID = %q", inbound.ChatID)
		}
		if inbound.Metadata["message_id"] != "msg-1" {
			t.Errorf("message_id = %q", inbound.Metadata["message_id"])
		}
		if len(inbound.Media) != 2 {
			t.Errorf("expected 2 media paths, got %d", len(inbound.Media))
		}
		if inbound.Metadata["peer_kind"] != "direct" {
			t.Errorf("peer_kind = %q", inbound.Metadata["peer_kind"])
		}
	})

	t.Run("attachments map", func(t *testing.T) {
		msg := map[string]interface{}{
			"from":    "sender1",
			"content": "with attach",
			"attachments": []interface{}{
				map[string]interface{}{
					"path":      "/tmp/x.pdf",
					"name":      "x.pdf",
					"mime_type": "application/pdf",
					"kind":      "document",
					"caption":   "cap",
				},
			},
		}
		ch.handleIncomingMessage(msg)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if len(inbound.Attachments) != 1 {
			t.Fatalf("expected 1 attachment, got %d", len(inbound.Attachments))
		}
		att := inbound.Attachments[0]
		if att.Name != "x.pdf" || att.Caption != "cap" || att.MIMEType != "application/pdf" {
			t.Errorf("attachment = %+v", att)
		}
		// chatID defaults to sender ID when missing.
		if inbound.ChatID != "sender1" {
			t.Errorf("chatID = %q", inbound.ChatID)
		}
	})

	t.Run("group message sets group peer kind", func(t *testing.T) {
		msg := map[string]interface{}{
			"from":    "sender1",
			"chat":    "group-99",
			"content": "group msg",
		}
		ch.handleIncomingMessage(msg)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Metadata["peer_kind"] != "group" {
			t.Errorf("peer_kind = %q", inbound.Metadata["peer_kind"])
		}
		if inbound.Metadata["peer_id"] != "group-99" {
			t.Errorf("peer_id = %q", inbound.Metadata["peer_id"])
		}
	})

	t.Run("missing from is ignored", func(t *testing.T) {
		msg := map[string]interface{}{
			"chat":    "123",
			"content": "no sender",
		}
		ch.handleIncomingMessage(msg)
		consumeCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if msg, ok := msgBus.ConsumeInbound(consumeCtx); ok {
			t.Fatalf("should not publish without sender, got %+v", msg)
		}
	})

	t.Run("from_name metadata", func(t *testing.T) {
		msg := map[string]interface{}{
			"from":      "123",
			"content":   "hi",
			"from_name": "Alice",
		}
		ch.handleIncomingMessage(msg)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Metadata["user_name"] != "Alice" {
			t.Errorf("user_name = %q", inbound.Metadata["user_name"])
		}
	})

	t.Run("non-string content handled", func(t *testing.T) {
		msg := map[string]interface{}{
			"from":    "123",
			"content": 42,
		}
		ch.handleIncomingMessage(msg)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if inbound.Content != "" {
			t.Errorf("content = %q, want empty", inbound.Content)
		}
	})

	t.Run("attachments edge cases", func(t *testing.T) {
		// A non-map item (string) in the attachments array is skipped; a map
		// with an empty path is also skipped.
		msg := map[string]interface{}{
			"from":    "edge",
			"content": "edge cases",
			"attachments": []interface{}{
				"not-a-map",
				map[string]interface{}{"name": "empty-path.pdf"},
				map[string]interface{}{"path": "/ok.pdf", "name": "ok.pdf", "kind": "file"},
			},
		}
		ch.handleIncomingMessage(msg)
		inbound, ok := msgBus.ConsumeInbound(ctx)
		if !ok {
			t.Fatal("no message published")
		}
		if len(inbound.Attachments) != 1 {
			t.Fatalf("expected 1 valid attachment, got %d", len(inbound.Attachments))
		}
		if inbound.Attachments[0].Path != "/ok.pdf" {
			t.Errorf("attachment path = %q", inbound.Attachments[0].Path)
		}
	})
}

func TestWhatsAppSend_WithMockServer(t *testing.T) {
	// Start a websocket server that acts as the WhatsApp bridge, capturing
	// the message payload sent by the channel.
	server, endpoint, received := newWhatsAppTestBridge(t)
	defer server.Close()

	msgBus := bus.NewMessageBus()
	cfg := config.WhatsAppConfig{BridgeURL: endpoint}
	ch, err := NewWhatsAppChannel(cfg, msgBus)
	if err != nil {
		t.Fatalf("NewWhatsAppChannel: %v", err)
	}

	ctx := context.Background()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop(ctx)

	if !ch.connected {
		t.Error("channel should be connected")
	}

	att := bus.FileAttachment{Name: "file.pdf", Path: "/tmp/file.pdf", MIMEType: "application/pdf", Kind: "document"}
	err = ch.Send(ctx, bus.OutboundMessage{
		ChatID:      "chat-1",
		Content:     "hello with file",
		Attachments: []bus.FileAttachment{att},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	payload := <-received
	if payload["to"] != "chat-1" {
		t.Errorf("to = %v", payload["to"])
	}
	if payload["content"] != "hello with file" {
		t.Errorf("content = %v", payload["content"])
	}
	atts, ok := payload["attachments"].([]interface{})
	if !ok {
		t.Fatalf("attachments missing: %+v", payload)
	}
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	attMap := atts[0].(map[string]interface{})
	if attMap["name"] != "file.pdf" {
		t.Errorf("attachment name = %v", attMap["name"])
	}
}

// newWhatsAppTestBridge starts an httptest websocket server that emulates the
// WhatsApp bridge, capturing incoming JSON payloads and returning them on the
// provided channel.
func newWhatsAppTestBridge(t *testing.T) (*httptest.Server, string, chan map[string]interface{}) {
	t.Helper()
	received := make(chan map[string]interface{}, 10)
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var payload map[string]interface{}
			if json.Unmarshal(msg, &payload) == nil {
				received <- payload
			}
		}
	}))
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	return server, endpoint, received
}