package agent

// Tests for the registry + loop wiring of the subagent ledger digest (plan T6).
//
// T5 gave the ContextBuilder a resolver; nothing reaches the prompt until the
// registry propagates it to every agent's builder and the loop supplies it. The
// wiring has three failure modes that are invisible at compile time, so each
// gets a test:
//
//   - the resolver captures the AgentInstance POINTER instead of its ID, so
//     after ReloadAgents (which preserves the ContextBuilder but replaces the
//     instance) every agent renders the ledger of a dead object (D7);
//   - one of the three apply sites is missed, so a reload path silently loses
//     the section (the brief's correction to the plan's "two sites");
//   - the digest sweeps every agent's manager instead of the owning one, which
//     multiplies the per-turn cost by the number of agents (C6).
//
// The digest renderer itself is covered here too, because its contract is what
// the wiring is for: metadata only, no elapsed time, at most 8 rows.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

// newLedgerRegistry builds a registry with two agents ("alpha" and "beta") over
// a throwaway workspace.
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

// askLedgerDigest asks an agent's builder for the ledger digest of a session.
func askLedgerDigest(t *testing.T, r *AgentRegistry, agentID, sessionKey string) string {
	t.Helper()
	agent, ok := r.GetAgent(agentID)
	if !ok || agent == nil || agent.ContextBuilder == nil {
		t.Fatalf("agent %q not found", agentID)
	}
	return agent.ContextBuilder.resolveSubagentLedger(sessionKey)
}

// TestRegistry_SetSubagentLedgerResolverBindsInstanceID pins D7's "by value"
// half: each agent's builder must report its OWN ID, not the last one wired and
// not an empty string.
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

// TestRegistry_SubagentLedgerResolverSurvivesReload pins the brief's correction
// to the plan: applyFolderResolverLocked has THREE call sites (the implicit-main
// branch, the preserved agent and the recreated agent) and the ledger apply must
// sit next to all of them. It exercises the preserved and the recreated paths.
func TestRegistry_SubagentLedgerResolverSurvivesReload(t *testing.T) {
	r, cfg := newLedgerRegistry(t)
	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string {
		return agentID + "|" + sessionKey
	})

	// Preserved path: unchanged config keeps the instance and its builder.
	r.ReloadAgents(cfg)
	for _, id := range []string{"alpha", "beta"} {
		if got, want := askLedgerDigest(t, r, id, "native:s1"), id+"|native:s1"; got != want {
			t.Errorf("preserved agent %q lost the resolver: %q", id, got)
		}
	}

	// Recreated path: a changed model gives the agent a fresh ContextBuilder.
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

// TestRegistry_SubagentLedgerResolverSurvivesImplicitMainReload pins the third
// apply site: the branch that recreates the implicit "main" agent when
// agents.list is empty. Without the ledger apply there, the section vanishes for
// every single-agent deployment on the first reload.
func TestRegistry_SubagentLedgerResolverSurvivesImplicitMainReload(t *testing.T) {
	workspace := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.List = []config.AgentConfig{{ID: "alpha"}}

	r := NewAgentRegistry(cfg)
	r.SetSubagentLedgerResolver(func(agentID, sessionKey string) string {
		return agentID + "|" + sessionKey
	})

	// Empty agents.list drops "alpha" and recreates the implicit "main" agent.
	implicit := config.DefaultConfig()
	implicit.Agents.Defaults.Workspace = workspace
	implicit.Agents.List = nil
	r.ReloadAgents(implicit)

	if got, want := askLedgerDigest(t, r, "main", "native:s1"), "main|native:s1"; got != want {
		t.Errorf("implicit main agent lost the resolver: %q", got)
	}
}

// TestRegistry_SubagentLedgerResolverWiresPreservedBuilderOnReload pins the
// PRESERVED apply site (registry.go, the "config unchanged" branch). That branch
// re-attaches the resolver so a builder created before SetSubagentLedgerResolver
// is wired on the next reload — the same reason applyFolderResolverLocked is
// re-run there. Without it, an agent whose builder predates the resolver keeps
// rendering no ledger forever.
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
	// Simulate a builder that predates the resolver: clear it directly.
	agent.ContextBuilder.SetSubagentLedgerResolver(nil)

	// Store the resolver WITHOUT applying it (the loop's attach path applies,
	// so this models "the resolver arrived after the builder was built").
	r.mu.Lock()
	r.subagentLedgerResolver = func(agentID, sessionKey string) string { return agentID }
	r.mu.Unlock()

	// An unchanged reload takes the preserved branch and must re-attach.
	r.ReloadAgents(cfg)

	if got := askLedgerDigest(t, r, "alpha", "native:s1"); got != "alpha" {
		t.Errorf("preserved builder was not re-wired on reload: digest = %q", got)
	}
}

