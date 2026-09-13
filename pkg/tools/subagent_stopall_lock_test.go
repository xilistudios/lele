package tools

import (
	"context"
	"sync"
	"testing"
	"time"
)

// ============================================================================
// StopAll lock hygiene: cancel() must NOT be invoked while sm.mu is held.
// ============================================================================
// cancel() unblocks waiter goroutines whose cancellation paths take sm.mu
// (runTask's ctx.Done branch, runTaskImpl's deferred cleanup). Calling it
// inside the lock serializes every concurrent manager operation behind the
// whole cancel fan-out — and with a cancel func that blocks (real-world
// analogue: a context with slow attached cleanup, or a waiter holding sm.mu
// in a callback), StopAll would deadlock outright.
//
// This test MUST fail against the pre-fix StopAll (cancel under lock): the
// watcher goroutine cannot acquire sm.mu while StopAll holds it, so TryLock
// keeps failing until the timeout. It passes with the collect-then-cancel
// pattern (same hygiene as StopTask).
// ============================================================================

func TestStopAll_DoesNotHoldMuDuringCancel(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	// A cancel func that blocks until the test releases it — simulates a
	// waiter that needs sm.mu (or any slow cleanup) at cancel time. StopAll
	// must not call it under the lock, or the lock would be held indefinitely.
	release := make(chan struct{})
	blockingCancel := func() { <-release }

	task := &SubagentTask{
		ID:               "stopall-lock-task",
		Task:             "lock hygiene probe",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-lock",
		OriginSessionKey: "native:chat-lock",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	manager.AddTaskForTest(task, blockingCancel)

	stopAllDone := make(chan int, 1)
	go func() { stopAllDone <- manager.StopAll() }()

	// While StopAll is inside its cancel phase, sm.mu must be free: the
	// collect-then-cancel pattern releases the lock before calling cancels.
	// Give StopAll a moment to reach the cancel phase, then poll TryLock.
	lockAcquired := false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && !lockAcquired {
		time.Sleep(10 * time.Millisecond)
		if manager.mu.TryLock() {
			manager.mu.Unlock()
			lockAcquired = true
		}
	}
	close(release) // never leave the blocking cancel hanging

	if !lockAcquired {
		t.Fatal("sm.mu stayed locked while StopAll invoked cancel funcs — cancel() runs under the lock")
	}

	select {
	case n := <-stopAllDone:
		if n != 1 {
			t.Fatalf("StopAll stopped %d tasks, want 1", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("StopAll did not return after cancels were released")
	}

	if task.Status != SubagentStatusCancelled {
		t.Fatalf("task status = %v, want cancelled", task.Status)
	}
}

// Sanity: StopAll still cancels a plain context-backed task (guards the
// refactor against silently skipping the cancel call).
func TestStopAll_CancelsContextTask(t *testing.T) {
	manager := NewSubagentManager(&flakySubagentProvider{failAt: 0, final: "ok"}, "test-model", t.TempDir(), nil, 10)

	ctx, cancel := context.WithCancel(context.Background())
	task := &SubagentTask{
		ID:               "stopall-ctx-task",
		Task:             "plain ctx task",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-ctx",
		OriginSessionKey: "native:chat-ctx",
		Status:           SubagentStatusRunning,
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	manager.AddTaskForTest(task, cancel)

	if n := manager.StopAll(); n != 1 {
		t.Fatalf("StopAll stopped %d tasks, want 1", n)
	}

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("context was not cancelled by StopAll")
	}
}
