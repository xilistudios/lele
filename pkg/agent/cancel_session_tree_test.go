// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/lele
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tools"
)

// newCancelTestAgentLoop builds a minimal AgentLoop isolated from the real
// user config (LELE_CONFIG_DIR points at a temp dir) so NewAgentLoop's store
// initialisation never touches the shared ~/.lele/lele.db — concurrent tests
// fighting over one SQLite file produce flakes in unrelated packages.
func newCancelTestAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	al, _ := createLLMRunnerTestAgentLoop(t)
	return al
}

// newCancelTestCoordinator builds a real toolCoordinatorImpl wired to a
// minimal AgentLoop (enough for ResolveSessionKey and GroupManager access)
// with the given subagent/background managers.
func newCancelTestCoordinator(t *testing.T, subagents map[string]*tools.SubagentManager, bgManagers map[string]*tools.BackgroundProcessManager) *toolCoordinatorImpl {
	t.Helper()
	return newToolCoordinatorWithSubagents(newCancelTestAgentLoop(t), subagents, bgManagers, nil)
}

// addRunningSubagent registers a fake running subagent task with the given
// identities and returns the cancel func whose invocation marks it stopped.
func addRunningSubagent(t *testing.T, sm *tools.SubagentManager, id, spawnerKey, originKey string) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sm.AddTaskForTest(&tools.SubagentTask{
		ID:                id,
		Task:              "fake task",
		Status:            tools.SubagentStatusRunning,
		SpawnerSessionKey: spawnerKey,
		OriginSessionKey:  originKey,
		Created:           time.Now().UnixMilli(),
		Updated:           time.Now().UnixMilli(),
	}, func() { cancel(); <-ctx.Done() })
	return cancel
}

func runningTaskStatus(t *testing.T, sm *tools.SubagentManager, id string) string {
	t.Helper()
	task, ok := sm.GetTask(id)
	if !ok {
		t.Fatalf("task %s not found", id)
	}
	return task.Status
}

// startBackgroundProcForTest runs a long command through the real ExecTool in
// background mode under the given agent tool context, so the process carries
// the same ownership attributes it would in production.
func startBackgroundProcForTest(t *testing.T, bgm *tools.BackgroundProcessManager, agentID, sessionKey string) *tools.BackgroundProcess {
	t.Helper()
	et := tools.NewExecTool(t.TempDir(), false)
	et.SetBackgroundManager(bgm)
	ctx := tools.WithAgentToolContext(context.Background(), agentID, sessionKey)
	res := et.Execute(ctx, map[string]interface{}{
		"command":    "sleep 30",
		"background": true,
	})
	if res == nil || res.IsError {
		t.Fatalf("background exec failed: %+v", res)
	}
	procs := bgm.List()
	if len(procs) == 0 {
		t.Fatal("expected one registered background process")
	}
	p := procs[len(procs)-1]
	t.Cleanup(func() { bgm.Stop(p.ID) })
	return p
}

func procStatus(t *testing.T, bgm *tools.BackgroundProcessManager, id string) string {
	t.Helper()
	p, ok := bgm.Get(id)
	if !ok {
		t.Fatalf("proc %s not found", id)
	}
	return p.Status
}

// TestStopSessionSubagents_NativeBareKeyIsIssue230 is the regression test for
// issue #230: the WebUI/native frontend stops sessions by the bare uuid while
// subagent tasks record their origin as "native:<uuid>". The old equality
// matcher never matched, so /stop (and the WS stop button) silently stopped
// zero subagents.
func TestStopSessionSubagents_NativeBareKeyIsIssue230(t *testing.T) {
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "", "native:550e8400-e29b-41d4-a716-446655440000")

	tc := newCancelTestCoordinator(t, map[string]*tools.SubagentManager{"main": sm}, nil)

	stopped := tc.stopSessionSubagents("550e8400-e29b-41d4-a716-446655440000")
	if stopped != 1 {
		t.Fatalf("stopSessionSubagents(bare uuid) = %d, want 1", stopped)
	}
	if s := runningTaskStatus(t, sm, "subagent-1"); s != tools.SubagentStatusCancelled {
		t.Errorf("task status = %q, want %q", s, tools.SubagentStatusCancelled)
	}
}

