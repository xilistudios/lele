package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// ============================================================================
// Issue #234, part C: identity inheritance for nested spawns.
//
// A subagent task's AgentID is the identity that reaches its tool loop as
// ToolLoopConfig.OwnerAgentID (pkg/tools/subagent_runner.go). Identity-scoped
// tools — the keyring secret lookup, nested spawn attribution — key on it, so
// an empty AgentID silently breaks them. Two layers guard against that:
//   - SpawnWithOptions inherits the spawner's own agent id from the tool
//     context when the caller passes no agent_id (change 1);
//   - the runner falls back to the manager's owning agent id when a task still
//     arrives without one, e.g. a legacy cron spawn job stored before its
//     creator was recorded (change 2).
// ============================================================================

// identitySpawnProvider answers with a completed outcome. It exists so the
// spawn path can be exercised without a real LLM or network when the test only
// cares about the task recorded at spawn time.
type identitySpawnProvider struct{}

func (identitySpawnProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted"}, nil
}

func (identitySpawnProvider) GetDefaultModel() string { return "test-model" }

// spawnCtxProbeTool records the agent identity the subagent's tool loop hands
// to the tools it executes. Registered as "ctxprobe" so identityCallProvider
// can trigger it.
type spawnCtxProbeTool struct {
	mu     sync.Mutex
	agent  string
	called bool
}

func (p *spawnCtxProbeTool) Name() string        { return "ctxprobe" }
func (p *spawnCtxProbeTool) Description() string { return "records the acting agent identity" }
func (p *spawnCtxProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (p *spawnCtxProbeTool) Execute(ctx context.Context, _ map[string]interface{}) *ToolResult {
	agent, _ := AgentToolContextFromCtx(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.agent, p.called = agent, true
	return &ToolResult{ForLLM: "ok"}
}

func (p *spawnCtxProbeTool) identity() (agent string, called bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.agent, p.called
}

// identityCallProvider drives exactly one tool call and then finishes, so the
// probe above runs inside the subagent's own tool loop.
type identityCallProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *identityCallProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID:        "call-1",
			Name:      "ctxprobe",
			Arguments: map[string]interface{}{},
		}}}, nil
	}
	return &providers.LLMResponse{Content: "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted"}, nil
}

func (p *identityCallProvider) GetDefaultModel() string { return "test-model" }

// spawnTaskID extracts the task id from a SpawnWithOptions result message so
// tests can look the task up with GetTask.
func spawnTaskID(t *testing.T, result string) string {
	t.Helper()
	id := ExtractSpawnTaskID(result)
	if id == "" {
		t.Fatalf("could not extract task id from spawn result %q", result)
	}
	return id
}

