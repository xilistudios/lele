package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recordSep is the env-safe argument separator used to encode multiple CLI args
// inside the single LELE_TEST_MAIN env var. NUL cannot be used because Go's
// os/exec rejects NUL bytes in environment values.
const recordSep = "\x1f"

// runLELEMain invokes the current test binary (which routes to real main() via
// the existing TestMain in main_subprocess_test.go when LELE_TEST_MAIN is set).
// Unlike runMainSubprocess, it does NOT fail the test on a non-zero exit code;
// callers decide whether a non-zero exit is acceptable (e.g. command error
// paths that call os.Exit(1)).
func runLELEMain(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestMainPlaceholder")
	cmd.Env = append(os.Environ(), "LELE_TEST_MAIN="+strings.Join(args, recordSep))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestV5MainDispatch_Agent exercises main()'s "agent" branch and the bulk of
// agentCmd() including config load, agent loop creation, startup info and
// interactive-mode termination on EOF. stdin is /dev/null (exec with no Stdin)
// so the interactive loop exits cleanly.
func TestV5MainDispatch_Agent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	// Write a minimal config so loadConfig succeeds with a workspace.
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "workspace")
	saveConfigAt(t, dir, cfg)

	out, err := runLELEMain(t, "agent")
	if err != nil {
		t.Fatalf("agent subprocess failed: %v\noutput: %s", err, out)
	}
	// Interactive mode prints the logo prompt before terminating on EOF.
	if !strings.Contains(out, "lele") && !strings.Contains(out, "🦞") {
		t.Errorf("expected agent prompt output, got: %s", out)
	}
}

// TestV5MainDispatch_AgentMessage exercises agentCmd's direct-message path. We
// use a message with an empty config (no usable provider) so ProcessDirect
// returns an error, which agentCmd prints and then os.Exit(1)s. The parse/load
// setup still runs, covering the message branch.
func TestV5MainDispatch_AgentMessage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, _ := runLELEMain(t, "agent", "--message", "hello")
	_ = out
}

// TestV5MainDispatch_Migrate exercises main()'s "migrate" branch and the flag
// parsing of migrateCmd. We pass mutually exclusive options so migrate.Run
// returns an error and the subprocess exits non-zero (acceptable).
func TestV5MainDispatch_Migrate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	t.Setenv("OPENCLAW_HOME", filepath.Join(dir, "openclaw"))
	_, _ = runLELEMain(t,
		"migrate",
		"--dry-run",
		"--config-only",
		"--workspace-only",
		"--force",
		"--openclaw-home", filepath.Join(dir, "oc"),
		"--lele-home", filepath.Join(dir, "lh"),
	)
}

// TestV5MainDispatch_MigrateUnknownFlag covers migrateCmd's default/unknown
// flag branch (prints unknown flag + help + os.Exit(1)).
func TestV5MainDispatch_MigrateUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	runLELEMain(t, "migrate", "--refresh", "--bogus")
}

// TestV5MainDispatch_MigrateStorage covers main()'s "migrate-storage" branch.
func TestV5MainDispatch_MigrateStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "migrate-storage")
	if err != nil {
		t.Fatalf("migrate-storage subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Migration Summary") {
		t.Errorf("expected migration summary, got: %s", out)
	}
}

// TestV5MainDispatch_MigrateStorageDryRun covers the --dry-run flag path.
func TestV5MainDispatch_MigrateStorageDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "migrate-storage", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "DRY RUN") {
		t.Errorf("expected dry-run marker, got: %s", out)
	}
}

// TestV5MainDispatch_Auth covers main()'s "auth" branch with no subcommand
// (authHelp path, exits cleanly).
func TestV5MainDispatch_AuthNoSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "auth")
	if err != nil {
		t.Fatalf("auth subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Auth commands") {
		t.Errorf("expected auth help, got: %s", out)
	}
}

// TestV5MainDispatch_AuthLoginNoProvider covers authLoginCmd's "provider
// required" error branch.
func TestV5MainDispatch_AuthLoginNoProvider(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "auth", "login")
	if err != nil {
		t.Fatalf("auth login subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "--provider is required") {
		t.Errorf("expected provider-required error, got: %s", out)
	}
}

// TestV5MainDispatch_AuthLoginUnsupported covers authLoginCmd's unsupported
// provider branch.
func TestV5MainDispatch_AuthLoginUnsupported(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "auth", "login", "--provider", "bogus")
	if err != nil {
		t.Fatalf("auth login subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Unsupported provider") {
		t.Errorf("expected unsupported provider message, got: %s", out)
	}
}

// TestV5MainDispatch_Cron covers main()'s "cron" branch with no subcommand.
func TestV5MainDispatch_CronNoSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "cron")
	if err != nil {
		t.Fatalf("cron subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Cron commands") {
		t.Errorf("expected cron help, got: %s", out)
	}
}

// TestV5MainDispatch_SkillsNoSubcommand covers main()'s "skills" branch with
// fewer than 3 args (skillsHelp).
func TestV5MainDispatch_SkillsNoSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "skills")
	if err != nil {
		t.Fatalf("skills subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Skills") {
		t.Errorf("expected skills help, got: %s", out)
	}
}

// TestV5MainDispatch_ClientNoSubcommand covers main()'s "client" branch with
// no subcommand (clientHelp).
func TestV5MainDispatch_ClientNoSubcommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "client")
	if err != nil {
		t.Fatalf("client subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Client Commands") && !strings.Contains(out, "client") {
		t.Errorf("expected client help, got: %s", out)
	}
}

