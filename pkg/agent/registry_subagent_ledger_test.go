package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

func newLedgerRegistry(t *testing.T) (*AgentRegistry, *config.Config) {
	t.Helper()
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.List = []config.AgentConfig{
		{ID: "alpha"},
		{ID: "beta"},
	}
	return NewAgentRegistry(cfg), cfg
}

func askLedgerDigest(t *testing.T, r *AgentRegistry, agentID, sessionKey string) string {
	t.Helper()
	agent, ok := r.GetAgent(agentID)
	if !ok || agent == nil || agent.ContextBuilder == nil {
		t.Fatalf("agent %q not found", agentID)
	}
	return agent.ContextBuilder.resolveSubagentLedger(sessionKey)
}

func TestRegistry_SetSubagentLedgerResolverBindsInstanceID(t *testing.T) {
	r, _ := newLedgerRegistry(t)

	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string {
		return agentID + "|" + sessionKey
	})

	for _, id := range []string{"alpha", "beta"} {
		if got, want := askLedgerDigest(t, r, id, "native:s1"), id+"|native:s1"; got != want {
			t.Errorf("agent %q: digest = %q, want %q", id, got, want)
		}
	}
}

func TestRegistry_SubagentLedgerResolverSurvivesReload(t *testing.T) {
	r, cfg := newLedgerRegistry(t)
	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string {
		return agentID + "|" + sessionKey
	})

	r.ReloadAgents(cfg)
	for _, id := range []string{"alpha", "beta"} {
		if got, want := askLedgerDigest(t, r, id, "native:s1"), id+"|native:s1"; got != want {
			t.Errorf("preserved agent %q lost the resolver: %q", id, got)
		}
	}

	updated := config.DefaultConfig()
	updated.Agents.Defaults.Workspace = cfg.Agents.Defaults.Workspace
	updated.Agents.Defaults.Model = "someother/model"
	updated.Agents.List = cfg.Agents.List
	r.ReloadAgents(updated)
	for _, id := range []string{"alpha", "beta"} {
		if got, want := askLedgerDigest(t, r, id, "native:s1"), id+"|native:s1"; got != want {
			t.Errorf("recreated agent %q lost the resolver: %q", id, got)
		}
	}
}

func TestRegistry_SubagentLedgerResolverSurvivesImplicitMainReload(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.List = []config.AgentConfig{{ID: "alpha"}}

	r := NewAgentRegistry(cfg)
	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string {
		return agentID + "|" + sessionKey
	})

	implicit := config.DefaultConfig()
	implicit.Agents.Defaults.Workspace = workspace
	implicit.Agents.List = nil
	r.ReloadAgents(implicit)

	if got, want := askLedgerDigest(t, r, "main", "native:s1"), "main|native:s1"; got != want {
		t.Errorf("implicit main agent lost the resolver: %q", got)
	}
}

func TestRegistry_SubagentLedgerResolverWiresPreservedBuilderOnReload(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.MaxToolIterations = 20
	cfg.Agents.List = []config.AgentConfig{{ID: "alpha", Workspace: workspace}}

	r := NewAgentRegistry(cfg)
	agent, ok := r.GetAgent("alpha")
	if !ok {
		t.Fatal("precondition: agent alpha missing")
	}
	agent.ContextBuilder.SetSubagentLedgerResolver(nil)

	r.mu.Lock()
	r.subagentLedgerResolver = func(agentID, sessionKey string) string { return agentID }
	r.mu.Unlock()

	r.ReloadAgents(cfg)

	if got := askLedgerDigest(t, r, "alpha", "native:s1"); got != "alpha" {
		t.Errorf("preserved builder was not re-wired on reload: digest = %q", got)
	}
}

func TestRegistry_SubagentLedgerResolverWiresRecreatedBuilderOnReload(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.List = []config.AgentConfig{{ID: "alpha"}}

	r := NewAgentRegistry(cfg)
	before, ok := r.GetAgent("alpha")
	if !ok {
		t.Fatal("precondition: agent alpha missing")
	}
	beforeBuilder := before.ContextBuilder

	r.mu.Lock()
	r.subagentLedgerResolver = func(agentID, sessionKey string) string { return agentID }
	r.mu.Unlock()

	updated := config.DefaultConfig()
	updated.Agents.Defaults.Workspace = workspace
	updated.Agents.List = []config.AgentConfig{{ID: "alpha"}, {ID: "delta"}}
	r.ReloadAgents(updated)

	after, ok := r.GetAgent("delta")
	if !ok {
		t.Fatal("agent delta missing after reload")
	}
	if after.ContextBuilder == beforeBuilder {
		t.Fatal("precondition: a new agent must have a fresh ContextBuilder")
	}
	if got := askLedgerDigest(t, r, "delta", "native:s1"); got != "delta" {
		t.Errorf("recreated builder was not wired on reload: digest = %q", got)
	}
}

