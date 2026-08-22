package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

func setupTestLeleDir(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	return dir
}

func saveConfigAt(t *testing.T, dir string, cfg *config.Config) {
	t.Helper()
	if err := config.SaveConfig(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
}

func TestStatusCmd_NoConfig(t *testing.T) {
	setupTestLeleDir(t)
	// loadConfig falls back to default when no config exists, so statusCmd
	// prints a status block rather than an error.
	out := runCmd(func() { statusCmd() })
	if !strings.Contains(out, "lele Status") {
		t.Errorf("status should still print with default config, got: %s", out)
	}
}

func TestStatusCmd_WithConfig(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	cfg.Providers.OpenRouter.APIKey = "sk-123"
	cfg.Providers.Anthropic.APIKey = "sk-456"
	cfg.Channels.Native.Enabled = true
	saveConfigAt(t, dir, cfg)

	out := runCmd(func() { statusCmd() })
	for _, want := range []string{"lele Status", "OpenRouter API: ✓", "Anthropic API: ✓", "Model:"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q, got:\n%s", want, out)
		}
	}
}

func TestStatusCmd_WithAuthStore(t *testing.T) {
	setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	temp := 0.7
	cfg.Agents.Defaults.Temperature = &temp
	// Populate providers so the config round-trip preserves a non-nil Providers.
	cfg.Providers.OpenRouter.APIKey = "sk-test-key"
	// Write config manually since statusCmd uses default config path.
	if err := config.SaveConfig(getConfigPath(), cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	store := &auth.AuthStore{Credentials: map[string]*auth.AuthCredential{
		"openai": {AccessToken: "tok", Provider: "openai", AuthMethod: "oauth"},
	}}
	if err := auth.SaveStore(store); err != nil {
		t.Fatalf("SaveStore: %v", err)
	}
	out := runCmd(func() { statusCmd() })
	if !strings.Contains(out, "OAuth/Token Auth") {
		t.Errorf("status should show auth section, got: %s", out)
	}
}

func TestLoadConfig_ReturnsDefaultsOnMissing(t *testing.T) {
	setupTestLeleDir(t)
	// No config file exists in the temp dir; LoadConfig returns the default
	// config rather than an error.
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg == nil {
		t.Error("expected non-nil cfg on missing config file (defaults used)")
	}
}

func TestLoadConfig_SuccessWithKeyringDisabled(t *testing.T) {
	setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Keyring.Enabled = false
	if err := config.SaveConfig(getConfigPath(), cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded cfg is nil")
	}
}

func TestSetupFileLogging_Enabled(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Logs.Enabled = true
	cfg.Logs.Path = filepath.Join(dir, "logs")
	logsPath := cfg.LogsPath()

	setupFileLogging(cfg)
	if fi, err := os.Stat(logsPath); err != nil || !fi.IsDir() {
		t.Errorf("logs dir should exist, err=%v", err)
	}
}

func TestSetupFileLogging_Disabled(t *testing.T) {
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Logs.Enabled = false
	// Must not panic or create anything.
	setupFileLogging(cfg)
}

func TestSetupFileLogging_ErrorPath(t *testing.T) {
	setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Logs.Enabled = true
	cfg.Logs.Path = "/proc/not/writable/logs"
	out := runCmd(func() { setupFileLogging(cfg) })
	if !strings.Contains(out, "Warning: could not enable file logging") {
		t.Errorf("expected warning, got: %s", out)
	}
}

func TestSetupFileLogging_CleanupOldLogs(t *testing.T) {
	dir := setupTestLeleDir(t)
	cfg, err := defaultTestConfig()
	if err != nil {
		t.Fatalf("defaultTestConfig: %v", err)
	}
	cfg.Logs.Enabled = true
	cfg.Logs.Path = filepath.Join(dir, "logs")
	cfg.Logs.MaxDays = 30
	_ = captureStdout(t)
	setupFileLogging(cfg)
	// cleanup ran without panic
}
