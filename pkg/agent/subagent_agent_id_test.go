package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/store"
)

func twoAgentConfig(t *testing.T, workspace string) *config.Config {
	t.Helper()
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				Provider:          "test",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Model: &config.AgentModelConfig{Primary: "test-model"}},
				{ID: "coder", Model: &config.AgentModelConfig{Primary: "test-model"}},
			},
		},
		Providers: &config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"test": {
					ProviderConfig: config.ProviderConfig{APIKey: "test-key"},
					Models: map[string]config.ProviderModelConfig{
						"test-model": {Model: "test-model"},
					},
				},
			},
		},
	}
}

func TestSubagentSpawn_PersistsExecutorAgent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	db, err := store.Open(filepath.Join(tmpDir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	}()
	// The loop wires the usage repos globally; undo that so later tests
	// do not inherit this (soon closed) store.
	t.Cleanup(func() {
		skills.SetUsageRepo(nil)
		mcp.SetUsageRepo(nil)
	})

	al := NewAgentLoopWithStore(twoAgentConfig(t, tmpDir), bus.NewMessageBus(), db)
	for _, id := range []string{"main", "coder"} {
		if agent, ok := al.registry.GetAgent(id); ok && agent != nil {
			agent.Provider = &mockProvider{mockResponse: "DONE"}
		}
	}

	mainAgent := al.registry.GetDefaultAgent()
	if mainAgent == nil {
		t.Fatal("no default agent")
	}
	subagentManager := al.GetSubagents()[mainAgent.ID]
	if subagentManager == nil {
		t.Fatal("no subagent manager for the default agent")
	}

	const originChannel, originChatID = "native", "tui:chat:persist-executor"
	result, err := subagentManager.Spawn(
		context.Background(),
		"Implement the fix",
		"goal-system-fix",
		"coder",
		originChannel,
		originChatID,
		nil,
	)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	taskID := extractSpawnTaskIDForTest(result)
	if taskID == "" {
		t.Fatalf("could not extract task ID from spawn result: %q", result)
	}
	parentKey := originChannel + ":" + originChatID
	childKey := parentKey + ":" + taskID

	deadline := time.Now().Add(10 * time.Second)
	for {
		meta, err := db.Sessions().GetSessionMeta(childKey)
		if err != nil {
			t.Fatalf("GetSessionMeta(%s): %v", childKey, err)
		}
		if meta != nil && meta.AgentID == "coder" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s never recorded the executor (meta=%+v)", childKey, meta)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := al.getSessionAgent(childKey); got != "coder" {
		t.Errorf("getSessionAgent(%s) = %q, want %q", childKey, got, "coder")
	}

	fresh := session.NewSessionManager()
	fresh.SetSessionRepo(db.Sessions())
	var got string
	for _, info := range fresh.FindSubagentSessions(parentKey) {
		if info.TaskID == taskID {
			got = info.AgentID
		}
	}
	if got != "coder" {
		t.Errorf("FindSubagentSessions after restart reported agent %q, want %q", got, "coder")
	}
}

func restartSessionsOverStore(t *testing.T, al *AgentLoop, inst *AgentInstance) *session.SessionManager {
	t.Helper()
	if al.dbStore == nil {
		t.Fatal("test agent loop has no SQLite store; cannot exercise the persisted path")
	}
	fresh := session.NewSessionManager()
	fresh.SetSessionRepo(al.dbStore.Sessions())
	inst.Sessions = fresh
	return fresh
}

func TestGetSessionSubagents_PersistedExecutorWins(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-60"
	key := parent + ":subagent-4"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")
	sm.SetSubagentAgentID(key, "researcher")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ap := &agentProvidableImpl{al: al}

	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("resident: expected 1 subagent info, got %d (%+v)", len(infos), infos)
	}
	if infos[0].AgentID != "researcher" {
		t.Errorf("resident AgentID = %q, want %q", infos[0].AgentID, "researcher")
	}

	restartSessionsOverStore(t, al, inst)
	infos = ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("after restart: expected 1 subagent info, got %d (%+v)", len(infos), infos)
	}
	if infos[0].AgentID != "researcher" {
		t.Errorf("after restart AgentID = %q, want %q", infos[0].AgentID, "researcher")
	}
	if infos[0].TaskID != "subagent-4" || infos[0].SessionKey != key {
		t.Errorf("after restart identity = (%q, %q), want (%q, %q)",
			infos[0].TaskID, infos[0].SessionKey, "subagent-4", key)
	}
}

func TestGetSessionSubagents_LegacyRowFallsBackToStorageOwner(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-61"
	key := parent + ":subagent-8"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	restartSessionsOverStore(t, al, inst)

	ap := &agentProvidableImpl{al: al}
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("expected 1 subagent info, got %d (%+v)", len(infos), infos)
	}
	if infos[0].AgentID != inst.ID {
		t.Errorf("legacy AgentID = %q, want the storage owner %q", infos[0].AgentID, inst.ID)
	}
}

func TestGetSessionSubagents_LegacyRowUsesDurablePin(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-62"
	key := parent + ":subagent-2"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}
	al.setSubagentSessionAgent(key, "coder")
	restartSessionsOverStore(t, al, inst)

	ap := &agentProvidableImpl{al: al}
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("expected 1 subagent info, got %d (%+v)", len(infos), infos)
	}
	if infos[0].AgentID != "coder" {
		t.Errorf("AgentID = %q, want the pinned executor %q", infos[0].AgentID, "coder")
	}
}
