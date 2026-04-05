package agent

import (
	"testing"
	"os"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestStartFreshConversation_CreatesCleanSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "fresh-session-test-*")
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
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	baseSessionKey := "telegram:12345"
	
	// Get the default agent
	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("No default agent found")
	}

	// Add some history to the base session
	defaultAgent.Sessions.AddMessage(baseSessionKey, "user", "Old message 1")
	defaultAgent.Sessions.AddMessage(baseSessionKey, "assistant", "Old response 1")
	defaultAgent.Sessions.AddTokenCounts(baseSessionKey, 100, 50)

	// Verify base session has history
	historyBefore := defaultAgent.Sessions.GetHistory(baseSessionKey)
	if len(historyBefore) != 2 {
		t.Fatalf("Expected 2 messages in base session, got %d", len(historyBefore))
	}

	// Start fresh conversation
	newSessionKey := al.startFreshConversation(baseSessionKey, "main", "test-model")
	if newSessionKey == "" {
		t.Fatal("startFreshConversation returned empty session key")
	}

	// Verify new session is different from base session
	if newSessionKey == baseSessionKey {
		t.Fatal("New session key should be different from base session key")
	}

	// Verify new session is empty
	historyNew := defaultAgent.Sessions.GetHistory(newSessionKey)
	if len(historyNew) != 0 {
		t.Errorf("Expected 0 messages in new session, got %d", len(historyNew))
	}

	// Verify token counts are zero in new session
	inputTokens, outputTokens := defaultAgent.Sessions.GetTokenCounts(newSessionKey)
	if inputTokens != 0 || outputTokens != 0 {
		t.Errorf("Expected token counts (0, 0) in new session, got (%d, %d)", inputTokens, outputTokens)
	}

	// Verify base session is preserved
	historyAfter := defaultAgent.Sessions.GetHistory(baseSessionKey)
	if len(historyAfter) != 2 {
		t.Errorf("Expected base session to be preserved with 2 messages, got %d", len(historyAfter))
	}

	// Verify base session token counts are preserved
	inputTokensBase, outputTokensBase := defaultAgent.Sessions.GetTokenCounts(baseSessionKey)
	if inputTokensBase != 100 || outputTokensBase != 50 {
		t.Errorf("Expected base session token counts (100, 50), got (%d, %d)", inputTokensBase, outputTokensBase)
	}
}