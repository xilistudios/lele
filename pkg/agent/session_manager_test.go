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
	"github.com/xilistudios/lele/pkg/routing"
)

// TestSummarizeSessionWithError_InsufficientMessages tests error handling for insufficient messages
func TestSummarizeSessionWithError_InsufficientMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
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
	// Create a mock provider that returns empty response
	emptyProvider := &mockProvider{
		returnEmpty: true,
	}
	al := NewAgentLoop(cfg, msgBus, emptyProvider)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
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
	// Create a mock provider that returns a valid summary
	successfulProvider := &mockProvider{
		mockResponse: "This is a comprehensive summary of the conversation.",
	}
	al := NewAgentLoop(cfg, msgBus, successfulProvider)

	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
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
	if summary != "This is a comprehensive summary of the conversation." {
		t.Errorf("Expected summary to be set, got: %s", summary)
	}

	// Verify history was truncated (should keep last 2 messages)
	afterHistory := agent.Sessions.GetHistory(sessionKey)
	if len(afterHistory) != 2 {
		t.Errorf("Expected 2 messages after compaction, got %d", len(afterHistory))
	}
}

// TestAddTokenCounts_SessionKeyWithoutAgentPrefix tests token counting with session keys without agent prefix
func TestAddTokenCounts_SessionKeyWithoutAgentPrefix(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "session-manager-test-*")
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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

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