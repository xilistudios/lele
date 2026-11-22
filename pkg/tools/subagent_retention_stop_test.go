package tools

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
	t.Cleanup(release)

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

func TestSubagentManager_StopRetentionCleanup_IsBounded(t *testing.T) {
	sm, _, release, calls := blockedSweep(t, 3)

	start := time.Now()
	sm.StopRetentionCleanup()
	elapsed := time.Since(start)

	if elapsed < DefaultRetentionStopGrace {
		t.Errorf("stop returned in %s, before the %s grace elapsed", elapsed, DefaultRetentionStopGrace)
	}
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

	addRetentionTask(sm, "subagent-4", SubagentStatusCompleted)
	if n := sm.CleanupTerminalTasks(); n != 0 {
		t.Errorf("detached manager removed %d tasks, want 0", n)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("evict callbacks = %d after detachment, want 1 (the blocked one)", got)
	}

	if err := sm.StopRetentionCleanupWithin(time.Second); err != nil {
		t.Errorf("stop after detachment = %v, want nil", err)
	}

	release()
	waitSweeperExit(t, sm)

	if got := calls.Load(); got != 1 {
		t.Errorf("evict callbacks = %d after release, want 1: the abandoned batch kept evicting", got)
	}
	if ids := retentionTaskIDs(sm); len(ids) != 1 || !ids["subagent-4"] {
		t.Errorf("tracked tasks = %v, want only subagent-4", ids)
	}
}

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
	waitSweeperExit(t, sm)

	sm.StopRetentionCleanup()
	sm.StopRetentionCleanup()
	if err := sm.StopRetentionCleanupWithin(0); err != nil {
		t.Errorf("unbounded stop of an exited sweeper = %v, want nil", err)
	}
}

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