// TestStopSessionSubagents_SpawnerKeyWins checks that a task spawned by an
// agent loop is found through its runtime spawner key even when the routing
// origin uses another form, and that a foreign session is left alone.
func TestStopSessionSubagents_SpawnerKeyWins(t *testing.T) {
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "agent:main:telegram:123", "telegram:123")
	addRunningSubagent(t, sm, "subagent-2", "agent:main:telegram:999", "telegram:999")

	tc := newCancelTestCoordinator(t, map[string]*tools.SubagentManager{"main": sm}, nil)

	if stopped := tc.stopSessionSubagents("telegram:123"); stopped != 1 {
		t.Fatalf("stopSessionSubagents = %d, want 1", stopped)
	}
	if s := runningTaskStatus(t, sm, "subagent-1"); s != tools.SubagentStatusCancelled {
		t.Errorf("owned task status = %q, want cancelled", s)
	}
	if s := runningTaskStatus(t, sm, "subagent-2"); s != tools.SubagentStatusRunning {
		t.Errorf("foreign task status = %q, want running (untouched)", s)
	}
}

// TestStopSessionSubagents_ChildSessionKey checks that stopping a subagent's
// own child session ("<origin>:<task_id>") also stops the task itself, the
// behaviour the pre-#230 code had via the taskSessionKey comparison.
func TestStopSessionSubagents_ChildSessionKey(t *testing.T) {
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-7", "", "telegram:123")

	tc := newCancelTestCoordinator(t, map[string]*tools.SubagentManager{"main": sm}, nil)

	if stopped := tc.stopSessionSubagents("telegram:123:subagent-7"); stopped != 1 {
		t.Fatalf("stopSessionSubagents(child key) = %d, want 1", stopped)
	}
}

// TestStopSessionSubagents_AliasResolution ensures a stop issued with the
// base key still reaches tasks attributed to the resolved (active) session.
func TestStopSessionSubagents_AliasResolution(t *testing.T) {
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "agent:main:native:active-uuid", "native:active-uuid")

	al := newCancelTestAgentLoop(t)
	al.sessionAliases.Store("base-uuid", "agent:main:native:active-uuid")
	tc := newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"main": sm}, nil, nil)

	// Stopping via the base (alias) key resolves to the active key.
	if stopped := tc.stopSessionSubagents("base-uuid"); stopped != 1 {
		t.Fatalf("stopSessionSubagents(alias) = %d, want 1", stopped)
	}
}

// TestStopSessionSubagents_SealsResults_MatchesStop uses the same matcher as
// the stop path: a finished task stoppable under a bare uuid must also be
// sealed under it, so its completion callback can never resume the parent.
func TestStopSessionSubagents_SealsResults_MatchesStop(t *testing.T) {
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "", "native:deadbeef")

	tc := newCancelTestCoordinator(t, map[string]*tools.SubagentManager{"main": sm}, nil)
	if stopped := tc.stopSessionSubagents("deadbeef"); stopped != 1 {
		t.Fatalf("stopSessionSubagents = %d, want 1", stopped)
	}

	// The stop path must seal the result: a subsequent injection claim loses.
	task := taskOrFatal(t, sm, "subagent-1")
	if !task.ResultClaimed() {
		t.Error("ResultClaimed() = false after stopSessionSubagents, want true (the stop path must seal every owned task)")
	}
	if sm.ClaimResultForInjection("subagent-1") {
		t.Error("ClaimResultForInjection = true after the seal, want false (a stopped task must not resume the parent)")
	}
}

// TestStopSessionSubagents_SealsTasksStopTaskCannotStop covers the task set the
// stop count does not see. `stopSessionSubagents` only counts the tasks
// StopTask actually stopped, but the seal must NOT be conditional on that:
//
//   - a task already terminal whose completion callback has not fired yet
//     (Status == completed, StopTask returns false) still owes the parent a
//     continuation turn, and injecting it after /stop would resurrect the
//     session;
//   - a task still `pending` (StopTask does stop it) must be sealed too.
//
// Gating SealResult on StopTask's return value leaves the first case injectable.
func TestStopSessionSubagents_SealsTasksStopTaskCannotStop(t *testing.T) {
	cases := []struct {
		name        string
		taskID      string
		status      string
		wantStopped int
	}{
		{name: "terminal_awaiting_callback", taskID: "subagent-completed", status: tools.SubagentStatusCompleted, wantStopped: 0},
		{name: "pending_not_started", taskID: "subagent-pending", status: tools.SubagentStatusPending, wantStopped: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
			sm.AddTaskForTest(&tools.SubagentTask{
				ID:                tc.taskID,
				Task:              "fake task",
				Status:            tc.status,
				SpawnerSessionKey: "agent:main:telegram:123",
				OriginSessionKey:  "telegram:123",
				Created:           time.Now().UnixMilli(),
				Updated:           time.Now().UnixMilli(),
			}, nil)

			coord := newCancelTestCoordinator(t, map[string]*tools.SubagentManager{"main": sm}, nil)

			if stopped := coord.stopSessionSubagents("telegram:123"); stopped != tc.wantStopped {
				t.Fatalf("stopSessionSubagents = %d, want %d for a %s task", stopped, tc.wantStopped, tc.status)
			}
			if !taskOrFatal(t, sm, tc.taskID).ResultClaimed() {
				t.Errorf("ResultClaimed() = false for an owned %s task, want true (the stop path must seal it)", tc.status)
			}
			if sm.ClaimResultForInjection(tc.taskID) {
				t.Errorf("ClaimResultForInjection = true for a %s task after /stop, want false", tc.status)
			}
		})
	}
}

