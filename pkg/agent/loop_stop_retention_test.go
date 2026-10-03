// The loop's half of the bounded subagent-retention stop (R2.1).
//
// AgentLoop.StopWithin stops the retention sweepers BEFORE it drains turns and
// closes the store: no sweep may touch the task map or evict a session while
// the store is being torn down. That join used to be unbounded, so a sweeper
// inside CleanupTerminalTasks -> sessionEvictCallback -> EvictSession
// (synchronous SQLite, per session) held the whole teardown open — and systemd
// would then SIGKILL every live turn after TimeoutStopUSec.
//
// These tests pin the fix at the loop level: the join honours the same grace as
// the turn drain, reports the abandonment, and leaves the store open while the
// eviction is still in flight.

package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tools"
)

// TestAgentLoop_StopWithin_BoundsStuckRetentionSweeper is the guard: a sweeper
// blocked in its session-evict callback must not be able to hold StopWithin
// open, and giving up on it must not close the store underneath it.
func TestAgentLoop_StopWithin_BoundsStuckRetentionSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)

	// The loop's own managers are replaced below; stop their sweepers first so
	// the test leaves no 1m ticker running.
	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"main": sm}, nil, nil)

	// The evict callback signals and then blocks: the sweeper is stuck in the
	// exact place (session eviction) that made the teardown hang.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(doRelease)
	sm.SetSessionEvictCallback(func(string) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	t.Cleanup(sm.StartRetentionCleanup(time.Millisecond))

	addOldTerminalTaskForTest(sm, time.Hour)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the retention sweeper never entered the evict callback")
	}

	const grace = 200 * time.Millisecond
	start := time.Now()
	err := al.StopWithin(grace)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("StopWithin() = nil, want an error: the stuck sweeper was abandoned")
	}
	if elapsed < grace {
		t.Errorf("StopWithin returned in %v, before the %v grace elapsed", elapsed, grace)
	}
	if elapsed > grace+2*time.Second {
		t.Errorf("StopWithin took %v; the bounded join must return at the grace", elapsed)
	}
	t.Logf("StopWithin returned in %v (grace %v): %v", elapsed, grace, err)

	if al.running.Load() {
		t.Error("Expected running to be false after a timed-out StopWithin")
	}
	// The abandoned eviction may still write into the session layer, which sits
	// on this store: it must stay open (closing it here is the bug the
	// stop-before-drain order exists to avoid).
	if al.dbStore == nil {
		t.Fatal("test harness has no store; the store-stays-open assertion cannot run")
	}
	if perr := al.dbStore.DB().Ping(); perr != nil {
		t.Errorf("Store was closed while a retention sweep was still in flight: %v", perr)
	}

	// Releasing the callback lets the abandoned sweep finish, and the manager
	// is detached: neither the rest of the batch nor any later tick may reap a
	// task. A terminal task added now must survive (the pre-fix sweeper would
	// take it within a millisecond).
	doRelease()
	addOldTerminalTaskForTest(sm, time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := len(sm.ListTasks()); got != 1 {
		t.Errorf("tracked tasks = %d, want 1: a detached sweeper kept reaping", got)
	}
	if n := sm.CleanupTerminalTasks(); n != 0 {
		t.Errorf("detached manager removed %d tasks after the teardown, want 0", n)
	}
}
