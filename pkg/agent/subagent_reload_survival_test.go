// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

// Regression tests for spontaneous subagent cancellation on config reload.
//
// Before the fix, AgentLoop.ReloadRegistry (invoked by every channels reload
// and by the config file watcher) called cancelAll(), which StopAll()ed every
// SubagentManager in the process — killing running subagents of agents whose
// configuration had not changed at all. The reload path must now only cancel
// the work of agents that were removed from the config.

// newSubagentSurvivalLoop builds an AgentLoop with agents "main" and "worker"
// isolated from the user's config dir.
func newSubagentSurvivalLoop(t *testing.T, workerName string) (*AgentLoop, string) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true},
				{ID: "worker", Name: workerName},
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	t.Cleanup(func() { al.Stop() })
	return al, tmpDir
}

// addFakeRunningTask registers a task that is not actually running (its cancel
// only flips a flag) so tests can assert on cancellation without a real LLM.
func addFakeRunningTask(t *testing.T, sm *tools.SubagentManager, id string) *bool {
	t.Helper()
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stopped := new(bool)
	sm.AddTaskForTest(&tools.SubagentTask{
		ID:      id,
		Task:    "fake task",
		Status:  tools.SubagentStatusRunning,
		Created: time.Now().UnixMilli(),
		Updated: time.Now().UnixMilli(),
	}, func() { *stopped = true; cancel() })
	return stopped
}

func mustAgentManager(t *testing.T, al *AgentLoop, agentID string) *tools.SubagentManager {
	t.Helper()
	sm, ok := al.toolCoordinator.GetSubagents()[agentID]
	if !ok || sm == nil {
		t.Fatalf("no subagent manager for agent %q", agentID)
	}
	return sm
}

// TestReloadRegistry_KeepsSubagentsOfUnchangedAgent is the core regression
// test: a reload whose config is byte-for-byte identical must not touch a
// running subagent task.
func TestReloadRegistry_KeepsSubagentsOfUnchangedAgent(t *testing.T) {
	al, _ := newSubagentSurvivalLoop(t, "Worker")
	sm := mustAgentManager(t, al, "worker")
	stopped := addFakeRunningTask(t, sm, "subagent-1")

	al.ReloadRegistry(al.cfg().Snapshot())

	if task, ok := sm.GetTask("subagent-1"); !ok {
		t.Fatal("task disappeared from the manager")
	} else if task.Status != tools.SubagentStatusRunning {
		t.Fatalf("task status = %q, want %q (unchanged config must not cancel anything)", task.Status, tools.SubagentStatusRunning)
	}
	if *stopped {
		t.Fatal("task cancel func was invoked on an identical-config reload")
	}
	if got := mustAgentManager(t, al, "worker"); got != sm {
		t.Error("reload replaced the SubagentManager of an unchanged agent")
	}
}

// TestReloadRegistry_RecreatedAgentKeepsRunningTasks covers the subtler half
// of the fix: "worker" changes name, so the registry recreates the agent
// instance and shared tools are re-registered for it. The OLD manager (with
// its running task) must be re-used, not replaced, or the task would be
// orphaned — invisible to wait/cancel/list tools afterwards.
func TestReloadRegistry_RecreatedAgentKeepsRunningTasks(t *testing.T) {
	al, _ := newSubagentSurvivalLoop(t, "Worker")
	sm := mustAgentManager(t, al, "worker")
	stopped := addFakeRunningTask(t, sm, "subagent-1")

	// Changing Name makes agentConfigChanged() recreate the instance.
	newCfg := al.cfg().Snapshot()
	for i := range newCfg.Agents.List {
		if newCfg.Agents.List[i].ID == "worker" {
			newCfg.Agents.List[i].Name = "Renamed Worker"
		}
	}
	al.ReloadRegistry(newCfg)

	if got := mustAgentManager(t, al, "worker"); got != sm {
		t.Fatal("reload replaced the manager of a recreated agent; running tasks would be orphaned")
	}
	if task, ok := sm.GetTask("subagent-1"); !ok {
		t.Fatal("task disappeared after agent recreation")
	} else if task.Status != tools.SubagentStatusRunning {
		t.Fatalf("task status = %q, want %q (recreating the agent must not cancel its tasks)", task.Status, tools.SubagentStatusRunning)
	}
	if *stopped {
		t.Fatal("task cancel func was invoked when its agent was recreated")
	}
	// The agent's toolset must still expose the subagent tools bound to the
	// same manager — otherwise wait_for_subagent could never find the task.
	agent, ok := al.registry.GetAgent("worker")
	if !ok {
		t.Fatal("worker agent missing after reload")
	}
	for _, name := range []string{"spawn", "wait_for_subagent", "cancel_subagent"} {
		if _, exists := agent.Tools.Get(name); !exists {
			t.Errorf("tool %q missing after reload of recreated agent", name)
		}
	}
}

