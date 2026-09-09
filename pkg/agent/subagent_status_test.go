package agent

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tools"
)

// Tests for GetSessionSubagents consuming the persisted subagent status
// instead of reporting every past subagent as "completed" (WebUI sidebar bug).

func getTestAgentWithSessions(t *testing.T, al *AgentLoop) *AgentInstance {
	t.Helper()
	inst, ok := al.registry.GetAgent("main")
	if !ok || inst == nil || inst.Sessions == nil {
		t.Fatal("test loop has no usable agent instance with sessions")
	}
	return inst
}

// TestGetSessionSubagents_PersistedStatusWins documents the fix: a subagent
// whose task ended as failed/cancelled/... must keep that status once its
// in-memory task is gone. Before the fix the persisted-session scan hard-coded
// Status: "completed", so the WebUI sidebar showed failures as green.
func TestGetSessionSubagents_PersistedStatusWins(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-42"
	cases := map[string]string{
		"subagent-1": "completed",
		"subagent-2": "failed",
		"subagent-3": "cancelled",
	}
	for taskID := range cases {
		key := parent + ":" + taskID
		sm.AddMessage(key, "user", "task")
		sm.AddMessage(key, "assistant", "done")
	}
	for taskID, status := range cases {
		sm.SetSubagentStatus(parent+":"+taskID, status)
	}

	ap := &agentProvidableImpl{al: al}
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != len(cases) {
		t.Fatalf("expected %d subagent infos, got %d", len(cases), len(infos))
	}
	got := make(map[string]string, len(infos))
	for _, info := range infos {
		got[info.TaskID] = info.Status
	}
	for taskID, want := range cases {
		if got[taskID] != want {
			t.Errorf("subagent %s status = %q, want %q", taskID, got[taskID], want)
		}
	}
}

// TestGetSessionSubagents_LegacySessionsFallBackToCompleted pins the
// backward-compat fallback: sessions persisted before subagent_status existed
// carry an empty status and are reported as "completed", matching the
// historical behavior for old data.
func TestGetSessionSubagents_LegacySessionsFallBackToCompleted(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-43"
	key := parent + ":subagent-7"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")

	ap := &agentProvidableImpl{al: al}
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("expected 1 subagent info, got %d", len(infos))
	}
	if infos[0].Status != tools.SubagentStatusCompleted {
		t.Errorf("legacy status = %q, want %q", infos[0].Status, tools.SubagentStatusCompleted)
	}
}

// TestGetSessionSubagents_InMemoryTakesPrecedence verifies that a live
// in-memory task (any status, e.g. needs_context) shadows the persisted
// record for the same task ID — live truth beats history.
func TestGetSessionSubagents_InMemoryTakesPrecedence(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-44"
	key := parent + ":subagent-5"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "partial")
	sm.SetSubagentStatus(key, tools.SubagentStatusNeedsContext)

	// Simulate a live manager with a needs_context task of the same ID.
	smm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	smm.AddTaskForTest(&tools.SubagentTask{
		ID:               "subagent-5",
		Task:             "task",
		AgentID:          inst.ID,
		OriginChannel:    "native",
		OriginChatID:     "client-44",
		OriginSessionKey: parent,
		Status:           tools.SubagentStatusNeedsContext,
		Summary:          "waiting for context",
		Created:          time.Now().UnixMilli(),
		Updated:          time.Now().UnixMilli(),
	}, nil)
	al.toolCoordinator = newToolCoordinatorWithSubagents(al,
		map[string]*tools.SubagentManager{"tester": smm}, nil)

	ap := &agentProvidableImpl{al: al}
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("expected 1 subagent info (in-memory shadowing persisted), got %d", len(infos))
	}
	if infos[0].Status != tools.SubagentStatusNeedsContext {
		t.Errorf("status = %q, want in-memory %q", infos[0].Status, tools.SubagentStatusNeedsContext)
	}
}
