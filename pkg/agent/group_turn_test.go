package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

func createGroupTurnTestHarness(t *testing.T, response *providers.LLMResponse) (*llmRunnerImpl, *AgentInstance, func()) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = &llmRunnerMockLLMProvider{response: response}

	al.registry.mu.Lock()
	al.registry.agents["test-agent"] = agent
	al.registry.mu.Unlock()

	lr := newLLMRunner(al)
	cleanup := func() { os.RemoveAll(tmpDir) }
	return lr, agent, cleanup
}

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

func TestRunGroupTurn_InstructionOnly(t *testing.T) {
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
	if agent.MaxTokens != originalMaxTokens {
		t.Errorf("original agent MaxTokens changed from %d to %d", originalMaxTokens, agent.MaxTokens)
	}
}

func TestRunGroupTurn_SemaphoreIsolation(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "proceeded",
		ToolCalls: []providers.ToolCall{},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	otherKey := "group:g1:OTHER"
	sem, _ := lr.al.sessionProcessing.LoadOrStore(otherKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	semCh <- struct{}{}
	defer func() { <-semCh }()

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

func TestRunGroupTurn_ContextCancelled(t *testing.T) {
	response := &providers.LLMResponse{
		Content:   "should not reach",
		ToolCalls: []providers.ToolCall{},
	}
	lr, _, cleanup := createGroupTurnTestHarness(t, response)
	defer cleanup()

	sessionKey := "group:g1:test-agent"
	sem, _ := lr.al.sessionProcessing.LoadOrStore(sessionKey, make(chan struct{}, 1))
	semCh := sem.(chan struct{})
	semCh <- struct{}{}
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
