// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

// createGroupTurnTestHarness sets up an AgentLoop with a registered test agent
// whose provider returns the given response. Returns the llmRunnerImpl, the
// registered agent instance, and a cleanup function.
func createGroupTurnTestHarness(t *testing.T, response *providers.LLMResponse) (*llmRunnerImpl, *AgentInstance, func()) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = &llmRunnerMockLLMProvider{response: response}

	// Register agent in the registry (same-package access to unexported map).
	al.registry.mu.Lock()
	al.registry.agents["test-agent"] = agent
	al.registry.mu.Unlock()

	lr := newLLMRunner(al)
	cleanup := func() { os.RemoveAll(tmpDir) }
	return lr, agent, cleanup
}

// TestRunGroupTurn_BasicExecution verifies that runGroupTurn resolves the agent,
// calls the LLM with the correct system/user messages, and returns content and
// token usage from the response.
func TestRunGroupTurn_BasicExecution(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "hello from group",
		ToolCalls: []providers.ToolCall{},
		Usage:     &providers.UsageInfo{TotalTokens: 42},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	ctx := context.Background()
	content, tokens, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Transcript:   "[a]: hi",
		Instruction:  "respond",
		MaxTokens:    0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if content != "hello from group" {
		t.Errorf("content = %q, want %q", content, "hello from group")
	}
	if tokens != 42 {
		t.Errorf("tokens = %d, want 42", tokens)
	}
}

// TestRunGroupTurn_InstructionOnly verifies that when Transcript is empty, the
// user message contains only the instruction (no transcript prefix).
func TestRunGroupTurn_InstructionOnly(t *testing.T) {
	var capturedMessages []providers.Message
	response := &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
	}

	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	// Replace provider with one that captures messages.
	lr.al.registry.mu.Lock()
	lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			capturedMessages = messages
			return response, nil
		},
	}
	lr.al.registry.mu.Unlock()

	ctx := context.Background()
	_, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Transcript:   "",
		Instruction:  "do something",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}
	if capturedMessages[1].Content != "do something" {
		t.Errorf("user message = %q, want %q", capturedMessages[1].Content, "do something")
	}
}

// TestRunGroupTurn_MaxTokensOverride verifies that a non-zero MaxTokens in the
// request is applied to the LLM call options without mutating the original
// AgentInstance.
func TestRunGroupTurn_MaxTokensOverride(t *testing.T) {
	var capturedMaxTokens int
	response := &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
	}

	lr, agent, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	originalMaxTokens := agent.MaxTokens

	lr.al.registry.mu.Lock()
	lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			if v, ok := opts["max_tokens"]; ok {
				if mt, ok := v.(int); ok {
					capturedMaxTokens = mt
				}
			}
			return response, nil
		},
	}
	lr.al.registry.mu.Unlock()

	ctx := context.Background()
	_, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "respond",
		MaxTokens:    999,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedMaxTokens != 999 {
		t.Errorf("LLM received max_tokens=%d, want 999", capturedMaxTokens)
	}
	// Original agent must not be mutated.
	if agent.MaxTokens != originalMaxTokens {
		t.Errorf("original agent MaxTokens changed from %d to %d", originalMaxTokens, agent.MaxTokens)
	}
}

// TestRunGroupTurn_SemaphoreIsolation verifies that the semaphore key
// "group:<groupID>:<speaker>" isolates group turns from each other: a busy
// semaphore for a different key does NOT block a runGroupTurn with a different
// speaker.
func TestRunGroupTurn_SemaphoreIsolation(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "proceeded",
		ToolCalls: []providers.ToolCall{},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	// Occupy the semaphore for a DIFFERENT session key.
	otherKey := "group:g1:OTHER"
	sem, _ := lr.al.sessionProcessing.LoadOrStore(otherKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	semCh <- struct{}{} // fill the semaphore — otherKey is now "busy"
	defer func() { <-semCh }()

	// runGroupTurn with Speaker="test-agent" uses key "group:g1:test-agent",
	// which is different from "group:g1:OTHER" and must NOT block.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	content, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "go",
	})
	if err != nil {
		t.Fatalf("runGroupTurn blocked or failed (semaphore isolation broken?): %v", err)
	}
	if content != "proceeded" {
		t.Errorf("content = %q, want %q", content, "proceeded")
	}
}

// TestRunGroupTurn_AgentNotFound verifies that requesting a speaker that does
// not exist in the registry returns a descriptive error.
func TestRunGroupTurn_AgentNotFound(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "irrelevant",
		ToolCalls: []providers.ToolCall{},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	_, _, err := lr.runGroupTurn(context.Background(), group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "nope",
		SystemPrompt: "sys",
		Instruction:  "go",
	})
	if err == nil {
		t.Fatal("expected error for non-existent agent, got nil")
	}
}

// TestRunGroupTurn_ContextCancelled verifies that runGroupTurn respects context
// cancellation when the semaphore cannot be acquired (i.e. the same session is
// already busy).
func TestRunGroupTurn_ContextCancelled(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "should not reach",
		ToolCalls: []providers.ToolCall{},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	// Occupy the semaphore for the SAME session key that runGroupTurn will use.
	sessionKey := "group:g1:test-agent"
	sem, _ := lr.al.sessionProcessing.LoadOrStore(sessionKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	semCh <- struct{}{} // block the semaphore
	defer func() { <-semCh }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Instruction:  "go",
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestRunGroupTurn_TranscriptIncluded verifies that when Transcript is non-empty,
// the user message contains the transcript prefix before the instruction.
func TestRunGroupTurn_TranscriptIncluded(t *testing.T) {
	var capturedMessages []providers.Message
	response := &providers.LLMResponse{
		Content:   "ok",
		ToolCalls: []providers.ToolCall{},
	}

	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	lr.al.registry.mu.Lock()
	lr.al.registry.agents["test-agent"].Provider = &llmRunnerMockLLMProvider{
		onChatCalled: func(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			capturedMessages = messages
			return response, nil
		},
	}
	lr.al.registry.mu.Unlock()

	ctx := context.Background()
	_, _, err := lr.runGroupTurn(ctx, group.TurnRequest{
		GroupID:      "g1",
		Speaker:      "test-agent",
		SystemPrompt: "sys",
		Transcript:   "[alice]: hello\n[bob]: world",
		Instruction:  "summarize",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(capturedMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(capturedMessages))
	}
	userMsg := capturedMessages[1].Content
	if userMsg == "summarize" {
		t.Error("user message should include transcript, but contains only instruction")
	}
	if !containsString(userMsg, "[alice]: hello") {
		t.Errorf("user message should contain transcript, got %q", userMsg)
	}
	if !containsString(userMsg, "summarize") {
		t.Errorf("user message should contain instruction, got %q", userMsg)
	}
}

func containsString(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstr(s, sub))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
