package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

func TestTelegramSessionFlow(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "telegram-session-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	agent := al.registry.GetDefaultAgent()
	if agent != nil {
		agent.Provider = &llmRunnerMockLLMProvider{
			response: &providers.LLMResponse{
				Content:   "I'm doing well, thank you!",
				ToolCalls: []providers.ToolCall{},
			},
		}
	}

	// Simulate a Telegram session
	sessionKey := "telegram:123456789"

	// Step 1: User sends a message
	msg1 := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user123",
		ChatID:     "123456789",
		Content:    "Hello, how are you?",
		SessionKey: sessionKey,
	}

	response1, err := al.messageProcessor.processMessage(context.Background(), msg1)
	if err != nil {
		t.Fatalf("Failed to process first message: %v", err)
	}
	if response1 == "" {
		t.Fatal("Expected response for first message")
	}

	// Get the active session key
	activeSession1 := al.ResolveSessionKey(sessionKey)
	defaultAgent := al.registry.GetDefaultAgent()
	history1 := defaultAgent.Sessions.GetHistory(activeSession1)
	if len(history1) != 2 { // user + assistant
		t.Errorf("Expected 2 messages after first interaction, got %d", len(history1))
	}

	// Step 2: User uses /new command
	msg2 := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user123",
		ChatID:     "123456789",
		Content:    "/new",
		SessionKey: sessionKey,
	}

	response2, err := al.messageProcessor.processMessage(context.Background(), msg2)
	if err != nil {
		t.Fatalf("Failed to process /new command: %v", err)
	}
	if response2 == "" || !strings.Contains(response2, "New conversation") {
		t.Fatalf("Expected new conversation response, got: %s", response2)
	}

	// Get the new active session key
	activeSession2 := al.ResolveSessionKey(sessionKey)
	if activeSession2 == activeSession1 {
		t.Fatal("Expected different session key after /new")
	}

	// Verify new session is empty
	history2 := defaultAgent.Sessions.GetHistory(activeSession2)
	if len(history2) != 0 {
		t.Errorf("Expected 0 messages in new session, got %d", len(history2))
	}

	// Step 3: User sends another message
	msg3 := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user123",
		ChatID:     "123456789",
		Content:    "What's the weather today?",
		SessionKey: sessionKey,
	}

	response3, err := al.messageProcessor.processMessage(context.Background(), msg3)
	if err != nil {
		t.Fatalf("Failed to process third message: %v", err)
	}
	if response3 == "" {
		t.Fatal("Expected response for third message")
	}

	// Verify new session now has 2 messages (user + assistant)
	history3 := defaultAgent.Sessions.GetHistory(activeSession2)
	if len(history3) != 2 {
		t.Errorf("Expected 2 messages in new session after third interaction, got %d", len(history3))
	}

	// Verify old session is preserved
	historyOld := defaultAgent.Sessions.GetHistory(activeSession1)
	if len(historyOld) != 2 {
		t.Errorf("Expected old session to be preserved with 2 messages, got %d", len(historyOld))
	}
}
