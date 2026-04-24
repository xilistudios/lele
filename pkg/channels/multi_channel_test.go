package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

type mockChannel struct {
	name    string
	bus     *bus.MessageBus
	running bool
	sent    []bus.OutboundMessage
	sentMu  sync.Mutex
	started atomic.Bool
	sendFn  func(ctx context.Context, msg bus.OutboundMessage) error
}

type trackingMockChannel struct {
	*mockChannel
	sentCount atomic.Int32
}

func (t *trackingMockChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	t.sentCount.Add(1)
	return t.mockChannel.Send(ctx, msg)
}

func newMockChannel(name string, messageBus *bus.MessageBus) *mockChannel {
	return &mockChannel{
		name: name,
		bus:  messageBus,
	}
}

func (m *mockChannel) Name() string          { return m.name }
func (m *mockChannel) IsRunning() bool       { return m.running }
func (m *mockChannel) IsAllowed(string) bool { return true }

func (m *mockChannel) Start(ctx context.Context) error {
	m.running = true
	m.started.Store(true)
	return nil
}

func (m *mockChannel) Stop(ctx context.Context) error {
	m.running = false
	return nil
}

func (m *mockChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if m.sendFn != nil {
		return m.sendFn(ctx, msg)
	}
	m.sentMu.Lock()
	defer m.sentMu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

func (m *mockChannel) getSent() []bus.OutboundMessage {
	m.sentMu.Lock()
	defer m.sentMu.Unlock()
	result := make([]bus.OutboundMessage, len(m.sent))
	copy(result, m.sent)
	return result
}

func TestDispatchOutboundDoesNotBlockOnSlowChannel(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	fastCh := newMockChannel("fast", messageBus)
	slowCh := newMockChannel("slow", messageBus)

	cfg := config.DefaultConfig()
	approvalMgr := NewApprovalManager()
	mgr, err := NewManager(cfg, messageBus, nil, approvalMgr)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	mgr.RegisterChannel("fast", fastCh)
	mgr.dispatchQueues["fast"] = make(chan bus.OutboundMessage, 200)
	mgr.RegisterChannel("slow", slowCh)
	mgr.dispatchQueues["slow"] = make(chan bus.OutboundMessage, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatchCtx, dispatchCancel := context.WithCancel(ctx)
	mgr.dispatchTask = &asyncTask{cancel: dispatchCancel}
	go mgr.dispatchOutbound(dispatchCtx)

	go mgr.startChannelDispatcher(dispatchCtx, "fast", fastCh, mgr.dispatchQueues["fast"])
	go mgr.startChannelDispatcher(dispatchCtx, "slow", slowCh, mgr.dispatchQueues["slow"])

	fastCh.Start(ctx)
	slowCh.Start(ctx)

	for i := 0; i < 300; i++ {
		messageBus.PublishOutbound(bus.OutboundMessage{
			Channel: "fast",
			ChatID:  "chat1",
			Content: fmt.Sprintf("fast-msg-%d", i),
		})
	}

	for i := 0; i < 300; i++ {
		messageBus.PublishOutbound(bus.OutboundMessage{
			Channel: "slow",
			ChatID:  "chat2",
			Content: fmt.Sprintf("slow-msg-%d", i),
		})
	}

	time.Sleep(500 * time.Millisecond)

	_, _, _, _, _, droppedOut := messageBus.Stats()
	_ = droppedOut

	fastSent := fastCh.getSent()
	if len(fastSent) < 100 {
		t.Errorf("expected fast channel to receive many messages, got %d", len(fastSent))
	}
}

func TestConcurrentInboundFromMultipleChannels(t *testing.T) {
	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	var received atomic.Int32
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		for {
			msg, ok := messageBus.ConsumeInbound(ctx)
			if !ok {
				return
			}
			received.Add(1)
			messageBus.PublishOutbound(bus.OutboundMessage{
				Channel: msg.Channel,
				ChatID:  msg.ChatID,
				Content: "response: " + msg.Content,
			})
		}
	}()

	var wg sync.WaitGroup
	channels := []string{"telegram", "native", "discord", "whatsapp"}
	for _, ch := range channels {
		wg.Add(1)
		go func(channel string) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				messageBus.PublishInbound(bus.InboundMessage{
					Channel:  channel,
					SenderID: "user1",
					ChatID:   "chat1",
					Content:  fmt.Sprintf("msg-%d", i),
				})
			}
		}(ch)
	}
	wg.Wait()

	time.Sleep(200 * time.Millisecond)

	count := received.Load()
	if count < 300 {
		t.Errorf("expected at least 300 messages processed, got %d", count)
	}
}

