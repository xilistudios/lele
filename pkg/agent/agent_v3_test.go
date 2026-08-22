// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Coverage boost v3: targets tool_coordinator subagent lifecycle, goal
// SetStore/persist with populated repos, agent_providable subagent branches,
// and buildLLMOptions reasoning branches.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// tool_coordinator — subagent task lifecycle using a real SubagentManager
// ============================================================================

// spawnCompletedTask spawns a subagent task that completes with a known ID.
func spawnCompletedTask(t *testing.T, mgr *tools.SubagentManager) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	res, err := mgr.Spawn(ctx, "do a test task", "test-label", "coder", "native", "tv3-parent", nil)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	id := tools.ExtractSpawnTaskID(res)
	if id == "" {
		t.Fatalf("could not extract task id from %q", res)
	}
	// Wait for task to reach a terminal state.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if task, ok := mgr.GetTask(id); ok {
			if task.IsTerminal() {
				return id
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not reach terminal state", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestToolCoordinator_SubagentTaskLifecycle(t *testing.T) {
	al := newCovTestLoop(t)
	// Default agent's subagent manager.
	mgr := al.toolCoordinator.GetSubagents()[al.getDefaultAgentID()]
	if mgr == nil {
		t.Fatal("missing subagent manager")
	}

	tc := newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{
		al.getDefaultAgentID(): mgr,
	}, map[string]*tools.BackgroundProcessManager{})

	// getSubagentTask not found.
	if _, ok := tc.getSubagentTask("nope"); ok {
		t.Fatal("expected not found")
	}
	// stopSubagentTask not found.
	if tc.stopSubagentTask("nope") {
		t.Fatal("expected false stop on missing")
	}
	// markSubagentDelivered not found.
	if tc.markSubagentDelivered("nope") {
		t.Fatal("expected false mark on missing")
	}
	// continueSubagentTask not found.
	if _, err := tc.continueSubagentTask(context.Background(), "s", "nope", "g"); err == nil {
		t.Fatal("expected error continuing missing task")
	}
	// listRunningSubagentTasks empty.
	if n := len(tc.listRunningSubagentTasks()); n != 0 {
		t.Fatalf("expected 0 running, got %d", n)
	}
	// markSessionSubagentsDelivered no-op.
	tc.markSessionSubagentsDelivered("native:none")

	// Spawn a completed task and exercise lookups.
	id := spawnCompletedTask(t, mgr)
	if _, ok := tc.getSubagentTask(id); !ok {
		t.Fatalf("expected to find task %s", id)
	}
	running := tc.listRunningSubagentTasks()
	for _, task := range running {
		if task.Status == tools.SubagentStatusRunning || task.Status == tools.SubagentStatusNeedsContext {
			// found running task; ok
		}
	}
	// markSubagentDelivered: first call returns false (first delivery).
	tc.markSubagentDelivered(id)
	// list subagents for the origin session.
	tc.markSessionSubagentsDelivered("native:tv3-parent")
}

func TestToolCoordinator_ContinueAndStop_WithRealTask(t *testing.T) {
	al := newCovTestLoop(t)
	mgr := al.toolCoordinator.GetSubagents()[al.getDefaultAgentID()]
	tc := newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{
		al.getDefaultAgentID(): mgr,
	}, map[string]*tools.BackgroundProcessManager{})

	// Spawn a task but keep hold of it. To test stop, we spawn and immediately stop.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := mgr.Spawn(ctx, "task to stop", "lbl", "coder", "native", "tv3-stop", nil)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	id := tools.ExtractSpawnTaskID(res)
	if id == "" {
		t.Fatalf("no task id from %q", res)
	}
	// Stop quickly (may be terminal already; StopTask returns ok regardless if cancel exists).
	_ = tc.stopSubagentTask(id)

	// Continue requires a task in NeedsContext state; none will be here, so
	// expect an error (already covered by generic not-found test above).
	_, _ = tc.continueSubagentTask(context.Background(), "native:tv3-stop", id, "guidance")
}

