package channels

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestMaixCam_NewChannel(t *testing.T) {
	ch, err := NewMaixCamChannel(config.MaixCamConfig{}, bus.NewMessageBus())
	if err != nil {
		t.Fatal(err)
	}
	if ch.Name() != "maixcam" {
		t.Errorf("name = %q", ch.Name())
	}
	if ch.clients == nil {
		t.Error("clients map should be initialized")
	}
}

func TestMaixCam_StopWithoutStart(t *testing.T) {
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, bus.NewMessageBus())
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running")
	}
}

func TestMaixCam_Send_NotRunning(t *testing.T) {
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, bus.NewMessageBus())
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "1", Content: "hi"}); err == nil {
		t.Fatal("expected not-running error")
	}
}

func TestMaixCam_Send_NoClients(t *testing.T) {
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, bus.NewMessageBus())
	ch.setRunning(true)
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "1", Content: "hi"}); err == nil {
		t.Fatal("expected no-device warning (len(clients)==0 path)")
	}
}

func TestMaixCam_Send_WithClient(t *testing.T) {
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, bus.NewMessageBus())
	ch.setRunning(true)

	// Use net.Pipe to simulate a connected peer; write to peer's write side,
	// read from the reader side.
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()

	ch.clientsMux.Lock()
	ch.clients[server] = true
	ch.clientsMux.Unlock()

	// Start reader goroutine on the peer end so the pipe doesn't block.
	go func() {
		buf := make([]byte, 4096)
		_, _ = peer.Read(buf)
	}()

	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "1", Content: "hi"}); err != nil {
		t.Fatalf("Send with client: %v", err)
	}
}

func TestMaixCam_ProcessMessage_UnknownType(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, mb)
	ch.processMessage(MaixCamMessage{Type: "weird"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if inb, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound: %+v", inb)
	}
}

func TestMaixCam_ProcessMessage_StatusControl(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, mb)
	ch.processMessage(MaixCamMessage{Type: "status", Data: map[string]interface{}{"temp": "40"}}, nil)
	ch.processMessage(MaixCamMessage{Type: "heartbeat"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if inb, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatalf("unexpected inbound: %+v", inb)
	}
}

func TestMaixCam_HandlePersonDetection(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, mb)

	ch.handlePersonDetection(MaixCamMessage{
		Type:      "person_detected",
		Timestamp: 1234,
		Data: map[string]interface{}{
			"class_name": "person",
			"score":      0.95,
			"x":          10.0,
			"y":          20.0,
			"w":          30.0,
			"h":          40.0,
			"class_id":   float64(1),
		},
	})

	msg, ok := waitOneInbound(t, mb)
	if !ok {
		t.Fatal("expected inbound for person detection")
	}
	if msg.ChatID != "default" {
		t.Errorf("chatID = %q", msg.ChatID)
	}
	if msg.Content == "" {
		t.Error("expected non-empty content")
	}
}

func TestMaixCam_HandlePersonDetection_Defaults(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewMaixCamChannel(config.MaixCamConfig{}, mb)

	// Missing class_name should default to "person", missing fields to 0.
	ch.handlePersonDetection(MaixCamMessage{Type: "person_detected", Data: map[string]interface{}{}})

	msg, ok := waitOneInbound(t, mb)
	if !ok {
		t.Fatal("expected inbound")
	}
	if msg.Content == "" {
		t.Error("expected content")
	}
}

func waitOneInbound(t *testing.T, mb *bus.MessageBus) (bus.InboundMessage, bool) {
	t.Helper()
	return receiveInbound(mb, time.Second)
}

func receiveInbound(mb *bus.MessageBus, timeout time.Duration) (bus.InboundMessage, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return mb.ConsumeInbound(ctx)
}