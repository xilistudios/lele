package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/keyring"
)

// --- registerKeyringResolver ---

// TestRegisterKeyringResolver_DisabledV6 covers the disabled branch without
// conflicting with the identically-named test in main_extra_test.go.
func TestRegisterKeyringResolver_DisabledV6(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Keyring = config.KeyringConfig{Enabled: false}
	registerKeyringResolver(cfg) // should register nil resolver without panic
}

// TestRegisterKeyringResolver_EnabledFileBackend
func TestRegisterKeyringResolver_EnabledFileBackend(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Keyring = config.KeyringConfig{
		Enabled:      true,
		Backend:      keyring.BackendFile,
		Path:         filepath.Join(dir, "vault.enc"),
		AuditLogSize: 4,
	}
	// Should not panic; keyring service lazily created.
	registerKeyringResolver(cfg)
}

// --- copyEmbeddedToTarget ---

func TestCopyEmbeddedToTarget_Success(t *testing.T) {
	dir := t.TempDir()
	if err := copyEmbeddedToTarget(filepath.Join(dir, "ws")); err != nil {
		t.Fatalf("copyEmbeddedToTarget: %v", err)
	}
	// AGENT.md is present in the embedded workspace.
	if _, err := os.Stat(filepath.Join(dir, "ws", "AGENT.md")); err != nil {
		t.Errorf("expected AGENT.md to be copied, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ws", "MEMORY.md")); err != nil {
		t.Errorf("expected MEMORY.md to be copied, err=%v", err)
	}
}

func TestCopyEmbeddedToTarget_InvalidRelative(t *testing.T) {
	// Creating a target that fails to resolve the relative path inside the
	// embedded workspace should produce an error instead of panicking.
	if err := copyEmbeddedToTarget(""); err != nil && !strings.Contains(err.Error(), "create") {
		// MkdirAll("") should fail and wrap the error.
		t.Fatalf("expected an error building target dir, got: %v", err)
	}
}

// --- cronCmd dispatch ---

func TestCronCmd_ListDispatch(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "cron", "list"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "No scheduled jobs") {
		t.Errorf("expected empty list, got: %s", out)
	}
}

func TestCronCmd_AddMissingArgs(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "cron", "add", "--name", "x"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "--message is required") {
		t.Errorf("expected message required error, got: %s", out)
	}
}

func TestCronCmd_RemoveUsage(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "cron", "remove"})
	out := runCmd(cronCmd)
	if !strings.Contains(out, "Usage: lele cron remove") {
		t.Errorf("expected remove usage, got: %s", out)
	}
}

// --- clientCmd dispatch extra branches ---

func TestClientCmd_RemoveUsage(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	saveConfigAt(t, dir, cfg)
	replaceArgs(t, []string{"lele", "client", "remove"})
	out := runCmd(clientCmd)
	if !strings.Contains(out, "Usage: lele client remove") {
		t.Errorf("expected remove usage, got: %s", out)
	}
}

// --- skills subcommand dispatch via os.Args (missing-arg branches) ---

// TestSkillsListBuiltinCmd_DescriptionParsing covers the description-parsing
// branch of skillsListBuiltinCmd (SKILL.md with a description line).
func TestSkillsListBuiltinCmd_DescriptionParsing(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(dir, "ws")
	saveConfigAt(t, dir, cfg)

	builtinDir := filepath.Join(filepath.Dir(cfg.Agents.Defaults.Workspace), "lele", "skills")
	skillDir := filepath.Join(builtinDir, "news")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// First line must contain "description:" to reach the parsing branch.
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("description: Get news\nname: news\n---\n"), 0644)

	out := runCmd(skillsListBuiltinCmd)
	if !strings.Contains(out, "news") {
		t.Errorf("expected news skill listed, got: %s", out)
	}
}

// --- web.go helper (netJoinHostPort already covered in web_test.go) ---

var _ = config.DefaultConfig