package tools

import (
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
	}
	running.InitDoneChannel()

	// A terminal task that is still within the retention window — must NOT be removed.
	fresh := &SubagentTask{
		ID:               "subagent-2",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusCompleted,
		Updated:          time.Now().UnixMilli(),
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
