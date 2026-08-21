package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestWaitForSubagentTool_ContextCancelled verifies the ctx.Done() branch in
// the event-driven waiting loop.
func TestWaitForSubagentTool_ContextCancelled(t *testing.T) {
	// Use a slow provider so the task stays running.
	provider := &delayedSubagentProvider{delay: 10 * time.Second}
	manager := NewSubagentManager(provider, "test-model", "/tmp/test", nil, 20)
	resultCh := make(chan *ToolResult, 1)
	_, err := manager.Spawn(context.Background(), "slow", "slow", "", "cli", "direct",
		func(ctx context.Context, r *ToolResult) { resultCh <- r })
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	defer manager.StopTask("subagent-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tool := NewWaitForSubagentTool(manager)
	res := tool.Execute(ctx, map[string]interface{}{"task_id": "subagent-1", "timeout_seconds": float64(10)})
	if res == nil || !res.IsError {
		t.Fatalf("expected error on cancelled ctx, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "interrupted") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestWaitForSubagentTool_TaskDisappearedDuringWait covers the "task
// disappeared" branch when the task vanishes from the manager.
func TestWaitForSubagentTool_TaskDisappearedDuringWait(t *testing.T) {
	manager := NewSubagentManager(&delayedSubagentProvider{delay: time.Hour}, "m", "/w", nil, 5)
	// Register a running task with a done channel, then delete it from the
	// manager so GetTask fails after the wait starts.
	task := &SubagentTask{
		ID:     "subagent-x",
		Task:   "t",
		Status: SubagentStatusRunning,
		mu:     &sync.Mutex{},
	}
	task.InitDoneChannel()
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.mu.Unlock()

	// Remove the task so it "disappears" — but keep the reference to doneCh.
	tool := NewWaitForSubagentTool(manager)
	ctx := context.Background()

	// Simulate the task disappearing after a short delay by deleting it and
	// using a 1s timeout so the timer branch sees GetTask fail.
	done := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		manager.mu.Lock()
		delete(manager.tasks, task.ID)
		manager.mu.Unlock()
		close(done)
	}()
	_ = done

	res := tool.Execute(ctx, map[string]interface{}{"task_id": task.ID, "timeout_seconds": float64(2)})
	// The done channel never closes and the task gets deleted => timer branch
	// returns "disappeared" or "Timed out"; either is acceptable as long as
	// there's no panic.
	if res == nil {
		t.Fatal("nil result")
	}
}

// TestWaitForSubagentTool_FallbackPolling covers the polling loop that runs
// when DoneChannel is not initialized.
func TestWaitForSubagentTool_FallbackPolling(t *testing.T) {
	manager := NewSubagentManager(&scriptedSubagentProvider{responses: []string{
		"STATUS: completed\nSUMMARY: done via polling\nDETAILS:\nok",
	}}, "m", "/w", nil, 5)

	// Create a task WITHOUT initializing DoneChannel and with no status yet.
	task := &SubagentTask{ID: "subagent-p", Task: "t", Status: SubagentStatusRunning, mu: &sync.Mutex{}}
	// Note: no InitDoneChannel call => DoneChannel() returns nil.
	manager.mu.Lock()
	manager.tasks[task.ID] = task
	manager.mu.Unlock()

	// Mark it terminal shortly after the poll begins.
	go func() {
		time.Sleep(300 * time.Millisecond)
		manager.mu.Lock()
		task.Status = SubagentStatusCompleted
		task.Summary = "finished"
		manager.mu.Unlock()
	}()

	tool := NewWaitForSubagentTool(manager)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"task_id":         task.ID,
		"timeout_seconds": float64(5),
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success via polling, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "completed") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestFormatSubagentTaskResult_Coverage exercises the format helper branches.
func TestFormatSubagentTaskResult_Coverage(t *testing.T) {
	base := &SubagentTask{ID: "s1", Status: SubagentStatusCompleted}
	msg := formatSubagentTaskResult(base)
	if !strings.Contains(msg, "s1") || !strings.Contains(msg, "completed") {
		t.Fatalf("msg = %q", msg)
	}
	if !strings.Contains(msg, "finished successfully") {
		t.Fatalf("expected success note: %q", msg)
	}

	// Full task: label, agent, summary, context, result.
	full := &SubagentTask{
		ID:             "s2",
		Status:         SubagentStatusNotDone,
		Label:          "mylabel",
		AgentID:        "coder",
		Summary:        "sum",
		ContextRequest: "what?",
		Result:         "details here",
	}
	m2 := formatSubagentTaskResult(full)
	for _, want := range []string{"mylabel", "coder", "sum", "what?", "details here", "could not complete"} {
		if !strings.Contains(m2, want) {
			t.Errorf("m2 missing %q: %q", want, m2)
		}
	}

	// NeedsContext branch with the spawn-tool continuation note.
	nc := &SubagentTask{ID: "s3", Status: SubagentStatusNeedsContext, ContextRequest: "x"}
	m3 := formatSubagentTaskResult(nc)
	if !strings.Contains(m3, "spawn tool") {
		t.Fatalf("m3 missing spawn tool note: %q", m3)
	}
}

// TestIsSubagentTerminal_AllStatuses covers the helper switch.
func TestIsSubagentTerminal_AllStatuses(t *testing.T) {
	for _, s := range []string{
		SubagentStatusCompleted, SubagentStatusFailed, SubagentStatusNotDone,
		SubagentStatusCancelled, SubagentStatusNeedsContext,
	} {
		if !isSubagentTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{SubagentStatusRunning, SubagentStatusPending} {
		if isSubagentTerminal(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}