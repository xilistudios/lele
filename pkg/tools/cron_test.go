package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/keyring"
)

// mockJobExecutor records the sessionKey used for each ProcessDirectWithChannel
// call and returns a canned response. It also captures the agent identity
// (issue #234) found in the context of the call.
type mockJobExecutor struct {
	lastSessionKey string
	lastContent    string
	lastAgentID    string
	lastAgentSess  string
	err            error
}

func (m *mockJobExecutor) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	m.lastSessionKey = sessionKey
	m.lastContent = content
	m.lastAgentID, m.lastAgentSess = AgentToolContextFromCtx(ctx)
	if m.err != nil {
		return "", m.err
	}
	return "ok: " + sessionKey, nil
}

func newTestCronJob(id string, mutate func(*cron.CronJob)) *cron.CronJob {
	job := &cron.CronJob{
		ID:   id,
		Name: "test job",
		Payload: cron.CronPayload{
			Kind:    "message",
			Message: "hello",
			Channel: "native",
			To:      "native:existing-chat",
		},
	}
	if mutate != nil {
		mutate(job)
	}
	return job
}

// TestExecuteJob_GlobalWithExistingToSession verifies that a global job whose
// `to` designates an existing native session processes the message in that
// session instead of creating a synthetic cron-<id> session.
func TestExecuteJob_GlobalWithExistingToSession(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return sk == "native:existing-chat" })

	job := newTestCronJob("job1", nil) // deliver=false, no SessionKey, spawn=nil
	tool.ExecuteJob(context.Background(), job)

	if exec.lastSessionKey != "native:existing-chat" {
		t.Errorf("sessionKey = %q, want designated existing session %q", exec.lastSessionKey, "native:existing-chat")
	}
}

// TestExecuteJob_GlobalWithUnknownToSession verifies that a global job whose
// `to` does not match an existing session keeps the synthetic cron-<id> key.
func TestExecuteJob_GlobalWithUnknownToSession(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return false })

	job := newTestCronJob("job2", nil)
	tool.ExecuteJob(context.Background(), job)

	want := "cron-job2"
	if exec.lastSessionKey != want {
		t.Errorf("sessionKey = %q, want synthetic %q", exec.lastSessionKey, want)
	}
}

// TestExecuteJob_GlobalWithUnprefixedToSession verifies that a bare `to` value
// (e.g. a native client UUID) is probed with the native: prefix.
func TestExecuteJob_GlobalWithUnprefixedToSession(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return sk == "native:bare-uuid" })

	job := newTestCronJob("job3", func(j *cron.CronJob) { j.Payload.To = "bare-uuid" })
	tool.ExecuteJob(context.Background(), job)

	if exec.lastSessionKey != "native:bare-uuid" {
		t.Errorf("sessionKey = %q, want %q", exec.lastSessionKey, "native:bare-uuid")
	}
}

// TestExecuteJob_SpawnWithExistingToSession verifies that a spawn job whose
// `to` designates an existing session uses it as the subagent origin session.
func TestExecuteJob_SpawnWithExistingToSession(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return sk == "native:existing-chat" })

	job := newTestCronJob("job4", func(j *cron.CronJob) {
		j.Payload.Spawn = &cron.SpawnConfig{Task: "do something"}
	})
	tool.ExecuteJob(context.Background(), job)

	if exec.lastSessionKey != "native:existing-chat" {
		t.Errorf("sessionKey = %q, want designated existing session %q", exec.lastSessionKey, "native:existing-chat")
	}
	if !strings.HasPrefix(exec.lastContent, "SYSTEM_SPAWN:") {
		t.Errorf("content = %q, want SYSTEM_SPAWN message", exec.lastContent)
	}
}

// TestExecuteJob_SpawnWithoutTo verifies that a spawn job without a designated
// existing session keeps the synthetic cron-spawn-<id> origin.
func TestExecuteJob_SpawnWithoutTo(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return sk == "native:existing-chat" })

	job := newTestCronJob("job5", func(j *cron.CronJob) {
		j.Payload.Spawn = &cron.SpawnConfig{Task: "do something"}
		j.Payload.To = ""
	})
	tool.ExecuteJob(context.Background(), job)

	want := "cron-spawn-job5"
	if exec.lastSessionKey != want {
		t.Errorf("sessionKey = %q, want synthetic %q", exec.lastSessionKey, want)
	}
}

