package tools

import (
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func newGuardedExecTool(t *testing.T, whitelist []string) *ExecTool {
	t.Helper()
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	cfg.Tools.Exec.WhitelistCommands = whitelist
	return NewExecToolWithConfig(filepath.Join(t.TempDir(), "ws"), false, cfg)
}

func TestNormalizeWhitelistKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  rm   -rf  /tmp/foo ", "rm -rf /tmp/foo"},
		{"Echo\tHELLO\nworld", "echo hello world"},
		{"git status", "git status"},
	}
	for _, c := range cases {
		if got := NormalizeWhitelistKey(c.in); got != c.want {
			t.Errorf("NormalizeWhitelistKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestExecWhitelist_BypassesDenyGuard is the core promise of the "always allow"
// approval option: a whitelisted command must not be blocked by deny patterns,
// while non-whitelisted commands still are.
func TestExecWhitelist_BypassesDenyGuard(t *testing.T) {
	tool := newGuardedExecTool(t, []string{"rm -rf /tmp/whitelisted"})

	if msg := tool.guardCommand("rm -rf /tmp/whitelisted", ""); msg != "" {
		t.Errorf("whitelisted command was blocked: %q", msg)
	}
	// Normalization must apply on both sides of the comparison.
	if msg := tool.guardCommand("  RM   -RF /tmp/whitelisted ", ""); msg != "" {
		t.Errorf("whitelisted command with different case/spacing was blocked: %q", msg)
	}
	if msg := tool.guardCommand("rm -rf /tmp/other", ""); msg == "" {
		t.Error("non-whitelisted dangerous command was not blocked")
	}
}

func TestExecWhitelist_SetAndAdd(t *testing.T) {
	tool := newGuardedExecTool(t, nil)

	if !tool.AddWhitelistCommand("git push origin main") {
		t.Fatal("AddWhitelistCommand reported no change for a new command")
	}
	if tool.AddWhitelistCommand("git  push origin main") {
		t.Error("AddWhitelistCommand added a duplicate under a different normalization")
	}
	if !tool.Whitelisted("git push origin main") {
		t.Error("command missing from whitelist after AddWhitelistCommand")
	}

	tool.SetWhitelist([]string{"docker ps"})
	if tool.Whitelisted("git push origin main") {
		t.Error("SetWhitelist did not replace the previous entries")
	}
	if !tool.Whitelisted("docker ps") {
		t.Error("SetWhitelist did not apply new entries")
	}
}

// TestNewExecTool_InitializesWhitelistFromConfig guards the persistence path:
// entries saved to config.json by the TUI must be active on the next startup.
func TestNewExecTool_InitializesWhitelistFromConfig(t *testing.T) {
	tool := newGuardedExecTool(t, []string{"rm -rf /tmp/from-config"})
	if !tool.Whitelisted("rm -rf /tmp/from-config") {
		t.Fatal("config whitelist was not loaded into the exec tool")
	}
	if msg := tool.guardCommand("rm -rf /tmp/from-config", ""); msg != "" {
		t.Errorf("config-whitelisted command still blocked: %q", msg)
	}
}
