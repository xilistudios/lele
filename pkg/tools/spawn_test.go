package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestSpawnTool_Metadata(t *testing.T) {
	tool := NewSpawnTool(nil)
	if tool.Name() != "spawn" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	props := tool.Parameters()["properties"].(map[string]interface{})
	for _, k := range []string{"task", "label", "task_id", "guidance", "agent_id", "model", "dependencies", "max_retries"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
	// AsyncTool interface compliance.
	var _ AsyncTool = tool
	// ContextualTool interface compliance.
	var _ ContextualTool = tool
}

// TestSpawnTool_SetCallback verifies the callback setter.
func TestSpawnTool_SetCallback(t *testing.T) {
	tool := NewSpawnTool(nil)
	var got []*ToolResult
	tool.SetCallback(func(ctx context.Context, res *ToolResult) {
		got = append(got, res)
	})
	if tool.callback == nil {
		t.Fatal("expected callback to be set")
	}
}

// TestSpawnTool_SetContextAndAllowlist verifies context/allowlist setters.
func TestSpawnTool_SetContextAndAllowlist(t *testing.T) {
	tool := NewSpawnTool(nil)
	tool.SetContext("telegram", "chat-1")
	if tool.originChannel != "telegram" || tool.originChatID != "chat-1" {
		t.Fatalf("origin = %s:%s", tool.originChannel, tool.originChatID)
	}
	denied := 0
	tool.SetAllowlistChecker(func(id string) bool {
		return id == "allowed"
	})
	if tool.allowlistCheck("denied") != false {
		t.Fatal("expected 'denied' to fail allowlist")
	}
	if tool.allowlistCheck("allowed") != true {
		t.Fatal("expected 'allowed' to pass allowlist")
	}
	_ = denied
}

// TestSpawnTool_Execute_NoTask verifies the task-required error when no task_id.
func TestSpawnTool_Execute_NoTask(t *testing.T) {
	tool := NewSpawnTool(nil)
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing task")
	}
	if !strings.Contains(res.ForLLM, "task is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSpawnTool_Execute_NilManager verifies the not-configured error.
func TestSpawnTool_Execute_NilManager(t *testing.T) {
	tool := NewSpawnTool(nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"task": "do something"})
	if res == nil || !res.IsError {
		t.Fatal("expected manager-not-configured error")
	}
	if !strings.Contains(res.ForLLM, "not configured") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSpawnTool_Execute_AllowlistDenied verifies agent-specific allowlist rejection.
func TestSpawnTool_Execute_AllowlistDenied(t *testing.T) {
	manager := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	tool := NewSpawnTool(manager)
	tool.SetAllowlistChecker(func(id string) bool { return id == "ok" })
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task":     "do something",
		"agent_id": "blocked",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected allowlist error")
	}
	if !strings.Contains(res.ForLLM, "not allowed to spawn") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSpawnTool_Execute_ContinueTask_NotFound verifies ContinueTask error path.
func TestSpawnTool_Execute_ContinueTask_NotFound(t *testing.T) {
	manager := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	tool := NewSpawnTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task_id":  "subagent-999",
		"guidance": "continue please",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected continue-task error")
	}
	if !strings.Contains(res.ForLLM, "failed to manage") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSpawnTool_ContinueTask_EmptyGuidance verifies the guidance-required error.
func TestSpawnTool_Execute_ContinueTask_EmptyGuidance(t *testing.T) {
	manager := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	tool := NewSpawnTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task_id": "subagent-1",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected guidance-required error")
	}
}

// TestSpawnTool_Execute_WithDependenciesAndRetries verifies the spawn path and
// arg extraction code for dependencies and max_retries. To avoid launching an
// actual subagent goroutine (nil provider), we set a concurrency limit of 1 and
// pre-claim one running task so the spawn returns an error synchronously after
// the arg-extraction branches run.
func TestSpawnTool_Execute_WithDependenciesAndRetries(t *testing.T) {
	manager := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	manager.SetMaxConcurrent(1)
	// Pre-claim a running task so the new spawn hits the concurrency limit.
	manager.mu.Lock()
	manager.tasks["fake-running"] = &SubagentTask{ID: "fake-running", Status: SubagentStatusRunning, mu: &sync.Mutex{}}
	manager.mu.Unlock()

	tool := NewSpawnTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task":         "do something",
		"label":        "my-label",
		"dependencies": []interface{}{"subagent-1", "", "subagent-2"},
		"max_retries":  float64(3),
		"model":        "anthropic:claude-opus",
	})
	if res == nil {
		t.Fatal("expected a result")
	}
	if !res.IsError {
		t.Fatalf("expected concurrency-limit error, got %+v", res)
	}
}

