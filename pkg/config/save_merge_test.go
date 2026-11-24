package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func writeSaveFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func readSavedString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

func sortedKeys(t *testing.T, parsed map[string]interface{}) []string {
	t.Helper()
	keys := make([]string, 0, len(parsed))
	for key := range parsed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func TestSaveConfigPreservesEnvPlaceholders(t *testing.T) {
	t.Setenv("LELE_TEST_OR_KEY", "sk-or-REAL-RESOLVED-VALUE")
	t.Setenv("LELE_TEST_TG_TOKEN", "123:REAL-TG-TOKEN")

	path := writeSaveFixture(t, `{
  "providers": {
    "openrouter": {"api_key": "{{ENV_LELE_TEST_OR_KEY}}"},
    "groq": {"api_key": "{{SECRET:groq.api_key}}"}
  },
  "channels": {
    "telegram": {"token": "{{ENV_LELE_TEST_TG_TOKEN}}", "enabled": true, "verbose": "basic"}
  }
}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	openrouter, ok := cfg.Providers.GetNamed("openrouter")
	if !ok {
		t.Fatal("openrouter provider missing from the loaded config")
	}
	if openrouter.APIKey != "sk-or-REAL-RESOLVED-VALUE" {
		t.Fatalf("in-memory api_key = %q, want the resolved env value", openrouter.APIKey)
	}
	if cfg.Channels.Telegram.Token != "123:REAL-TG-TOKEN" {
		t.Fatalf("in-memory token = %q, want the resolved env value", cfg.Channels.Telegram.Token)
	}

	if err := cfg.PersistTelegramVerbose(path, VerboseFull); err != nil {
		t.Fatalf("PersistTelegramVerbose: %v", err)
	}

	saved := readSavedString(t, path)
	for _, placeholder := range []string{"{{ENV_LELE_TEST_OR_KEY}}", "{{SECRET:groq.api_key}}", "{{ENV_LELE_TEST_TG_TOKEN}}"} {
		if !strings.Contains(saved, placeholder) {
			t.Errorf("saved file lost %s:\n%s", placeholder, saved)
		}
	}
	for _, resolved := range []string{"sk-or-REAL-RESOLVED-VALUE", "123:REAL-TG-TOKEN"} {
		if strings.Contains(saved, resolved) {
			t.Errorf("saved file inlined the resolved secret %q:\n%s", resolved, saved)
		}
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Channels.Telegram.Verbose != VerboseFull {
		t.Errorf("verbose = %q, want %q persisted", reloaded.Channels.Telegram.Verbose, VerboseFull)
	}
}

func TestSaveConfigDoesNotFreezeDefaults(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"host":"127.0.0.1","port":8080}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Gateway.Port = 9090
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	parsed := readSavedJSON(t, path)
	if keys := sortedKeys(t, parsed); !reflect.DeepEqual(keys, []string{"gateway"}) {
		t.Fatalf("saved top-level keys = %v, want exactly [gateway]", keys)
	}
	gateway := savedSection(t, parsed, "gateway")
	if keys := sortedKeys(t, gateway); !reflect.DeepEqual(keys, []string{"host", "port"}) {
		t.Fatalf("saved gateway keys = %v, want exactly [host port]", keys)
	}
	if got := gateway["port"]; got != float64(9090) {
		t.Fatalf("gateway.port = %v, want 9090", got)
	}
}

func TestSaveConfigPreservesUnmodeledKeys(t *testing.T) {
	path := writeSaveFixture(t, `{
  "gateway": {"host": "127.0.0.1", "port": 8080},
  "language": "es",
  "tui": {"mouse_enabled": true, "onboarding_completed": true},
  "updates": {"enabled": true, "repo": "myorg/lele-fork"},
  "keyring": {"enabled": false},
  "goal": {"judge": {"mode": "inline"}, "max_active": 3},
  "server": {"read_timeout_seconds": 42},
  "future_section": {"anything": "yes"}
}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Gateway.Port = 9090
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	parsed := readSavedJSON(t, path)
	if got := parsed["language"]; got != "es" {
		t.Errorf("language = %v, want es", got)
	}
	tui := savedSection(t, parsed, "tui")
	if tui["mouse_enabled"] != true || tui["onboarding_completed"] != true {
		t.Errorf("tui = %v, want the original values", tui)
	}
	updates := savedSection(t, parsed, "updates")
	if updates["repo"] != "myorg/lele-fork" {
		t.Errorf("updates.repo = %v, want myorg/lele-fork", updates["repo"])
	}
	keyring := savedSection(t, parsed, "keyring")
	if keyring["enabled"] != false {
		t.Errorf("keyring.enabled = %v, want false", keyring["enabled"])
	}
	goal := savedSection(t, parsed, "goal")
	if goal["max_active"] != float64(3) {
		t.Errorf("goal.max_active = %v, want 3", goal["max_active"])
	}
	server := savedSection(t, parsed, "server")
	if server["read_timeout_seconds"] != float64(42) {
		t.Errorf("server.read_timeout_seconds = %v, want 42", server["read_timeout_seconds"])
	}
	if _, ok := parsed["future_section"]; !ok {
		t.Errorf("unknown top-level section dropped: keys %v", sortedKeys(t, parsed))
	}
	gateway := savedSection(t, parsed, "gateway")
	if gateway["port"] != float64(9090) || gateway["host"] != "127.0.0.1" {
		t.Errorf("gateway = %v, want host 127.0.0.1 port 9090", gateway)
	}
}

func TestSaveConfigEnvOverrideNotPersisted(t *testing.T) {
	t.Setenv("LELE_LANG", "es")

	path := writeSaveFixture(t, `{"gateway":{"host":"127.0.0.1","port":8080}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Language != "es" {
		t.Fatalf("Language = %q, want the LELE_LANG override", cfg.Language)
	}
	cfg.Gateway.Port = 9090
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	parsed := readSavedJSON(t, path)
	if keys := sortedKeys(t, parsed); !reflect.DeepEqual(keys, []string{"gateway"}) {
		t.Fatalf("saved top-level keys = %v, want exactly [gateway]: env overrides must not be persisted", keys)
	}
}

func TestSaveConfigConcurrentExternalEditNotClobbered(t *testing.T) {
	path := writeSaveFixture(t, `{"language":"fr","gateway":{"host":"127.0.0.1","port":8080}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Language != "fr" {
		t.Fatalf("Language = %q, want fr", cfg.Language)
	}

	if err := os.WriteFile(path, []byte(`{"language":"es","gateway":{"host":"127.0.0.1","port":8080}}`), 0600); err != nil {
		t.Fatalf("external rewrite: %v", err)
	}

	cfg.Gateway.Port = 9090
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	parsed := readSavedJSON(t, path)
	if got := parsed["language"]; got != "es" {
		t.Errorf("language = %v, want the concurrent external edit es", got)
	}
	gateway := savedSection(t, parsed, "gateway")
	if gateway["port"] != float64(9090) {
		t.Errorf("gateway.port = %v, want 9090", gateway["port"])
	}
}

func TestSaveConfigPlaceholderGuardSkipsResolvedValue(t *testing.T) {
	t.Setenv("LELE_TEST_GUARD_TOKEN", "123:GUARD")

	path := writeSaveFixture(t, `{"channels":{"telegram":{"token":"{{ENV_LELE_TEST_GUARD_TOKEN}}"}}}`)

	cfg := DefaultConfig()
	cfg.Channels.Telegram.Token = "123:GUARD"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	saved := readSavedString(t, path)
	if !strings.Contains(saved, "{{ENV_LELE_TEST_GUARD_TOKEN}}") {
		t.Fatalf("placeholder replaced by its resolved value:\n%s", saved)
	}
	if strings.Contains(saved, "123:GUARD") {
		t.Fatalf("saved file inlined the secret:\n%s", saved)
	}
}

func TestSaveConfigRevertsValueWrittenByEarlierSave(t *testing.T) {
	path := writeSaveFixture(t, `{"tui":{"mouse_enabled":true,"max_rendered_messages":200}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	cfg.TUI.MaxRenderedMessages = 50
	cfg.TUI.MouseEnabled = false
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig (perturb): %v", err)
	}
	tui := savedSection(t, readSavedJSON(t, path), "tui")
	if tui["max_rendered_messages"] != float64(50) || tui["mouse_enabled"] != false {
		t.Fatalf("tui after perturb = %v, want 50 and false", tui)
	}

	cfg.TUI.MaxRenderedMessages = 200
	cfg.TUI.MouseEnabled = true
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig (revert): %v", err)
	}

	tui = savedSection(t, readSavedJSON(t, path), "tui")
	if tui["max_rendered_messages"] != float64(200) {
		t.Fatalf("tui.max_rendered_messages after revert = %v, want 200 on disk", tui["max_rendered_messages"])
	}
	if tui["mouse_enabled"] != true {
		t.Fatalf("tui.mouse_enabled after revert = %v, want true on disk", tui["mouse_enabled"])
	}
}

func TestSaveConfigRevertedOmitEmptyValueReloadsToDefault(t *testing.T) {
	path := writeSaveFixture(t, `{"tui":{"keep_awake":true,"mouse_enabled":true}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.TUI.KeepAwake {
		t.Fatal("KeepAwake = false, want the on-disk true")
	}

	cfg.TUI.KeepAwake = false
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.TUI.KeepAwake {
		t.Error("TUI.KeepAwake reloaded as true, want the saved false")
	}
	if !reloaded.TUI.MouseEnabled {
		t.Error("TUI.MouseEnabled = false, want the untouched true")
	}
}

func TestSaveConfigRepeatedSavesStayMinimal(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"host":"127.0.0.1","port":8080}}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	cfg.Gateway.Port = 9090
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig (port): %v", err)
	}
	cfg.Gateway.Host = "0.0.0.0"
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig (host): %v", err)
	}

	parsed := readSavedJSON(t, path)
	if keys := sortedKeys(t, parsed); !reflect.DeepEqual(keys, []string{"gateway"}) {
		t.Fatalf("saved top-level keys = %v, want exactly [gateway]", keys)
	}
	gateway := savedSection(t, parsed, "gateway")
	if keys := sortedKeys(t, gateway); !reflect.DeepEqual(keys, []string{"host", "port"}) {
		t.Fatalf("saved gateway keys = %v, want exactly [host port]", keys)
	}
	if gateway["host"] != "0.0.0.0" || gateway["port"] != float64(9090) {
		t.Fatalf("gateway = %v, want host 0.0.0.0 port 9090", gateway)
	}
}

func TestDiffJSON(t *testing.T) {
	base := map[string]interface{}{
		"keep":   "same",
		"nested": map[string]interface{}{"changed": "old", "dropped": "gone", "kept": "still"},
		"list":   []interface{}{"a", "b"},
		"scalar": "x",
	}
	next := map[string]interface{}{
		"keep":   "same",
		"nested": map[string]interface{}{"changed": "new", "kept": "still", "added": true},
		"list":   []interface{}{"a", "b", "c"},
		"newkey": "fresh",
	}

	changes := diffJSON(base, next)
	got := make(map[string]interface{}, len(changes))
	for _, ch := range changes {
		key := strings.Join(ch.path, ".")
		if ch.remove {
			got[key] = "<delete>"
			continue
		}
		got[key] = ch.value
	}

	want := map[string]interface{}{
		"nested.changed": "new",
		"nested.dropped": "<delete>",
		"nested.added":   true,
		"list":           []interface{}{"a", "b", "c"},
		"newkey":         "fresh",
		"scalar":         "<delete>",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffJSON changes = %v, want %v", got, want)
	}
}

func TestDiffJSONNoChanges(t *testing.T) {
	base := map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{1, 2}}}
	next := map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{1, 2}}}
	if changes := diffJSON(base, next); len(changes) != 0 {
		t.Fatalf("diffJSON = %v, want no changes", changes)
	}
}

