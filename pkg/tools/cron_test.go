package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/cron"
)

// mockJobExecutor records the sessionKey used for each ProcessDirectWithChannel
// call and returns a canned response.
type mockJobExecutor struct {
	lastSessionKey string
	lastContent    string
	err            error
}

func (m *mockJobExecutor) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	m.lastSessionKey = sessionKey
	m.lastContent = content
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
		{"native", "uuid-1", "native:uuid-1"},   // bare key probed with prefix
		{"", "native:abc", "native:abc"},        // empty channel defaults to native
		{"native", "unknown", ""},               // unknown key → no designation
		{"telegram", "1779224049", ""},          // non-native chat ID → not a session
		{"native", "", ""},                      // empty `to` → no designation
		{"native", "agent:main:session", "agent:main:session"},
	}
	for _, c := range cases {
		if got := tool.designatedSessionKey(c.channel, c.to); got != c.want {
			t.Errorf("designatedSessionKey(%q, %q) = %q, want %q", c.channel, c.to, got, c.want)
		}
	}
}