// TestSpawnTool_Execute_ContinueTask_Success verifies the ContinueTask success
// path, including the async result metadata population (task_id +
// subagent_session_key) and guidance append.
func TestSpawnTool_Execute_ContinueTask_Success(t *testing.T) {
	manager := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	// Pre-create a needs_context task so ContinueTask resumes it. runTask will
	// run with a nil provider; we force it to end promptly by closing done.
	manager.mu.Lock()
	task := &SubagentTask{
		ID:        "subagent-7",
		Task:      "original task",
		Status:    SubagentStatusNeedsContext,
		mu:        &sync.Mutex{},
		doneCh:    make(chan struct{}),
		delivered: false,
	}
	manager.tasks[task.ID] = task
	manager.mu.Unlock()

	tool := NewSpawnTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task_id":  "subagent-7",
		"guidance": "go deeper",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "Continuing subagent task subagent-7") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
	if res.Metadata == nil || res.Metadata["task_id"] != "subagent-7" {
		t.Fatalf("Metadata = %v", res.Metadata)
	}
	if res.Metadata["subagent_session_key"] != "subagent:subagent-7" {
		t.Fatalf("subagent_session_key = %v", res.Metadata["subagent_session_key"])
	}
	// Verify guidance was appended and status flipped to running.
	manager.mu.Lock()
	updated := manager.tasks["subagent-7"]
	manager.mu.Unlock()
	if len(updated.Guidance) != 1 || updated.Guidance[0] != "go deeper" {
		t.Fatalf("guidance = %v", updated.Guidance)
	}
	if updated.Status != SubagentStatusRunning {
		t.Fatalf("status = %q", updated.Status)
	}
	// Clean up the running goroutine.
	manager.mu.Lock()
	if cancel := manager.cancels["subagent-7"]; cancel != nil {
		cancel()
	}
	manager.mu.Unlock()
}

// TestSpawnTool_Execute_ContextOriginOverride verifies originChannel/chatID are
// overridden from the context when provided.
func TestSpawnTool_Execute_ContextOriginOverride(t *testing.T) {
	manager := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	manager.SetMaxConcurrent(1)
	manager.mu.Lock()
	manager.tasks["fake-running"] = &SubagentTask{ID: "fake-running", Status: SubagentStatusRunning, mu: &sync.Mutex{}}
	manager.mu.Unlock()

	tool := NewSpawnTool(manager)
	ctx := WithToolContext(context.Background(), "telegram", "chat-99")
	res := tool.Execute(ctx, map[string]interface{}{"task": "do it"})
	if res == nil {
		t.Fatal("expected a result")
	}
	// We can't easily inspect the origin passed to SpawnWithOptions when it
	// fails at concurrency limit, so just assert we got the expected error.
	if !res.IsError {
		t.Fatalf("expected concurrency-limit error, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "concurrent") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestExtractSpawnTaskID covers the public helper and its private impl.
func TestExtractSpawnTaskID(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`Spawned subagent task subagent-3 ('label') for task: x`, "subagent-3"},
		{`Spawned subagent task subagent-12 for task: y`, "subagent-12"},
		{"no task id here", ""},
		{"", ""},
		{"subagent-5 at the end", "subagent-5"},
	}
	for _, tc := range tests {
		if got := ExtractSpawnTaskID(tc.in); got != tc.want {
			t.Errorf("ExtractSpawnTaskID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Direct private function check.
	if got := extractSpawnTaskID("subagent-7"); got != "subagent-7" {
		t.Fatalf("extractSpawnTaskID = %q", got)
	}
	if got := extractSpawnTaskID("x"); got != "" {
		t.Fatalf("extractSpawnTaskID(none) = %q", got)
	}
}
