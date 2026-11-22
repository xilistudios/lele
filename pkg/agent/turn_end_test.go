package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTurnEndTestLoop(t *testing.T, provider *blockingMockProvider) (*AgentLoop, *bus.MessageBus) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "agent-turnend-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{APIKey: "test-key"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	if agent := al.registry.GetDefaultAgent(); agent != nil {
		agent.Provider = provider
	}
	return al, msgBus
}

func recvOutbound(t *testing.T, msgBus *bus.MessageBus, timeout time.Duration) (bus.OutboundMessage, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return msgBus.SubscribeOutbound(ctx)
}

func assertNoOutbound(t *testing.T, msgBus *bus.MessageBus, within time.Duration) {
	t.Helper()
	if msg, ok := recvOutbound(t, msgBus, within); ok {
		t.Fatalf("expected no outbound message, got %+v", msg)
	}
}

func TestPublishTurnEnd_TelegramChannelEmitsEvent(t *testing.T) {
	al, msgBus := newTurnEndTestLoop(t, &blockingMockProvider{started: make(chan struct{})})

	al.publishTurnEnd(bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "12345",
		SessionKey: "telegram:12345",
		Metadata:   map[string]string{"message_id": "678", "sender_id": "me"},
	})

	msg, ok := recvOutbound(t, msgBus, time.Second)
	if !ok {
		t.Fatal("turn.end was not published for an external channel")
	}
	if msg.Event != "turn.end" {
		t.Fatalf("event = %q, want %q", msg.Event, "turn.end")
	}
	if msg.Channel != "telegram" || msg.ChatID != "12345" {
		t.Fatalf("routing = %q/%q, want telegram/12345", msg.Channel, msg.ChatID)
	}
	if msg.Content != "" {
		t.Fatalf("turn.end must carry no content, got %q", msg.Content)
	}
	if msg.IsIntermediate {
		t.Fatal("turn.end must not be marked intermediate: it is the terminal signal")
	}
	if got := msg.Metadata["message_id"]; got != "678" {
		t.Fatalf("metadata[message_id] = %q, want 678", got)
	}
	for k := range msg.Metadata {
		if k != "message_id" {
			t.Fatalf("metadata must be limited to message_id, got key %q (%v)", k, msg.Metadata)
		}
	}
}

func TestPublishTurnEnd_SkipsInternalAndEmptyChannels(t *testing.T) {
	for _, channel := range []string{"subagent", "cli", "system", ""} {
		t.Run("channel="+channel, func(t *testing.T) {
			al, msgBus := newTurnEndTestLoop(t, &blockingMockProvider{started: make(chan struct{})})

			al.publishTurnEnd(bus.InboundMessage{
				Channel:    channel,
				ChatID:     "999",
				SessionKey: channel + ":999",
				Metadata:   map[string]string{"message_id": "1"},
			})

			assertNoOutbound(t, msgBus, 100*time.Millisecond)

			al.publishTurnEnd(bus.InboundMessage{Channel: "telegram", ChatID: "999"})
			if msg, ok := recvOutbound(t, msgBus, time.Second); !ok || msg.Event != "turn.end" {
				t.Fatalf("control publish failed (bus unusable?): ok=%v msg=%+v", ok, msg)
			}
		})
	}
}

func TestPublishTurnEnd_NoMetadataIsStillValid(t *testing.T) {
	al, msgBus := newTurnEndTestLoop(t, &blockingMockProvider{started: make(chan struct{})})

	al.publishTurnEnd(bus.InboundMessage{Channel: "telegram", ChatID: "42"})

	msg, ok := recvOutbound(t, msgBus, time.Second)
	if !ok {
		t.Fatal("turn.end was not published when metadata is nil")
	}
	if msg.Event != "turn.end" || msg.ChatID != "42" {
		t.Fatalf("unexpected signal: %+v", msg)
	}
	if len(msg.Metadata) != 0 {
		t.Fatalf("expected no metadata, got %v", msg.Metadata)
	}
}

func TestRun_CanceledTurnPublishesTurnEnd(t *testing.T) {
	provider := &blockingMockProvider{started: make(chan struct{})}
	al, msgBus := newTurnEndTestLoop(t, provider)

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() { done <- al.Run(runCtx) }()

	msgBus.PublishInbound(bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user1",
		ChatID:     "123",
		Content:    "Hello",
		SessionKey: "telegram:123",
		Metadata:   map[string]string{"message_id": "55"},
	})

	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not start processing")
	}

	if response := al.providable.StopAgent("telegram:123"); response == "" {
		t.Fatal("expected a stop response")
	}

	msg, ok := recvOutbound(t, msgBus, 3*time.Second)
	if !ok {
		t.Fatal("canceled turn published no terminal signal: typing would stay on forever")
	}
	if msg.Event != "turn.end" {
		t.Fatalf("first outbound after cancel = event %q content %q, want turn.end", msg.Event, msg.Content)
	}
	if msg.Channel != "telegram" || msg.ChatID != "123" {
		t.Fatalf("signal routing = %q/%q, want telegram/123", msg.Channel, msg.ChatID)
	}
	if got := msg.Metadata["message_id"]; got != "55" {
		t.Fatalf("signal metadata[message_id] = %q, want 55", got)
	}

	cancelRun()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent loop returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent loop did not stop")
	}
}