// TestExecuteJob_NonNativeToIsNotSessionKey verifies that numeric chat IDs on
// non-native channels (e.g. telegram) are never treated as session keys.
func TestExecuteJob_NonNativeToIsNotSessionKey(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)
	// Checker that would match anything — must not be consulted.
	tool.SetSessionExistsCallback(func(sk string) bool { return true })

	job := newTestCronJob("job6", func(j *cron.CronJob) {
		j.Payload.Channel = "telegram"
		j.Payload.To = "1779224049"
	})
	tool.ExecuteJob(context.Background(), job)

	want := "cron-job6"
	if exec.lastSessionKey != want {
		t.Errorf("sessionKey = %q, want synthetic %q (non-native `to` must not be a session key)", exec.lastSessionKey, want)
	}
}

// TestExecuteJob_SessionScopedUnaffected verifies that session-scoped jobs
// (with Payload.SessionKey) still use their stored session key.
func TestExecuteJob_SessionScopedUnaffected(t *testing.T) {
	exec := &mockJobExecutor{}
	job := newTestCronJob("job7", func(j *cron.CronJob) {
		j.Payload.SessionKey = "native:origin-session"
		j.Payload.To = "native:existing-chat"
	})
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, bus.NewMessageBus(), t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return sk == "native:existing-chat" })
	tool.ExecuteJob(context.Background(), job)

	if exec.lastSessionKey != "native:origin-session" {
		t.Errorf("sessionKey = %q, want stored session key %q", exec.lastSessionKey, "native:origin-session")
	}
}

// TestExecuteJob_NilSessionChecker verifies backward compatibility: without a
// checker injected, behavior is unchanged (synthetic keys).
func TestExecuteJob_NilSessionChecker(t *testing.T) {
	exec := &mockJobExecutor{}
	tool := NewCronTool(cron.NewCronService(t.TempDir()+"/jobs.json", nil), exec, nil, t.TempDir(), false, 0, nil)

	job := newTestCronJob("job8", nil)
	tool.ExecuteJob(context.Background(), job)

	want := "cron-job8"
	if exec.lastSessionKey != want {
		t.Errorf("sessionKey = %q, want %q", exec.lastSessionKey, want)
	}
}

// TestDesignatedSessionKey unit-tests the resolution helper directly.
func TestDesignatedSessionKey(t *testing.T) {
	existing := map[string]bool{
		"native:abc":         true,
		"native:uuid-1":      true,
		"agent:main:session": true,
	}
	tool := NewCronTool(cron.NewCronService(fmt.Sprintf("%s/jobs.json", t.TempDir()), nil), nil, nil, t.TempDir(), false, 0, nil)
	tool.SetSessionExistsCallback(func(sk string) bool { return existing[sk] })

	cases := []struct {
		channel, to, want string
	}{
		{"native", "native:abc", "native:abc"},
		{"native", "uuid-1", "native:uuid-1"}, // bare key probed with prefix
		{"", "native:abc", "native:abc"},      // empty channel defaults to native
		{"native", "unknown", ""},             // unknown key → no designation
		{"telegram", "1779224049", ""},        // non-native chat ID → not a session
		{"native", "", ""},                    // empty `to` → no designation
		{"native", "agent:main:session", "agent:main:session"},
	}
	for _, c := range cases {
		if got := tool.designatedSessionKey(c.channel, c.to); got != c.want {
			t.Errorf("designatedSessionKey(%q, %q) = %q, want %q", c.channel, c.to, got, c.want)
		}
	}
}

// ── Issue #234, Part B: cron jobs carry the creating agent's identity ───────
//
// Cron jobs created by a non-default agent (planner/coder) must remember that
// agent so that, when the scheduler fires the job with a bare
// context.Background(), the tools executed during the run (exec command path,
// spawned subagents) see a valid identity and scoped keyring secrets resolve.

// newIdentityCronTool builds a CronTool wired to a JSON-backed CronService in
// a temp dir, with a session context set so add() succeeds.
func newIdentityCronTool(t *testing.T, storePath string) (*CronTool, *mockJobExecutor) {
	t.Helper()
	cs := cron.NewCronService(storePath, nil)
	exec := &mockJobExecutor{}
	tool := NewCronTool(cs, exec, bus.NewMessageBus(), t.TempDir(), false, 0, nil)
	tool.SetSessionContext("native", "chat-1", "native:creator-session")
	return tool, exec
}