func TestRegistry_NilSubagentLedgerResolverClearsWiredBuilders(t *testing.T) {
	r, _ := newLedgerRegistry(t)
	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string { return "wired" })

	if got := askLedgerDigest(t, r, "alpha", "native:s1"); got != "wired" {
		t.Fatalf("precondition: resolver not wired, got %q", got)
	}

	r.SetSubagentLedgerResolver(nil)
	for _, id := range []string{"alpha", "beta"} {
		if got := askLedgerDigest(t, r, id, "native:s1"); got != "" {
			t.Errorf("agent %q still renders a digest after clearing: %q", id, got)
		}
	}
}

func TestRegistry_SubagentLedgerResolverDoesNotPinStaleInstance(t *testing.T) {
	r, _ := newLedgerRegistry(t)
	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string { return agentID })

	agent, ok := r.GetAgent("alpha")
	if !ok {
		t.Fatal("precondition: agent alpha missing")
	}
	if got := agent.ContextBuilder.resolveSubagentLedger("native:s1"); got != "alpha" {
		t.Fatalf("precondition: digest = %q, want alpha", got)
	}

	agent.ID = "renamed"
	r.mu.Lock()
	r.applySubagentLedgerResolverLocked(agent)
	r.mu.Unlock()

	if got := agent.ContextBuilder.resolveSubagentLedger("native:s1"); got != "renamed" {
		t.Errorf("closure reports %q, want renamed (ID captured at bind time instead of read at call time)", got)
	}
}

func TestLoop_AttachSubagentLedgerResolverNilCoordinator(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	al.toolCoordinator = nil

	al.attachSubagentLedgerResolver()

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	if got := agent.ContextBuilder.resolveSubagentLedger("native:s1"); got != "" {
		t.Errorf("nil coordinator must leave the resolver unwired, got %q", got)
	}
}

func TestLoop_AttachSubagentLedgerResolverWiresPrompt(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}

	sm := al.toolCoordinator.GetSubagents()[agent.ID]
	if sm == nil {
		t.Fatalf("no subagent manager for agent %q", agent.ID)
	}

	const sessionKey = "native:client-1:conv-1"
	addLedgerDigestTask(sm, "subagent-1", sessionKey, tools.SubagentStatusRunning, "coder", "Audit auth", time.Now().UnixMilli())

	prompt := agent.ContextBuilder.BuildSystemPromptForSessionWithFolder(sessionKey, "native")
	if !strings.Contains(prompt, "## Subagent Tasks") {
		t.Fatalf("prompt is missing the ledger section:\n%s", prompt)
	}
	if !strings.Contains(prompt, `- subagent-1 [running] coder "Audit auth"`) {
		t.Errorf("prompt is missing the task row:\n%s", prompt)
	}
}

func addLedgerDigestTask(sm *tools.SubagentManager, id, origin, status, agentID, label string, created int64) *tools.SubagentTask {
	task := &tools.SubagentTask{
		ID:               id,
		Task:             "task " + id,
		Label:            label,
		AgentID:          agentID,
		OriginChannel:    "native",
		OriginChatID:     "client-1",
		OriginSessionKey: origin,
		Status:           status,
		Created:          created,
		Updated:          created,
	}
	sm.AddTaskForTest(task, func() {})
	return task
}

func newDigestTestCoordinator(t *testing.T, agentIDs ...string) (*toolCoordinatorImpl, *AgentLoop) {
	t.Helper()
	al, _ := createLLMRunnerTestAgentLoop(t)
	subagents := make(map[string]*tools.SubagentManager, len(agentIDs))
	for _, id := range agentIDs {
		subagents[id] = tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	}
	return newToolCoordinatorWithSubagents(al, subagents, nil, nil), al
}

func TestRenderSubagentLedgerDigest_EmptySessionReturnsEmpty(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")

	for _, tc2 := range []struct{ agentID, sessionKey string }{
		{"main", "native:no-tasks"},
		{"main", ""},
		{"missing", "native:s1"},
	} {
		if got := tc.renderSubagentLedgerDigest(tc2.agentID, tc2.sessionKey); got != "" {
			t.Errorf("agent=%q session=%q: digest = %q, want empty", tc2.agentID, tc2.sessionKey, got)
		}
	}

	var nilTC *toolCoordinatorImpl
	if got := nilTC.renderSubagentLedgerDigest("main", "native:s1"); got != "" {
		t.Errorf("nil coordinator digest = %q, want empty", got)
	}
}