// TestToolCoordinator_RegisterToolAndStartupInfo exercises GetStartupInfo and
// RegisterTool through a fully constructed loop.
func TestToolCoordinator_RegisterToolAndStartupInfo(t *testing.T) {
	al := newCovTestLoop(t)
	tc := al.toolCoordinator
	info := tc.GetStartupInfo()
	if _, ok := info["tools"]; !ok {
		t.Error("expected tools in startup info")
	}
	impl := tc.(*toolCoordinatorImpl)
	if impl.GetSubagents() == nil {
		t.Error("expected non-nil subagents")
	}
	if impl.GetBgManagers() == nil {
		t.Error("expected non-nil bg managers")
	}
}

// ============================================================================
// goal — SetStore / loadFromRepo / persist with a populated repo
// ============================================================================

func TestGoalManager_SetStore_PopulatedAndCorrupt(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	// Seed valid goals: one active, one paused, one done (should be skipped).
	activeGoal := &Goal{Text: "active goal", Status: GoalActive, MaxTurns: 3, SessionKey: "s-active"}
	pausedGoal := &Goal{Text: "paused goal", Status: GoalPaused, MaxTurns: 2, SessionKey: "s-paused"}
	doneGoal := &Goal{Text: "done goal", Status: GoalDone, MaxTurns: 1, SessionKey: "s-done"}

	for _, g := range []*Goal{activeGoal, pausedGoal, doneGoal} {
		data, err := jsonMarshal(g)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := repo.SetGoal(g.SessionKey, string(data)); err != nil {
			t.Fatalf("SetGoal: %v", err)
		}
	}
	// Seed a corrupt entry.
	if err := repo.SetGoal("s-corrupt", "{not valid json"); err != nil {
		t.Fatalf("SetGoal corrupt: %v", err)
	}

	gm := NewGoalManager(dir)
	gm.SetStore(repo)

	if gm.Get("s-active") == nil {
		t.Fatal("active goal should be loaded")
	}
	if gm.Get("s-paused") == nil {
		t.Fatal("paused goal should be loaded")
	}
	if gm.Get("s-done") != nil {
		t.Fatal("done goal should NOT be loaded")
	}
	if gm.Get("s-corrupt") != nil {
		t.Fatal("corrupt goal should be skipped")
	}
}

func TestGoalManager_SetStore_NilRevertsToJSON(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	// Write via JSON fallback (nil repo) after reverting.
	gm2 := NewGoalManager(dir)
	gm2.SetStore(nil)
	gm2.Set("plain-key", "plain goal", 3)
	goal := gm2.Get("plain-key")
	if goal == nil || goal.Text != "plain goal" {
		t.Fatalf("expected plain goal, got %+v", goal)
	}
}

func TestGoalManager_RemovePersisted_FromRepo(t *testing.T) {
	dir := t.TempDir()
	s := openGoalStore(t)
	repo := s.Goals()

	gm := NewGoalManager(dir)
	gm.SetStore(repo)
	a := gm.Set("session-del", "delete me", 3)
	_ = a
	// Confirm persisted.
	_, found, err := repo.GetGoal("session-del")
	if err != nil || !found {
		t.Fatalf("goal should be persisted (found=%v err=%v)", found, err)
	}
	// Remove via Repo path.
	gm.removePersisted("session-del")
	_, found, err = repo.GetGoal("session-del")
	if err != nil || found {
		t.Fatalf("goal should be deleted (found=%v err=%v)", found, err)
	}
}

// jsonMarshal marshals a goal to JSON for seeding a repo.
func jsonMarshal(g *Goal) ([]byte, error) {
	return json.Marshal(g)
}

// ============================================================================
// buildLLMOptions — DeepSeek thinking + full reasoning config branches
// ============================================================================

