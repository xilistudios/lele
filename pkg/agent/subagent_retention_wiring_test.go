// T5 — production wiring of the periodic subagent retention sweeper.
//
// The sweeper itself is covered in pkg/tools (subagent_retention_test.go). These
// tests pin the parts only the agent layer can get wrong:
//
//  1. agents.defaults.subagent_retention_minutes must reach
//     SubagentManager.SetRetentionPeriod (until this change the setter was never
//     called in production — dead code — so the window was always the built-in
//     5m).
//  2. Every manager gets its sweeper stopped on AgentLoop teardown
//     (StopWithin/stopOnce), so no goroutine outlives the loop.
//  3. A manager dropped by a config reload (cancelRemovedSubagents) loses its
//     sweeper too.
//  4. The production wiring actually starts one sweeper per manager, and the
//     reload path re-uses the manager instead of stacking a second one.

package agent

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

// addOldTerminalTaskForTest registers a finished task whose Updated stamp is
// age old, the way a run that ended a while ago leaves it behind.
func addOldTerminalTaskForTest(sm *tools.SubagentManager, age time.Duration) {
	finished := time.Now().Add(-age).UnixMilli()
	sm.AddTaskForTest(&tools.SubagentTask{
		ID:               "subagent-1",
		Task:             "finished task",
		AgentID:          "main",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
		OriginSessionKey: "cli:direct",
		Status:           tools.SubagentStatusCompleted,
		Created:          finished,
		Updated:          finished,
	}, nil)
}

// waitForTaskCount polls until the manager tracks want tasks.
func waitForTaskCount(t *testing.T, sm *tools.SubagentManager, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(sm.ListTasks()) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("tracked tasks = %d, want %d", len(sm.ListTasks()), want)
}

// TestSubagentRetentionWiring_ConfigSetsRetentionWindow pins the config ->
// setter path using the pre-existing spawn-triggered sweep
// (CleanupTerminalTasks), which depends only on the retention window — no
// ticker involved, so the assertions are timing-free.
func TestSubagentRetentionWiring_ConfigSetsRetentionWindow(t *testing.T) {
	const agentID = "main"

	tests := []struct {
		name       string
		minutes    int
		taskAge    time.Duration
		wantReaped bool
	}{
		// 2m window: a 3m-old terminal task is eligible.
		{name: "configured window is applied", minutes: 2, taskAge: 3 * time.Minute, wantReaped: true},
		// ...while a 1m-old one is still inside the window.
		{name: "task inside the configured window survives", minutes: 2, taskAge: time.Minute, wantReaped: false},
		// 0 = keep the manager's built-in default (5m), never "no retention".
		{name: "zero keeps the built-in 5m default", minutes: 0, taskAge: 3 * time.Minute, wantReaped: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: agentID, Default: true})
			cfg.Agents.Defaults.SubagentRetentionMinutes = tc.minutes

			_, sm := registerSharedForTest(t, cfg, agentID)
			// registerSharedToolsForAgent also starts the real sweeper: stop it
			// so the test process does not keep a 1m ticker alive.
			t.Cleanup(sm.StopRetentionCleanup)

			addOldTerminalTaskForTest(sm, tc.taskAge)
			removed := sm.CleanupTerminalTasks()
			if reaped := removed == 1; reaped != tc.wantReaped {
				t.Errorf("CleanupTerminalTasks removed %d task(s), want reaped=%v (subagent_retention_minutes=%d, task age=%v)",
					removed, tc.wantReaped, tc.minutes, tc.taskAge)
			}
		})
	}
}

// TestSubagentRetentionWiring_StopWithinStopsSweeper is the lifecycle test:
// AgentLoop.StopWithin (stopOnce) must stop the sweepers, or every loop
// teardown would leave one goroutine per manager behind.
func TestSubagentRetentionWiring_StopWithinStopsSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)

	// The loop's own managers are replaced below; stop their sweepers first so
	// the test leaves no 1m ticker running.
	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	// Swap in a manager whose sweeper ticks fast enough for a test to observe.
	// The production interval (1m) is far longer than a test can wait.
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	t.Cleanup(sm.StartRetentionCleanup(time.Millisecond))
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"main": sm}, nil, nil)

	// Red: the live sweeper reaps a terminal task on its own (no spawn).
	addOldTerminalTaskForTest(sm, time.Hour)
	waitForTaskCount(t, sm, 0)

	if err := al.StopWithin(0); err != nil {
		t.Fatalf("StopWithin: %v", err)
	}

	// Green: the sweeper is gone, so an equally old terminal task added only
	// NOW survives. Adding it after the teardown matters — before it, the 1ms
	// cadence could legitimately reap it in the window between the insert and
	// the stop. 50ms is ≫ the cadence the sweeper ran at, so a regression (a
	// sweeper that outlived StopWithin) would empty the list.
	sm.SetRetentionPeriod(time.Millisecond)
	addOldTerminalTaskForTest(sm, time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := len(sm.ListTasks()); got != 1 {
		t.Fatalf("tracked tasks = %d, want 1: StopWithin did not stop the retention sweeper", got)
	}
}

