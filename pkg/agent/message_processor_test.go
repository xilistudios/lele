package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

func TestProcessSystemMessage_ClearCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "message-processor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
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
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	mp := newMessageProcessor(al)

	sessionKey := "telegram:12345"
	agent := al.registry.GetDefaultAgent()
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi there!")

	msg := bus.InboundMessage{
		Channel:    "system",
		SenderID:   "user1",
		ChatID:     "telegram:12345",
		Content:    "/clear",
		SessionKey: sessionKey,
		Metadata: map[string]string{
			"message_id": "67890",
		},
	}

	result, err := mp.processSystemMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("processSystemMessage failed: %v", err)
	}
	if result != "" {
		t.Errorf("Expected empty result from processSystemMessage, got: %s", result)
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) > 0 {
		t.Logf("History after clear: %d messages (may include system prompt)", len(history))
	}

	summary := agent.Sessions.GetSummary(sessionKey)
	if summary != "" {
		t.Errorf("Expected empty summary after clear, got: %s", summary)
	}
}

func TestProcessSystemMessage_CompactCommand_NotEnoughMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "message-processor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
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
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	mp := newMessageProcessor(al)

	sessionKey := "telegram:12345"
	agent := al.registry.GetDefaultAgent()
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi")

	msg := bus.InboundMessage{
		Channel:    "system",
		SenderID:   "user1",
		ChatID:     "telegram:12345",
		Content:    "/compact",
		SessionKey: sessionKey,
		Metadata: map[string]string{
			"message_id": "67890",
		},
	}

	result, err := mp.processSystemMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("processSystemMessage failed: %v", err)
	}
	if result != "" {
		t.Errorf("Expected empty result from processSystemMessage, got: %s", result)
	}

	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Errorf("Expected 2 messages after insufficient compact attempt, got: %d", len(history))
	}
}

func TestProcessSystemMessage_SummarizeSessionWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "message-processor-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
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

	sm := newSessionManager(al)

	sessionKey := "test:summarize-error"
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary of conversation",
			ToolCalls: []providers.ToolCall{},
		},
	}
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for insufficient messages")
	}
	if stats != nil {
		t.Error("Expected nil stats for insufficient messages")
	}
	if !strings.Contains(err.Error(), "not enough messages") {
		t.Errorf("Expected not enough messages error, got: %v", err)
	}

	agent.Sessions.TruncateHistory(sessionKey, 0)
	for i := 0; i < 10; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Message %d", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Response %d", i))
	}

	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Logf("Summarization failed as expected with mock provider: %v", err)
		if !strings.Contains(err.Error(), "LLM summarization failed") {
			t.Errorf("Expected LLM summarization error, got: %v", err)
		}
	} else {
		if stats == nil {
			t.Error("Expected stats when no error")
		} else if stats.BeforeMessages <= stats.AfterMessages {
			t.Errorf("Expected fewer messages after compaction: before=%d, after=%d",
				stats.BeforeMessages, stats.AfterMessages)
		}
	}
}
