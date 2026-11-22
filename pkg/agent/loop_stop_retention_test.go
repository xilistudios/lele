package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tools"
)

func TestAgentLoop_StopWithin_BoundsStuckRetentionSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)

	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"main": sm}, nil, nil)

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
	if al.dbStore == nil {
		t.Fatal("test harness has no store; the store-stays-open assertion cannot run")
	}
	if perr := al.dbStore.DB().Ping(); perr != nil {
		t.Errorf("Store was closed while a retention sweep was still in flight: %v", perr)
	}

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
