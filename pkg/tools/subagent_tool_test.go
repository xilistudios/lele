package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// MockLLMProvider is a test implementation of LLMProvider
type MockLLMProvider struct {
	lastOptions map[string]interface{}
}

type scriptedSubagentProvider struct {
	mu        sync.Mutex
	responses []string
	calls     int
}

func (m *scriptedSubagentProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted"
	if len(m.responses) > 0 {
		if m.calls < len(m.responses) {
			response = m.responses[m.calls]
		} else {
			response = m.responses[len(m.responses)-1]
		}
	}
	m.calls++

	return &providers.LLMResponse{Content: response}, nil
}

func (m *scriptedSubagentProvider) GetDefaultModel() string {
	return "test-model"
}

func (m *scriptedSubagentProvider) SupportsTools() bool {
	return false
}

func (m *scriptedSubagentProvider) GetContextWindow() int {
	return 4096
}

func waitForSubagentCallback(t *testing.T, ch <-chan *ToolResult) *ToolResult {
	t.Helper()

	select {
	case result := <-ch:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for subagent callback")
		return nil
	}
}

func (m *MockLLMProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	m.lastOptions = options
	// Find the last user message to generate a response
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return &providers.LLMResponse{
				Content: "Task completed: " + messages[i].Content,
			}, nil
		}
	}
	return &providers.LLMResponse{Content: "No task provided"}, nil
}

func (m *MockLLMProvider) GetDefaultModel() string {
	return "test-model"
}

func (m *MockLLMProvider) SupportsTools() bool {
	return false
}

func (m *MockLLMProvider) GetContextWindow() int {
	return 4096
}

func TestSubagentManager_SetLLMOptions_AppliesToRunToolLoop(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	manager.SetLLMOptions(2048, 0.6)
	tool := NewSubagentTool(manager)
	tool.SetContext("cli", "direct")

	ctx := context.Background()
	args := map[string]interface{}{"task": "Do something"}
	result := tool.Execute(ctx, args)

	if result == nil || result.IsError {
		t.Fatalf("Expected successful result, got: %+v", result)
	}

	if provider.lastOptions == nil {
		t.Fatal("Expected LLM options to be passed, got nil")
	}
	if provider.lastOptions["max_tokens"] != 2048 {
		t.Fatalf("max_tokens = %v, want %d", provider.lastOptions["max_tokens"], 2048)
	}
	if provider.lastOptions["temperature"] != 0.6 {
		t.Fatalf("temperature = %v, want %v", provider.lastOptions["temperature"], 0.6)
	}
}

// TestSubagentTool_Name verifies tool name
func TestSubagentTool_Name(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	tool := NewSubagentTool(manager)

	if tool.Name() != "subagent" {
		t.Errorf("Expected name 'subagent', got '%s'", tool.Name())
	}
}

// TestSubagentTool_Description verifies tool description
func TestSubagentTool_Description(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	tool := NewSubagentTool(manager)

	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if !strings.Contains(desc, "subagent") {
		t.Errorf("Description should mention 'subagent', got: %s", desc)
	}
}

// TestSubagentTool_Parameters verifies tool parameters schema
func TestSubagentTool_Parameters(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	tool := NewSubagentTool(manager)

	params := tool.Parameters()
	if params == nil {
		t.Error("Parameters should not be nil")
	}

	// Check type
	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got: %v", params["type"])
	}

	// Check properties
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Properties should be a map")
	}

	// Verify task parameter
	task, ok := props["task"].(map[string]interface{})
	if !ok {
		t.Fatal("Task parameter should exist")
	}
	if task["type"] != "string" {
		t.Errorf("Task type should be 'string', got: %v", task["type"])
	}

	// Verify label parameter
	label, ok := props["label"].(map[string]interface{})
	if !ok {
		t.Fatal("Label parameter should exist")
	}
	if label["type"] != "string" {
		t.Errorf("Label type should be 'string', got: %v", label["type"])
	}

	// Check required fields
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("Required should be a string array")
	}
	if len(required) != 1 || required[0] != "task" {
		t.Errorf("Required should be ['task'], got: %v", required)
	}
}

// TestSubagentTool_SetContext verifies context setting
func TestSubagentTool_SetContext(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	tool := NewSubagentTool(manager)

	tool.SetContext("test-channel", "test-chat")

	// Verify context is set (we can't directly access private fields,
	// but we can verify it doesn't crash)
	// The actual context usage is tested in Execute tests
}

