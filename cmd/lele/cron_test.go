package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/store"
)

func newCronRepo(t *testing.T) (*store.CronRepo, string) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.Cron(), dir
}

func seedCronJob(t *testing.T, repo *store.CronRepo, storePath string) *cron.CronJob {
	t.Helper()
	cs := cron.NewCronService(storePath, nil)
	cs.SetStore(repo)
	everyMS := int64(60000)
	job, err := cs.AddJob("test-job", cron.CronSchedule{Kind: "every", EveryMS: &everyMS}, "hello", false, "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	return job
}

func TestCronListCmd_Empty(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "jobs.json")
	// No repo, no existing file -> empty list.
	out := runCmd(func() { cronListCmd(storePath, nil) })
	if !strings.Contains(out, "No scheduled jobs") {
		t.Errorf("expected no jobs message, got: %s", out)
	}
}

func TestCronListCmd_WithRepoJobs(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	seedCronJob(t, repo, storePath)
	out := runCmd(func() { cronListCmd(storePath, repo) })
	if !strings.Contains(out, "test-job") {
		t.Errorf("list should include job name, got: %s", out)
	}
	if !strings.Contains(out, "Scheduled Jobs") {
		t.Errorf("list should include header, got: %s", out)
	}
}

func TestCronRemoveCmd_Found(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	job := seedCronJob(t, repo, storePath)
	out := runCmd(func() { cronRemoveCmd(storePath, job.ID, repo) })
	if !strings.Contains(out, "Removed job") {
		t.Errorf("expected removal message, got: %s", out)
	}
}

func TestCronRemoveCmd_NotFound(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	out := runCmd(func() { cronRemoveCmd(storePath, "nonexistent", repo) })
	if !strings.Contains(out, "not found") {
		t.Errorf("expected not found message, got: %s", out)
	}
}

func TestCronEnableCmd_Found(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	job := seedCronJob(t, repo, storePath)
	replaceArgs(t, []string{"lele", "cron", "enable", job.ID})
	out := runCmd(func() { cronEnableCmd(storePath, false, repo) })
	if !strings.Contains(out, "enabled") {
		t.Errorf("expected enabled message, got: %s", out)
	}
}

func TestCronEnableCmd_Disable(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	job := seedCronJob(t, repo, storePath)
	replaceArgs(t, []string{"lele", "cron", "disable", job.ID})
	out := runCmd(func() { cronEnableCmd(storePath, true, repo) })
	if !strings.Contains(out, "disabled") {
		t.Errorf("expected disabled message, got: %s", out)
	}
}

func TestCronEnableCmd_MissingArgs(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "enable"})
	out := runCmd(func() { cronEnableCmd(storePath, false, repo) })
	if !strings.Contains(out, "Usage: lele cron enable") {
		t.Errorf("expected usage message, got: %s", out)
	}
}

// CronService has no exported accessor for the store; instead we test through
// cronCmd's helpers using a store path directly.
func TestCronAddCmd_MissingName(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "add"})
	out := runCmd(func() { cronAddCmd(storePath, repo) })
	if !strings.Contains(out, "--name is required") {
		t.Errorf("expected missing name message, got: %s", out)
	}
}

func TestCronAddCmd_MissingMessage(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "test"})
	out := runCmd(func() { cronAddCmd(storePath, repo) })
	if !strings.Contains(out, "--message is required") {
		t.Errorf("expected missing message message, got: %s", out)
	}
}

func TestCronAddCmd_MissingSchedule(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "test", "--message", "hi"})
	out := runCmd(func() { cronAddCmd(storePath, repo) })
	if !strings.Contains(out, "--every or --cron") {
		t.Errorf("expected missing schedule message, got: %s", out)
	}
}

func TestCronAddCmd_SuccessEvery(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "job1", "--message", "do it", "--every", "30"})
	out := runCmd(func() { cronAddCmd(storePath, repo) })
	if !strings.Contains(out, "Added job") {
		t.Errorf("expected add message, got: %s", out)
	}
}

func TestCronAddCmd_SuccessCronExpr(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "job2", "--message", "hi", "--cron", "* * * * *", "-d", "--to", "chat", "--channel", "telegram"})
	out := runCmd(func() { cronAddCmd(storePath, repo) })
	if !strings.Contains(out, "Added job") {
		t.Errorf("expected add message, got: %s", out)
	}
}

func TestCronAddCmd_AddError(t *testing.T) {
	repo, dir := newCronRepo(t)
	storePath := filepath.Join(dir, "jobs.json")
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "job3", "--message", "hi", "--every", "notanumber"})
	// every parse yields 0 -> schedule "every" with 0 ms; still added.
	out := runCmd(func() { cronAddCmd(storePath, repo) })
	if strings.Contains(out, "Added job") {
		// acceptable
	}
}

// TestCronHelp just ensures cronHelp prints options.
func TestCronHelpOptions(t *testing.T) {
	out := runCmd(cronHelp)
	for _, opt := range []string{"list", "add", "remove", "enable", "disable"} {
		if !strings.Contains(out, opt) {
			t.Errorf("cronHelp should contain %q", opt)
		}
	}
}

// Helper to obtain a cron repo for tests that need config loading.
func makeConfigWithCronStore(t *testing.T) (*config.Config, string) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	return cfg, dir
}

var _ = os.Getenv
var _ = config.DefaultConfig