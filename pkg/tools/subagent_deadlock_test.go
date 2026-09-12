package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// R-1: deadlock in safeGoroutine recover when sm.mu is held
// ============================================================================
// The dependency-polling goroutine (SpawnWithOptions, initialStatus==Pending)
// held sm.mu.Lock() WITHOUT defer around checkDependencies and state
// mutations. A panic inside that critical section (e.g. nil dereference in
// checkDependencies) would leave sm.mu locked on the goroutine: the
// safeGoroutine recover handler also calls sm.mu.Lock() → sync.Mutex is
// not reentrant → permanent deadlock → SignalDone never fires → waiters
// hang forever.
//
// This test MUST fail against commit 5b5359a (pre-fix) and pass after the
// R-1 fix (closure + defer pattern in the polling goroutine, TryLock in the
// recover handler).
// ============================================================================

// TestR1_PanicWhileHoldingMu_DoesNotDeadlock exercises the exact deadlock
// path: a task starts in Pending status (has unsatisfied dependencies), the
// polling goroutine fires on ticker.C, acquires sm.mu, and then
// checkDependencies panics (via a nil task entry injected into the map).
//
// In the BASE code (no defer on sm.mu.Unlock), the lock is never released
// on panic → safeGoroutine's recover tries sm.mu.Lock() on the same
// goroutine → deadlock.
//
// In the FIX (closure + defer), the closure's defer releases sm.mu before
// the panic propagates → recover acquires the lock → task marked failed →
// SignalDone fires.
func TestR1_PanicWhileHoldingMu_DoesNotDeadlock(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	// Dependency task in non-terminal status so the spawned task starts as Pending.
	dep := &SubagentTask{
		ID:               "dep-stuck",
		Task:             "stuck dependency",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-r1",
		OriginSessionKey: "native:chat-r1",
		Status:           SubagentStatusRunning, // non-terminal
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	dep.InitDoneChannel()
	manager.AddTaskForTest(dep, nil)

	// Spawn a task that depends on dep. It starts as Pending and the
	// polling goroutine begins ticking every 2 seconds.
	msg, err := manager.SpawnWithOptions(
		context.Background(),
		"r1 deadlock test",
		"r1-test",
		"agent",
		"native",
		"chat-r1",
		nil,
		SpawnOptions{Dependencies: []string{"dep-stuck"}},
	)
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	// Extract the spawned task ID.
	var spawnedID string
	for _, part := range strings.Fields(msg) {
		if strings.HasPrefix(part, "subagent-") {
			spawnedID = part
			break
		}
	}
	if spawnedID == "" {
		t.Fatalf("could not extract task ID from %q", msg)
	}

	// Grab the raw task pointer BEFORE corruption so we can wait on
	// DoneChannel even if the mutex gets deadlocked.
	rawTask := manager.tasks[spawnedID]
	if rawTask == nil {
		t.Fatalf("task %q not in manager.tasks", spawnedID)
	}

	// Corrupt the dependency entry to nil. The next time the polling
	// goroutine's ticker fires and calls checkDependencies under sm.mu,
	// it will look up this entry (ok=true, dep=nil), call
	// nil.IsTerminal() → nil pointer dereference → PANIC while sm.mu
	// is held.
	manager.mu.Lock()
	manager.tasks["dep-stuck"] = nil
	manager.mu.Unlock()

	// (a) SignalDone must fire. In the base code, the recover handler
	// deadlocks on sm.mu.Lock() and SignalDone never fires → timeout.
	select {
	case <-rawTask.DoneChannel():
		// OK — recover handler didn't deadlock.
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone never called — safeGoroutine recover deadlocked on sm.mu.Lock() (R-1 bug)")
	}

	// (b) sm.mu must still be acquireable.
	acquired := make(chan struct{})
	go func() {
		manager.mu.RLock()
		manager.mu.RUnlock()
		close(acquired)
	}()
	select {
	case <-acquired:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("sm.mu appears permanently locked")
	}

	// (c) Task must be marked failed.
	snap, ok := manager.GetTask(spawnedID)
	if !ok {
		t.Fatalf("task %q not found", spawnedID)
	}
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
}

// TestR1_TaskCtxDoneWhilePending_DoesNotDeadlock exercises the taskCtx.Done()
// path of the polling goroutine. With the fix, the cancel-path mutation runs
// inside a closure with defer sm.mu.Unlock(), so even if it panicked, the
// lock would be released.
func TestR1_TaskCtxDoneWhilePending_DoesNotDeadlock(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	// Dependency that will never complete.
	neverDone := &SubagentTask{
		ID:               "dep-cancel-never",
		Task:             "never finishes",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-r1-cancel",
		OriginSessionKey: "native:chat-r1-cancel",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	neverDone.InitDoneChannel()
	manager.AddTaskForTest(neverDone, nil)

	msg, err := manager.SpawnWithOptions(
		context.Background(),
		"cancel-path test",
		"r1-cancel",
		"agent",
		"native",
		"chat-r1-cancel",
		nil,
		SpawnOptions{Dependencies: []string{"dep-cancel-never"}},
	)
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	var spawnedID string
	for _, part := range strings.Fields(msg) {
		if strings.HasPrefix(part, "subagent-") {
			spawnedID = part
			break
		}
	}

	rawTask := manager.tasks[spawnedID]
	if rawTask == nil {
		t.Fatalf("task %q not found", spawnedID)
	}

	// Stop the task while it's still pending. This triggers taskCtx.Done()
	// in the polling goroutine.
	manager.StopTask(spawnedID)

	select {
	case <-rawTask.DoneChannel():
		// OK
	case <-time.After(10 * time.Second):
		t.Fatal("SignalDone not called after cancel")
	}

	snap, _ := manager.GetTask(spawnedID)
	if snap.Status != SubagentStatusCancelled {
		t.Errorf("status = %q, want %q", snap.Status, SubagentStatusCancelled)
	}

	// Mutex must still be usable.
	done := make(chan struct{})
	go func() {
		manager.ListTasks()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sm.mu permanently locked after cancel-path test")
	}
}

// TestR1_MutexUsableAfterPanicRecovery is a stress test: spawn multiple
// tasks that all panic via the polling path, then verify the mutex is still
// usable for new operations.
func TestR1_MutexUsableAfterPanicRecovery(t *testing.T) {
	manager := NewSubagentManager(&panicProvider{}, "test-model", t.TempDir(), nil, 10)

	// Create one dependency that stays pending until we release it.
	lockDep := &SubagentTask{
		ID:               "dep-stress-lock",
		Task:             "hold the gate",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-stress",
		OriginSessionKey: "native:chat-stress",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	lockDep.InitDoneChannel()
	manager.AddTaskForTest(lockDep, nil)

	const N = 3
	spawnedIDs := make([]string, N)
	rawTasks := make([]*SubagentTask, N)
	for i := 0; i < N; i++ {
		msg, err := manager.SpawnWithOptions(
			context.Background(),
			fmt.Sprintf("stress task %d", i),
			"stress",
			"agent",
			"native",
			"chat-stress",
			nil,
			SpawnOptions{Dependencies: []string{"dep-stress-lock"}},
		)
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		for _, p := range strings.Fields(msg) {
			if strings.HasPrefix(p, "subagent-") {
				spawnedIDs[i] = p
			}
		}
		rawTasks[i] = manager.tasks[spawnedIDs[i]]
	}

	// Release the gate — all N tasks transition to running simultaneously
	// and panic in parallel (via panicProvider.Chat).
	lockDep.mu.Lock()
	lockDep.Status = SubagentStatusCompleted
	lockDep.mu.Unlock()
	lockDep.SignalDone()

	// Wait for all tasks to finish.
	for i, raw := range rawTasks {
		select {
		case <-raw.DoneChannel():
		case <-time.After(15 * time.Second):
			t.Fatalf("task %d: SignalDone not called", i)
		}
		final, _ := manager.GetTask(spawnedIDs[i])
		if final.Status != SubagentStatusFailed {
			t.Errorf("task %d status = %q, want %q", i, final.Status, SubagentStatusFailed)
		}
	}

	// Final mutex sanity check.
	done := make(chan struct{})
	go func() {
		manager.ListTasks()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sm.mu permanently locked after stress test")
	}
}
