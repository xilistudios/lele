package tools

import (
	"sync"
	"testing"
	"time"
)

func addRetentionTask(sm *SubagentManager, id, status string) *SubagentTask {
	finished := time.Now().Add(-time.Hour).UnixMilli()
	task := &SubagentTask{
		ID:               id,
		Task:             "task " + id,
		AgentID:          "default",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "cli:direct",
		Status:           status,
		Created:          finished,
		Updated:          finished,
	}
	sm.AddTaskForTest(task, func() {})
	return task
}

func retentionTaskIDs(sm *SubagentManager) map[string]bool {
	ids := make(map[string]bool)
	for _, task := range sm.ListTasks() {
		ids[task.ID] = true
	}
	return ids
}

func waitForCondition(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitSweeperExit(t *testing.T, sm *SubagentManager) {
	t.Helper()
	sm.retentionMu.Lock()
	exited := sm.retentionExited
	sm.retentionMu.Unlock()
	if exited == nil {
		t.Fatal("retention sweeper was never started")
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("retention sweeper goroutine did not exit after stop (goroutine leak)")
	}
}

func TestSubagentManager_RetentionSweeperReapsTerminalTasks(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)

	running := addRetentionTask(sm, "subagent-1", SubagentStatusRunning)
	paused := addRetentionTask(sm, "subagent-2", SubagentStatusNeedsContext)
	terminal := []string{"subagent-3", "subagent-4", "subagent-5"}
	addRetentionTask(sm, terminal[0], SubagentStatusCompleted)
	addRetentionTask(sm, terminal[1], SubagentStatusFailed)
	addRetentionTask(sm, terminal[2], SubagentStatusCancelled)

	var evictedMu sync.Mutex
	evicted := map[string]int{}
	sm.SetSessionEvictCallback(func(sessionKey string) {
		evictedMu.Lock()
		defer evictedMu.Unlock()
		evicted[sessionKey]++
	})

	stop := sm.StartRetentionCleanup(2 * time.Millisecond)
	defer stop()

	waitForCondition(t, 3*time.Second, "the sweeper to reap the terminal tasks", func() bool {
		return len(sm.ListTasks()) == 2
	})

	ids := retentionTaskIDs(sm)
	if !ids[running.ID] || !ids[paused.ID] {
		t.Fatalf("sweeper removed a non-terminal task: kept=%v, want %s and %s",
			ids, running.ID, paused.ID)
	}
	for _, id := range terminal {
		if ids[id] {
			t.Errorf("terminal task %s still tracked after the sweep", id)
		}
	}

	sm.mu.RLock()
	cancels := len(sm.cancels)
	sm.mu.RUnlock()
	if cancels != 2 {
		t.Errorf("cancel funcs left = %d, want 2 (one per surviving task)", cancels)
	}

	evictedMu.Lock()
	defer evictedMu.Unlock()
	for _, id := range terminal {
		key := "cli:direct:" + id
		if evicted[key] != 1 {
			t.Errorf("evict callback for %s called %d times, want exactly 1", key, evicted[key])
		}
	}
	if len(evicted) != len(terminal) {
		t.Errorf("evict callback called for %d sessions, want %d: %v", len(evicted), len(terminal), evicted)
	}
}

func TestSubagentManager_RetentionSweeperKeepsNonTerminalTasks(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)

	nonTerminal := map[string]string{
		"subagent-1": SubagentStatusRunning,
		"subagent-2": SubagentStatusPending,
		"subagent-3": SubagentStatusNeedsContext,
	}
	for id, status := range nonTerminal {
		addRetentionTask(sm, id, status)
	}

	stop := sm.StartRetentionCleanup(time.Millisecond)
	defer stop()

	time.Sleep(100 * time.Millisecond)

	ids := retentionTaskIDs(sm)
	for id, status := range nonTerminal {
		if !ids[id] {
			t.Errorf("task %s (%s) was reaped by the sweeper — only terminal tasks are eligible", id, status)
		}
	}
	if len(ids) != len(nonTerminal) {
		t.Fatalf("tracked tasks = %d, want %d (%v)", len(ids), len(nonTerminal), ids)
	}
}

func TestSubagentManager_RetentionSweeperStop(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)

	stop := sm.StartRetentionCleanup(2 * time.Millisecond)
	defer stop()

	again := sm.StartRetentionCleanup(time.Hour)
	if again == nil {
		t.Fatal("StartRetentionCleanup returned a nil stopper")
	}
	again()
	waitSweeperExit(t, sm)

	stop()
	sm.StopRetentionCleanup()
	sm.StopRetentionCleanup()

	addRetentionTask(sm, "subagent-1", SubagentStatusCompleted)
	time.Sleep(50 * time.Millisecond)
	if ids := retentionTaskIDs(sm); !ids["subagent-1"] {
		t.Fatal("a stopped sweeper still reaped a terminal task")
	}

	stopAfterStop := sm.StartRetentionCleanup(time.Millisecond)
	stopAfterStop()
	time.Sleep(10 * time.Millisecond)
	if ids := retentionTaskIDs(sm); !ids["subagent-1"] {
		t.Fatal("StartRetentionCleanup after a stop resurrected the sweeper")
	}
}
