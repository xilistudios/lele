package main

import (
	"testing"
)

// --- updateCmd flag handling ---

// TestMigrateCmd_DryRunNoOpenClaw exercises the flag-parsing + migrate.Run path
// when there is no ~/.openclaw home. Run returns a result without error and
// skips printing a summary on dry-run.
func TestMigrateCmd_DryRunNoOpenClaw(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	t.Setenv("OPENCLAW_HOME", t.TempDir()) // point away from real home

	replaceArgs(t, []string{"lele", "migrate", "--dry-run"})
	out := runCmd(migrateCmd)
	_ = out // Run may print diagnostic; assert no crash
}

// --- onboard provider helper helpers ---

// TestValidateProvider_Local bypasses network validation by returning true for
// localhost bases.
func TestValidateProvider_LocalV4(t *testing.T) {
	if !validateProvider("openai", "key", "localhost:4321", "Bearer") {
		t.Error("localhost provider should validate without network")
	}
	if !validateProvider("openai", "key", "http://localhost:11434/v1", "Bearer") {
		t.Error("http://localhost provider should validate")
	}
}

// TestValidateProvider_EmptyRejects missing key or base.
func TestValidateProvider_EmptyV4(t *testing.T) {
	if validateProvider("openai", "", "https://api.openai.com/v1", "Bearer") {
		t.Error("missing key should not validate")
	}
	if validateProvider("openai", "key", "", "Bearer") {
		t.Error("missing base should not validate")
	}
	if validateProvider("openai", "", "", "Bearer") {
		t.Error("empty key+base should not validate")
	}
}

// TestMaskAPIKey exercises maskAPIKey short/long cases.
func TestMaskAPIKey_V4(t *testing.T) {
	if got := maskAPIKey("1234567"); got != "***" {
		t.Errorf("short key mask = %q, want ***", got)
	}
	// 8+ char key keeps first 4 and last 4.
	masked := maskAPIKey("abcdefgh")
	if masked != "abcd...efgh" {
		t.Errorf("long key mask = %q, want abcd...efgh", masked)
	}
}