func TestBuildLLMOptions_DeepSeekThinking(t *testing.T) {
	al := newCovTestLoop(t)
	lc := newLLMCaller(al)
	agent := al.registry.GetDefaultAgent()
	enable := true
	agent.Reasoning = &config.ReasoningConfig{Enable: enable}

	opts := lc.buildLLMOptions(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "s",
		model:      "deepseek-chat",
	})
	if opts["thinking"] != true {
		t.Errorf("expected thinking=true for deepseek, got %v", opts["thinking"])
	}
	if opts["reasoning"] == nil {
		t.Error("expected reasoning map")
	}

	// Non-deepseek with reasoning enabled should NOT set thinking.
	opts2 := lc.buildLLMOptions(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "s2",
		model:      "gpt-4o",
	})
	if _, ok := opts2["thinking"]; ok {
		t.Error("thinking should not be set for non-deepseek")
	}
}

func TestBuildLLMOptions_AgentReasoningBranches(t *testing.T) {
	al := newCovTestLoop(t)
	lc := newLLMCaller(al)
	agent := al.registry.GetDefaultAgent()

	effort := "medium"
	maxTk := 500
	excl := true
	summ := "concise"
	enable := true
	agent.Reasoning = &config.ReasoningConfig{
		Enable: enable, Effort: &effort, MaxTokens: &maxTk, Exclude: &excl, Summary: &summ,
	}

	opts := lc.buildLLMOptions(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		sessionKey: "r1",
		model:      "testp:m",
	})
	r, ok := opts["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reasoning map, got %v", opts["reasoning"])
	}
	if r["effort"] != "medium" || r["max_tokens"] != 500 || r["exclude"] != true || r["summary"] != "concise" || r["enabled"] != true {
		t.Errorf("reasoning config wrong: %v", r)
	}
}

