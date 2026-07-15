package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestWaitForSubagentTool_Name(t *testing.T) {
	tool := NewWaitForSubagentTool(nil)
	if tool.Name() != "wait_for_subagent" {
		t.Errorf("Expected name 'wait_for_subagent', got '%s'", tool.Name())
	}
}

func TestWaitForSubagentTool_Description(t *testing.T) {
	tool := NewWaitForSubagentTool(nil)
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if !strings.Contains(desc, "subagent") {
		t.Errorf("Description should mention 'subagent', got: %s", desc)
	}
}

func TestWaitForSubagentTool_Parameters(t *testing.T) {
	tool := NewWaitForSubagentTool(nil)
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters should not be nil")
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Properties should be a map")
	}
	if _, ok := props["task_id"]; !ok {
		t.Error("task_id parameter should exist")
	}
	if _, ok := props["timeout_seconds"]; !ok {
		t.Error("timeout_seconds parameter should exist")
	}
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("Required should be a string array")
	}
	if len(required) != 1 || required[0] != "task_id" {
		t.Errorf("Required should be ['task_id'], got %v", required)
	}
}

func TestWaitForSubagentTool_MissingTaskID(t *testing.T) {
	tool := NewWaitForSubagentTool(nil)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	if !result.IsError {
		t.Error("Expected error for missing task_id")
	}
}

func TestWaitForSubagentTool_NilManager(t *testing.T) {
	tool := NewWaitForSubagentTool(nil)
	args := map[string]interface{}{"task_id": "subagent-1"}
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Error("Expected error for nil manager")
	}
	if !strings.Contains(result.ForLLM, "not configured") {
		t.Errorf("Expected 'not configured' error, got: %s", result.ForLLM)
	}
}

func TestWaitForSubagentTool_TaskNotFound(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	tool := NewWaitForSubagentTool(manager)

	args := map[string]interface{}{"task_id": "nonexistent"}
	result := tool.Execute(context.Background(), args)

	if !result.IsError {
		t.Error("Expected error for non-existent task")
	}
	if !strings.Contains(result.ForLLM, "not found") {
		t.Errorf("Expected 'not found' error, got: %s", result.ForLLM)
	}
}

func TestWaitForSubagentTool_AlreadyCompleted(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: completed\nSUMMARY: Done\nDETAILS:\nTask completed successfully.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Quick task", "quick", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for subagent to complete.
	<-resultCh

	// Now wait_for_subagent should return immediately.
	tool := NewWaitForSubagentTool(manager)
	args := map[string]interface{}{"task_id": "subagent-1"}
	waitResult := tool.Execute(context.Background(), args)

	if waitResult.IsError {
		t.Errorf("Expected success, got error: %s", waitResult.ForLLM)
	}
	if !strings.Contains(waitResult.ForLLM, "completed") {
		t.Errorf("Expected 'completed' in result, got: %s", waitResult.ForLLM)
	}
	if !strings.Contains(waitResult.ForLLM, "Task completed successfully") {
		t.Errorf("Expected task details in result, got: %s", waitResult.ForLLM)
	}
}

func TestWaitForSubagentTool_WaitForCompletion(t *testing.T) {
	// Use a provider that takes a moment to respond.
	provider := &delayedSubagentProvider{delay: 200 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Slow task", "slow", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	// Wait for subagent in a separate tool call.
	tool := NewWaitForSubagentTool(manager)
	args := map[string]interface{}{
		"task_id":         "subagent-1",
		"timeout_seconds": float64(10),
	}
	waitResult := tool.Execute(context.Background(), args)

	if waitResult.IsError {
		t.Errorf("Expected success, got error: %s", waitResult.ForLLM)
	}
	if !strings.Contains(waitResult.ForLLM, "completed") {
		t.Errorf("Expected 'completed' status, got: %s", waitResult.ForLLM)
	}

	// Also drain the callback channel.
	<-resultCh
}

func TestWaitForSubagentTool_Timeout(t *testing.T) {
	// Provider that never completes (long delay).
	provider := &delayedSubagentProvider{delay: 10 * time.Second}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	_ = manager // suppress unused warning
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Very slow task", "very-slow", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewWaitForSubagentTool(manager)
	args := map[string]interface{}{
		"task_id":         "subagent-1",
		"timeout_seconds": float64(1), // 1 second timeout
	}
	start := time.Now()
	waitResult := tool.Execute(context.Background(), args)
	elapsed := time.Since(start)

	if !waitResult.IsError {
		t.Error("Expected timeout error")
	}
	if !strings.Contains(waitResult.ForLLM, "Timed out") {
		t.Errorf("Expected 'Timed out' message, got: %s", waitResult.ForLLM)
	}
	// Should timeout around 1 second, not 10.
	if elapsed > 3*time.Second {
		t.Errorf("Expected timeout around 1s, took %v", elapsed)
	}

	// Clean up: stop the subagent.
	manager.StopTask("subagent-1")
}

func TestWaitForSubagentTool_NeedsContext(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: needs_context\nSUMMARY: Missing info\nCONTEXT_NEEDED: What file?\nDETAILS:\nI need a file path.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task needing context", "ctx-task", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	<-resultCh

	tool := NewWaitForSubagentTool(manager)
	args := map[string]interface{}{"task_id": "subagent-1"}
	waitResult := tool.Execute(context.Background(), args)

	if waitResult.IsError {
		t.Errorf("Expected success (needs_context is terminal), got error: %s", waitResult.ForLLM)
	}
	if !strings.Contains(waitResult.ForLLM, "needs_context") {
		t.Errorf("Expected 'needs_context' status, got: %s", waitResult.ForLLM)
	}
	if !strings.Contains(waitResult.ForLLM, "What file?") {
		t.Errorf("Expected context request in result, got: %s", waitResult.ForLLM)
	}
}

// delayedSubagentProvider is a test provider that sleeps before responding.
type delayedSubagentProvider struct {
	delay time.Duration
	once  bool
}

func (p *delayedSubagentProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	time.Sleep(p.delay)
	return &providers.LLMResponse{
		Content: "STATUS: completed\nSUMMARY: Done after delay\nDETAILS:\nTask completed.",
	}, nil
}

func (p *delayedSubagentProvider) GetDefaultModel() string {
	return "test-model"
}

func (p *delayedSubagentProvider) SupportsTools() bool {
	return false
}

func (p *delayedSubagentProvider) GetContextWindow() int {
	return 4096
}