func TestRenderSubagentLedgerDigest_MaxEightRowsAndOverflow(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:overflow"

	base := time.Now().UnixMilli()
	for i := 1; i <= 11; i++ {
		addLedgerDigestTask(sm, "subagent-"+itoa(i), sessionKey, tools.SubagentStatusCompleted, "coder", "task", base+int64(i))
	}

	digest := tc.renderSubagentLedgerDigest("main", sessionKey)
	if digest == "" {
		t.Fatal("digest is empty for a session with 11 tasks")
	}

	rows := digestRows(digest)
	if len(rows) != 8 {
		t.Fatalf("rendered %d rows, want 8:\n%s", len(rows), digest)
	}
	if !strings.HasPrefix(rows[0], "- subagent-11 ") {
		t.Errorf("first row = %q, want subagent-11", rows[0])
	}
	if !strings.HasPrefix(rows[7], "- subagent-4 ") {
		t.Errorf("last row = %q, want subagent-4", rows[7])
	}
	if !strings.Contains(digest, "… 3 older task(s) not shown.") {
		t.Errorf("missing overflow line for 3 hidden rows:\n%s", digest)
	}

	tc2, _ := newDigestTestCoordinator(t, "main")
	sm2 := tc2.subagents["main"]
	for i := 1; i <= 8; i++ {
		addLedgerDigestTask(sm2, "subagent-"+itoa(i), sessionKey, tools.SubagentStatusCompleted, "coder", "task", base+int64(i))
	}
	digest2 := tc2.renderSubagentLedgerDigest("main", sessionKey)
	if strings.Contains(digest2, "older task(s) not shown") {
		t.Errorf("8 tasks must not render an overflow line:\n%s", digest2)
	}
	if got := len(digestRows(digest2)); got != 8 {
		t.Errorf("rendered %d rows for 8 tasks, want 8", got)
	}
}

func TestRenderSubagentLedgerDigest_NoElapsedTime(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:elapsed"

	addLedgerDigestTask(sm, "subagent-1", sessionKey, tools.SubagentStatusRunning, "coder", "Audit auth",
		time.Now().Add(-2*time.Hour).UnixMilli())

	digest := tc.renderSubagentLedgerDigest("main", sessionKey)
	if digest == "" {
		t.Fatal("digest is empty")
	}
	for _, forbidden := range []string{"(running ", "s)", "m)", "h)"} {
		if strings.Contains(digest, forbidden) {
			t.Errorf("digest contains elapsed-time marker %q:\n%s", forbidden, digest)
		}
	}

	if again := tc.renderSubagentLedgerDigest("main", sessionKey); again != digest {
		t.Errorf("digest is not stable across renders:\nfirst:  %q\nsecond: %q", digest, again)
	}
}

func TestRenderSubagentLedgerDigest_MetadataOnlyNoStoreQuery(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:metadata"

	addLedgerDigestTask(sm, "subagent-2", sessionKey, tools.SubagentStatusRunning, "coder", "live", time.Now().UnixMilli())

	var mu sync.Mutex
	listerCalls := 0
	sm.SetLedgerWiring(
		func(parentPrefixes []string) []tools.PersistedSubagentTask {
			mu.Lock()
			listerCalls++
			mu.Unlock()
			return []tools.PersistedSubagentTask{{
				TaskID:     "subagent-1",
				SessionKey: sessionKey + ":subagent-1",
				ParentKey:  sessionKey,
				Label:      "old task",
				Status:     tools.SubagentStatusCompleted,
				Created:    time.Now().Add(-time.Hour).UnixMilli(),
			}}
		},
		func(string) bool { return false },
	)
	detailCalls := 0
	sm.SetPersistedTaskDetail(func(keys []string) map[string]tools.PersistedSubagentDetail {
		detailCalls++
		return nil
	})

	digest := tc.renderSubagentLedgerDigest("main", sessionKey)
	if digest == "" {
		t.Fatal("digest is empty")
	}
	if !strings.Contains(digest, "- subagent-1 [completed] (persisted)") {
		t.Errorf("persisted row missing or malformed:\n%s", digest)
	}

	mu.Lock()
	calls := listerCalls
	mu.Unlock()
	if calls != 1 {
		t.Errorf("lister invoked %d times for one digest, want 1", calls)
	}
	if detailCalls != 0 {
		t.Errorf("detail source invoked %d times; the digest must not query the store", detailCalls)
	}
}

func TestRenderSubagentLedgerDigest_ExecutorFallbackUsesPin(t *testing.T) {
	tc, al := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:pin"
	pinnedKey := sessionKey + ":subagent-1"

	al.setSubagentSessionAgent(pinnedKey, "coder")

	sm.SetLedgerWiring(
		func([]string) []tools.PersistedSubagentTask {
			return []tools.PersistedSubagentTask{{
				TaskID:     "subagent-1",
				SessionKey: pinnedKey,
				ParentKey:  sessionKey,
				Label:      "legacy task",
				Status:     tools.SubagentStatusCompleted,
				AgentID:    "",
				Created:    time.Now().UnixMilli(),
			}}
		},
		func(string) bool { return false },
	)
	digest := tc.renderSubagentLedgerDigest("main", sessionKey)
	if !strings.Contains(digest, `- subagent-1 [completed] (persisted) coder "legacy task"`) {
		t.Errorf("persisted row did not fall back to the pin:\n%s", digest)
	}
}

