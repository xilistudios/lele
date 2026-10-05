package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

// Tests for the persisted executor of a subagent session (#332). The WebUI
// header names the agent of a HISTORICAL subagent from the session listing, and
// every agent shares one SessionManager, so without a persisted executor the
// only agent the listing could report was whichever one the scan swept first.

// twoAgentConfig builds a config with a default "main" agent and a "coder"
// agent, both served by the "test" provider, so a spawn can target an agent
// that is not the registry's first one.
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

// TestSubagentSpawn_PersistsExecutorAgent is the write half of the fix: a real
// spawn targeting "coder" must leave the executor on the child session row, so
// a process that restarts (and therefore loses the in-memory spawn mapping) can
// still name the agent that ran the task.
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
		"coder", // target agent — NOT the owner ("main") of the session storage
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

	// The session-key callback runs inside the subagent goroutine: wait for the
	// executor to land on the session row.
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

	// The in-memory routing mapping must stay intact (it is what the TUI header
	// and agentForSession use for a live subagent).
	if got := al.getSessionAgent(childKey); got != "coder" {
		t.Errorf("getSessionAgent(%s) = %q, want %q", childKey, got, "coder")
	}

	// And the listing must report the executor reading ONLY from disk: a fresh
	// manager over the same store has no in-memory state at all, which is the
	// state of a gateway right after a restart.
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

// restartSessionsOverStore swaps the agent's SessionManager for a brand-new one
// over the same SQLite store: nothing is resident and no in-memory spawn state
// survives, which is the situation of a gateway right after a restart.
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

// TestGetSessionSubagents_PersistedExecutorWins is the read half of the fix:
// a historical subagent must be reported with the agent that ran it, read back
// from disk with no in-memory state. Before the fix this returned the first
// agent of the registry ("main" here), because the persisted branch reported
// the owner of the session storage instead of the executor.
func TestGetSessionSubagents_PersistedExecutorWins(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	inst := getTestAgentWithSessions(t, al)
	sm := inst.Sessions

	parent := "native:client-60"
	key := parent + ":subagent-4"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "done")
	// An executor that is not in the registry at all: nothing but the persisted
	// column can produce this value.
	sm.SetSubagentAgentID(key, "researcher")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ap := &agentProvidableImpl{al: al}

	// Resident path.
	infos := ap.GetSessionSubagents(parent)
	if len(infos) != 1 {
		t.Fatalf("resident: expected 1 subagent info, got %d (%+v)", len(infos), infos)
	}
	if infos[0].AgentID != "researcher" {
		t.Errorf("resident AgentID = %q, want %q", infos[0].AgentID, "researcher")
	}

	// Restart path: evicted from memory and re-discovered from the store only.
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

// TestGetSessionSubagents_LegacyRowFallsBackToStorageOwner pins the backward
// compatibility of the read path: a session persisted before agent_id existed
// (and with no routing pin) keeps the historical value instead of reporting an
// empty agent, which the WebUI header would render as a blank name.
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

// TestGetSessionSubagents_LegacyRowUsesDurablePin covers the rows written
// before the column existed but after the spawn mapping became durable: the
// routing pin still names the right executor, so upgrading does not leave every
// historical subagent mislabeled until it is re-run.
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
	// What a pre-v8 spawn left behind: the durable pin, no column.
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