// TestSubagentTool_Execute_Success tests successful execution
func TestSubagentTool_Execute_Success(t *testing.T) {
	provider := &MockLLMProvider{}
	msgBus := bus.NewMessageBus()
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", msgBus)
	tool := NewSubagentTool(manager)
	tool.SetContext("telegram", "chat-123")

	ctx := context.Background()
	args := map[string]interface{}{
		"task":  "Write a haiku about coding",
		"label": "haiku-task",
	}

	result := tool.Execute(ctx, args)

	// Verify basic ToolResult structure
	if result == nil {
		t.Fatal("Result should not be nil")
	}

	// Verify no error
	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}

	// Verify not async
	if result.Async {
		t.Error("SubagentTool should be synchronous, not async")
	}

	// Verify not silent
	if result.Silent {
		t.Error("SubagentTool should not be silent")
	}

	// Verify ForUser contains brief summary (not empty)
	if result.ForUser == "" {
		t.Error("ForUser should contain result summary")
	}
	if !strings.Contains(result.ForUser, "Task completed") {
		t.Errorf("ForUser should contain task completion, got: %s", result.ForUser)
	}

	// Verify ForLLM contains full details
	if result.ForLLM == "" {
		t.Error("ForLLM should contain full details")
	}
	if !strings.Contains(result.ForLLM, "haiku-task") {
		t.Errorf("ForLLM should contain label 'haiku-task', got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Task completed:") {
		t.Errorf("ForLLM should contain task result, got: %s", result.ForLLM)
	}
}

// TestSubagentTool_Execute_NoLabel tests execution without label
func TestSubagentTool_Execute_NoLabel(t *testing.T) {
	provider := &MockLLMProvider{}
	msgBus := bus.NewMessageBus()
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", msgBus)
	tool := NewSubagentTool(manager)

	ctx := context.Background()
	args := map[string]interface{}{
		"task": "Test task without label",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success without label, got error: %s", result.ForLLM)
	}

	// ForLLM should show (unnamed) for missing label
	if !strings.Contains(result.ForLLM, "(unnamed)") {
		t.Errorf("ForLLM should show '(unnamed)' for missing label, got: %s", result.ForLLM)
	}
}

// TestSubagentTool_Execute_MissingTask tests error handling for missing task
func TestSubagentTool_Execute_MissingTask(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	tool := NewSubagentTool(manager)

	ctx := context.Background()
	args := map[string]interface{}{
		"label": "test",
	}

	result := tool.Execute(ctx, args)

	// Should return error
	if !result.IsError {
		t.Error("Expected error for missing task parameter")
	}

	// ForLLM should contain error message
	if !strings.Contains(result.ForLLM, "task is required") {
		t.Errorf("Error message should mention 'task is required', got: %s", result.ForLLM)
	}

	// Err should be set
	if result.Err == nil {
		t.Error("Err should be set for validation failure")
	}
}

// TestSubagentTool_Execute_NilManager tests error handling for nil manager
func TestSubagentTool_Execute_NilManager(t *testing.T) {
	tool := NewSubagentTool(nil)

	ctx := context.Background()
	args := map[string]interface{}{
		"task": "test task",
	}

	result := tool.Execute(ctx, args)

	// Should return error
	if !result.IsError {
		t.Error("Expected error for nil manager")
	}

	if !strings.Contains(result.ForLLM, "Subagent manager not configured") {
		t.Errorf("Error message should mention manager not configured, got: %s", result.ForLLM)
	}
}

// TestSubagentTool_Execute_ContextPassing verifies context is properly used
func TestSubagentTool_Execute_ContextPassing(t *testing.T) {
	provider := &MockLLMProvider{}
	msgBus := bus.NewMessageBus()
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", msgBus)
	tool := NewSubagentTool(manager)

	// Set context
	channel := "test-channel"
	chatID := "test-chat"
	tool.SetContext(channel, chatID)

	ctx := context.Background()
	args := map[string]interface{}{
		"task": "Test context passing",
	}

	result := tool.Execute(ctx, args)

	// Should succeed
	if result.IsError {
		t.Errorf("Expected success with context, got error: %s", result.ForLLM)
	}

	// The context is used internally; we can't directly test it
	// but execution success indicates context was handled properly
}