// TestRegistry_SubagentLedgerResolverWiresRecreatedBuilderOnReload pins the
// RECREATED apply site: an agent whose workspace changed gets a fresh
// ContextBuilder (the preserved-builder migration only applies when a previous
// instance exists for the same key, and a workspace change rebuilds the builder
// from the new path), which must be wired or the section vanishes after a
// config edit.
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

	// Store the resolver without applying it, then force a recreation.
	r.mu.Lock()
	r.subagentLedgerResolver = func(agentID, sessionKey string) string { return agentID }
	r.mu.Unlock()

	// A new agent (different ID) has no previous instance, so its builder is
	// freshly built by NewAgentInstance and only the recreated apply site can
	// wire it.
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

// TestRegistry_NilSubagentLedgerResolverClearsWiredBuilders pins that clearing
// reaches builders that were already wired: a nil resolver must be propagated,
// not skipped, or disabling the section would be impossible at runtime.
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

// TestRegistry_SubagentLedgerResolverDoesNotPinStaleInstance pins D7's "not the
// pointer" half at the only place it is observable.
//
// ReloadAgents keys instances by normalized ID, so a rename is a delete+create
// and never exercises the preserved branch with a changed ID. What D7 actually
// protects is the closure's binding: it must read the instance's ID at CALL
// time, never a copy taken when the builder was wired, and never a captured
// instance pointer that a later replacement would leave dangling. This test
// re-applies the resolver to an instance whose ID changed in place — the
// builder survives, the identity does not — and asserts the closure follows.
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

	// The instance is renamed in place: its ContextBuilder (and therefore the
	// wired closure) survives, its ID does not.
	agent.ID = "renamed"
	r.mu.Lock()
	r.applySubagentLedgerResolverLocked(agent)
	r.mu.Unlock()

	if got := agent.ContextBuilder.resolveSubagentLedger("native:s1"); got != "renamed" {
		t.Errorf("closure reports %q, want renamed (ID captured at bind time instead of read at call time)", got)
	}
}

// TestLoop_AttachSubagentLedgerResolverNilCoordinator pins the nil-guard: tests
// build loops without a coordinator and the attach must be a no-op, not a panic.
func TestLoop_AttachSubagentLedgerResolverNilCoordinator(t *testing.T) {
	al, _ := createLLMRunnerTestAgentLoop(t)
	al.toolCoordinator = nil

	// Must not panic.
	al.attachSubagentLedgerResolver()

	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	if got := agent.ContextBuilder.resolveSubagentLedger("native:s1"); got != "" {
		t.Errorf("nil coordinator must leave the resolver unwired, got %q", got)
	}
}

// TestLoop_AttachSubagentLedgerResolverWiresPrompt is the end-to-end check: a
// loop built the normal way renders the digest of a task registered in its
// agent's manager, straight into the system prompt.
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

// addLedgerDigestTask registers a live task in a manager with the given identity
// and returns it.
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

// newDigestTestCoordinator builds a real coordinator over a test loop with one
// manager per given agent ID.
func newDigestTestCoordinator(t *testing.T, agentIDs ...string) (*toolCoordinatorImpl, *AgentLoop) {
	t.Helper()
	al, _ := createLLMRunnerTestAgentLoop(t)
	subagents := make(map[string]*tools.SubagentManager, len(agentIDs))
	for _, id := range agentIDs {
		subagents[id] = tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	}
	return newToolCoordinatorWithSubagents(al, subagents, nil, nil), al
}

// TestRenderSubagentLedgerDigest_EmptySessionReturnsEmpty pins acceptance 5 at
// the renderer: a session with no tasks renders nothing at all, so the prompt
// stays byte-identical.
func TestRenderSubagentLedgerDigest_EmptySessionReturnsEmpty(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")

	for _, tc2 := range []struct{ agentID, sessionKey string }{
		{"main", "native:no-tasks"},
		{"main", ""},             // no session key
		{"missing", "native:s1"}, // unknown agent
	} {
		if got := tc.renderSubagentLedgerDigest(tc2.agentID, tc2.sessionKey); got != "" {
			t.Errorf("agent=%q session=%q: digest = %q, want empty", tc2.agentID, tc2.sessionKey, got)
		}
	}

	// A nil coordinator must not panic either.
	var nilTC *toolCoordinatorImpl
	if got := nilTC.renderSubagentLedgerDigest("main", "native:s1"); got != "" {
		t.Errorf("nil coordinator digest = %q, want empty", got)
	}
}