// awaitIdentityTask polls GetTask until the task is registered or the deadline
// passes. SpawnWithOptions registers the task synchronously, but the poll keeps
// the tests honest if that ever changes.
func awaitIdentityTask(t *testing.T, sm *SubagentManager, id string) *SubagentTask {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if task, ok := sm.GetTask(id); ok {
			return task
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s never appeared in the manager", id)
	return nil
}

// TestC1_SpawnWithoutAgentIDInheritsSpawnerIdentity covers change 1: a spawn
// that names no target agent must inherit the spawner's identity from the tool
// context instead of storing an empty AgentID.
func TestC1_SpawnWithoutAgentIDInheritsSpawnerIdentity(t *testing.T) {
	sm := NewSubagentManager(identitySpawnProvider{}, "test-model", t.TempDir(), nil, 10)

	ctx := WithAgentToolContext(context.Background(), "planner", "sess-9")
	result, err := sm.SpawnWithOptions(ctx, "task", "label", "", "telegram", "chat1", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	task := awaitIdentityTask(t, sm, spawnTaskID(t, result))
	if task.AgentID != "planner" {
		t.Errorf("task.AgentID = %q, want %q (spawner identity must be inherited)", task.AgentID, "planner")
	}
	if task.SpawnerSessionKey != "sess-9" {
		t.Errorf("task.SpawnerSessionKey = %q, want %q", task.SpawnerSessionKey, "sess-9")
	}
}

// TestC2_SpawnWithExplicitAgentIDWins verifies change 1 never overrides an
// explicit target agent — nested spawns must still be routable to an agent
// other than the spawner.
func TestC2_SpawnWithExplicitAgentIDWins(t *testing.T) {
	sm := NewSubagentManager(identitySpawnProvider{}, "test-model", t.TempDir(), nil, 10)

	ctx := WithAgentToolContext(context.Background(), "planner", "sess-9")
	result, err := sm.SpawnWithOptions(ctx, "task", "label", "reviewer", "telegram", "chat1", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	task := awaitIdentityTask(t, sm, spawnTaskID(t, result))
	if task.AgentID != "reviewer" {
		t.Errorf("task.AgentID = %q, want explicit %q (must not be overwritten)", task.AgentID, "reviewer")
	}
	// The spawner session is still recorded: it is what cancellation cascades
	// match on (#230) and is independent of the target identity.
	if task.SpawnerSessionKey != "sess-9" {
		t.Errorf("task.SpawnerSessionKey = %q, want %q", task.SpawnerSessionKey, "sess-9")
	}
}

// TestC3_SpawnWithoutAnyIdentityStaysEmpty pins the boundary of change 1: with
// neither an agent_id nor a tool context (cron-spawned jobs, direct manager
// use) the task keeps an empty AgentID. That case is handled at run time by
// change 2, not at spawn time.
func TestC3_SpawnWithoutAnyIdentityStaysEmpty(t *testing.T) {
	sm := NewSubagentManager(identitySpawnProvider{}, "test-model", t.TempDir(), nil, 10)

	result, err := sm.SpawnWithOptions(context.Background(), "task", "label", "", "telegram", "chat1", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}

	task := awaitIdentityTask(t, sm, spawnTaskID(t, result))
	if task.AgentID != "" {
		t.Errorf("task.AgentID = %q, want empty (no identity available anywhere)", task.AgentID)
	}
}

// TestC4_OwnerAgentIDResolution covers change 2's decision table directly: the
// runner's owner is the task identity when present, the manager's owning agent
// otherwise.
func TestC4_OwnerAgentIDResolution(t *testing.T) {
	sm := NewSubagentManager(identitySpawnProvider{}, "test-model", t.TempDir(), nil, 10)
	sm.SetDefaultAgentID("planner")

	tests := []struct {
		name string
		task *SubagentTask
		want string
	}{
		{"explicit task identity wins", &SubagentTask{AgentID: "reviewer"}, "reviewer"},
		{"empty task identity falls back to owner", &SubagentTask{}, "planner"},
		{"nil task falls back to owner", nil, "planner"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sm.ownerAgentID(tc.task); got != tc.want {
				t.Errorf("ownerAgentID = %q, want %q", got, tc.want)
			}
		})
	}

	// A manager with no configured owner (standalone construction, tests) must
	// not invent an identity: it stays empty and RunToolLoop keeps whatever
	// identity its context carries.
	bare := NewSubagentManager(identitySpawnProvider{}, "test-model", t.TempDir(), nil, 10)
	if got := bare.ownerAgentID(&SubagentTask{}); got != "" {
		t.Errorf("ownerAgentID without configured owner = %q, want empty", got)
	}

	// SetDefaultAgentID trims: config files routinely carry padded values.
	padded := NewSubagentManager(identitySpawnProvider{}, "test-model", t.TempDir(), nil, 10)
	padded.SetDefaultAgentID("  coder  ")
	if got := padded.ownerAgentID(nil); got != "coder" {
		t.Errorf("ownerAgentID = %q, want %q (trimmed)", got, "coder")
	}
}

// TestC5_LegacyTaskWithoutIdentityRunsOwnedToolLoop is the end-to-end guard for
// change 2: a task that reaches the runner with no AgentID (a cron spawn job
// stored before its creator was recorded) must still expose a real identity to
// the tools it executes, otherwise the scoped secret lookup fails.
func TestC5_LegacyTaskWithoutIdentityRunsOwnedToolLoop(t *testing.T) {
	probe := &spawnCtxProbeTool{}
	reg := NewToolRegistry()
	reg.Register(probe)

	sm := NewSubagentManager(&identityCallProvider{}, "test-model", t.TempDir(), nil, 10)
	sm.SetTools(reg)
	sm.SetDefaultAgentID("planner")

	task := &SubagentTask{
		ID:               "subagent-legacy-1",
		Task:             "do the thing",
		OriginChannel:    "native",
		OriginChatID:     "cron-1",
		OriginSessionKey: "native:cron-1",
		Status:           SubagentStatusPending,
	}
	task.InitDoneChannel()

	sm.runTask(context.Background(), task, nil)

	snap := task.Snapshot()
	if snap.Status != SubagentStatusCompleted {
		t.Fatalf("status = %q, want %q (result=%q)", snap.Status, SubagentStatusCompleted, snap.Result)
	}

	agent, called := probe.identity()
	if !called {
		t.Fatal("probe tool never executed — the tool loop did not run as expected")
	}
	if agent != "planner" {
		t.Errorf("tool saw agent id %q, want %q (OwnerAgentID must never be empty when the runner knows the owner)", agent, "planner")
	}
}

// TestC6_InheritedIdentityReachesToolLoop proves the two changes compose: a
// spawn without agent_id inherits the spawner identity at spawn time, and that
// identity — not the manager default — is what the tool loop reports.
func TestC6_InheritedIdentityReachesToolLoop(t *testing.T) {
	probe := &spawnCtxProbeTool{}
	reg := NewToolRegistry()
	reg.Register(probe)

	sm := NewSubagentManager(&identityCallProvider{}, "test-model", t.TempDir(), nil, 10)
	sm.SetTools(reg)
	// Deliberately a different agent: the task identity must win over this.
	sm.SetDefaultAgentID("other")

	ctx := WithAgentToolContext(context.Background(), "planner", "sess-9")
	result, err := sm.SpawnWithOptions(ctx, "task", "label", "", "native", "chat1", nil, SpawnOptions{})
	if err != nil {
		t.Fatalf("SpawnWithOptions: %v", err)
	}
	task := awaitIdentityTask(t, sm, spawnTaskID(t, result))

	select {
	case <-task.DoneChannel():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for subagent task to finish")
	}

	agent, called := probe.identity()
	if !called {
		t.Fatal("probe tool never executed — the tool loop did not run as expected")
	}
	if agent != "planner" {
		t.Errorf("tool saw agent id %q, want %q (inherited spawner identity)", agent, "planner")
	}
}
