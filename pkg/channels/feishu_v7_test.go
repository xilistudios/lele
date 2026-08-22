//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package channels

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestFeishu_Stop_Idempotent(t *testing.T) {
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop on fresh channel: %v", err)
	}
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop")
	}
}

func TestFeishu_Start_RequiresAppID(t *testing.T) {
	// Exercise the missing-app_id branch (existing test only covers app_secret).
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "", AppSecret: "sec"}, mb)
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("expected error when app_id is empty")
	}
}

func TestFeishu_Start_SuccessSetsRunning(t *testing.T) {
	// Start() with valid credentials returns synchronously and sets running.
	// We pass an already-cancelled context so the background goroutine's
	// websocket endpoint request fails immediately (cancelled ctx) instead of
	// attempting a real network call, keeping the test fast and offline.
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the ws goroutine never dials the network
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer ch.Stop(context.Background())

	if !ch.IsRunning() {
		t.Error("channel should be running after Start")
	}
	if ch.wsClient == nil {
		t.Error("wsClient should be set after Start")
	}

	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if ch.IsRunning() {
		t.Error("should not be running after Stop")
	}
	if ch.wsClient != nil {
		t.Error("wsClient should be cleared after Stop")
	}
}

func TestFeishu_Send_StopClearsRunning(t *testing.T) {
	// Verify that Stop clears IsRunning so that a subsequent Send fails fast.
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)
	ch.setRunning(true)
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "oc_1", Content: "hi"}); err == nil {
		t.Fatal("expected not-running error after Stop")
	}
}

func TestFeishu_Send_EmptyChatID(t *testing.T) {
	// Running channel with empty chat ID → fast error before network.
	mb := bus.NewMessageBus()
	ch, _ := NewFeishuChannel(config.FeishuConfig{AppID: "app", AppSecret: "sec"}, mb)
	ch.setRunning(true)
	err := ch.Send(context.Background(), bus.OutboundMessage{Content: "hi"})
	if err == nil {
		t.Fatal("expected error for empty chat ID")
	}
}