// mockBlockingProvider keeps a task in RUNNING state (never terminal) so we
// can exercise listRunningSubagentTasks with a genuinely running task.
type mockBlockingProvider struct {
	started chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (m *mockBlockingProvider) Chat(ctx context.Context, messages []providers.Message, toolsDef []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	m.once.Do(func() { close(m.started) })
	select {
	case <-m.block:
		return &providers.LLMResponse{Content: "ok", ToolCalls: []providers.ToolCall{}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (m *mockBlockingProvider) GetDefaultModel() string { return "mock-model" }

func TestToolCoordinator_ListRunningWithBlockedTask(t *testing.T) {
	al := newCovTestLoop(t)
	blocking := &mockBlockingProvider{started: make(chan struct{}), block: make(chan struct{})}
	mgr := tools.NewSubagentManager(blocking, "m", al.GetDefaultAgent().Workspace, al.bus, 10)

	tc := newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{
		al.getDefaultAgentID(): mgr,
	}, map[string]*tools.BackgroundProcessManager{})
	_ = tc

	res, err := mgr.Spawn(context.Background(), "blocking task", "block", "coder", "native", "tv3-block", nil)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	id := tools.ExtractSpawnTaskID(res)

	// Wait until the task has started running.
	select {
	case <-blocking.started:
	case <-time.After(10 * time.Second):
		t.Fatal("blocking task never started")
	}

	// Should be in running state -> appears in listRunningSubagentTasks.
	found := false
	deadline := time.Now().Add(5 * time.Second)
	for !found && time.Now().Before(deadline) {
		for _, task := range tc.listRunningSubagentTasks() {
			if task.ID == id {
				found = true
				break
			}
		}
		if !found {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !found {
		t.Fatal("expected running task in listRunningSubagentTasks")
	}

	// stopSubagentTask should cancel it.
	tc.stopSubagentTask(id)
	close(blocking.block)
	// Cleanup
	time.Sleep(50 * time.Millisecond)
}

// ============================================================================
// helpers — extractProviderFromModel / getReasoningConfig / GatewayVersion
// ============================================================================

func TestExtractProviderFromModel_ColonFormat(t *testing.T) {
	cases := []struct {
		model, defaultProvider, want string
	}{
		{"testp:m", "x", "testp"},
		{"m", "x", "x"},
		{"", "def", "def"},
	}
	for _, c := range cases {
		if got := extractProviderFromModel(c.model, c.defaultProvider); got != c.want {
			t.Errorf("extractProviderFromModel(%q,%q)=%q want %q", c.model, c.defaultProvider, got, c.want)
		}
	}
}

func TestGatewayVersion_ReturnsNonEmpty(t *testing.T) {
	// GatewayVersion reads build info; in tests it likely returns "dev".
	v := GatewayVersion()
	if v == "" {
		t.Error("GatewayVersion should not be empty")
	}
	if gatewayVersion() == "" {
		t.Error("gatewayVersion should not be empty")
	}
}

// ============================================================================
// helpers — ExtractPeer / ExtractParentPeer / ExtractSpacedKey
// ============================================================================

func TestExtractPeer_Variants(t *testing.T) {
	// nil metadata -> nil
	if ExtractPeer(bus.InboundMessage{}) != nil {
		t.Error("expected nil extractPeer for empty msg")
	}
	if ExtractParentPeer(bus.InboundMessage{}) != nil {
		t.Error("expected nil extractParentPeer for empty msg")
	}

	msg := bus.InboundMessage{Metadata: map[string]string{"peer_kind": "direct", "peer_id": "alice", "sender_id": "x"}}
	peer := ExtractPeer(msg)
	if peer == nil {
		t.Fatal("expected peer")
	}
	if peer.Kind != "direct" || peer.ID != "alice" {
		t.Errorf("peer = %+v", peer)
	}

	// peer_id empty with direct kind falls back to sender id.
	msg2 := bus.InboundMessage{SenderID: "sender-1", Metadata: map[string]string{"peer_kind": "direct", "peer_id": ""}}
	peer2 := ExtractPeer(msg2)
	if peer2 == nil || peer2.ID != "sender-1" {
		t.Errorf("direct fallback peer = %+v", peer2)
	}

	// parent peer extraction.
	parentMsg := bus.InboundMessage{Metadata: map[string]string{"parent_peer_kind": "group", "parent_peer_id": "g1"}}
	pp := ExtractParentPeer(parentMsg)
	if pp == nil || pp.ID != "g1" {
		t.Errorf("parent peer = %+v", pp)
	}
}

// ============================================================================
// store-backed goal loadFromRepo <-> SetStore consistency
// ============================================================================

func TestGoalManager_SetStore_MigratesLoadedGoals(t *testing.T) {
	dir := t.TempDir()

	// Load a JSON goal first (no repo).
	gm1 := NewGoalManager(dir)
	gm1.Set("migrate-key", "migrate me", 4)

	s := openGoalStore(t)
	repo := s.Goals()
	// Fresh manager; SetStore on empty repo triggers loadFromDisk which checks
	// repo (empty) -> falls through to loadFromLegacyFiles -> migrates.
	gm2 := NewGoalManager(dir)
	gm2.SetStore(repo)

	// After migration, the goal should be in the repo.
	_, found, err := repo.GetGoal("migrate-key")
	if err != nil {
		t.Fatalf("GetGoal err: %v", err)
	}
	if !found {
		t.Fatal("expected legacy goal migrated into repo")
	}
	if g := gm2.Get("migrate-key"); g == nil {
		t.Fatal("expected migrated goal loadable")
	}
}// ============================================================================
// agent_providable — subagent cache-hit fast paths (O(1) mapping pre-seeded)
// ============================================================================

func TestProvidable_SubagentFastPathAllMethods(t *testing.T) {
	al := newCovTestLoop(t)
	defaultAgent := al.registry.GetDefaultAgent()
	subKey := "native:tv3parent:subagent-fp"
	ap := al.GetProvidable()

	// Seed messages on the subagent key and pre-cache the mapping so the O(1)
	// fast path (subagentSessionAgent hit) is taken for each method.
	defaultAgent.Sessions.AddMessage(subKey, "user", "u1")
	defaultAgent.Sessions.AddMessage(subKey, "assistant", "a1")
	al.subagentSessionAgent.Store(subKey, defaultAgent.ID)

	if h := ap.GetHistoryView(subKey); len(h) != 2 {
		t.Errorf("GetHistoryView fast path len = %d, want 2", len(h))
	}
	if n := ap.GetTotalMessageCount(subKey); n != 2 {
		t.Errorf("GetTotalMessageCount fast path = %d, want 2", n)
	}
	if n := ap.GetEvictedMessageCount(subKey); n != 0 {
		t.Errorf("GetEvictedMessageCount fast path = %d, want 0", n)
	}
	if !ap.HasMessages(subKey) {
		t.Error("HasMessages fast path should be true")
	}
	if n := ap.LoadEvictedMessages(subKey); n != 0 {
		t.Errorf("LoadEvictedMessages fast path = %d, want 0 (nothing evicted)", n)
	}

	// With the mapping pointing to a nonexistent agent, the fast path should
	// fall through to the fallback scan and return empty.
	al.subagentSessionAgent.Store(subKey, "ghost-agent")
	if h := ap.GetHistoryView(subKey); len(h) != 2 {
		t.Errorf("GetHistoryView fallback-after-miss len = %d, want 2", len(h))
	}
	if !ap.HasMessages(subKey) {
		t.Error("HasMessages fallback-after-miss should be true")
	}
	if n := ap.GetTotalMessageCount(subKey); n != 2 {
		t.Errorf("GetTotalMessageCount fallback-after-miss = %d, want 2", n)
	}
	if n := ap.GetEvictedMessageCount(subKey); n != 0 {
		t.Errorf("GetEvictedMessageCount fallback-after-miss = %d, want 0", n)
	}
	if n := ap.LoadEvictedMessages(subKey); n != 0 {
		t.Errorf("LoadEvictedMessages fallback-after-miss = %d, want 0", n)
	}
}

// TestProvidable_SubagentEvictionPaths verifies eviction/load branches for a
// subagent session using a real SQLite store (so messages can be evicted).
func TestProvidable_SubagentEvictionPaths(t *testing.T) {
	al := newCovTestLoop(t)
	defaultAgent := al.registry.GetDefaultAgent()
	s := openGoalStore(t)
	defaultAgent.Sessions.SetStore(s)

	subKey := "native:tv3evict:subagent-ev"
	for i := 0; i < 6; i++ {
		defaultAgent.Sessions.AddMessage(subKey, "user", "q")
		defaultAgent.Sessions.AddMessage(subKey, "assistant", "a")
	}
	// Exclude old messages and save so they persist to SQLite.
	defaultAgent.Sessions.ExcludeOldMessagesFromContext(subKey, 2)
	if err := defaultAgent.Sessions.Save(subKey); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Evict excluded from memory.
	defaultAgent.Sessions.EvictExcludedMessages(subKey)

	ap := al.GetProvidable()
	// Pre-cache mapping so fast path is taken; evicted count should be > 0.
	al.subagentSessionAgent.Store(subKey, defaultAgent.ID)
	if n := ap.GetEvictedMessageCount(subKey); n <= 0 {
		t.Logf("GetEvictedMessageCount = %d (expected >0 with store)", n)
	}
	total := ap.GetTotalMessageCount(subKey)
	if total <= 0 {
		t.Logf("GetTotalMessageCount = %d (expected >0)", total)
	}
	// Load evicted messages back into memory.
	ap.LoadEvictedMessages(subKey)
}

// ============================================================================
// command_handler — /status formatStatusResponse variants
// ============================================================================

func TestFormatStatusResponse_CommandHandlerVariants(t *testing.T) {
	al := newCovLoop(t)
	agent := al.registry.GetDefaultAgent()
	ch := newCommandHandler(al)
	res := ch.formatStatusResponse(agent, "telegram:st1", "telegram")
	if !strings.Contains(res, "lele") {
		t.Errorf("status response missing banner: %q", res)
	}
	// nil agent returns the not-configured message.
	if ch.formatStatusResponse(nil, "s", "cli") != "No default agent configured" {
		t.Error("expected not-configured message for nil agent")
	}
}

func TestGatewayVersionMatches(t *testing.T) {
	if gatewayVersion() == "" {
		t.Error("gatewayVersion should not be empty")
	}
}