func findJob(t *testing.T, cs *cron.CronService, name string) cron.CronJob {
	t.Helper()
	for _, j := range cs.ListJobs(true) {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("job with name %q not found (jobs: %d)", name, len(cs.ListJobs(true)))
	return cron.CronJob{}
}

// TestB1_AddJobStoresCreatorAgentID verifies that adding a job from a context
// carrying an agent identity persists Payload.AgentID, and that the value
// survives a reload from disk (AddJob/UpdateJob save the store).
func TestB1_AddJobStoresCreatorAgentID(t *testing.T) {
	storePath := t.TempDir() + "/jobs.json"
	tool, _ := newIdentityCronTool(t, storePath)

	ctx := WithAgentToolContext(context.Background(), "planner", "sess-1")
	res := tool.Execute(ctx, map[string]interface{}{
		"action":        "add",
		"message":       "hourly check",
		"command":       "echo hi",
		"every_seconds": float64(3600),
	})
	if res.IsError {
		t.Fatalf("add failed: %s", res.ForLLM)
	}

	job := findJob(t, tool.cronService, "hourly check")
	if job.Payload.AgentID != "planner" {
		t.Errorf("Payload.AgentID = %q, want %q", job.Payload.AgentID, "planner")
	}

	// Persistence: a fresh service reading the same store file must see it.
	reloaded := cron.NewCronService(storePath, nil)
	persisted := findJob(t, reloaded, "hourly check")
	if persisted.Payload.AgentID != "planner" {
		t.Errorf("after reload Payload.AgentID = %q, want %q", persisted.Payload.AgentID, "planner")
	}
	if persisted.Payload.Command != "echo hi" {
		t.Errorf("after reload Payload.Command = %q, want %q (UpdateJob must not drop the command)", persisted.Payload.Command, "echo hi")
	}
}

// TestB1_AddJobWithoutAgentIdentityLeavesEmpty verifies backward compatibility:
// jobs created outside an agent context (TUI/user, no identity in ctx) keep an
// empty AgentID so execution behaves exactly as before.
func TestB1_AddJobWithoutAgentIdentityLeavesEmpty(t *testing.T) {
	tool, _ := newIdentityCronTool(t, t.TempDir()+"/jobs.json")

	res := tool.Execute(context.Background(), map[string]interface{}{
		"action":        "add",
		"message":       "no identity",
		"every_seconds": float64(60),
	})
	if res.IsError {
		t.Fatalf("add failed: %s", res.ForLLM)
	}

	job := findJob(t, tool.cronService, "no identity")
	if job.Payload.AgentID != "" {
		t.Errorf("Payload.AgentID = %q, want empty", job.Payload.AgentID)
	}
}

// TestB2_SpawnInheritsCreatorAgentID verifies a spawn job without an explicit
// agent_id inherits the creating agent, so SYSTEM_SPAWN carries AGENT_ID.
func TestB2_SpawnInheritsCreatorAgentID(t *testing.T) {
	tool, _ := newIdentityCronTool(t, t.TempDir()+"/jobs.json")

	ctx := WithAgentToolContext(context.Background(), "planner", "sess-1")
	res := tool.Execute(ctx, map[string]interface{}{
		"action":        "add",
		"message":       "spawn task",
		"every_seconds": float64(3600),
		"spawn":         map[string]interface{}{"task": "x"},
	})
	if res.IsError {
		t.Fatalf("add failed: %s", res.ForLLM)
	}

	job := findJob(t, tool.cronService, "spawn task")
	if job.Payload.Spawn == nil {
		t.Fatal("Payload.Spawn = nil, want spawn config")
	}
	if job.Payload.Spawn.AgentID != "planner" {
		t.Errorf("Payload.Spawn.AgentID = %q, want inherited %q", job.Payload.Spawn.AgentID, "planner")
	}
	if job.Payload.AgentID != "planner" {
		t.Errorf("Payload.AgentID = %q, want %q", job.Payload.AgentID, "planner")
	}
}

// TestB2_SpawnExplicitAgentIDNotOverwritten verifies an explicitly requested
// target agent is never replaced by the creator's identity.
func TestB2_SpawnExplicitAgentIDNotOverwritten(t *testing.T) {
	tool, _ := newIdentityCronTool(t, t.TempDir()+"/jobs.json")

	ctx := WithAgentToolContext(context.Background(), "planner", "sess-1")
	res := tool.Execute(ctx, map[string]interface{}{
		"action":        "add",
		"message":       "explicit spawn",
		"every_seconds": float64(3600),
		"spawn":         map[string]interface{}{"task": "x", "agent_id": "reviewer"},
	})
	if res.IsError {
		t.Fatalf("add failed: %s", res.ForLLM)
	}

	job := findJob(t, tool.cronService, "explicit spawn")
	if job.Payload.Spawn == nil {
		t.Fatal("Payload.Spawn = nil, want spawn config")
	}
	if job.Payload.Spawn.AgentID != "reviewer" {
		t.Errorf("Payload.Spawn.AgentID = %q, want explicit %q", job.Payload.Spawn.AgentID, "reviewer")
	}
}

// TestB3_ExecuteJobRestoresIdentityOnAgentPath verifies the scheduler-side
// restore: a job whose payload records the creating agent runs its agent turn
// with that identity in the context (the fake executor reads it back).
func TestB3_ExecuteJobRestoresIdentityOnAgentPath(t *testing.T) {
	tool, exec := newIdentityCronTool(t, t.TempDir()+"/jobs.json")

	job := newTestCronJob("job-b3", func(j *cron.CronJob) {
		j.Payload.AgentID = "planner"
		j.Payload.SessionKey = "native:creator-session"
		j.Payload.Deliver = false
	})
	tool.ExecuteJob(context.Background(), job)

	if exec.lastAgentID != "planner" {
		t.Errorf("executor saw agentID = %q, want %q", exec.lastAgentID, "planner")
	}
	if exec.lastAgentSess != "native:creator-session" {
		t.Errorf("executor saw sessionKey = %q, want %q", exec.lastAgentSess, "native:creator-session")
	}
}

// TestB3_ExecuteJobWithoutAgentIDKeepsContextClean verifies legacy jobs (no
// recorded creator) are unaffected: no identity is injected.
func TestB3_ExecuteJobWithoutAgentIDKeepsContextClean(t *testing.T) {
	tool, exec := newIdentityCronTool(t, t.TempDir()+"/jobs.json")

	job := newTestCronJob("job-b3b", nil) // Payload.AgentID == ""
	tool.ExecuteJob(context.Background(), job)

	if exec.lastAgentID != "" {
		t.Errorf("executor saw agentID = %q, want empty", exec.lastAgentID)
	}
}

// TestB4_ExecuteJobCommandPathResolvesScopedSecret is the end-to-end check for
// the reported bug: a command job created by "planner" runs at fire time with
// planner's identity, so a planner-scoped {{SECRET:...}} placeholder resolves.
// Without the fix the exec tool sees agent "unknown" and the keyring denies
// access.
func TestB4_ExecuteJobCommandPathResolvesScopedSecret(t *testing.T) {
	dir := t.TempDir()
	svc := keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    dir + "/keyring.enc",
		Backend:      keyring.BackendFile,
		AuditLogSize: 10,
		LeleDir:      dir,
	})
	if err := svc.SetFromUI("tok", "planner-only-value", "scoped to planner", nil, []string{"planner"}, "test"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	tool, _ := newIdentityCronTool(t, t.TempDir()+"/jobs.json")
	tool.execTool.SetKeyringService(svc)

	job := newTestCronJob("job-b4", func(j *cron.CronJob) {
		j.Payload.AgentID = "planner"
		j.Payload.Command = "echo {{SECRET:tok}}"
		j.Payload.Deliver = false
	})
	if got := tool.ExecuteJob(context.Background(), job); got != "ok" {
		t.Fatalf("ExecuteJob = %q, want ok", got)
	}

	msg, ok := tool.msgBus.SubscribeOutbound(context.Background())
	if !ok {
		t.Fatal("no outbound message published")
	}
	if !strings.Contains(msg.Content, "planner-only-value") {
		t.Errorf("output = %q, want substituted secret (identity not restored?)", msg.Content)
	}
}

