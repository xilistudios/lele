package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// ============================================================================
// AGT-05: recover() in subagent goroutines
// ============================================================================
// A panic anywhere inside runTask/runTaskImpl must NOT kill the process.
// More importantly, SignalDone() must always fire so waiters
// (wait_for_subagent, concurrency slots) are never left hanging, and the
// task must be observable as "failed" with the panic text.
// ============================================================================

// panicProvider implements providers.LLMProvider and panics in Chat().
type panicProvider struct{}

func (p *panicProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	panic("boom")
}

func (p *panicProvider) GetDefaultModel() string  { return "test-model" }
func (p *panicProvider) SupportsTools() bool      { return false }
func (p *panicProvider) GetContextWindow() int     { return 4096 }

// TestSubagentPanicRecovery_SignalsDoneAndMarksFailed verifies AGT-05 core
// invariants:
//   (a) the test process survives the panic (if it didn't, the test runner
//       itself would die),
//   (b) SignalDone() fires — the test waits on the done channel with a
//       timeout; if it hangs, the test fails by timeout,
//   (c) the task's terminal status is "failed" and the panic text ("boom")
//       appears in the result.
func TestSubagentPanicRecovery_SignalsDoneAndMarksFailed(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 10)

	task := &SubagentTask{
		ID:               "subagent-panic-1",
		Task:             "this will panic",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()

	// Register the task and a cancel func (mirrors AddTaskForTest).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.AddTaskForTest(task, cancel)

	// Launch the goroutine through safeGoroutine — exactly what
	// SpawnWithOptions and ContinueTask do.
	manager.safeGoroutine(task, func() {
		manager.runTask(ctx, task, nil)
	})

	// (b) SignalDone must fire. Wait with a generous timeout.
	select {
	case <-task.DoneChannel():
		// OK — SignalDone fired.
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone was never called — waiter would hang forever")
	}

	// (c) Task must be marked failed with the panic text.
	snap := task.Snapshot()
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
	if !strings.Contains(snap.Result, "boom") {
		t.Errorf("result = %q, want it to contain %q", snap.Result, "boom")
	}
	if !strings.Contains(snap.Summary, "boom") {
		t.Errorf("summary = %q, want it to contain %q", snap.Summary, "boom")
	}
}

// TestSubagentPanicRecovery_SlotFreedForNextTask verifies AGT-05 regression
// for the concurrency slot: after a panic in one task, a second task can
// still be launched and complete normally (the slot held by the panicked
// goroutine is released).
func TestSubagentPanicRecovery_SlotFreedForNextTask(t *testing.T) {
	// maxConcurrent = 1: the slot from the panicked task must be freed.
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 1)
	// Allow only 1 concurrent subagent.
	manager.SetMaxConcurrent(1)

	// --- Task 1: will panic ---
	task1 := &SubagentTask{
		ID:               "subagent-panic-slot-1",
		Task:             "this will panic",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task1.InitDoneChannel()

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	manager.AddTaskForTest(task1, cancel1)

	manager.safeGoroutine(task1, func() {
		manager.runTask(ctx1, task1, nil)
	})

	// Wait for task1 to finish (panic recovery + SignalDone).
	select {
	case <-task1.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("task1: SignalDone was never called")
	}

	if snap := task1.Snapshot(); snap.Status != SubagentStatusFailed {
		t.Fatalf("task1 status = %q, want %q", snap.Status, SubagentStatusFailed)
	}

	// --- Task 2: uses a normal provider, should complete ---
	normalProvider := &flakySubagentProvider{
		failAt:  0, // never fail
		final:   "STATUS: completed\nSUMMARY: Done\nDETAILS:\nAll good",
	}
	// Swap the provider on the manager.
	manager.mu.Lock()
	manager.provider = normalProvider
	manager.mu.Unlock()

	task2 := &SubagentTask{
		ID:               "subagent-panic-slot-2",
		Task:             "this should succeed",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task2.InitDoneChannel()

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	manager.AddTaskForTest(task2, cancel2)

	manager.safeGoroutine(task2, func() {
		manager.runTask(ctx2, task2, nil)
	})

	select {
	case <-task2.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("task2: SignalDone was never called")
	}

	snap2 := task2.Snapshot()
	if snap2.Status != SubagentStatusCompleted {
		t.Errorf("task2 status = %q, want %q (result=%q)", snap2.Status, SubagentStatusCompleted, snap2.Result)
	}
}

// TestSubagentPanicRecovery_ContinueTaskPath verifies AGT-05 for the
// ContinueTask goroutine path: a panic during a continued task also
// recovers, signals done, and marks the task as failed.
func TestSubagentPanicRecovery_ContinueTaskPath(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 10)

	task := &SubagentTask{
		ID:               "subagent-panic-continue",
		Task:             "continued task that panics",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.AddTaskForTest(task, cancel)

	// Simulate the ContinueTask goroutine path directly.
	manager.safeGoroutine(task, func() {
		manager.runTask(ctx, task, nil)
	})

	select {
	case <-task.DoneChannel():
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone was never called — waiter would hang forever")
	}

	snap := task.Snapshot()
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
	if !strings.Contains(snap.Result, "boom") {
		t.Errorf("result = %q, want it to contain %q", snap.Result, "boom")
	}
}
