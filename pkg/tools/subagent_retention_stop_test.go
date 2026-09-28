package tools

// Tests for the BOUNDED stop of the retention sweeper: StopRetentionCleanup /
// StopRetentionCleanupWithin.
//
// The unbounded form was called by AgentLoop.StopWithin BEFORE its turn drain,
// so the whole teardown waited on the sweeper without a limit. A sweep can be
// inside CleanupTerminalTasks -> sessionEvictCallback -> EvictSession
// (synchronous SQLite, per session), i.e. it can block for as long as that I/O
// takes — and a callback that never returns hung the teardown (systemd's
// TimeoutStopUSec would then SIGKILL every live turn).
//
// These tests pin the bound, the detachment contract that makes giving up safe,
// and the no-leak healthy path.
//
// Helpers (addRetentionTask, retentionTaskIDs, waitForCondition,
// waitSweeperExit) live in subagent_retention_test.go.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockedSweep wires a manager whose retention sweeper is provably stuck inside
// the session-evict callback: the callback signals on `entered` and then blocks
// until the returned release func is called. It also returns the number of
// callback invocations (read it with Load).
//
// The manager keeps `tasks` terminal tasks, all of them collected by the sweep
// that blocks, so a batch abandoned half-way still has evictions pending.
func blockedSweep(t *testing.T, tasks int) (sm *SubagentManager, entered <-chan struct{}, release func(), calls *atomic.Int64) {
	t.Helper()
	sm = NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	for i := 0; i < tasks; i++ {
		addRetentionTask(sm, fmt.Sprintf("subagent-%d", i+1), SubagentStatusCompleted)
	}

	enteredCh := make(chan struct{}, 1)
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(release) // never leave the sweeper blocked if the test fails early

	calls = &atomic.Int64{}
	sm.SetSessionEvictCallback(func(string) {
		select {
		case enteredCh <- struct{}{}:
		default:
		}
		calls.Add(1)
		<-releaseCh
	})

	stop := sm.StartRetentionCleanup(time.Millisecond)
	t.Cleanup(stop)

	// Wait until the sweeper is INSIDE the callback: the state that used to
	// make the stop (and the whole teardown) wait without a limit.
	waitForCondition(t, 3*time.Second, "the sweeper to enter the evict callback", func() bool {
		select {
		case <-enteredCh:
			return true
		default:
			return false
		}
	})
	return sm, enteredCh, release, calls
}

// TestSubagentManager_StopRetentionCleanup_IsBounded is the reported bug: the
// public stop must return on its own budget even when the sweeper is blocked in
// the evict callback, instead of waiting for it forever.
func TestSubagentManager_StopRetentionCleanup_IsBounded(t *testing.T) {
	sm, _, release, calls := blockedSweep(t, 3)

	start := time.Now()
	sm.StopRetentionCleanup()
	elapsed := time.Since(start)

	if elapsed < DefaultRetentionStopGrace {
		t.Errorf("stop returned in %s, before the %s grace elapsed", elapsed, DefaultRetentionStopGrace)
	}
	// Generous upper bound (the machine is shared) that still fails loudly if
	// the stop waits for the blocked callback instead of giving up.
	if elapsed > DefaultRetentionStopGrace+2*time.Second {
		t.Errorf("stop took %s, want ≈%s (bounded by the grace)", elapsed, DefaultRetentionStopGrace)
	}
	if !sm.retentionDetached.Load() {
		t.Error("stop gave up on the sweeper without detaching the manager")
	}
	t.Logf("bounded StopRetentionCleanup returned in %s (grace %s)", elapsed, DefaultRetentionStopGrace)

	release()
	waitSweeperExit(t, sm)
	if got := calls.Load(); got != 1 {
		t.Errorf("evict callbacks = %d, want 1: an abandoned sweep kept evicting sessions", got)
	}
	if !sm.retentionDetached.Load() {
		t.Error("detachment flag cleared by the sweeper's exit")
	}
}