// TestB4_ExecuteJobCommandPathDeniesWrongAgent guards against over-fixing:
// the identity restored must be the one recorded, not a blanket bypass — a
// secret scoped to "planner" must still fail for a job recorded as "coder".
func TestB4_ExecuteJobCommandPathDeniesWrongAgent(t *testing.T) {
	dir := t.TempDir()
	svc := keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    dir + "/keyring.enc",
		Backend:      keyring.BackendFile,
		AuditLogSize: 10,
		LeleDir:      dir,
	})
	if err := svc.SetFromUI("tok", "planner-only-value", "", nil, []string{"planner"}, "test"); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	tool, _ := newIdentityCronTool(t, t.TempDir()+"/jobs.json")
	tool.execTool.SetKeyringService(svc)

	job := newTestCronJob("job-b4b", func(j *cron.CronJob) {
		j.Payload.AgentID = "coder"
		j.Payload.Command = "echo {{SECRET:tok}}"
		j.Payload.Deliver = false
	})
	tool.ExecuteJob(context.Background(), job)

	msg, ok := tool.msgBus.SubscribeOutbound(context.Background())
	if !ok {
		t.Fatal("no outbound message published")
	}
	if strings.Contains(msg.Content, "planner-only-value") {
		t.Errorf("output = %q, coder must not access a planner-scoped secret", msg.Content)
	}
}
