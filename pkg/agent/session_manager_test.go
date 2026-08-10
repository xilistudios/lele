// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/routing"
	"github.com/xilistudios/lele/pkg/session"
)

// TestSummarizeSessionWithError_InsufficientMessages tests error handling for insufficient messages
func TestSummarizeSessionWithError_InsufficientMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
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
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary",
			ToolCalls: []providers.ToolCall{},
		},
	}
	sessionKey := "test:insufficient"

	// Test with no messages
	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for no messages")
	}
	if stats != nil {
		t.Error("Expected nil stats for no messages")
	}
	if !strings.Contains(err.Error(), "not enough messages") {
		t.Errorf("Expected not enough messages error, got: %v", err)
	}

	// Test with 1 message
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for 1 message")
	}
	if stats != nil {
		t.Error("Expected nil stats for 1 message")
	}

	// Test with 2 messages
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi")
	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for 2 messages")
	}
	if stats != nil {
		t.Error("Expected nil stats for 2 messages")
	}

	// Test with 3 messages (minimum required)
	agent.Sessions.AddMessage(sessionKey, "user", "How are you?")
	// This should attempt summarization but may fail due to mock provider
	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		// Should be LLM-related error since we have enough messages
		if !strings.Contains(err.Error(), "LLM summarization failed") {
			t.Errorf("Expected LLM error for 3 messages, got: %v", err)
		}
	} else {
		// If it succeeds, verify the stats
		if stats == nil {
			t.Error("Expected stats when no error")
		} else if stats.BeforeMessages < 3 {
			t.Errorf("Expected at least 3 messages before compaction, got: %d", stats.BeforeMessages)
		}
	}
}

// TestSummarizeSessionWithError_EmptyResult tests handling of empty summarization results
func TestSummarizeSessionWithError_EmptyResult(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
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
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "", // Empty response to test handling
			ToolCalls: []providers.ToolCall{},
		},
	}
	sessionKey := "test:empty-result"

	// Add enough messages to trigger summarization
	for i := 0; i < 5; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Message %d", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Response %d", i))
	}

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err == nil {
		t.Error("Expected error for empty result")
	}
	if stats != nil {
		t.Error("Expected nil stats for empty result")
	}
	if !strings.Contains(err.Error(), "empty result") {
		t.Errorf("Expected empty result error, got: %v", err)
	}
}

// TestSummarizeSessionWithError_Success tests successful summarization
func TestSummarizeSessionWithError_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	// Create a dedicated session directory for this test to avoid loading
	// real user sessions from ~/.lele/sessions.

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

	// Override the shared session manager with a test-local one so we don't
	// load or persist data in the user's real ~/.lele/sessions directory.
	testSessionMgr := session.NewSessionManager()
	al.registry.SetSharedSessionManager(testSessionMgr)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}
	agent.Provider = &llmRunnerMockLLMProvider{
		response: &providers.LLMResponse{
			Content:   "Summary: The user asked several questions about important topics and received detailed answers.",
			ToolCalls: []providers.ToolCall{},
		},
	}
	sessionKey := "test:success"

	// Add enough messages to trigger summarization
	for i := 0; i < 6; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Question %d about important topic", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Answer %d with detailed information", i))
	}

	beforeCount := len(agent.Sessions.GetHistory(sessionKey))

	stats, err := sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		t.Fatalf("Unexpected error in successful summarization: %v", err)
	}
	if stats == nil {
		t.Fatal("Expected stats for successful summarization")
	}

	// Verify stats
	if stats.BeforeMessages != beforeCount {
		t.Errorf("Expected BeforeMessages=%d, got %d", beforeCount, stats.BeforeMessages)
	}
	if stats.AfterMessages >= stats.BeforeMessages {
		t.Errorf("Expected fewer messages after: before=%d, after=%d",
			stats.BeforeMessages, stats.AfterMessages)
	}
	if stats.DroppedMessages <= 0 {
		t.Errorf("Expected positive dropped messages, got %d", stats.DroppedMessages)
	}

	// Verify summary was set
	summary := agent.Sessions.GetSummary(sessionKey)
	if summary == "" {
		t.Error("Expected summary to be set, got empty string")
	}
	if !strings.Contains(summary, "Summary") {
		t.Errorf("Expected summary to contain 'Summary', got: %s", summary)
	}

	// Verify messages are preserved in memory with ExcludeFromContext flags.
	// Old messages stay in memory (for TUI display) but are marked as excluded
	// from the LLM context.
	afterHistory := agent.Sessions.GetHistory(sessionKey)
	excludedCount := 0
	includedCount := 0
	for _, m := range afterHistory {
		if m.ExcludeFromContext {
			excludedCount++
		} else {
			includedCount++
		}
	}
	if excludedCount == 0 {
		t.Error("Expected some messages to be excluded from context")
	}
	if includedCount != 2 {
		t.Errorf("Expected 2 messages included in context, got %d", includedCount)
	}
}