// TestSubagentManager_StopRetentionCleanupWithin_DetachesOnTimeout pins the
// contract that makes giving up safe: after the grace expires the sweeper may
// no longer touch the task map or fire session-evict callbacks, and the caller
// is told the join was abandoned.
func TestSubagentManager_StopRetentionCleanupWithin_DetachesOnTimeout(t *testing.T) {
	sm, _, release, calls := blockedSweep(t, 3)

	const grace = 150 * time.Millisecond
	start := time.Now()
	err := sm.StopRetentionCleanupWithin(grace)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("StopRetentionCleanupWithin returned nil while the sweeper was blocked in the evict callback")
	}
	if elapsed > grace+2*time.Second {
		t.Errorf("stop took %s, want ≈%s (bounded by the grace)", elapsed, grace)
	}
	t.Logf("bounded stop returned in %s (grace %s): %v", elapsed, grace, err)

	// Detached: an eligible terminal task added now must survive, and no
	// callback may fire for it.
	addRetentionTask(sm, "subagent-4", SubagentStatusCompleted)
	if n := sm.CleanupTerminalTasks(); n != 0 {
		t.Errorf("detached manager removed %d tasks, want 0", n)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("evict callbacks = %d after detachment, want 1 (the blocked one)", got)
	}

	// The abandonment is remembered: a later stop does not wait again, it just
	// reports that there is nothing left to join.
	if err := sm.StopRetentionCleanupWithin(time.Second); err != nil {
		t.Errorf("stop after detachment = %v, want nil", err)
	}

	// Releasing the callback lets the blocked eviction finish. The two
	// remaining sessions of that batch must NOT be evicted (they belong to a
	// session layer the caller may already be tearing down), and the sweeper
	// must exit instead of sweeping again.
	release()
	waitSweeperExit(t, sm)

	if got := calls.Load(); got != 1 {
		t.Errorf("evict callbacks = %d after release, want 1: the abandoned batch kept evicting", got)
	}
	if ids := retentionTaskIDs(sm); len(ids) != 1 || !ids["subagent-4"] {
		t.Errorf("tracked tasks = %v, want only subagent-4", ids)
	}
}

// TestSubagentManager_StopRetentionCleanupWithin_HealthySweeper covers the
// normal path: the sweeper exits promptly, without detachment, and without
// leaking its goroutine.
func TestSubagentManager_StopRetentionCleanupWithin_HealthySweeper(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	addRetentionTask(sm, "subagent-1", SubagentStatusCompleted)

	stop := sm.StartRetentionCleanup(time.Millisecond)
	defer stop()

	start := time.Now()
	if err := sm.StopRetentionCleanupWithin(2 * time.Second); err != nil {
		t.Fatalf("bounded stop of a healthy sweeper = %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("healthy stop took %s, want immediate", elapsed)
	}
	if sm.retentionDetached.Load() {
		t.Error("a healthy stop detached the manager")
	}
	waitSweeperExit(t, sm) // no goroutine leak

	// Idempotent through both entry points, and the bounded-without-grace form
	// still joins a sweeper that already exited.
	sm.StopRetentionCleanup()
	sm.StopRetentionCleanup()
	if err := sm.StopRetentionCleanupWithin(0); err != nil {
		t.Errorf("unbounded stop of an exited sweeper = %v, want nil", err)
	}
}

// TestSubagentManager_StopRetentionCleanup_NeverStarted pins the pre-existing
// no-op contract of the stop (also exercised by the wiring tests, which call it
// on managers whose sweeper was never wired).
func TestSubagentManager_StopRetentionCleanup_NeverStarted(t *testing.T) {
	sm := NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)

	done := make(chan struct{})
	go func() {
		sm.StopRetentionCleanup()
		sm.StopRetentionCleanupWithin(time.Minute)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stopping a manager whose sweeper never started blocked")
	}
}
