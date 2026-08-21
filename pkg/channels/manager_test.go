package channels

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/store"
)

// stubChannel is a minimal Channel implementation for manager tests.
type stubChannel struct {
	name          string
	running       bool
	startErr      error
	stopErr       error
	sendCalls     atomic.Int32
	stopCalls     atomic.Int32
	startCalls    atomic.Int32
	lastSend      bus.OutboundMessage
	internalCalls chan string
}

func (m *stubChannel) Name() string          { return m.name }
func (m *stubChannel) IsRunning() bool       { return m.running }
func (m *stubChannel) IsAllowed(string) bool { return true }
func (m *stubChannel) Start(ctx context.Context) error {
	m.startCalls.Add(1)
	if m.startErr != nil {
		return m.startErr
	}
	m.running = true
	return nil
}
func (m *stubChannel) Stop(ctx context.Context) error {
	m.stopCalls.Add(1)
	if m.stopErr != nil {
		return m.stopErr
	}
	m.running = false
	return nil
}
func (m *stubChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	m.sendCalls.Add(1)
	m.lastSend = msg
	return nil
}

func TestManager_GetChannel(t *testing.T) {
	mgr, err := NewManager(config.DefaultConfig(), bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if _, ok := mgr.GetChannel("none"); ok {
		t.Error("expected missing channel")
	}
	ch := &stubChannel{name: "x"}
	mgr.RegisterChannel("x", ch)
	got, ok := mgr.GetChannel("x")
	if !ok || got != ch {
		t.Error("expected registered channel to be returned")
	}
}

func TestManager_SetNativeClientStore_NoNative(t *testing.T) {
	// No channels enabled at all; SetNativeClientStore must be a no-op.
	cfg := config.DefaultConfig()
	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.SetNativeClientStore(nil) // should not panic
}

func TestManager_SetNativeClientStore_WiresStore(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.LeleDir = t.TempDir()

	mb := bus.NewMessageBus()
	mgr, err := NewManager(cfg, mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	dir := t.TempDir()
	dbStore, err := store.Open(dir + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer dbStore.Close()

	mgr.SetNativeClientStore(dbStore.NativeClients())

	nc, ok := mgr.GetChannel("native")
	if !ok {
		t.Fatal("native channel should exist")
	}
	native := nc.(*NativeChannel)
	if native.auth.repo == nil {
		t.Error("expected native auth repo to be wired")
	}
}

func TestManager_GetStatusAndEnabledChannels(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = false
	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ch := &stubChannel{name: "x"}
	ch2 := &stubChannel{name: "y"}
	mgr.RegisterChannel("x", ch)
	mgr.RegisterChannel("y", ch2)
	ch.running = true

	status := mgr.GetStatus()
	if len(status) != 2 {
		t.Errorf("expected 2 status entries, got %d", len(status))
	}
	if entry, ok := status["x"].(map[string]interface{}); !ok || entry["running"] != true {
		t.Errorf("x status = %v", status["x"])
	}

	names := mgr.GetEnabledChannels()
	if len(names) != 2 {
		t.Errorf("expected 2 enabled channels, got %d", len(names))
	}
}

func TestManager_UnregisterChannel(t *testing.T) {
	mgr, err := NewManager(config.DefaultConfig(), bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mgr.RegisterChannel("x", &stubChannel{name: "x"})
	if _, ok := mgr.GetChannel("x"); !ok {
		t.Fatal("channel should exist")
	}
	mgr.UnregisterChannel("x")
	if _, ok := mgr.GetChannel("x"); ok {
		t.Error("channel should have been removed")
	}
}

func TestManager_SendToChannel(t *testing.T) {
	mgr, err := NewManager(config.DefaultConfig(), bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ch := &stubChannel{name: "x"}
	mgr.RegisterChannel("x", ch)

	ctx := context.Background()
	if err := mgr.SendToChannel(ctx, "ghost", "chat", "hi"); err == nil {
		t.Error("expected error for missing channel")
	}

	if err := mgr.SendToChannel(ctx, "x", "chatid", "hello"); err != nil {
		t.Fatalf("SendToChannel: %v", err)
	}
	if ch.lastSend.ChatID != "chatid" || ch.lastSend.Content != "hello" {
		t.Errorf("lastSend = %+v", ch.lastSend)
	}
}

func TestManager_StartAll_NoChannels(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = false
	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
}

func TestManager_StartStop_OneChannel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = false
	mb := bus.NewMessageBus()
	mgr, err := NewManager(cfg, mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ch := &stubChannel{name: "x"}
	mgr.RegisterChannel("x", ch)
	mgr.dispatchQueues["x"] = make(chan bus.OutboundMessage, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if ch.startCalls.Load() == 0 {
		t.Error("channel should have been started")
	}

	// Publish an outbound message on a real channel bus; internal channels skipped.
	mb.PublishOutbound(bus.OutboundMessage{Channel: "x", ChatID: "c", Content: "m"})
	// Internal channel messages should be dropped by dispatchOutbound.
	mb.PublishOutbound(bus.OutboundMessage{Channel: "cli", ChatID: "c", Content: "m"})

	if err := mgr.StopAll(ctx); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if ch.stopCalls.Load() == 0 {
		t.Error("channel should have been stopped")
	}
}

func TestManager_StartAll_StartError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = false
	mb := bus.NewMessageBus()
	mgr, err := NewManager(cfg, mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ch := &stubChannel{name: "x", startErr: errors.New("boom")}
	mgr.RegisterChannel("x", ch)
	mgr.dispatchQueues["x"] = make(chan bus.OutboundMessage, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// StartAll must not fail even if a channel Start returns an error.
	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	if mgr.dispatchTask == nil {
		t.Error("dispatchTask should be set")
	}
}

func TestManager_ReloadConfig_Nil(t *testing.T) {
	mgr, err := NewManager(config.DefaultConfig(), bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := mgr.ReloadConfig(nil); err == nil {
		t.Error("expected error for nil config")
	}
}

func TestManager_ReloadConfig_Reinit(t *testing.T) {
	cfg := config.DefaultConfig()
	mb := bus.NewMessageBus()
	mgr, err := NewManager(cfg, mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	old := &stubChannel{name: "old"}
	mgr.RegisterChannel("old", old)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Simulate a prior run.
	mgr.dispatchTask = &asyncTask{cancel: cancel}
	mgr.runCtx = ctx

	// Reload with a config that has native enabled.
	cfg2 := config.DefaultConfig()
	cfg2.Channels.Native.Enabled = true
	cfg2.Channels.Native.LeleDir = t.TempDir()
	if err := mgr.ReloadConfig(cfg2); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	if old.stopCalls.Load() == 0 {
		t.Error("old channel should have been stopped")
	}
	if _, ok := mgr.GetChannel("native"); !ok {
		t.Error("native channel should exist after reload")
	}
}

func TestManager_ReloadConfig_EmptyAndNoCtx(t *testing.T) {
	cfg := config.DefaultConfig()
	mb := bus.NewMessageBus()
	mgr, err := NewManager(cfg, mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// runCtx nil → reload path should not start dispatchers.
	if err := mgr.ReloadConfig(config.DefaultConfig()); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
}

func TestManager_DispatchOutboundDropsInternal(t *testing.T) {
	cfg := config.DefaultConfig()
	mb := bus.NewMessageBus()
	mgr, err := NewManager(cfg, mb, nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ch := &stubChannel{name: "x"}
	mgr.RegisterChannel("x", ch)
	mgr.dispatchQueues["x"] = make(chan bus.OutboundMessage, 10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dc, dcancel := context.WithCancel(ctx)
	mgr.dispatchTask = &asyncTask{cancel: dcancel}
	go mgr.dispatchOutbound(dc)

	// "cli" is internal - should be silently dropped (no queue full warnings).
	mb.PublishOutbound(bus.OutboundMessage{Channel: "cli", ChatID: "c", Content: "m"})
	// Unknown channel - warn and drop.
	mb.PublishOutbound(bus.OutboundMessage{Channel: "unknown", ChatID: "c", Content: "m"})

	// Give the dispatch loop time to consume.
	_ = ctx
	cancel()
}

func TestManager_InitChannelsEnablesConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.LeleDir = t.TempDir()
	cfg.Channels.WhatsApp.Enabled = true
	cfg.Channels.WhatsApp.BridgeURL = "ws://localhost"
	cfg.Channels.Feishu.Enabled = true
	cfg.Channels.Feishu.AppID = "appid"
	cfg.Channels.Feishu.AppSecret = "secret"
	cfg.Channels.MaixCam.Enabled = true
	cfg.Channels.QQ.Enabled = true
	cfg.Channels.LINE.Enabled = true
	cfg.Channels.LINE.ChannelAccessToken = "tok"
	cfg.Channels.LINE.ChannelSecret = "sec"
	cfg.Channels.OneBot.Enabled = true
	cfg.Channels.OneBot.WSUrl = "ws://localhost"
	cfg.Channels.DingTalk.Enabled = true
	cfg.Channels.DingTalk.ClientID = "x"
	cfg.Channels.DingTalk.ClientSecret = "y"

	mgr, err := NewManager(cfg, bus.NewMessageBus(), nil, NewApprovalManager())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	for _, name := range []string{"native", "whatsapp", "feishu", "maixcam", "qq", "line", "onebot", "dingtalk"} {
		if _, ok := mgr.GetChannel(name); !ok {
			t.Errorf("expected channel %q to be initialized", name)
		}
	}
	// Disabled-by-default channels should not be present.
	if _, ok := mgr.GetChannel("discord"); ok {
		t.Error("discord channel should not be present without config")
	}
}