package tools

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestCleanupTerminalTasks_EvictOutsideLock is a regression test for a deadlock.
//
// CleanupTerminalTasks used to invoke the session-evict callback while still
// holding the SubagentManager write lock. The callback (SessionManager.EvictSession)
// takes the session manager lock and does synchronous disk I/O, and the reverse
// path (session cancel -> SubagentManager.mu) created an ABBA deadlock that
// permanently blocked every subsequent spawn.
//
// This test registers an evict callback that re-enters the manager via GetTask
// (which takes sm.mu.RLock). If CleanupTerminalTasks still held the write lock
// when calling the callback, GetTask would block forever and the test would
// deadlock (and fail via the -timeout watchdog). With the fix, the lock is
// released before the callback runs, so GetTask succeeds.
func TestCleanupTerminalTasks_EvictOutsideLock(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(1 * time.Millisecond)

	// Insert a terminal task whose Updated timestamp is well past the retention
	// window so it is eligible for cleanup.
	task := &SubagentTask{
		ID:               "subagent-1",
		Task:             "test task",
		Label:            "test",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusCompleted,
		Created:          time.Now().Add(-time.Hour).UnixMilli(),
		Updated:          time.Now().Add(-time.Hour).UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	callbackCalled := false
	sm.SetSessionEvictCallback(func(sessionKey string) {
		callbackCalled = true
		// Re-enter the manager. Under the old (buggy) code the write lock was
		// still held here, so this RLock would block forever -> deadlock.
		if _, ok := sm.GetTask("subagent-1"); ok {
			t.Errorf("task should have been removed before eviction callback")
		}
	})

	removed := sm.CleanupTerminalTasks()

	if removed != 1 {
		t.Fatalf("expected 1 task removed, got %d", removed)
	}
	if !callbackCalled {
		t.Fatal("evict callback was not invoked")
	}

	// Task must be gone.
	if _, ok := sm.GetTask("subagent-1"); ok {
		t.Fatal("task still present after cleanup")
	}
}

// TestCleanupTerminalTasks_NoCallbackWhenNothingEligible ensures cleanup is a
// safe no-op when no terminal tasks have crossed the retention threshold, and
// that a non-terminal task is never removed or evicted.
func TestCleanupTerminalTasks_NoCallbackWhenNothingEligible(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(5 * time.Minute)

	// A running (non-terminal) task that is old — must NOT be removed.
	running := &SubagentTask{
		ID:               "subagent-1",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusRunning,
		Updated:          time.Now().Add(-time.Hour).UnixMilli(),
		mu:               &sync.Mutex{},
	}
	running.InitDoneChannel()

	// A terminal task that is still within the retention window — must NOT be removed.
	fresh := &SubagentTask{
		ID:               "subagent-2",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusCompleted,
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	fresh.InitDoneChannel()

	sm.mu.Lock()
	sm.tasks[running.ID] = running
	sm.tasks[fresh.ID] = fresh
	sm.mu.Unlock()

	callbackCalled := false
	sm.SetSessionEvictCallback(func(sessionKey string) {
		callbackCalled = true
	})

	removed := sm.CleanupTerminalTasks()

	if removed != 0 {
		t.Fatalf("expected 0 tasks removed, got %d", removed)
	}
	if callbackCalled {
		t.Fatal("evict callback should not have been invoked")
	}
	if _, ok := sm.GetTask("subagent-1"); !ok {
		t.Fatal("running task was incorrectly removed")
	}
	if _, ok := sm.GetTask("subagent-2"); !ok {
		t.Fatal("fresh terminal task was incorrectly removed")
	}
}

// TestCleanupTerminalTasks_ReapsCancels is the regression test for AGT-02.
//
// CleanupTerminalTasks used to delete terminal tasks from sm.tasks but leave
// their entries in sm.cancels untouched. A lingering entry pins the
// context.CancelFunc (and its whole context.Context tree) alive for the
// lifetime of the process, and for tasks that reached terminal state through
// paths that bypass runTask's deferred cleanup (e.g. StopAll on a pending
// poller, or tasks registered without a runner goroutine) the CancelFunc is
// also never invoked, leaking goroutines blocked on taskCtx.Done().
//
// After the fix, CleanupTerminalTasks removes the cancel entry together with
// the task and invokes the CancelFunc outside the lock (pure resource
// reclamation — the task is already terminal).
func TestCleanupTerminalTasks_ReapsCancels(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(1 * time.Millisecond)

	task := &SubagentTask{
		ID:               "subagent-1",
		Task:             "test task",
		Label:            "test",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusCompleted,
		Created:          time.Now().Add(-time.Hour).UnixMilli(),
		Updated:          time.Now().Add(-time.Hour).UnixMilli(),
		mu:               &sync.Mutex{},
	}
	task.InitDoneChannel()

	cancelled := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() { close(cancelled) })
	}

	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.cancels[task.ID] = cancel
	sm.mu.Unlock()

	removed := sm.CleanupTerminalTasks()

	if removed != 1 {
		t.Fatalf("expected 1 task removed, got %d", removed)
	}

	// The task must be gone.
	if _, ok := sm.GetTask(task.ID); ok {
		t.Fatal("task still present after cleanup")
	}

	// The cancel entry must have been removed...
	sm.mu.RLock()
	_, stillThere := sm.cancels[task.ID]
	sm.mu.RUnlock()
	if stillThere {
		t.Fatal("sm.cancels entry still present after cleanup (leak)")
	}

	// ...and the CancelFunc must have been invoked (outside the lock).
	select {
	case <-cancelled:
	default:
		t.Fatal("CancelFunc was not invoked by cleanup")
	}
}

// TestCleanupTerminalTasks_DoesNotTouchActiveCancels verifies the sweep is
// scoped to reaped terminal tasks only: cancel entries of non-terminal tasks
// (or terminal tasks still inside the retention window) must survive cleanup,
// since StopTask/StopAll still need them.
func TestCleanupTerminalTasks_DoesNotTouchActiveCancels(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(5 * time.Minute)

	// Old but RUNNING (non-terminal) task with a cancel entry.
	running := &SubagentTask{
		ID:               "subagent-1",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusRunning,
		Updated:          time.Now().Add(-time.Hour).UnixMilli(),
		mu:               &sync.Mutex{},
	}
	running.InitDoneChannel()

	// Terminal task still fresh (within retention) with a cancel entry.
	fresh := &SubagentTask{
		ID:               "subagent-2",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusFailed,
		Updated:          time.Now().UnixMilli(),
		mu:               &sync.Mutex{},
	}
	fresh.InitDoneChannel()

	cancels := map[string]context.CancelFunc{
		"subagent-1": func() {},
		"subagent-2": func() {},
	}

	sm.mu.Lock()
	sm.tasks[running.ID] = running
	sm.tasks[fresh.ID] = fresh
	for id, c := range cancels {
		sm.cancels[id] = c
	}
	sm.mu.Unlock()

	if got := sm.CleanupTerminalTasks(); got != 0 {
		t.Fatalf("expected 0 tasks removed, got %d", got)
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if _, ok := sm.cancels["subagent-1"]; !ok {
		t.Error("cancel of running task was removed — StopTask/StopAll would break")
	}
	if _, ok := sm.cancels["subagent-2"]; !ok {
		t.Error("cancel of fresh terminal task was removed prematurely")
	}
}
