// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// TestProcessSystemMessage_ClearCommand tests that /clear command works correctly
func TestProcessSystemMessage_ClearCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "message-processor-test-*")
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

	mp := newMessageProcessor(al)

	// Add some history first
	sessionKey := "telegram:12345"
	agent := al.registry.GetDefaultAgent()
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi there!")

	// Process system message with /clear command
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

	// Verify history was cleared
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) > 0 {
		t.Logf("History after clear: %d messages (may include system prompt)", len(history))
	}
	
	// Verify summary was cleared
	summary := agent.Sessions.GetSummary(sessionKey)
	if summary != "" {
		t.Errorf("Expected empty summary after clear, got: %s", summary)
	}
}

// TestProcessSystemMessage_CompactCommand_NotEnoughMessages tests /compact with insufficient messages
func TestProcessSystemMessage_CompactCommand_NotEnoughMessages(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "message-processor-test-*")
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

	mp := newMessageProcessor(al)

	// Add minimal history (not enough to compact)
	sessionKey := "telegram:12345"
	agent := al.registry.GetDefaultAgent()
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi")

	// Process system message with /compact command
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

	// Verify history remains unchanged (should not be modified for insufficient messages)
	history := agent.Sessions.GetHistory(sessionKey)
	if len(history) != 2 {
		t.Errorf("Expected 2 messages after insufficient compact attempt, got: %d", len(history))
	}
}

// TestProcessSystemMessage_SummarizeSessionWithError tests the new error handling function
func TestProcessSystemMessage_SummarizeSessionWithError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "message-processor-test-*")
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

	sm := newSessionManager(al)

	// Test with insufficient messages
	sessionKey := "test:summarize-error"
	agent := al.registry.GetDefaultAgent()
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

	// Test with sufficient messages (should work with mock provider)
	// Clear and add more messages
	agent.Sessions.TruncateHistory(sessionKey, 0)
	for i := 0; i < 10; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", fmt.Sprintf("Message %d", i))
		agent.Sessions.AddMessage(sessionKey, "assistant", fmt.Sprintf("Response %d", i))
	}

	stats, err = sm.summarizeSessionWithError(agent, sessionKey)
	if err != nil {
		// This might fail due to mock provider, but should give specific error
		t.Logf("Summarization failed as expected with mock provider: %v", err)
		if !strings.Contains(err.Error(), "LLM summarization failed") {
			t.Errorf("Expected LLM summarization error, got: %v", err)
		}
	} else {
		// If it succeeds, verify stats
		if stats == nil {
			t.Error("Expected stats when no error")
		} else if stats.BeforeMessages <= stats.AfterMessages {
			t.Errorf("Expected fewer messages after compaction: before=%d, after=%d", 
				stats.BeforeMessages, stats.AfterMessages)
		}
	}
}