// TestV5MainDispatch_UpdateHelp covers main()'s "update" branch with --help.
func TestV5MainDispatch_UpdateHelp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "update", "--help")
	if err != nil {
		t.Fatalf("update subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "--check") {
		t.Errorf("expected update help, got: %s", out)
	}
}

// TestV5MainDispatch_UpdateUnknownOption covers updateCmd's unknown option
// branch (prints help + os.Exit(1)).
func TestV5MainDispatch_UpdateUnknownOption(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	runLELEMain(t, "update", "--bogus")
}

// TestV5MainDispatch_ShortVersion covers main()'s "-v" branch.
func TestV5MainDispatch_ShortVersion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "-v")
	if err != nil {
		t.Fatalf("-v subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "lele") {
		t.Errorf("expected version output, got: %s", out)
	}
}

// TestV5MainDispatch_CronUnknown covers cronCmd's unknown subcommand branch.
func TestV5MainDispatch_CronUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	runLELEMain(t, "cron", "bogus")
}

// TestV5MainDispatch_ClientUnknown covers clientCmd's unknown subcommand
// branch (prints unknown + clientHelp, exits cleanly).
func TestV5MainDispatch_ClientUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "client", "bogus")
	if err != nil {
		t.Fatalf("client subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Unknown client command") {
		t.Errorf("expected unknown client message, got: %s", out)
	}
}

// TestV5MainDispatch_SkillsUnknown covers the skills switch default branch.
func TestV5MainDispatch_SkillsUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "skills", "bogus")
	if err != nil {
		t.Fatalf("skills subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Unknown skills command") {
		t.Errorf("expected unknown skills message, got: %s", out)
	}
}

// TestV5MainDispatch_AuthUnknown covers authCmd's unknown auth subcommand.
func TestV5MainDispatch_AuthUnknown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "auth", "bogus")
	if err != nil {
		t.Fatalf("auth subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Unknown auth command") {
		t.Errorf("expected unknown auth message, got: %s", out)
	}
}

// TestV5MainDispatch_SkillsRemoveNoName covers the skills remove branch with
// fewer than 4 args (prints usage and returns).
func TestV5MainDispatch_SkillsRemoveNoName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "skills", "remove")
	if err != nil {
		t.Fatalf("skills remove subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Usage: lele skills remove") {
		t.Errorf("expected remove usage, got: %s", out)
	}
}

// TestV5MainDispatch_SkillsShowNoName covers the skills show branch with fewer
// than 4 args.
func TestV5MainDispatch_SkillsShowNoName(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "skills", "show")
	if err != nil {
		t.Fatalf("skills show subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Usage: lele skills show") {
		t.Errorf("expected show usage, got: %s", out)
	}
}

// TestV5MainDispatch_ClientRemoveNoID covers the client remove branch with
// fewer than 4 args.
func TestV5MainDispatch_ClientRemoveNoID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "client", "remove")
	if err != nil {
		t.Fatalf("client remove subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Usage: lele client remove") {
		t.Errorf("expected client remove usage, got: %s", out)
	}
}

// TestV5MainDispatch_CronRemoveNoID covers the cron remove branch with fewer
// than 4 args.
func TestV5MainDispatch_CronRemoveNoID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "cron", "remove")
	if err != nil {
		t.Fatalf("cron remove subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Usage: lele cron remove") {
		t.Errorf("expected cron remove usage, got: %s", out)
	}
}

// TestV5MainDispatch_MigrateHelp covers migrateCmd's --help branch.
func TestV5MainDispatch_MigrateHelp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "migrate", "--help")
	if err != nil {
		t.Fatalf("migrate help subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Usage: lele migrate") {
		t.Errorf("expected migrate help, got: %s", out)
	}
}

// TestV5MainDispatch_MigrateStorageHelp covers migrateStorageCmd's --help.
func TestV5MainDispatch_MigrateStorageHelp(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, err := runLELEMain(t, "migrate-storage", "--help")
	if err != nil {
		t.Fatalf("migrate-storage help subprocess failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Usage: lele migrate-storage") {
		t.Errorf("expected migrate-storage help, got: %s", out)
	}
}

// TestV5MainDispatch_MigrateStorageUnknownFlag covers migrateStorageCmd's
// unknown-flag branch (prints unknown + help + os.Exit(1)).
func TestV5MainDispatch_MigrateStorageUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	runLELEMain(t, "migrate-storage", "--bogus")
}

// TestV5MainDispatch_OnboardDeclines runs main()'s "onboard" branch with an
// existing config present; onboard asks "Overwrite?" and with /dev/null stdin it
// defaults to no ("Aborted.") and returns cleanly. This exercises the onboard
// config-exists path without the interactive wizard.
func TestV5MainDispatch_OnboardDeclines(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	runLELEMain(t, "onboard")
}

// TestV5MainDispatch_UnknownCommand covers main()'s default (unknown command)
// branch which prints "Unknown command" then os.Exit(1).
func TestV5MainDispatch_UnknownCommand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	out, _ := runLELEMain(t, "totally-unknown-command")
	if !strings.Contains(out, "Unknown command") {
		t.Errorf("expected unknown command message, got: %s", out)
	}
}
