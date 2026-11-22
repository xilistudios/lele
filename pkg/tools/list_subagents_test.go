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
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

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
	<-resultCh

	tool := NewListSubagentsTool(manager)

	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents") {
		t.Errorf("Expected 'No active subagents', got: %s", result.ForLLM)
	}

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
	<-resultCh

	tool := NewListSubagentsTool(manager)
	result := tool.Execute(context.Background(), map[string]interface{}{})

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

	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") || !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected both tasks without session context, got: %s", result.ForLLM)
	}

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

	ctxB := WithAgentToolContext(context.Background(), "test-agent", "native:session-B")
	result = tool.Execute(ctxB, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Expected task B with session B context, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Task A from another session should be hidden, got: %s", result.ForLLM)
	}

	ctxC := WithAgentToolContext(context.Background(), "test-agent", "native:session-C")
	result = tool.Execute(ctxC, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents in this session") {
		t.Errorf("Expected session-scoped empty message, got: %s", result.ForLLM)
	}

	ctxChild := WithAgentToolContext(context.Background(), "test-agent", "native:session-A:subagent-1")
	result = tool.Execute(ctxChild, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected child session to see origin session tasks, got: %s", result.ForLLM)
	}

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

	ctx := WithToolContext(context.Background(), "native", "native:session-A:subagent-1")
	result := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected chatID fallback to scope to session A, got: %s", result.ForLLM)
	}

	<-resultCh
}

const (
	reproChatUUID = "da9ad89c-08fd-4db8-b30f-fb8687eb5230"
	otherChatUUID = "11111111-2222-3333-4444-555555555555"
)

func TestBuildOriginSessionKey(t *testing.T) {
	cases := []struct {
		channel, chatID string
		want            string
	}{
		{"native", "abc", "native:abc"},
		{"native", "native:abc", "native:abc"},
		{"", "abc", "abc"},
		{"native", "", "native"},
		{"", "", ""},
		{"telegram", "123", "telegram:123"},
	}
	for _, tc := range cases {
		if got := BuildOriginSessionKey(tc.channel, tc.chatID); got != tc.want {
			t.Errorf("BuildOriginSessionKey(%q, %q) = %q, want %q", tc.channel, tc.chatID, got, tc.want)
		}
	}
}

func TestListSubagentsTool_MatchesUnprefixedRuntimeSessionKey(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task 1", "one", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	t.Run("tool context only", func(t *testing.T) {
		ctx := WithToolContext(context.Background(), "native", reproChatUUID)
		result := tool.Execute(ctx, map[string]interface{}{})
		if result.IsError {
			t.Fatalf("Expected success, got error: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "subagent-1") {
			t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "Total: 1") {
			t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
		}
	})

	t.Run("runtime parity with unprefixed session key", func(t *testing.T) {
		ctx := WithAgentToolContext(
			WithToolContext(context.Background(), "native", reproChatUUID),
			"test-agent", reproChatUUID)
		result := tool.Execute(ctx, map[string]interface{}{})
		if result.IsError {
			t.Fatalf("Expected success, got error: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "subagent-1") {
			t.Errorf("Expected 'subagent-1' in list, got: %s", result.ForLLM)
		}
		if !strings.Contains(result.ForLLM, "Total: 1") {
			t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
		}
	})

	<-resultCh
}

func TestListSubagentsTool_AgentsIsolationPreserved(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Task B", "beta", "", "native", otherChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn B failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)
	ctxA := WithToolContext(context.Background(), "native", reproChatUUID)
	result := tool.Execute(ctxA, map[string]interface{}{})

	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected caller's own task 'subagent-1', got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Task from another chat must stay hidden, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1") {
		t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
	}

	<-resultCh
	<-resultCh
}

func TestListSubagentsTool_SubagentToolLoopSeesParentTasks(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Parent task", "parent", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn parent failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Unrelated task", "other-chat", "", "native", otherChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn unrelated failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	childCtx := WithToolContext(context.Background(), "native", reproChatUUID)
	result := tool.Execute(childCtx, map[string]interface{}{})

	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected child to see parent's 'subagent-1', got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "subagent-2") {
		t.Errorf("Unrelated chat's task must stay hidden from the child, got: %s", result.ForLLM)
	}

	<-resultCh
	<-resultCh
}

func TestListSubagentsTool_RoutedSessionKeyWithChannelChatID(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task 1", "one", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)
	ctx := WithAgentToolContext(
		WithToolContext(context.Background(), "native", reproChatUUID),
		"software-engineer", "agent:software-engineer:main")
	result := tool.Execute(ctx, map[string]interface{}{})

	if !strings.Contains(result.ForLLM, "subagent-1") {
		t.Errorf("Expected 'subagent-1' despite routed session key, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Total: 1") {
		t.Errorf("Expected 'Total: 1 task(s)', got: %s", result.ForLLM)
	}

	<-resultCh
}

func TestListSubagentsTool_ScopingHidesCount(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)

	_, err := manager.Spawn(context.Background(), "Task X", "xray", "", "native", reproChatUUID,
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)
	ctx := WithToolContext(context.Background(), "native", otherChatUUID)

	result := tool.Execute(ctx, map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "No active subagents in this session") {
		t.Errorf("Expected session-scoped empty message, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "1 task(s) from other sessions were hidden") {
		t.Errorf("Expected hidden-task count in message, got: %s", result.ForLLM)
	}

	result = tool.Execute(ctx, map[string]interface{}{"include_completed": true})
	if !strings.Contains(result.ForLLM, "1 task(s) from other sessions were hidden") {
		t.Errorf("Expected hidden-task count with include_completed, got: %s", result.ForLLM)
	}

	<-resultCh
}

func TestListSubagentsTool_SessionKeyOnlyFallback(t *testing.T) {
	provider := &delayedSubagentProvider{delay: 500 * time.Millisecond}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 2)

	_, err := manager.Spawn(context.Background(), "Task A", "alpha", "", "native", "uuid-A",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn A failed: %v", err)
	}
	_, err = manager.Spawn(context.Background(), "Task B", "beta", "", "native", "uuid-B",
		func(ctx context.Context, result *ToolResult) { resultCh <- result })
	if err != nil {
		t.Fatalf("Spawn B failed: %v", err)
	}

	tool := NewListSubagentsTool(manager)

	result := tool.Execute(context.Background(), map[string]interface{}{})
	if !strings.Contains(result.ForLLM, "subagent-1") || !strings.Contains(result.ForLLM, "subagent-2") {
		t.Fatalf("Expected both tasks with no context, got: %s", result.ForLLM)
	}

	cases := []struct {
		name       string
		sessionKey string
	}{
		{"raw session key", "native:uuid-A"},
		{"conversation alias", "native:uuid-A:chat:2"},
		{"child subagent session", "native:uuid-A:subagent-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithAgentToolContext(context.Background(), "test-agent", tc.sessionKey)
			result := tool.Execute(ctx, map[string]interface{}{})

			if result.IsError {
				t.Fatalf("Expected success, got error: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "subagent-1") {
				t.Errorf("Expected own task 'subagent-1' for key %q, got: %s", tc.sessionKey, result.ForLLM)
			}
			if strings.Contains(result.ForLLM, "subagent-2") {
				t.Errorf("Task of another session must stay hidden for key %q, got: %s", tc.sessionKey, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "Total: 1") {
				t.Errorf("Expected 'Total: 1 task(s)' for key %q, got: %s", tc.sessionKey, result.ForLLM)
			}
		})
	}

	<-resultCh
	<-resultCh
}
