package tools

import (
	"sync"
	"testing"
	"time"
)

// These tests cover the PERIODIC retention sweeper (StartRetentionCleanup).
//
// Before it existed, CleanupTerminalTasks ran only from SpawnWithOptions and
// ContinueTask: an owner that stopped spawning kept every finished task in
// sm.tasks — and its "native:<parent>:subagent-N" session resident — forever,
// so memory grew without bound and every TUI frame got more expensive.
//
// The sweeper polls on a short interval here (a couple of milliseconds) while
// the retention window is 1ms, so the tests exercise the real production path
// (tick -> CleanupTerminalTasks) without sleeping for minutes.

// addRetentionTask registers a task the way a finished run leaves one behind:
// done channel initialised, a cancel func registered, and a Created/Updated
// stamp well past the retention window.
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

// retentionTaskIDs returns the IDs currently tracked by the manager.
func retentionTaskIDs(sm *SubagentManager) map[string]bool {
	ids := make(map[string]bool)
	for _, task := range sm.ListTasks() {
		ids[task.ID] = true
	}
	return ids
}

// waitForCondition polls cond until it holds or the timeout expires.
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

// waitSweeperExit blocks until the sweeper goroutine has returned. The sweeper
// closes retentionExited on exit, so this is a deterministic leak check (the
// stop path itself also waits on it).
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

// TestSubagentManager_RetentionSweeperReapsTerminalTasks covers the core of the
// change: after tasks reach a terminal state, the ticker alone removes them —
// no new spawn, no manual CleanupTerminalTasks call — and it fires the same
// session-evict callback the spawn-triggered sweep fires.
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

	// CleanupTerminalTasks also reaps the evicted tasks' cancel funcs; the two
	// survivors must keep theirs (StopTask/StopAll still need them).
	sm.mu.RLock()
	cancels := len(sm.cancels)
	sm.mu.RUnlock()
	if cancels != 2 {
		t.Errorf("cancel funcs left = %d, want 2 (one per surviving task)", cancels)
	}

	// Consistency with the spawn-triggered path: the evict callback must run
	// once per reaped task, keyed by its session key.
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

// TestSubagentManager_RetentionSweeperKeepsNonTerminalTasks pins the sweeper's
// scope: only terminal tasks are eligible, no matter how old the timestamp is.
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

	// 1ms ticks over 100ms: ~100 sweeps must leave every task untouched.
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

// TestSubagentManager_RetentionSweeperStop covers the lifecycle: start is
// idempotent, the stopper stops the goroutine (no leak), stop is idempotent,
// and a stopped manager stays stopped.
func TestSubagentManager_RetentionSweeperStop(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)

	stop := sm.StartRetentionCleanup(2 * time.Millisecond)
	defer stop()

	// A config reload re-uses the manager and calls Start again: it must not
	// stack a second goroutine and must hand back the same stopper. Stopping
	// through the second handle proves it is the same sweeper.
	again := sm.StartRetentionCleanup(time.Hour)
	if again == nil {
		t.Fatal("StartRetentionCleanup returned a nil stopper")
	}
	again()
	waitSweeperExit(t, sm)

	// Idempotent: neither the returned stopper nor StopRetentionCleanup may
	// panic or block when called again.
	stop()
	sm.StopRetentionCleanup()
	sm.StopRetentionCleanup()

	// Once stopped, the goroutine is gone: a terminal task added afterwards
	// must survive, even though it is already past the retention window.
	addRetentionTask(sm, "subagent-1", SubagentStatusCompleted)
	time.Sleep(50 * time.Millisecond)
	if ids := retentionTaskIDs(sm); !ids["subagent-1"] {
		t.Fatal("a stopped sweeper still reaped a terminal task")
	}

	// Start after stop does not resurrect the goroutine (the manager's
	// teardown is final — see StopRetentionCleanup).
	stopAfterStop := sm.StartRetentionCleanup(time.Millisecond)
	stopAfterStop()
	time.Sleep(10 * time.Millisecond)
	if ids := retentionTaskIDs(sm); !ids["subagent-1"] {
		t.Fatal("StartRetentionCleanup after a stop resurrected the sweeper")
	}
}
