package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestListSubagentsTool_Name(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	if tool.Name() != "list_active_subagents" {
		t.Errorf("Expected name 'list_active_subagents', got '%s'", tool.Name())
	}
}

func TestListSubagentsTool_Description(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
	if !strings.Contains(desc, "subagent") {
		t.Errorf("Description should mention 'subagent', got: %s", desc)
	}
}

func TestListSubagentsTool_Parameters(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters should not be nil")
	}
	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got %v", params["type"])
	}
}

func TestListSubagentsTool_NilManager(t *testing.T) {
	tool := NewListSubagentsTool(nil)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	if !result.IsError {
		t.Error("Expected error for nil manager")
	}
}

func TestListSubagentsTool_NoTasks(t *testing.T) {
	provider := &MockLLMProvider{}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	tool := NewListSubagentsTool(manager)

	result := tool.Execute(context.Background(), map[string]interface{}{})

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "No subagent tasks") {
		t.Errorf("Expected 'No subagent tasks', got: %s", result.ForLLM)
	}
}

func TestListSubagentsTool_ActiveTasks(t *testing.T) {
	// Use a delayed provider so tasks stay running long enough to list them.
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	// Spawn two tasks.
	_, err := manager.Spawn(context.Background(), "Task 1", "first", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn 1 failed: %v", err)
	}

	_, err = manager.Spawn(context.Background(), "Task 2", "second", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn 2 failed: %v", err)
	}

	// List active subagents while they're running.
	tool := NewListSubagentsTool(manager)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Active subagents:") {
		t.Errorf("Expected 'Active subagents:' header, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected 'subagent-2' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "first") {
		t.Errorf("Expected label 'first' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "second") {
		t.Errorf("Expected label 'second' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 2") {
		t.Errorf("Expected 'Total: 2 task(s)', got: %s", result.ForLLM)
	}

	// Wait for tasks to complete.
	<-resultCh
	<-resultCh
}

func TestListSubagentsTool_IncludeCompleted(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Quick task", "quick", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	<-resultCh // Wait for completion.

	tool := NewListSubagentsTool(manager)

	// Without include_completed: should show "no active subagents".
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents") {
		t.Errorf("Expected 'No active subagents', got: %s", result.ForLLM)
	}

	// With include_completed: should show the completed task.
	result = tool.Execute(context.Background(), map[string]interface{}{
		"include_completed": true,
	})
	if !strings.Contains(result.ForLLM, "All subagent tasks:") {
		t.Errorf("Expected 'All subagent tasks:' header, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "completed") {
		t.Errorf("Expected 'completed' status in list, got: %s", result.ForLLM)
	}
}

func TestListSubagentsTool_NeedsContextShown(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: needs_context\nSUMMARY: Missing info\nCONTEXT_NEEDED: Need a path\nDETAILS:\nWaiting for path.",
	}}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Context task", "ctx", "", "telegram", "chat-1",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	<-resultCh // Wait for needs_context.

	tool := NewListSubagentsTool(manager)
	result := tool.Execute(context.Background(), map[string]interface{}{})

	// needs_context tasks should show in the active list.
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' in active list, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "needs_context") {
		t.Errorf("Expected 'needs_context' status, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Need a path") {
		t.Errorf("Expected context request in list, got: %s", result.ForLLM)
	}
}

func TestListSubagentsTool_SessionScoping(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 3)

	// Spawn tasks from two different sessions.
	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", "session-A",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Task B", "beta", "", "native", "session-B",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn B failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	// Without session context: all tasks visible (backwards compatibility).
	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") || !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected both tasks without session context, got: %s", result.ForLLM)
	}

	// With session A context: only task A visible.
	ctxA := WithAgentToolContext(context.Background(), "test-agent", "native:session-A")
	result = tool.Execute(ctxA, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected task A with session A context, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Task B from another session should be hidden, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1") {
		t.Errorf("Expected 'Total: 1 task(s)' with session scoping, got: %s", result.ForLLM)
	}

	// With session B context: only task B visible.
	ctxB := WithAgentToolContext(context.Background(), "test-agent", "native:session-B")
	result = tool.Execute(ctxB, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected task B with session B context, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Task A from another session should be hidden, got: %s", result.ForLLM)
	}

	// From an unrelated session: no tasks.
	ctxC := WithAgentToolContext(context.Background(), "test-agent", "native:session-C")
	result = tool.Execute(ctxC, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents in this session") {
		t.Errorf("Expected session-scoped empty message, got: %s", result.ForLLM)
	}

	// A subagent child session (native:session-A:subagent-1) should still see
	// tasks of its origin session.
	ctxChild := WithAgentToolContext(context.Background(), "test-agent", "native:session-A:subagent-1")
	result = tool.Execute(ctxChild, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected child session to see origin session tasks, got: %s", result.ForLLM)
	}

	// Wait for tasks to complete.
	<-resultCh
	<-resultCh
}

func TestListSubagentsTool_SessionScopingViaChatIDFallback(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", "session-A",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	// Subagent toolloop passes channel/chatID but no agent context; chatID
	// carries the full subagent session key.
	ctx := WithToolContext(context.Background(), "native", "native:session-A:subagent-1")
	result := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected chatID fallback to scope to session A, got: %s", result.ForLLM)
	}

	<-resultCh
}

func TestSameSessionKey(t *testing.T) {
	cases := []struct {
		origin, session string
		want            bool
	}{
		{"native:abc", "native:abc", true},
		{"native:abc", "native:abc:subagent-1", true},  // child subagent session
		{"native:abc", "native:abc:cron-1:subagent-2", true},
		{"native:abc", "native:abd", false},
		{"native:abc", "", false},
		{"", "native:abc", false},
		{"native:abc:subagent-1", "native:abc", false}, // origin is more specific
		{"telegram:123", "telegram:123", true},
	}
	for _, tc := range cases {
		if got := sameSessionKey(tc.origin, tc.session); got != tc.want {
			t.Errorf("sameSessionKey(%q, %q) = %v, want %v", tc.origin, tc.session, got, tc.want)
		}
	}
}