func TestRenderSubagentLedgerDigest_AliasAndBaseBothResolve(t *testing.T) {
	tc, al := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const base = "native:client-1"
	const alias = "native:client-1:chat:2"

	addLedgerDigestTask(sm, "subagent-1", base, tools.SubagentStatusCompleted, "coder", "Docs review", time.Now().UnixMilli())

	al.setSessionAlias(base, alias)

	for _, key := range []string{base, alias} {
		digest := tc.renderSubagentLedgerDigest("main", key)
		if !strings.Contains(digest, "- subagent-1 [completed]") {
			t.Errorf("session key %q did not resolve the ledger:\n%s", key, digest)
		}
	}
}

func TestRenderSubagentLedgerDigest_SingleManagerNotSweptPerAgent(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "alpha", "beta", "gamma")
	const sessionKey = "native:sweep"

	var mu sync.Mutex
	calls := make(map[string]int)
	for id, sm := range tc.subagents {
		agentID := id
		sm.SetLedgerWiring(
			func([]string) []tools.PersistedSubagentTask {
				mu.Lock()
				calls[agentID]++
				mu.Unlock()
				return nil
			},
			func(string) bool { return false },
		)
		addLedgerDigestTask(sm, "subagent-1", sessionKey, tools.SubagentStatusCompleted, agentID, "task", time.Now().UnixMilli())
	}

	digest := tc.renderSubagentLedgerDigest("beta", sessionKey)
	if digest == "" {
		t.Fatal("digest is empty")
	}

	mu.Lock()
	defer mu.Unlock()
	if calls["beta"] != 1 {
		t.Errorf("owning manager invoked %d times, want 1", calls["beta"])
	}
	for _, other := range []string{"alpha", "gamma"} {
		if calls[other] != 0 {
			t.Errorf("manager %q was swept (%d calls); the digest must read one manager", other, calls[other])
		}
	}
}

func TestRenderSubagentLedgerDigest_SummaryOnlyForLiveRows(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:summary"

	live := addLedgerDigestTask(sm, "subagent-2", sessionKey, tools.SubagentStatusCompleted, "coder", "Docs review", time.Now().UnixMilli())
	live.Summary = "3 gaps found"
	sm.AddTaskForTest(live, func() {})

	sm.SetLedgerWiring(
		func([]string) []tools.PersistedSubagentTask {
			return []tools.PersistedSubagentTask{{
				TaskID:     "subagent-1",
				SessionKey: sessionKey + ":subagent-1",
				ParentKey:  sessionKey,
				Label:      "Auth audit",
				Status:     tools.SubagentStatusFailed,
				Created:    time.Now().Add(-time.Hour).UnixMilli(),
			}}
		},
		func(string) bool { return false },
	)
	digest := tc.renderSubagentLedgerDigest("main", sessionKey)
	if !strings.Contains(digest, `- subagent-2 [completed] coder "Docs review": 3 gaps found`) {
		t.Errorf("live row must carry its summary:\n%s", digest)
	}
	if !strings.Contains(digest, `- subagent-1 [failed] (persisted) "Auth audit"`) {
		t.Errorf("persisted row must render label only:\n%s", digest)
	}
}

func TestRenderSubagentLedgerDigest_FormatPinsTheHeaderAndHint(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:format"

	addLedgerDigestTask(sm, "subagent-1", sessionKey, tools.SubagentStatusRunning, "coder", "Audit auth", time.Now().UnixMilli())

	digest := tc.renderSubagentLedgerDigest("main", sessionKey)
	if !strings.HasPrefix(digest, "## Subagent Tasks\n\n") {
		t.Errorf("digest must start with the section header:\n%s", digest)
	}
	if !strings.Contains(digest, "survives context compaction") {
		t.Errorf("header must state the compaction property:\n%s", digest)
	}
	if !strings.HasSuffix(digest, subagentLedgerDigestHint) {
		t.Errorf("digest must end with the static usage hint:\n%s", digest)
	}
	if !strings.Contains(digest, "`list_active_subagents`") || !strings.Contains(digest, "`wait_for_subagent`") {
		t.Errorf("hint must name both tools:\n%s", digest)
	}
}

func digestRows(digest string) []string {
	var rows []string
	for _, line := range strings.Split(digest, "\n") {
		if strings.HasPrefix(line, "- ") {
			rows = append(rows, line)
		}
	}
	return rows
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

var _ = context.Background
