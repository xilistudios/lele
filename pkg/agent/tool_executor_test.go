// Lele - Ultra-lightweight personal AI agent
// Tests for chat-mode tool guard in toolExecutor.Execute
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// TestChatModeToolGuard_BlockedTools verifies that in chat mode, tools
// other than web_search and web_fetch are rejected with an error result.
func TestChatModeToolGuard_BlockedTools(t *testing.T) {
	blockedToolNames := []string{"exec", "read_file", "write_file", "spawn", "list_dir", "edit_file"}

	for _, toolName := range blockedToolNames {
		t.Run(toolName, func(t *testing.T) {
			sm := session.NewSessionManager()
			sessionKey := "test:chat-guard:" + toolName

			// Set session to chat mode
			sm.GetOrCreate(sessionKey)
			if err := sm.SetMode(sessionKey, "chat"); err != nil {
				t.Fatalf("SetMode failed: %v", err)
			}

			// Build a minimal AgentLoop — the guard returns early before
			// touching bus or verboseManager, so nil-safe fields are fine.
			al := &AgentLoop{
				bus:            bus.NewMessageBus(),
				verboseManager: session.NewVerboseManager(),
			}
			te := newToolExecutor(al)

			agent := &AgentInstance{
				Sessions: sm,
			}

			opts := toolExecOptions{
				ctx:        context.Background(),
				agent:      agent,
				sessionKey: sessionKey,
				tc: providers.ToolCall{
					ID:   "call-test",
					Name: toolName,
				},
				channel: "test",
			}

			result, err := te.Execute(opts)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil ToolResult for blocked tool")
			}
			if !result.IsError {
				t.Errorf("expected IsError=true for blocked tool %q", toolName)
			}
			if result.ForLLM == "" {
				t.Errorf("expected error message in ForLLM for blocked tool %q", toolName)
			}
			// Verify the tool was not actually executed (error message mentions "chat mode")
			if !containsSubstring(result.ForLLM, "chat mode") {
				t.Errorf("expected error message to mention 'chat mode', got: %s", result.ForLLM)
			}
		})
	}
}

// TestChatModeToolGuard_AllowedTools verifies that web_search and web_fetch
// are NOT blocked by the chat mode guard. We test this by confirming the
// guard does NOT return an error result for these tool names.
// Full tool execution requires a registered tool, so we just verify the
// guard doesn't reject them (the code falls through to publishExecuting).
func TestChatModeToolGuard_AllowedTools(t *testing.T) {
	allowedToolNames := []string{"web_search", "web_fetch"}

	for _, toolName := range allowedToolNames {
		t.Run(toolName, func(t *testing.T) {
			sm := session.NewSessionManager()
			sessionKey := "test:chat-allow:" + toolName

			sm.GetOrCreate(sessionKey)
			if err := sm.SetMode(sessionKey, "chat"); err != nil {
				t.Fatalf("SetMode failed: %v", err)
			}

			// Create a registry with a mock tool so ExecuteWithContext doesn't nil-deref
			registry := tools.NewToolRegistry()
			mockWebTool := &mockToolForExecutor{
				name:        toolName,
				description: "Mock " + toolName,
			}
			registry.Register(mockWebTool)

			al := &AgentLoop{
				bus:            bus.NewMessageBus(),
				verboseManager: session.NewVerboseManager(),
			}
			te := newToolExecutor(al)

			agent := &AgentInstance{
				Sessions: sm,
				Tools:    registry,
			}

			opts := toolExecOptions{
				ctx:        context.Background(),
				agent:      agent,
				sessionKey: sessionKey,
				tc: providers.ToolCall{
					ID:   "call-test",
					Name: toolName,
				},
				channel:   "test",
				chatID:    "test-chat-id",
				iteration: 1,
			}

			result, err := te.Execute(opts)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil ToolResult for allowed tool")
			}
			// The guard should NOT have rejected it — IsError should be false
			// (the mock tool returns a success result).
			if result.IsError {
				t.Errorf("web tool %q should not be blocked by chat mode guard, got error: %s", toolName, result.ForLLM)
			}
		})
	}
}

// TestChatModeGuard_AgentModeNoBlock verifies that the same tools are
// NOT blocked when the session is in agent mode (default).
func TestChatModeGuard_AgentModeNoBlock(t *testing.T) {
	sm := session.NewSessionManager()
	sessionKey := "test:agent-mode"

	sm.GetOrCreate(sessionKey)
	// Explicitly set agent mode
	if err := sm.SetMode(sessionKey, "agent"); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	registry := tools.NewToolRegistry()
	mockExecTool := &mockToolForExecutor{
		name:        "exec",
		description: "Mock exec",
	}
	registry.Register(mockExecTool)

	al := &AgentLoop{
		bus:            bus.NewMessageBus(),
		verboseManager: session.NewVerboseManager(),
	}
	te := newToolExecutor(al)

	agent := &AgentInstance{
		Sessions: sm,
		Tools:    registry,
	}

	opts := toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: sessionKey,
		tc: providers.ToolCall{
			ID:   "call-test",
			Name: "exec",
		},
		channel:   "test",
		chatID:    "test-chat-id",
		iteration: 1,
	}

	result, err := te.Execute(opts)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult")
	}
	// Should NOT be blocked — exec should execute normally in agent mode
	if result.IsError && containsSubstring(result.ForLLM, "chat mode") {
		t.Error("exec should not be blocked in agent mode")
	}
}

// TestChatModeGuard_NilAgent verifies that nil agent is safely skipped
// (the guard doesn't panic and doesn't block the tool).
func TestChatModeGuard_NilAgent(t *testing.T) {
	// Create a registry with a mock exec tool so ExecuteWithContext
	// can complete without panic.
	registry := tools.NewToolRegistry()
	mockExec := &mockToolForExecutor{name: "exec", description: "Mock exec"}
	registry.Register(mockExec)

	al := &AgentLoop{
		bus:            bus.NewMessageBus(),
		verboseManager: session.NewVerboseManager(),
	}
	te := newToolExecutor(al)

	// Minimal agent — only Tools populated so ExecuteWithContext works.
	agent := &AgentInstance{
		Tools: registry,
	}

	opts := toolExecOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "test:nil-sessions",
		tc: providers.ToolCall{
			ID:   "call-test",
			Name: "exec",
		},
		channel:   "test",
		chatID:    "test-chat-id",
		iteration: 1,
	}

	// Should not panic — the guard skips when Sessions is nil
	result, err := te.Execute(opts)
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ToolResult")
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// mockToolForExecutor is a minimal tools.Tool implementation for testing.
type mockToolForExecutor struct {
	name        string
	description string
}

func (m *mockToolForExecutor) Name() string        { return m.name }
func (m *mockToolForExecutor) Description() string { return m.description }
func (m *mockToolForExecutor) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (m *mockToolForExecutor) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "mock result for " + m.name}
}