func TestApplyChangesPreservesUntouchedRawValues(t *testing.T) {
	raw := map[string]json.RawMessage{
		"untouched": json.RawMessage(`{"big": 9007199254740993, "text": "{{ENV_SOMETHING}}"}`),
		"gateway":   json.RawMessage(`{"port": 8080}`),
	}
	changes := []change{
		{path: []string{"gateway", "port"}, value: 9090},
		{path: []string{"added"}, value: map[string]interface{}{"k": "v"}},
	}

	out, err := applyChanges(raw, changes)
	if err != nil {
		t.Fatalf("applyChanges: %v", err)
	}
	if got := string(out["untouched"]); got != string(raw["untouched"]) {
		t.Fatalf("untouched raw value changed: %s", got)
	}

	var gateway map[string]interface{}
	if err := json.Unmarshal(out["gateway"], &gateway); err != nil {
		t.Fatalf("unmarshal gateway: %v", err)
	}
	if gateway["port"] != float64(9090) {
		t.Fatalf("gateway.port = %v, want 9090", gateway["port"])
	}
	if _, ok := out["added"]; !ok {
		t.Fatal("added key missing from the merged raw document")
	}
}

func TestApplyChangesDeletesKeys(t *testing.T) {
	raw := map[string]json.RawMessage{
		"gateway":  json.RawMessage(`{"port": 9999, "host": "127.0.0.1"}`),
		"language": json.RawMessage(`"es"`),
	}
	changes := []change{
		{path: []string{"gateway", "port"}, remove: true},
		{path: []string{"language"}, remove: true},
	}

	out, err := applyChanges(raw, changes)
	if err != nil {
		t.Fatalf("applyChanges: %v", err)
	}
	if _, ok := out["language"]; ok {
		t.Error("language key was not deleted")
	}
	var gateway map[string]interface{}
	if err := json.Unmarshal(out["gateway"], &gateway); err != nil {
		t.Fatalf("unmarshal gateway: %v", err)
	}
	if _, ok := gateway["port"]; ok {
		t.Errorf("gateway.port was not deleted: %v", gateway)
	}
	if gateway["host"] != "127.0.0.1" {
		t.Errorf("gateway.host = %v, want the untouched value", gateway["host"])
	}
}
