package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestCancelSubagentTool_Metadata(t *testing.T) {
	tool := NewCancelSubagentTool(nil)
	if tool.Name() != "cancel_subagent" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	props := tool.Parameters()["properties"].(map[string]interface{})
	if _, ok := props["task_id"]; !ok {
		t.Fatal("missing task_id param")
	}
}

// TestCancelSubagentTool_Execute_MissingTaskID verifies the required error.
func TestCancelSubagentTool_Execute_MissingTaskID(t *testing.T) {
	tool := NewCancelSubagentTool(nil)
	res := tool.Execute(context.Background(), map[string]interface{}{})
	if res == nil || !res.IsError {
		t.Fatal("expected error for missing task_id")
	}
	if !strings.Contains(res.ForLLM, "task_id is required") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestCancelSubagentTool_Execute_NilManager verifies the not-configured error.
func TestCancelSubagentTool_Execute_NilManager(t *testing.T) {
	tool := NewCancelSubagentTool(nil)
	res := tool.Execute(context.Background(), map[string]interface{}{"task_id": "subagent-1"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for nil manager")
	}
	if !strings.Contains(res.ForLLM, "not configured") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestCancelSubagentTool_Execute_TaskNotFound verifies the not-found error.
func TestCancelSubagentTool_Execute_TaskNotFound(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	tool := NewCancelSubagentTool(sm)
	res := tool.Execute(context.Background(), map[string]interface{}{"task_id": "subagent-99"})
	if res == nil || !res.IsError {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(res.ForLLM, "not found") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestCancelSubagentTool_Execute_AlreadyTerminal verifies terminal-task path.
func TestCancelSubagentTool_Execute_AlreadyTerminal(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	sm.mu.Lock()
	sm.tasks["subagent-1"] = &SubagentTask{
		ID:     "subagent-1",
		Status: SubagentStatusCompleted,
		mu:     &sync.Mutex{},
	}
	sm.mu.Unlock()

	tool := NewCancelSubagentTool(sm)
	res := tool.Execute(context.Background(), map[string]interface{}{"task_id": "subagent-1"})
	if res == nil || res.IsError {
		t.Fatalf("expected silent terminal result, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "already in terminal status") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestCancelSubagentTool_Execute_Running verifies cancelling a running task.
func TestCancelSubagentTool_Execute_Running(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	sm.mu.Lock()
	sm.tasks["subagent-1"] = &SubagentTask{
		ID:     "subagent-1",
		Status: SubagentStatusRunning,
		mu:     &sync.Mutex{},
	}
	sm.cancels["subagent-1"] = func() {}
	sm.mu.Unlock()

	tool := NewCancelSubagentTool(sm)
	res := tool.Execute(context.Background(), map[string]interface{}{"task_id": "subagent-1"})
	if res == nil || res.IsError {
		t.Fatalf("expected cancellation success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "has been cancelled") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
	// Task status should be updated to cancelled.
	task, ok := sm.GetTask("subagent-1")
	if !ok || task.Status != SubagentStatusCancelled {
		t.Fatalf("expected task cancelled, got %+v", task)
	}
}

// TestCancelSubagentTool_Execute_FailedStop verifies StopTask returning false.
// A running task without a registered cancel func returns true from StopTask
// (it sets the status), so to trigger false we use a task that already has its
// cancel func deleted but still running — StopTask returns ok||canStop which
// ends up true here; instead exercise the nil-manager-error path is covered
// above. This test ensures no panic for unmatched cancel entries.
func TestCancelSubagentTool_Execute_NoCancelEntry(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", "/tmp/test", nil, 20)
	sm.mu.Lock()
	sm.tasks["subagent-1"] = &SubagentTask{
		ID:     "subagent-1",
		Status: SubagentStatusNeedsContext,
		mu:     &sync.Mutex{},
	}
	sm.mu.Unlock()

	tool := NewCancelSubagentTool(sm)
	res := tool.Execute(context.Background(), map[string]interface{}{"task_id": "subagent-1"})
	if res == nil || res.IsError {
		t.Fatalf("expected cancellation, got %+v", res)
	}
}