// TestStopSessionSubagents_LeavesForeignTasksUnsealed is the negative control:
// sealing must use the same ownership matcher as stopping, so a foreign
// session's result stays injectable.
func TestStopSessionSubagents_LeavesForeignTasksUnsealed(t *testing.T) {
	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "", "native:deadbeef")
	addRunningSubagent(t, sm, "subagent-2", "", "native:cafebabe")

	tc := newCancelTestCoordinator(t, map[string]*tools.SubagentManager{"main": sm}, nil)
	if stopped := tc.stopSessionSubagents("deadbeef"); stopped != 1 {
		t.Fatalf("stopSessionSubagents = %d, want 1", stopped)
	}

	foreign := taskOrFatal(t, sm, "subagent-2")
	if foreign.ResultClaimed() {
		t.Error("ResultClaimed() = true for a foreign task, want false (only owned tasks are sealed)")
	}
}

func taskOrFatal(t *testing.T, sm *tools.SubagentManager, id string) *tools.SubagentTask {
	t.Helper()
	task, ok := sm.GetTask(id)
	if !ok || task == nil {
		t.Fatalf("task %s not found", id)
	}
	return task
}

// TestCancelSessionTree_StopsBackgroundProcesses verifies the bg cascade:
// processes owned by the session (and by its subagent children) are stopped,
// foreign ones survive.
func TestCancelSessionTree_StopsBackgroundProcesses(t *testing.T) {
	bgm := tools.NewBackgroundProcessManager()
	own := startBackgroundProcForTest(t, bgm, "main", "native:uuid-1")
	// A process started inside a subagent's tool loop is attributed to the
	// subagent's ownership key, which is the parent's runtime spawner key
	// (see taskOwnershipKey), not the child session key.
	child := startBackgroundProcForTest(t, bgm, "main", "agent:main:native:uuid-1")
	foreign := startBackgroundProcForTest(t, bgm, "main", "telegram:999")

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "", "native:uuid-1")

	tc := newCancelTestCoordinator(t,
		map[string]*tools.SubagentManager{"main": sm},
		map[string]*tools.BackgroundProcessManager{"main": bgm})

	subagents, groups, procs := tc.cancelSessionTree("uuid-1")
	if subagents != 1 {
		t.Errorf("subagents stopped = %d, want 1", subagents)
	}
	if groups != 0 {
		t.Errorf("groups stopped = %d, want 0", groups)
	}
	if procs != 2 {
		t.Errorf("procs stopped = %d, want 2 (owner + child)", procs)
	}
	if s := procStatus(t, bgm, own.ID); s != tools.BgExecStatusStopped {
		t.Errorf("owned proc status = %q, want stopped", s)
	}
	if s := procStatus(t, bgm, child.ID); s != tools.BgExecStatusStopped {
		t.Errorf("child proc status = %q, want stopped", s)
	}
	if s := procStatus(t, bgm, foreign.ID); s != tools.BgExecStatusRunning {
		t.Errorf("foreign proc status = %q, want running (untouched)", s)
	}
}

// TestCancelSessionTree_EmptyKeyIsNoop guards the destructive case: a stop
// without session context must not kill anything.
func TestCancelSessionTree_EmptyKeyIsNoop(t *testing.T) {
	bgm := tools.NewBackgroundProcessManager()
	p := startBackgroundProcForTest(t, bgm, "main", "telegram:123")

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
	addRunningSubagent(t, sm, "subagent-1", "telegram:123", "telegram:123")

	tc := newCancelTestCoordinator(t,
		map[string]*tools.SubagentManager{"main": sm},
		map[string]*tools.BackgroundProcessManager{"main": bgm})

	subagents, groups, procs := tc.cancelSessionTree("")
	if subagents+groups+procs != 0 {
		t.Fatalf("cancelSessionTree(\"\") cancelled %d/%d/%d, want 0/0/0", subagents, groups, procs)
	}
	if s := procStatus(t, bgm, p.ID); s != tools.BgExecStatusRunning {
		t.Errorf("proc status = %q, want running", s)
	}
}