// TestSubagentTool_ForUserTruncation verifies long content is truncated for user
func TestSubagentTool_ForUserTruncation(t *testing.T) {
	// Create a mock provider that returns very long content
	provider := &MockLLMProvider{}
	msgBus := bus.NewMessageBus()
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", msgBus)
	tool := NewSubagentTool(manager)

	ctx := context.Background()

	// Create a task that will generate long response
	longTask := strings.Repeat("This is a very long task description. ", 100)
	args := map[string]interface{}{
		"task":  longTask,
		"label": "long-test",
	}

	result := tool.Execute(ctx, args)

	// ForUser should be truncated to 500 chars + "..."
	maxUserLen := 500
	if len(result.ForUser) > maxUserLen+3 { // +3 for "..."
		t.Errorf("ForUser should be truncated to ~%d chars, got: %d", maxUserLen, len(result.ForUser))
	}

	// ForLLM should have full content
	if !strings.Contains(result.ForLLM, longTask[:50]) {
		t.Error("ForLLM should contain reference to original task")
	}
}

func TestSubagentManager_ContinueTask(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: needs_context\nSUMMARY: Missing repository target\nCONTEXT_NEEDED: Which repository should I inspect?\nDETAILS:\nI need the repository path before I can continue.",
		"STATUS: completed\nSUMMARY: Repository inspected\nDETAILS:\nI used the supplied repository path and completed the task.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	resultCh := make(chan *ToolResult, 2)

	spawned, err := manager.Spawn(context.Background(), "Inspect the repository", "repo-inspect", "", "telegram", "chat-123", func(ctx context.Context, result *ToolResult) {
		resultCh <- result
	})
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}
	if !strings.Contains(spawned, "subagent-1") {
		t.Fatalf("Spawn response should include task ID, got: %s", spawned)
	}

	first := waitForSubagentCallback(t, resultCh)
	if first == nil {
		t.Fatal("Expected first callback result")
	}
	if !strings.Contains(first.ForLLM, "Status: needs_context") {
		t.Fatalf("Expected needs_context status, got: %s", first.ForLLM)
	}

	task, ok := manager.GetTask("subagent-1")
	if !ok {
		t.Fatal("Expected subagent task to exist")
	}
	if task.Status != SubagentStatusNeedsContext {
		t.Fatalf("Expected task status %q, got %q", SubagentStatusNeedsContext, task.Status)
	}
	if task.ContextRequest != "Which repository should I inspect?" {
		t.Fatalf("Unexpected context request: %s", task.ContextRequest)
	}

	continued, err := manager.ContinueTask(context.Background(), task.ID, "Use repository /tmp/picoclaw", func(ctx context.Context, result *ToolResult) {
		resultCh <- result
	})
	if err != nil {
		t.Fatalf("ContinueTask returned error: %v", err)
	}
	if !strings.Contains(continued, task.ID) {
		t.Fatalf("ContinueTask response should include task ID, got: %s", continued)
	}

	second := waitForSubagentCallback(t, resultCh)
	if second == nil {
		t.Fatal("Expected second callback result")
	}
	if !strings.Contains(second.ForLLM, "Status: completed") {
		t.Fatalf("Expected completed status, got: %s", second.ForLLM)
	}

	task, ok = manager.GetTask(task.ID)
	if !ok {
		t.Fatal("Expected subagent task after continuation")
	}
	if task.Status != SubagentStatusCompleted {
		t.Fatalf("Expected task status %q, got %q", SubagentStatusCompleted, task.Status)
	}
	if len(task.Guidance) != 1 || task.Guidance[0] != "Use repository /tmp/picoclaw" {
		t.Fatalf("Expected stored guidance, got: %#v", task.Guidance)
	}
}

func TestSubagentManager_StopPausedTask(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: needs_context\nSUMMARY: Missing target\nCONTEXT_NEEDED: Which file should I open?\nDETAILS:\nI need the target file.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Open the file", "file-task", "", "telegram", "chat-123", func(ctx context.Context, result *ToolResult) {
		resultCh <- result
	})
	if err != nil {
		t.Fatalf("Spawn returned error: %v", err)
	}
	waitForSubagentCallback(t, resultCh)

	if !manager.StopTask("subagent-1") {
		t.Fatal("Expected StopTask to succeed for paused task")
	}

	task, ok := manager.GetTask("subagent-1")
	if !ok {
		t.Fatal("Expected paused task to remain addressable")
	}
	if task.Status != SubagentStatusCancelled {
		t.Fatalf("Expected paused task to become %q, got %q", SubagentStatusCancelled, task.Status)
	}
}
