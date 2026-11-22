package tools

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestCleanupTerminalTasks_EvictOutsideLock(t *testing.T) {
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
	sm.mu.Lock()
	sm.tasks[task.ID] = task
	sm.mu.Unlock()

	callbackCalled := false
	sm.SetSessionEvictCallback(func(sessionKey string) {
		callbackCalled = true
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

	if _, ok := sm.GetTask("subagent-1"); ok {
		t.Fatal("task still present after cleanup")
	}
}

func TestCleanupTerminalTasks_NoCallbackWhenNothingEligible(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(5 * time.Minute)

	running := &SubagentTask{
		ID:               "subagent-1",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusRunning,
		Updated:          time.Now().Add(-time.Hour).UnixMilli(),
		mu:               &sync.Mutex{},
	}
	running.InitDoneChannel()

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

	if _, ok := sm.GetTask(task.ID); ok {
		t.Fatal("task still present after cleanup")
	}

	sm.mu.RLock()
	_, stillThere := sm.cancels[task.ID]
	sm.mu.RUnlock()
	if stillThere {
		t.Fatal("sm.cancels entry still present after cleanup (leak)")
	}

	select {
	case <-cancelled:
	default:
		t.Fatal("CancelFunc was not invoked by cleanup")
	}
}

func TestCleanupTerminalTasks_DoesNotTouchActiveCancels(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(5 * time.Minute)

	running := &SubagentTask{
		ID:               "subagent-1",
		OriginSessionKey: "cli:direct",
		Status:           SubagentStatusRunning,
		Updated:          time.Now().Add(-time.Hour).UnixMilli(),
		mu:               &sync.Mutex{},
	}
	running.InitDoneChannel()

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