// TestAddTokenCounts_SessionKeyWithoutAgentPrefix tests token counting with session keys without agent prefix
func TestAddTokenCounts_SessionKeyWithoutAgentPrefix(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
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

	sm := newSessionManager(al)

	// Test with session key without agent prefix (e.g., "telegram:12345")
	sessionKey := "telegram:12345"
	inputTokens := 100
	outputTokens := 50

	// This should use the default agent and add token counts
	sm.AddTokenCounts(sessionKey, inputTokens, outputTokens)

	// Verify token counts were added
	defaultAgent := al.registry.GetDefaultAgent()
	inputTokensActual, outputTokensActual := defaultAgent.Sessions.GetTokenCounts(sessionKey)
	if inputTokensActual != inputTokens || outputTokensActual != outputTokens {
		t.Errorf("Expected input=%d, output=%d, got input=%d, output=%d",
			inputTokens, outputTokens, inputTokensActual, outputTokensActual)
	}

	// Test with session key with agent prefix (e.g., "agent:main:telegram:12345")
	sessionKeyWithPrefix := "agent:main:telegram:12345"
	inputTokens2 := 200
	outputTokens2 := 75

	sm.AddTokenCounts(sessionKeyWithPrefix, inputTokens2, outputTokens2)

	// Verify token counts were added to the correct agent
	parsed := routing.ParseAgentSessionKey(sessionKeyWithPrefix)
	if parsed == nil {
		t.Fatal("Failed to parse session key with prefix")
	}

	agent, ok := al.registry.GetAgent(parsed.AgentID)
	if !ok {
		t.Fatalf("Failed to get agent %s", parsed.AgentID)
	}

	inputTokensActual2, outputTokensActual2 := agent.Sessions.GetTokenCounts(sessionKeyWithPrefix)
	if inputTokensActual2 != inputTokens2 || outputTokensActual2 != outputTokens2 {
		t.Errorf("Expected input=%d, output=%d, got input=%d, output=%d",
			inputTokens2, outputTokens2, inputTokensActual2, outputTokensActual2)
	}
}

// TestMaybeSummarize_TriggersWhenThresholdExceeded proves that maybeSummarize
// actually triggers compaction when the token estimate exceeds the configured
// threshold (ContextWindow * thresholdPercent / 100).
func TestMaybeSummarize_TriggersWhenThresholdExceeded(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	// The agent model must be registered in the provider's Models map so that
	// getSessionContextWindow (which resolves via getContextWindow) returns the
	// small context window instead of the 128000 default.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				Provider:          "anthropic",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: &config.ProvidersConfig{
			Anthropic: config.ProviderConfig{
				APIKey: "test-key",
			},
			Named: map[string]config.NamedProviderConfig{
				"anthropic": {
					Type:           "anthropic",
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"test-model": {ContextWindow: 1000},
					},
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	// Use a test-local session manager so we don't touch real sessions.

	testSessionMgr := session.NewSessionManager()
	al.registry.SetSharedSessionManager(testSessionMgr)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	// Also set agent.ContextWindow as a safety net.
	agent.ContextWindow = 1000

	// Mock provider returns a non-empty summary string.
	agent.Provider = &mockProvider{
		mockResponse: "Summary of the conversation about testing compaction behavior.",
	}

	sessionKey := "test:maybe-summarize-triggers-threshold-exceeded"

	// Seed session with enough messages so EstimateTokens exceeds threshold.
	// With ContextWindow=1000 and default 75% threshold, the threshold is 750
	// tokens. Each message is 200 chars + 10 structural overhead = 210 chars;
	// EstimateTokens uses chars*2/5, so ~84 tokens per message.
	// 20 messages × 84 = ~1680 tokens, plus system prompt — comfortably > 750.
	longContent := strings.Repeat("x", 200)
	for i := 0; i < 10; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Question %d: %s", i, longContent))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Answer %d: %s", i, longContent))
	}

	beforeHistory := agent.Sessions.GetHistory(sessionKey)
	beforeCount := len(beforeHistory)
	t.Logf("Before maybeSummarize: %d messages", beforeCount)
	if beforeCount != 20 {
		t.Fatalf("Expected 20 seeded messages, got %d", beforeCount)
	}

	// Act: call maybeSummarize. With a 1000-token context window the threshold
	// (750) should already be exceeded by history alone.
	stats := sm.maybeSummarize(agent, sessionKey, "test", "test-chat")
	if stats == nil {
		t.Fatal("Expected non-nil SummarizeStats; compaction should have been triggered")
	}

	// Assert: at least one message was dropped.
	if stats.DroppedMessages <= 0 {
		t.Errorf("Expected DroppedMessages > 0, got %d", stats.DroppedMessages)
	}
	t.Logf("Compaction stats: before=%d after=%d dropped=%d (tokens %d→%d, saved %d)",
		stats.BeforeMessages, stats.AfterMessages, stats.DroppedMessages,
		stats.BeforeTokens, stats.AfterTokens, stats.SavedTokens)

	// Assert: old excluded messages are marked with ExcludeFromContext but
	// remain in the in-memory slice for TUI display.
	afterHistory := agent.Sessions.GetHistory(sessionKey)
	excludedCount := 0
	includedCount := 0
	for _, m := range afterHistory {
		if m.ExcludeFromContext {
			excludedCount++
		} else {
			includedCount++
		}
	}
	if excludedCount == 0 {
		t.Error("Expected some messages to be excluded from context")
	}
	if includedCount != 2 {
		t.Errorf("Expected 2 messages included in context, got %d", includedCount)
	}

	// Summary messages (if any) should never be excluded from context.
	for i, m := range afterHistory {
		if isSummaryMessage(m) && m.ExcludeFromContext {
			t.Errorf("Summary message [%d] should not be excluded from context", i)
		}
	}
}