// TestRenderSubagentLedgerDigest_MaxEightRowsAndOverflow pins the row cap: at
// most 8 rows, then the overflow line with the exact count of hidden rows.
func TestRenderSubagentLedgerDigest_MaxEightRowsAndOverflow(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:overflow"

	// 11 tasks, newest = highest ID (Created ascends with the ID).
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
	// Newest first: subagent-11 … subagent-4.
	if !strings.HasPrefix(rows[0], "- subagent-11 ") {
		t.Errorf("first row = %q, want subagent-11", rows[0])
	}
	if !strings.HasPrefix(rows[7], "- subagent-4 ") {
		t.Errorf("last row = %q, want subagent-4", rows[7])
	}
	if !strings.Contains(digest, "… 3 older task(s) not shown.") {
		t.Errorf("missing overflow line for 3 hidden rows:\n%s", digest)
	}

	// Exactly 8 tasks ⇒ no overflow line.
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

// TestRenderSubagentLedgerDigest_NoElapsedTime pins C12: the digest must not
// render elapsed time. A volatile field would change the prompt on every turn
// even when nothing happened, breaking the byte-identical-prompt invariant
// BuildMessages documents (and the prompt cache with it).
func TestRenderSubagentLedgerDigest_NoElapsedTime(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:elapsed"

	// A task that started long ago: the tool WOULD render "(running 2h...)".
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

	// Rendering twice must be byte-identical: the section is stable across turns.
	if again := tc.renderSubagentLedgerDigest("main", sessionKey); again != digest {
		t.Errorf("digest is not stable across renders:\nfirst:  %q\nsecond: %q", digest, again)
	}
}

// TestRenderSubagentLedgerDigest_MetadataOnlyNoStoreQuery pins C5/C11/D2: the
// digest is rendered on every turn and on every context-usage refresh, so it
// must never touch the session store. A counting lister proves the disk side is
// read through the metadata mirror only, and a counting repo proves no store
// query happens at all.
func TestRenderSubagentLedgerDigest_MetadataOnlyNoStoreQuery(t *testing.T) {
	tc, _ := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:metadata"

	// A live task plus a persisted row, so both merge paths run.
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
	// A detail source that must never be consulted: it is the store-querying
	// path, and the digest renders persisted rows by label only.
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

// TestRenderSubagentLedgerDigest_ExecutorFallbackUsesPin pins R8/C7: a persisted
// row written before agent_id existed carries an empty AgentID and must fall
// back to the spawn-time pin, not be rendered without an executor.
func TestRenderSubagentLedgerDigest_ExecutorFallbackUsesPin(t *testing.T) {
	tc, al := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const sessionKey = "native:pin"
	pinnedKey := sessionKey + ":subagent-1"

	// The pin is what setSubagentSessionAgent records at spawn time.
	al.setSubagentSessionAgent(pinnedKey, "coder")

	sm.SetLedgerWiring(
		func([]string) []tools.PersistedSubagentTask {
			return []tools.PersistedSubagentTask{{
				TaskID:     "subagent-1",
				SessionKey: pinnedKey,
				ParentKey:  sessionKey,
				Label:      "legacy task",
				Status:     tools.SubagentStatusCompleted,
				AgentID:    "", // pre-v8 row
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

// TestRenderSubagentLedgerDigest_AliasAndBaseBothResolve pins C8: tasks are
// recorded under the BASE session key, but the prompt resolver receives the
// runtime key, which after /new is the ":chat:N" alias. Both forms must find
// the same rows.
func TestRenderSubagentLedgerDigest_AliasAndBaseBothResolve(t *testing.T) {
	tc, al := newDigestTestCoordinator(t, "main")
	sm := tc.subagents["main"]
	const base = "native:client-1"
	const alias = "native:client-1:chat:2"

	// The task was spawned from the base key (OriginSessionKey never carries
	// the alias).
	addLedgerDigestTask(sm, "subagent-1", base, tools.SubagentStatusCompleted, "coder", "Docs review", time.Now().UnixMilli())

	// /new rotated the conversation: base -> alias.
	al.setSessionAlias(base, alias)

	for _, key := range []string{base, alias} {
		digest := tc.renderSubagentLedgerDigest("main", key)
		if !strings.Contains(digest, "- subagent-1 [completed]") {
			t.Errorf("session key %q did not resolve the ledger:\n%s", key, digest)
		}
	}
}

// TestRenderSubagentLedgerDigest_SingleManagerNotSweptPerAgent pins C6: the
// digest reads exactly ONE manager — the owning agent's — so the per-turn cost
// does not scale with the number of agents. A lister installed on every manager
// counts invocations: rendering for one agent must touch one manager.
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

// TestRenderSubagentLedgerDigest_SummaryOnlyForLiveRows pins D2: a persisted
// row renders its label but never a summary, because that summary lives in the
// session store and fetching it would turn the per-turn render into a query.
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

// TestRenderSubagentLedgerDigest_FormatPinsTheHeaderAndHint pins the exact
// section shape: header, rows, optional overflow, blank line, static hint. The
// hint is always present when the section renders, because it is what tells the
// model how to page the full history.
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

// digestRows extracts the "- " row lines of a rendered digest.
func digestRows(digest string) []string {
	var rows []string
	for _, line := range strings.Split(digest, "\n") {
		if strings.HasPrefix(line, "- ") {
			rows = append(rows, line)
		}
	}
	return rows
}

// itoa is a tiny local helper so the tests do not import strconv just for IDs.
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

// ensure context stays imported if a future edit drops its only use.
var _ = context.Background
