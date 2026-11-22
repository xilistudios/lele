package agent

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/tools"
)

func getTestAgentWithSessions(t *testing.T, al *AgentLoop) *AgentInstance {
	t.Helper()
	inst, ok := al.registry.GetAgent("main")
	if !ok || inst == nil || inst.Sessions == nil {
		t.Fatal("test loop has no usable agent instance with sessions")
	}
	return inst
}

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

func TestGetSessionSubagents_InMemoryTakesPrecedence(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-44"
	key := parent + ":subagent-5"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "partial")
	sm.SetSubagentStatus(key, tools.SubagentStatusNeedsContext)

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
		map[string]*tools.SubagentManager{"tester": smm}, nil, nil)

	ap := &agentProvidableImpl{al: al}
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("expected 1 subagent info (in-memory shadowing persisted), got %d", len(infos))
	}
	if infos[0].Status != tools.SubagentStatusNeedsContext {
		t.Errorf("status = %q, want in-memory %q", infos[0].Status, tools.SubagentStatusNeedsContext)
	}
}