func TestNativeStreamingDoesNotBlockOtherChannels(t *testing.T) {
	nativeCfg := config.DefaultConfig()
	nativeCfg.Channels.Native.Enabled = true
	nativeCfg.Channels.Native.Port = 0

	tmpDir := t.TempDir()
	nativeCfg.Channels.Native.LeleDir = tmpDir

	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	agentLoop := newNativeTestAgentLoop(nativeCfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(nativeCfg, messageBus, agentLoop, approvalMgr)
	if err != nil {
		t.Fatalf("failed to create native channel: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := native.Start(ctx); err != nil {
		t.Fatalf("failed to start native channel: %v", err)
	}
	defer func() {
		cancel()
		native.Stop(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Create a mock channel and override send via sendFn
	telegramCh := &mockChannel{name: "telegram"}
	telegramCh.running = true
	var telegramSent atomic.Int32
	telegramCh.sendFn = func(ctx context.Context, msg bus.OutboundMessage) error {
		telegramSent.Add(1)
		return nil
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			messageBus.PublishOutbound(bus.OutboundMessage{
				Channel:   "native",
				ChatID:    "native:test-client",
				Event:     "message.stream",
				Content:   fmt.Sprintf("chunk-%d", i),
				MessageID: "msg-1",
				Metadata:  map[string]string{"done": "false"},
			})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			messageBus.PublishOutbound(bus.OutboundMessage{
				Channel: "telegram",
				ChatID:  "12345",
				Content: fmt.Sprintf("telegram-response-%d", i),
			})
		}
	}()

	// Consume telegram messages via a mock channel dispatcher
	telegramQueue := make(chan bus.OutboundMessage, 200)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-telegramQueue:
				if !ok {
					return
				}
				telegramCh.Send(ctx, msg)
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, ok := messageBus.SubscribeOutbound(ctx)
				if !ok {
					continue
				}
				if msg.Channel == "telegram" {
					select {
					case telegramQueue <- msg:
					default:
					}
				}
			}
		}
	}()

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	ts := telegramSent.Load()
	if ts == 0 {
		t.Error("expected telegram messages to be sent while native channel is streaming")
	}
}

func TestWebSocketRapidMessagesUnderLoad(t *testing.T) {
	nativeCfg := config.DefaultConfig()
	nativeCfg.Channels.Native.Enabled = true
	nativeCfg.Channels.Native.Port = 0
	nativeCfg.Channels.Native.MaxClients = 100

	tmpDir := t.TempDir()
	nativeCfg.Channels.Native.LeleDir = tmpDir

	messageBus := bus.NewMessageBus()
	defer messageBus.Close()

	agentLoop := newNativeTestAgentLoop(nativeCfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(nativeCfg, messageBus, agentLoop, approvalMgr)
	if err != nil {
		t.Fatalf("failed to create native channel: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := native.Start(ctx); err != nil {
		t.Fatalf("failed to start native channel: %v", err)
	}
	defer native.Stop(ctx)

	time.Sleep(50 * time.Millisecond)

	mux := http.NewServeMux()
	native.registerRoutes(mux)
	handler := native.corsMiddleware(native.securityHeadersMiddleware(native.authMiddleware(mux)))
	server := httptest.NewServer(handler)
	defer server.Close()

	pin, err := native.auth.GeneratePIN("TestDevice")
	if err != nil {
		t.Fatalf("failed to generate PIN: %v", err)
	}

	client, token, _, err := native.auth.PairWithPIN(pin.PIN, "TestDevice")
	if err != nil {
		t.Fatalf("failed to pair: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?token=" + token
	wsConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to connect websocket: %v", err)
	}
	defer wsConn.Close()

	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, welcomeRaw, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read welcome: %v", err)
	}
	var welcomeMsg map[string]interface{}
	json.Unmarshal(welcomeRaw, &welcomeMsg)
	if welcomeMsg["event"] != "welcome" {
		t.Fatalf("expected welcome event, got %v", welcomeMsg["event"])
	}

	var received atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, msgRaw, err := wsConn.ReadMessage()
			if err != nil {
				return
			}
			var wsMsg map[string]interface{}
			json.Unmarshal(msgRaw, &wsMsg)
			if wsMsg["event"] == "message.stream" {
				received.Add(1)
			}
		}
	}()

	// Start a goroutine to consume messages from the bus and dispatch them
	// to the native channel so they reach the websocket clients.
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				msg, ok := messageBus.SubscribeOutbound(ctx)
				if !ok {
					continue
				}
				if msg.Channel == "native" {
					native.dispatchOutboundMessage(msg)
				}
			}
		}
	}()

	for i := 0; i < 50; i++ {
		messageBus.PublishOutbound(bus.OutboundMessage{
			Channel:   "native",
			ChatID:    "native:" + client.ClientID,
			Event:     "message.stream",
			Content:   fmt.Sprintf("stream-chunk-%d", i),
			MessageID: "stream-msg-1",
			Metadata:  map[string]string{"done": fmt.Sprintf("%v", i == 49)},
		})
	}

	time.Sleep(500 * time.Millisecond)
	wsConn.Close()
	<-done
	cancel()
	wg.Wait()

	count := received.Load()
	if count == 0 {
		t.Error("expected to receive stream events via websocket")
	}
}

func TestBusDropMetricsUnderOverflow(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	for i := 0; i < 600; i++ {
		mb.PublishOutbound(bus.OutboundMessage{Channel: "test", Content: "msg"})
	}

	_, _, _, outLen, outCap, dropped := mb.Stats()
	if outCap != 500 {
		t.Errorf("expected capacity 500, got %d", outCap)
	}
	if outLen != 500 {
		t.Errorf("expected 500 buffered, got %d", outLen)
	}
	if dropped < 100 {
		t.Errorf("expected at least 100 drops, got %d", dropped)
	}
}
