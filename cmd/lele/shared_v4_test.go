package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestLoadConfig_ErrorPath exercises the error-return branch in loadConfig by
// writing a corrupt config.json in the config dir.
func TestLoadConfig_ErrorPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{ not valid json"), 0644); err != nil {
		t.Fatalf("write corrupt config: %v", err)
	}

	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for corrupt config, got nil")
	}
}

// TestRegisterKeyringResolver_Enabled creates a config with keyring enabled and
// verifies registerKeyringResolver registers a resolver without panicking.
func TestRegisterKeyringResolver_EnabledV4(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Keyring.Enabled = true
	cfg.Keyring.Path = filepath.Join(dir, "vault")
	registerKeyringResolver(cfg) // must not panic
}

// TestSetupFileLogging_ErrorWithMaxDays covers the CleanupOldLogs warning path.
func TestSetupFileLogging_MaxDaysErrorV4(t *testing.T) {
	dir := t.TempDir()
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Logs.Enabled = true
	cfg.Logs.Path = filepath.Join(dir, "logs")
	cfg.Logs.MaxDays = 5
	// Point cleanup at an invalid path to trigger the warning branch.
	cfg.Logs.Path = "/proc/nonexistent-unwritable/logs"
	_ = captureStdout(t)
	setupFileLogging(cfg) // must not panic
}

var _ = config.DefaultConfig
