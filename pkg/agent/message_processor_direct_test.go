package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

func drainOutbound(mb *bus.MessageBus) []bus.OutboundMessage {
	var drained []bus.OutboundMessage
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		msg, ok := mb.SubscribeOutbound(ctx)
		cancel()
		if !ok {
			return drained
		}
		drained = append(drained, msg)
	}
}

func newDirectTestLoop(t *testing.T, finalText string) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
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
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   finalText,
			ToolCalls: []providers.ToolCall{},
		},
	}
	return al, msgBus
}

func TestProcessDirectReturnsFinalResponseAndDoesNotPublish(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-7a3f"
	al, msgBus := newDirectTestLoop(t, finalText)

	resp, err := al.providable.ProcessDirect(context.Background(), "hello from the cli", "cli:default")
	if err != nil {
		t.Fatalf("ProcessDirect failed: %v", err)
	}
	if resp != finalText {
		t.Fatalf("ProcessDirect returned %q, want exactly %q", resp, finalText)
	}

	drained := drainOutbound(msgBus)
	if len(drained) != 0 {
		var listed strings.Builder
		for _, out := range drained {
			listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
		}
		t.Fatalf("ProcessDirect published %d message(s) to the outbound bus; the CLI contract is to return the text and publish nothing at all:%s",
			len(drained), listed.String())
	}
}

func TestProcessDirectWithChannelStillPublishesAndReturnsEmpty(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-7a3f"
	al, msgBus := newDirectTestLoop(t, finalText)

	resp, err := al.providable.ProcessDirectWithChannel(context.Background(), "scheduled job", "telegram:123", "telegram", "123")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if resp != "" {
		t.Fatalf("ProcessDirectWithChannel returned %q, want exactly \"\" (its delivery is the bus publish)", resp)
	}

	drained := drainOutbound(msgBus)
	for _, out := range drained {
		if out.Content == finalText && out.Channel == "telegram" && out.ChatID == "123" {
			return
		}
	}
	var listed strings.Builder
	for _, out := range drained {
		listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
	}
	t.Fatalf("ProcessDirectWithChannel did not publish Content=%q on Channel=telegram ChatID=123; drained outbound:%s",
		finalText, listed.String())
}

func TestProcessDirectWithChannelCronShapePublishesToChannelNotSessionKey(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-7a3f"
	al, msgBus := newDirectTestLoop(t, finalText)

	resp, err := al.providable.ProcessDirectWithChannel(context.Background(), "daily report", "cron:daily-report", "telegram", "1234567890")
	if err != nil {
		t.Fatalf("ProcessDirectWithChannel failed: %v", err)
	}
	if resp != "" {
		t.Fatalf("ProcessDirectWithChannel returned %q, want exactly \"\" (the cron delivery IS the bus publish)", resp)
	}

	drained := drainOutbound(msgBus)
	var listed strings.Builder
	for _, out := range drained {
		listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
	}

	var withFinalText []bus.OutboundMessage
	for _, out := range drained {
		if out.Content == finalText {
			withFinalText = append(withFinalText, out)
		}
	}
	if len(withFinalText) != 1 {
		t.Fatalf("expected EXACTLY ONE bus message with Content=%q, got %d (a second copy is a duplicate-delivery regression); drained outbound:%s",
			finalText, len(withFinalText), listed.String())
	}
	delivery := withFinalText[0]
	if delivery.Channel != "telegram" || delivery.ChatID != "1234567890" {
		t.Fatalf("cron delivery went to the wrong coordinates: Channel=%q ChatID=%q, want Channel=%q ChatID=%q (the JOB's coordinates); drained outbound:%s",
			delivery.Channel, delivery.ChatID, "telegram", "1234567890", listed.String())
	}
	for _, out := range drained {
		if out.Channel == "cron:daily-report" {
			t.Fatalf("the session key leaked into the delivery coordinates: Channel=%q (sessionKey must never be a delivery channel); drained outbound:%s",
				out.Channel, listed.String())
		}
	}
}

func TestProcessDirectWithActiveGoalPinsSideDeliveryLimitation(t *testing.T) {
	finalText := "DIRECT-FINAL-RESPONSE-4e8a"
	al, msgBus := newDirectTestLoop(t, finalText)

	const sessionKey = "cli:goal-tripwire"
	al.goalManager.Set(sessionKey, "keep the CLI contract honest", 1)

	resp, err := al.providable.ProcessDirect(context.Background(), "start working", sessionKey)
	if err != nil {
		t.Fatalf("ProcessDirect failed: %v", err)
	}
	if resp != finalText {
		t.Fatalf("ProcessDirect returned %q, want exactly %q — side deliveries must not leak into the CLI return value", resp, finalText)
	}

	var notice *bus.OutboundMessage
	drained := drainOutbound(msgBus)
	for i, out := range drained {
		if strings.Contains(out.Content, "Goal budget exhausted") {
			notice = &drained[i]
		}
	}
	if notice == nil {
		var listed strings.Builder
		for _, out := range drained {
			listed.WriteString("\n  Channel=" + out.Channel + " ChatID=" + out.ChatID + " Content=" + out.Content)
		}
		t.Fatalf("expected the goal budget-exhausted notice on the outbound bus (documented CLI blind spot); drained:%s", listed.String())
	}
	if notice.Channel != "cli" || notice.ChatID != "direct" {
		t.Errorf("side delivery went to Channel=%q ChatID=%q, want cli/direct (ProcessDirect's synthetic coordinates)", notice.Channel, notice.ChatID)
	}
}
