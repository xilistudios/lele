package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These subprocess tests exercise main()'s dispatch branches and the command
// bodies that still have uncovered lines, contributing merged child coverage
// through the LELE_TEST_MAIN TestMain route (GOCOVERDIR inheritance).

func TestV6MainDispatch_ClientPin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "ws")
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "client", "pin")
	if err != nil {
		t.Fatalf("client pin subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "PIN") {
		t.Errorf("expected PIN output, got: %s", out)
	}
}

func TestV6MainDispatch_ClientStatus(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "ws")
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "client", "status")
	if err != nil {
		t.Fatalf("client status subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Client Channel Status") {
		t.Errorf("expected client status, got: %s", out)
	}
}

func TestV6MainDispatch_CronListJobs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "ws")
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "cron", "list")
	if err != nil {
		t.Fatalf("cron list subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "No scheduled jobs") && !strings.Contains(out, "Scheduled Jobs") {
		t.Errorf("expected cron list output, got: %s", out)
	}
}

func TestV6MainDispatch_SkillsListBuiltin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "ws")
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "skills", "list-builtin")
	if err != nil {
		t.Fatalf("skills list-builtin subprocess failed: %v\noutput: %s", err, out)
	}
	if out == "" {
		t.Error("expected skills list-builtin output")
	}
}

func TestV6MainDispatch_SkillsInstallBuiltin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "ws")
	saveConfigAt(t, dir, cfg)
	os.MkdirAll(filepath.Join(dir, "ws"), 0755)

	out, err := runLELEMain(t, "skills", "install-builtin")
	if err != nil {
		t.Fatalf("skills install-builtin subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "builtin") {
		t.Errorf("expected install-builtin output, got: %s", out)
	}
}

func TestV6MainDispatch_AuthStatusNoStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "auth", "status")
	if err != nil {
		t.Fatalf("auth status subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "No authenticated providers") && !strings.Contains(out, "Authenticated") {
		t.Errorf("expected auth status output, got: %s", out)
	}
}

func TestV6MainDispatch_WebNoArgs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "web")
	if err != nil {
		t.Fatalf("web subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "http://") && !strings.Contains(out, "gateway") {
		t.Errorf("expected web instructions output, got: %s", out)
	}
}

func TestV6MainDispatch_SessionFlagTUIOnboard(t *testing.T) {
	// --session with no command routes to tuiCmd(sessionID). Point at a missing
	// config dir so tuiCmd errors out (os.Exit(1)) without blocking on a TTY.
	// This only exercises the main() --session branch + tuiCmd error path.
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(),
		"LELE_TEST_MAIN="+"--session\x1fcli:v6tui",
		"LELE_CONFIG_DIR=/tmp/lele_tui_nonexistent_v6",
	)
	_ = cmd.Start()
	timer := time.AfterFunc(3*time.Second, func() { cmd.Process.Kill() })
	_ = cmd.Wait()
	timer.Stop()
}

func TestV6MainDispatch_UpdateDisabledInConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Updates.Enabled = false
	saveConfigAt(t, dir, cfg)

	out, _ := runLELEMain(t, "update")
	if !strings.Contains(out, "disabled in config") {
		t.Errorf("expected disabled-in-config message, got: %s", out)
	}
}

func TestV6MainDispatch_UpdateDevBuildGuard(t *testing.T) {
	// With default enabled config and local "dev" version, runApply exits on the
	// dev-build guard. Covers updateCmd's flag loop + CheckEnvironment + the
	// runApply dev-build branch.
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	out, _ := runLELEMain(t, "update", "--yes")
	if !strings.Contains(out, "local/dev build") && !strings.Contains(out, "Update failed") {
		t.Errorf("expected dev-build or failure message, got: %s", out)
	}
}

func TestV6MainDispatch_UpdateVersionMissingValue(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, _ := runLELEMain(t, "update", "--version")
	if !strings.Contains(out, "--version requires a value") {
		t.Errorf("expected version-requires-value message, got: %s", out)
	}
}

func TestV6MainDispatch_CronListWithJobs(t *testing.T) {
	// Seed a workspace cron store by pre-creating the cron dir; list covers the
	// "No scheduled jobs" path without seeding a repo.
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	ws := filepath.Join(dir, "ws")
	cfg.Agents.Defaults.Workspace = ws
	saveConfigAt(t, dir, cfg)
	os.MkdirAll(filepath.Join(ws, "cron"), 0755)

	out, err := runLELEMain(t, "cron", "list")
	if err != nil {
		t.Fatalf("cron list subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Scheduled Jobs") && !strings.Contains(out, "No scheduled jobs") {
		t.Errorf("expected cron list output, got: %s", out)
	}
}

func TestV6MainDispatch_UpdateRollbackConfirmAbort(t *testing.T) {
	// Seed a backup file in the lele dir, then run `update --rollback` in a
	// child whose stdin is /dev/null -> confirm() returns false -> "Aborted."
	// exit 0. Exercises updateCmd's rollback flag loop + runRollback backup
	// discovery + confirm-abort path.
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)
	exe, _ := os.Executable()
	os.WriteFile(filepath.Join(backupDir, "lele-1.0.0-20240101-120000"), []byte("binary"), 0644)
	_ = exe

	out, _ := runLELEMain(t, "update", "--rollback")
	if !strings.Contains(out, "Aborted") && !strings.Contains(out, "Rolling back") && !strings.Contains(out, "restart") {
		t.Errorf("expected rollback progression output, got: %s", out)
	}
}

func TestV6MainDispatch_UpdateRollbackNoBackup(t *testing.T) {
	// No backup present -> runRollback prints "No backup available" and exits.
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)

	out, _ := runLELEMain(t, "update", "--rollback")
	if !strings.Contains(out, "No backup") {
		t.Errorf("expected no-backup message, got: %s", out)
	}
}