// TestReloadRegistry_CancelsRemovedAgentWork verifies the fix does not swing
// the other way: an agent that leaves the config must have its running tasks
// cancelled and its manager dropped, or they would leak forever.
func TestReloadRegistry_CancelsRemovedAgentWork(t *testing.T) {
	al, _ := newSubagentSurvivalLoop(t, "Worker")
	workerSM := mustAgentManager(t, al, "worker")
	mainSM := mustAgentManager(t, al, "main")
	workerStopped := addFakeRunningTask(t, workerSM, "subagent-w1")
	mainStopped := addFakeRunningTask(t, mainSM, "subagent-m1")

	newCfg := al.cfg().Snapshot()
	kept := newCfg.Agents.List[:0]
	for _, ac := range newCfg.Agents.List {
		if ac.ID != "worker" {
			kept = append(kept, ac)
		}
	}
	newCfg.Agents.List = kept
	al.ReloadRegistry(newCfg)

	if !*workerStopped {
		t.Error("removed agent's task was not cancelled")
	}
	if task, ok := workerSM.GetTask("subagent-w1"); ok && task.Status != tools.SubagentStatusCancelled {
		t.Errorf("removed agent task status = %q, want %q", task.Status, tools.SubagentStatusCancelled)
	}
	if *mainStopped {
		t.Error("surviving agent's task was cancelled when a different agent was removed")
	}
	if task, _ := mainSM.GetTask("subagent-m1"); task.Status != tools.SubagentStatusRunning {
		t.Errorf("surviving agent task status = %q, want %q", task.Status, tools.SubagentStatusRunning)
	}
	if _, ok := al.toolCoordinator.GetSubagents()["worker"]; ok {
		t.Error("removed agent's manager still tracked after reload")
	}
	if _, ok := al.toolCoordinator.GetSubagents()["main"]; !ok {
		t.Error("surviving agent's manager was dropped")
	}
}

// TestReloadRegistry_PreservesBackgroundProcessesOfRecreatedAgent guards the
// companion leak: a subagent's backgrounded command lives in the agent's
// BackgroundProcessManager. Replacing that manager on reload made running
// processes invisible (and un-stoppable) while their goroutines kept the old
// manager alive.
func TestReloadRegistry_PreservesBackgroundProcessesOfRecreatedAgent(t *testing.T) {
	al, _ := newSubagentSurvivalLoop(t, "Worker")

	bgm := al.toolCoordinator.(*toolCoordinatorImpl).bgManagers["worker"]
	if bgm == nil {
		t.Fatal("no background manager for worker")
	}
	proc := startBackgroundProcForTest(t, bgm, "worker", "agent:worker:test:chat")

	newCfg := al.cfg().Snapshot()
	for i := range newCfg.Agents.List {
		if newCfg.Agents.List[i].ID == "worker" {
			newCfg.Agents.List[i].Name = "Renamed Worker"
		}
	}
	al.ReloadRegistry(newCfg)

	after := al.toolCoordinator.(*toolCoordinatorImpl).bgManagers["worker"]
	if after != bgm {
		t.Fatal("reload replaced the BackgroundProcessManager of a recreated agent")
	}
	if _, ok := after.Get(proc.ID); !ok {
		t.Errorf("background process %s lost after reload", proc.ID)
	}
	after.Stop(proc.ID)
}

// TestReloadRegistry_TwoAgentLoopSmoke keeps the original production scenario
// honest: with several agents configured, an identical reload (what the
// channels watcher fires) leaves every manager and task in place.
func TestReloadRegistry_TwoAgentLoopSmoke(t *testing.T) {
	al, _ := newSubagentSurvivalLoop(t, "Worker")
	before := al.toolCoordinator.GetSubagents()
	if len(before) != 2 {
		t.Fatalf("expected managers for main+worker, got %d", len(before))
	}

	for i := 0; i < 3; i++ {
		al.ReloadRegistry(al.cfg().Snapshot())
	}

	after := al.toolCoordinator.GetSubagents()
	if len(after) != 2 {
		t.Fatalf("managers after 3 identical reloads = %d, want 2", len(after))
	}
	for id, sm := range before {
		if after[id] != sm {
			t.Errorf("manager for %q was replaced by identical reloads", id)
		}
	}
}
