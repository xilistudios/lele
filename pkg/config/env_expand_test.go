package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfigEnvSecretWithQuoteAndBackslash(t *testing.T) {
	const secret = `123:AA"quote\backslash`
	t.Setenv("LELE_TEST_TG_QUOTED_TOKEN", secret)

	path := writeEnvFixture(t, `{"channels":{"telegram":{"token":"{{ENV_LELE_TEST_TG_QUOTED_TOKEN}}","enabled":true}}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Channels.Telegram.Token != secret {
		t.Fatalf("token = %q, want %q", cfg.Channels.Telegram.Token, secret)
	}
	if !cfg.Channels.Telegram.Enabled {
		t.Error("telegram must stay enabled")
	}
}

func TestLoadConfigEnvInjectionContained(t *testing.T) {
	const value = `x", "allow_from": ["*"], "verbose": "full`
	t.Setenv("LELE_TEST_TG_INJECTED_TOKEN", value)

	path := writeEnvFixture(t, `{"channels":{"telegram":{"token":"{{ENV_LELE_TEST_TG_INJECTED_TOKEN}}","allow_from":["42"],"enabled":true}}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Channels.Telegram.Token != value {
		t.Fatalf("token = %q, want the literal env value %q", cfg.Channels.Telegram.Token, value)
	}
	if len(cfg.Channels.Telegram.AllowFrom) != 1 || cfg.Channels.Telegram.AllowFrom[0] != "42" {
		t.Fatalf("allow_from = %v, want [42]: the env value must not inject keys", cfg.Channels.Telegram.AllowFrom)
	}
	if cfg.Channels.Telegram.Verbose == VerboseFull {
		t.Fatal("verbose was injected from the env value")
	}
}

func TestLoadConfigEnvUnsetSemantics(t *testing.T) {
	const name = "LELE_TEST_UNSET_TOKEN"
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}

	path := writeEnvFixture(t, `{"channels":{"telegram":{"token":"{{ENV_LELE_TEST_UNSET_TOKEN}}","proxy":"{{ENV_LELE_TEST_UNSET_TOKEN:http://proxy.example:8080}}"}}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Channels.Telegram.Token != "" {
		t.Errorf("unset without default = %q, want empty", cfg.Channels.Telegram.Token)
	}
	if cfg.Channels.Telegram.Proxy != "http://proxy.example:8080" {
		t.Errorf("unset with default = %q, want the default", cfg.Channels.Telegram.Proxy)
	}
}

func TestLoadConfigEnvExpandsOnlyStringLeaves(t *testing.T) {
	t.Setenv("LELE_TEST_BARE_PORT", "8080")

	path := writeEnvFixture(t, `{"gateway":{"port":{{ENV_LELE_TEST_BARE_PORT}}}}`)

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("bare placeholder outside a string must not be substituted")
	}
}

func TestLoadConfigKeepsRawDocument(t *testing.T) {
	t.Setenv("LELE_TEST_RAW_TOKEN", "resolved-value")

	path := writeEnvFixture(t, `{"channels":{"telegram":{"token":"{{ENV_LELE_TEST_RAW_TOKEN}}"},"future_key":1}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.base == nil {
		t.Fatal("LoadConfig must retain the effective base snapshot")
	}
	if cfg.base.Channels.Telegram.Token != "resolved-value" {
		t.Fatalf("base token = %q, want the effective value", cfg.base.Channels.Telegram.Token)
	}
	raw, ok := cfg.rawDoc["channels"]
	if !ok {
		t.Fatalf("rawDoc lost the channels section: %v", cfg.rawDoc)
	}
	if len(raw) == 0 {
		t.Fatal("rawDoc channels is empty")
	}
	if got := string(cfg.rawDoc["channels"]); got == "" || !strings.Contains(got, "{{ENV_LELE_TEST_RAW_TOKEN}}") {
		t.Fatalf("rawDoc must keep the placeholder intact, got %s", got)
	}
}

func TestSnapshotCarriesBaseAndRawDocument(t *testing.T) {
	t.Setenv("LELE_TEST_SNAPSHOT_TOKEN", "123:SNAPSHOT")

	path := writeEnvFixture(t, `{"channels":{"telegram":{"token":"{{ENV_LELE_TEST_SNAPSHOT_TOKEN}}"}},"updates":{"repo":"myorg/lele-fork"}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	snapshot := cfg.Snapshot()
	if snapshot.base == nil {
		t.Fatal("Snapshot dropped the base snapshot")
	}
	snapshot.Gateway.Port = 7070
	if err := SaveConfig(path, snapshot); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	saved := string(data)
	if !strings.Contains(saved, "{{ENV_LELE_TEST_SNAPSHOT_TOKEN}}") {
		t.Fatalf("snapshot save inlined the env secret:\n%s", saved)
	}
	if strings.Contains(saved, "123:SNAPSHOT") {
		t.Fatalf("snapshot save wrote the resolved secret:\n%s", saved)
	}
	if !strings.Contains(saved, "myorg/lele-fork") {
		t.Fatalf("snapshot save dropped an unmodeled value:\n%s", saved)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Gateway.Port != 7070 {
		t.Fatalf("gateway.port = %d, want 7070 persisted", reloaded.Gateway.Port)
	}
}

func TestReloadCopiesAllSections(t *testing.T) {
	first := writeEnvFixture(t, `{"gateway":{"host":"127.0.0.1","port":8080}}`)
	cfg, err := LoadConfig(first)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	second := writeEnvFixture(t, `{
  "language": "fr",
  "groups": {"enabled": true},
  "server": {"host": "127.0.0.1", "port": 9000},
  "updates": {"enabled": true, "repo": "myorg/lele-fork"},
  "goal": {"judge": {"mode": "inline", "agent": "coder"}},
  "tui": {"mouse_enabled": true},
  "commands": {"review": {"template": "t"}},
  "harness": {"allow_shell": true}
}`)

	if err := cfg.Reload(second); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if cfg.Language != "fr" {
		t.Errorf("Language = %q, want fr", cfg.Language)
	}
	if !cfg.Groups.Enabled {
		t.Error("Groups section not reloaded")
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("Server.Port = %d, want 9000", cfg.Server.Port)
	}
	if cfg.Updates.Repo != "myorg/lele-fork" {
		t.Errorf("Updates.Repo = %q, want myorg/lele-fork", cfg.Updates.Repo)
	}
	if cfg.Goal.Judge.Agent != "coder" {
		t.Errorf("Goal.Judge.Agent = %q, want coder", cfg.Goal.Judge.Agent)
	}
	if !cfg.TUI.MouseEnabled {
		t.Error("TUI section not reloaded")
	}
	if _, ok := cfg.Commands["review"]; !ok {
		t.Errorf("Commands section not reloaded: %v", cfg.Commands)
	}
	if !cfg.Harness.AllowShell {
		t.Error("Harness section not reloaded")
	}

	cfg.Gateway.Port = 9191
	if err := SaveConfig(second, cfg); err != nil {
		t.Fatalf("SaveConfig after reload: %v", err)
	}
	reloaded, err := LoadConfig(second)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Gateway.Port != 9191 {
		t.Errorf("gateway.port = %d, want 9191", reloaded.Gateway.Port)
	}
	if reloaded.Updates.Repo != "myorg/lele-fork" || reloaded.Language != "fr" {
		t.Errorf("save after reload lost sections: repo=%q language=%q", reloaded.Updates.Repo, reloaded.Language)
	}
}