// TestSubagentRetentionWiring_RemovedAgentStopsSweeper covers the third hook:
// an agent removed from the config has its manager dropped by
// cancelRemovedSubagents, and that manager's sweeper must go with it (nothing
// can reach the task map anymore).
func TestSubagentRetentionWiring_RemovedAgentStopsSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)
	t.Cleanup(func() { _ = al.StopWithin(0) })

	// The loop's own managers are replaced below; stop their sweepers first so
	// the test leaves no 1m ticker running.
	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	t.Cleanup(sm.StartRetentionCleanup(time.Millisecond))
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"gone": sm}, nil, nil)

	// Red: the live sweeper reaps a terminal task on its own.
	addOldTerminalTaskForTest(sm, time.Hour)
	waitForTaskCount(t, sm, 0)

	// The agent leaves the config: its manager is dropped...
	tc := al.toolCoordinator.(*toolCoordinatorImpl)
	tc.cancelRemovedSubagents([]string{"main"})
	if _, ok := tc.subagents["gone"]; ok {
		t.Fatal("cancelRemovedSubagents did not drop the manager of the removed agent")
	}

	// ...so its sweeper must be gone too (see the comment in the teardown test:
	// the task is added only after the stop to avoid a legitimate reap).
	sm.SetRetentionPeriod(time.Millisecond)
	addOldTerminalTaskForTest(sm, time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := len(sm.ListTasks()); got != 1 {
		t.Fatalf("tracked tasks = %d, want 1: cancelRemovedSubagents left the retention sweeper running", got)
	}
}

// TestSubagentRetentionWiring_ProductionWiringStartsSweeper pins the other half
// of the wiring: NewAgentLoop must have STARTED a sweeper per manager. The
// production cadence (1m) is too slow to observe, so the test discriminates by
// what a stop+nudge can do:
//
//   - sweeper already running -> StopRetentionCleanup stops it and a later
//     Start is a deliberate no-op (a stopped manager stays stopped), so the old
//     terminal task below survives;
//   - sweeper never started (the regression this guards) -> the Stop is a no-op
//     and the Start takes effect, so the task is reaped within a few ticks.
func TestSubagentRetentionWiring_ProductionWiringStartsSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)
	t.Cleanup(func() { _ = al.StopWithin(0) })

	managers := al.toolCoordinator.GetSubagents()
	if len(managers) == 0 {
		t.Fatal("no subagent manager wired into the loop")
	}

	for id, sm := range managers {
		sm.StopRetentionCleanup() // no-op when the wiring never started one
		sm.StartRetentionCleanup(time.Millisecond)

		// Older than the manager's built-in 5m default (the test config leaves
		// subagent_retention_minutes unset).
		addOldTerminalTaskForTest(sm, time.Hour)
		time.Sleep(50 * time.Millisecond)
		if got := len(sm.ListTasks()); got != 1 {
			t.Errorf("manager %s: production wiring did not start the retention sweeper (late start reaped the task)", id)
		}
	}
}

// TestSubagentRetentionWiring_ReloadReusesManager pins the property the reload
// path depends on: updateSharedToolsForAgent (and therefore the retention
// wiring inside registerSharedToolsForAgent) must re-use the existing manager
// instead of building a new one — otherwise a config reload would orphan the
// running tasks AND stack a second sweeper goroutine.
func TestSubagentRetentionWiring_ReloadReusesManager(t *testing.T) {
	const agentID = "main"
	cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: agentID, Default: true})

	registry := NewAgentRegistry(cfg)
	agent, ok := registry.GetAgent(agentID)
	if !ok {
		t.Fatalf("agent %q not in registry", agentID)
	}

	msgBus := bus.NewMessageBus()
	existing := make(map[string]*tools.SubagentManager)
	bgManagers := make(map[string]*tools.BackgroundProcessManager)
	first := registerSharedToolsForAgent(agent, cfg, msgBus, registry, nil, agentID, existing, bgManagers, nil, nil)
	t.Cleanup(first.StopRetentionCleanup)

	second := updateSharedToolsForAgent(cfg, msgBus, registry, nil, agentID, existing, bgManagers, nil, nil)
	if second != first {
		t.Fatal("updateSharedToolsForAgent replaced the manager instead of re-using it (reload would orphan running tasks)")
	}

	// Stopping through either handle stops the one goroutine; the second call
	// must be a no-op (identical stopper), never a double close or a hang.
	first.StopRetentionCleanup()
	second.StopRetentionCleanup()
}
