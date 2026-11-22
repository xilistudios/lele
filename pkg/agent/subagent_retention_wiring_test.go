package agent

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

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

func TestSubagentRetentionWiring_ConfigSetsRetentionWindow(t *testing.T) {
	const agentID = "main"

	tests := []struct {
		name       string
		minutes    int
		taskAge    time.Duration
		wantReaped bool
	}{
		{name: "configured window is applied", minutes: 2, taskAge: 3 * time.Minute, wantReaped: true},
		{name: "task inside the configured window survives", minutes: 2, taskAge: time.Minute, wantReaped: false},
		{name: "zero keeps the built-in 5m default", minutes: 0, taskAge: 3 * time.Minute, wantReaped: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := sharedToolsTestConfig(t, config.AgentConfig{ID: agentID, Default: true})
			cfg.Agents.Defaults.SubagentRetentionMinutes = tc.minutes

			_, sm := registerSharedForTest(t, cfg, agentID)
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

func TestSubagentRetentionWiring_StopWithinStopsSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)

	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	t.Cleanup(sm.StartRetentionCleanup(time.Millisecond))
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"main": sm}, nil, nil)

	addOldTerminalTaskForTest(sm, time.Hour)
	waitForTaskCount(t, sm, 0)

	if err := al.StopWithin(0); err != nil {
		t.Fatalf("StopWithin: %v", err)
	}

	sm.SetRetentionPeriod(time.Millisecond)
	addOldTerminalTaskForTest(sm, time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := len(sm.ListTasks()); got != 1 {
		t.Fatalf("tracked tasks = %d, want 1: StopWithin did not stop the retention sweeper", got)
	}
}

func TestSubagentRetentionWiring_RemovedAgentStopsSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)
	t.Cleanup(func() { _ = al.StopWithin(0) })

	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	t.Cleanup(sm.StartRetentionCleanup(time.Millisecond))
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"gone": sm}, nil, nil)

	addOldTerminalTaskForTest(sm, time.Hour)
	waitForTaskCount(t, sm, 0)

	tc := al.toolCoordinator.(*toolCoordinatorImpl)
	tc.cancelRemovedSubagents([]string{"main"})
	if _, ok := tc.subagents["gone"]; ok {
		t.Fatal("cancelRemovedSubagents did not drop the manager of the removed agent")
	}

	sm.SetRetentionPeriod(time.Millisecond)
	addOldTerminalTaskForTest(sm, time.Hour)
	time.Sleep(50 * time.Millisecond)
	if got := len(sm.ListTasks()); got != 1 {
		t.Fatalf("tracked tasks = %d, want 1: cancelRemovedSubagents left the retention sweeper running", got)
	}
}

func TestSubagentRetentionWiring_ProductionWiringStartsSweeper(t *testing.T) {
	al := newShutdownTestLoop(t)
	t.Cleanup(func() { _ = al.StopWithin(0) })

	managers := al.toolCoordinator.GetSubagents()
	if len(managers) == 0 {
		t.Fatal("no subagent manager wired into the loop")
	}

	for id, sm := range managers {
		sm.StopRetentionCleanup()
		sm.StartRetentionCleanup(time.Millisecond)

		addOldTerminalTaskForTest(sm, time.Hour)
		time.Sleep(50 * time.Millisecond)
		if got := len(sm.ListTasks()); got != 1 {
			t.Errorf("manager %s: production wiring did not start the retention sweeper (late start reaped the task)", id)
		}
	}
}

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

	first.StopRetentionCleanup()
	second.StopRetentionCleanup()
}
