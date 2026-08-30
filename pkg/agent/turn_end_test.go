package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// The typing indicator of a messaging channel is only stopped by something
// leaving the turn: a final message (which sweeps as a side effect) or the
// terminal turn.end event. Before #240 the "processing canceled" and "empty
// response" exits published nothing at all, so Telegram kept "typing..."
// forever (76 occurrences/day in production logs).
//
// These tests pin the invariant: every turn of an external channel ends with
// exactly one turn.end, and internal channels never receive one.

// newTurnEndTestLoop builds an AgentLoop backed by a real bus, configured like
// the other loop tests but with a provider that blocks until its context is
// cancelled (so the turn ends through the context.Canceled path).
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

// recvOutbound waits for one outbound message, failing the test on timeout.
func recvOutbound(t *testing.T, msgBus *bus.MessageBus, timeout time.Duration) (bus.OutboundMessage, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return msgBus.SubscribeOutbound(ctx)
}

// assertNoOutbound asserts nothing is published within the window.
func assertNoOutbound(t *testing.T, msgBus *bus.MessageBus, within time.Duration) {
	t.Helper()
	if msg, ok := recvOutbound(t, msgBus, within); ok {
		t.Fatalf("expected no outbound message, got %+v", msg)
	}
}

// TestPublishTurnEnd_TelegramChannelEmitsEvent checks the shape of the signal:
// event name, routing fields, no content, and the propagated message id that
// lets the channel cancel the exact indicator key.
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

// TestPublishTurnEnd_SkipsInternalAndEmptyChannels pins the other half of the
// contract: cli/system/subagent have no typing indicator to stop, and an empty
// channel cannot be routed — publishing there would only add queue pressure.
// Each case proves the signal was skipped by showing the same bus still
// delivers a turn.end for a real channel (so a closed/busy bus can never make
// the negative assertion pass vacuously).
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

			// Liveness control: this bus is still able to publish.
			al.publishTurnEnd(bus.InboundMessage{Channel: "telegram", ChatID: "999"})
			if msg, ok := recvOutbound(t, msgBus, time.Second); !ok || msg.Event != "turn.end" {
				t.Fatalf("control publish failed (bus unusable?): ok=%v msg=%+v", ok, msg)
			}
		})
	}
}

// TestPublishTurnEnd_NoMetadataIsStillValid guards the nil-map path: a turn
// whose inbound message had no metadata must still emit a routable signal.
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

// TestRun_CanceledTurnPublishesTurnEnd is the regression test for the exact
// production path in the bug report: StopAgent cancels the in-flight turn,
// processMessage returns context.Canceled, and Run returns early without a
// final message. The terminal signal must still reach the channel.
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
