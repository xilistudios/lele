package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const auditWebUIFixture = `{
  "language": "es",
  "tui": {"mouse_enabled": true, "onboarding_completed": true, "keep_awake": true},
  "updates": {"enabled": true, "repo": "myorg/lele-fork"},
  "keyring": {"enabled": false},
  "goal": {"enabled": true, "max_active": 3},
  "server": {"read_timeout_seconds": 42},
  "agents": {"defaults": {"model": "deepseek-v4-pro"}},
  "providers": {"openrouter": {"api_key": "sk-literal-123", "headers": {"X-Trace": "1"}}}
}`

func TestSaveEditableDocumentPreservesUnmodeledTopLevel(t *testing.T) {
	path := writeSaveFixture(t, auditWebUIFixture)

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Agents.Defaults.MaxTokens = 2048
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	parsed := readSavedJSON(t, path)
	if got := parsed["language"]; got != "es" {
		t.Errorf("language = %v, want es", got)
	}
	tui := savedSection(t, parsed, "tui")
	if tui["mouse_enabled"] != true || tui["onboarding_completed"] != true || tui["keep_awake"] != true {
		t.Errorf("tui = %v, want the original values", tui)
	}
	updates := savedSection(t, parsed, "updates")
	if updates["enabled"] != true || updates["repo"] != "myorg/lele-fork" {
		t.Errorf("updates = %v, want the fork repo preserved", updates)
	}
	keyring := savedSection(t, parsed, "keyring")
	if keyring["enabled"] != false {
		t.Errorf("keyring.enabled = %v, want false preserved", keyring["enabled"])
	}
	goal := savedSection(t, parsed, "goal")
	if goal["max_active"] != float64(3) {
		t.Errorf("goal.max_active = %v, want 3 preserved", goal["max_active"])
	}
	server := savedSection(t, parsed, "server")
	if server["read_timeout_seconds"] != float64(42) {
		t.Errorf("server.read_timeout_seconds = %v, want 42 preserved", server["read_timeout_seconds"])
	}
	openrouter := objectAt(t, savedSection(t, parsed, "providers"), "openrouter")
	if openrouter["api_key"] != "sk-literal-123" {
		t.Errorf("providers.openrouter.api_key = %v, want the literal key preserved", openrouter["api_key"])
	}
	headers := objectAt(t, openrouter, "headers")
	if headers["X-Trace"] != "1" {
		t.Errorf("providers.openrouter.headers = %v, want X-Trace preserved", headers)
	}
	defaults := objectAt(t, savedSection(t, parsed, "agents"), "defaults")
	if defaults["model"] != "deepseek-v4-pro" || defaults["max_tokens"] != float64(2048) {
		t.Errorf("agents.defaults = %v, want model preserved and max_tokens updated", defaults)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Updates.Repo != "myorg/lele-fork" || reloaded.Language != "es" || !reloaded.TUI.MouseEnabled {
		t.Errorf("reloaded config lost sections: repo=%q language=%q tui=%v", reloaded.Updates.Repo, reloaded.Language, reloaded.TUI)
	}
	if reloaded.Agents.Defaults.MaxTokens != 2048 {
		t.Errorf("reloaded max_tokens = %d, want 2048", reloaded.Agents.Defaults.MaxTokens)
	}
}

func TestSaveEditableDocumentResetToDefaultDropsPortKeepsNamedHost(t *testing.T) {
	path := writeSaveFixture(t, `{"gateway":{"host":"127.0.0.1","port":9999}}`)

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	def := DefaultConfig()
	doc.Gateway.Host = def.Gateway.Host
	doc.Gateway.Port = def.Gateway.Port
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	block := savedSection(t, readSavedJSON(t, path), "gateway")
	if _, hasPort := block["port"]; hasPort {
		t.Errorf("gateway.port survived a reset to default: %v", block)
	}
	if host, hasHost := block["host"]; !hasHost || host != def.Gateway.Host {
		t.Errorf("gateway.host = %v (present %v), want the explicitly named %q kept even at the default", host, hasHost, def.Gateway.Host)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if reloaded.Gateway.Port != def.Gateway.Port || reloaded.Gateway.Host != def.Gateway.Host {
		t.Errorf("gateway = %+v, want the defaults after reset", reloaded.Gateway)
	}
	if !reloaded.HostExplicitlyConfigured() {
		t.Error("HostExplicitlyConfigured() = false, want true: the file still names the host")
	}
}

func TestSaveEditableDocumentPreservesNestedUnmodeled(t *testing.T) {
	path := writeSaveFixture(t, `{
  "channels": {"telegram": {"token": "t", "enabled": true, "allow_from": ["42"], "future_key": "keep-me"}},
  "agents": {"defaults": {"model": "gpt-4o", "future_agent_key": true}}
}`)

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Agents.Defaults.Model = "gpt-4o-mini"
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	parsed := readSavedJSON(t, path)
	telegram := objectAt(t, savedSection(t, parsed, "channels"), "telegram")
	if telegram["future_key"] != "keep-me" {
		t.Errorf("channels.telegram.future_key = %v, want keep-me preserved", telegram["future_key"])
	}
	if telegram["token"] != "t" {
		t.Errorf("channels.telegram.token = %v, want t preserved", telegram["token"])
	}
	allowFrom, ok := telegram["allow_from"].([]interface{})
	if !ok || len(allowFrom) != 1 || allowFrom[0] != "42" {
		t.Errorf("channels.telegram.allow_from = %v, want [42]", telegram["allow_from"])
	}

	defaults := objectAt(t, savedSection(t, parsed, "agents"), "defaults")
	if defaults["future_agent_key"] != true {
		t.Errorf("agents.defaults.future_agent_key = %v, want true preserved", defaults["future_agent_key"])
	}
	if defaults["model"] != "gpt-4o-mini" {
		t.Errorf("agents.defaults.model = %v, want gpt-4o-mini", defaults["model"])
	}
}

func TestSaveEditableDocumentIsAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const original = `{"language":"es"}`
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Agents.Defaults.Model = "gpt-4o"

	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })

	if err := SaveEditableDocument(path, doc); err == nil {
		t.Fatal("SaveEditableDocument into a read-only directory succeeded; want error")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatalf("chmod back: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != original {
		t.Errorf("config file changed after a failed save:\n%s", after)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".lele-config-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind after a failed save: %v", leftovers)
	}
	if perm := filePerm(t, path); perm != 0600 {
		t.Errorf("config permission = %04o, want 0600", perm)
	}
}

func TestSaveEditableDocumentKeepsPlaceholdersOnPartialSave(t *testing.T) {
	t.Setenv("LELE_TEST_PARTIAL_TOKEN", "123:PARTIAL")

	path := writeSaveFixture(t, `{
  "channels": {"telegram": {"token": "{{ENV_LELE_TEST_PARTIAL_TOKEN}}", "enabled": true}},
  "providers": {"llmproxy": {"type": "openai", "api_key": "k", "api_base": "{{ENV_LELE_TEST_PARTIAL_BASE}}"}}
}`)

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Agents.Defaults.Model = "gpt-4o"
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	saved := readSavedString(t, path)
	if !strings.Contains(saved, "{{ENV_LELE_TEST_PARTIAL_TOKEN}}") {
		t.Errorf("telegram token placeholder lost:\n%s", saved)
	}
	if strings.Contains(saved, "123:PARTIAL") {
		t.Errorf("telegram token inlined:\n%s", saved)
	}
	if !strings.Contains(saved, "{{ENV_LELE_TEST_PARTIAL_BASE}}") {
		t.Errorf("api_base placeholder lost:\n%s", saved)
	}
}

func objectAt(t *testing.T, parent map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	raw, ok := parent[key]
	if !ok {
		t.Fatalf("%q missing; present: %v", key, sortedKeys(t, parent))
	}
	obj, ok := raw.(map[string]interface{})
	if !ok {
		t.Fatalf("%q is %T, want object", key, raw)
	}
	return obj
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	return info.Mode().Perm()
}

func TestSaveEditableDocumentKeySetStable(t *testing.T) {
	path := writeSaveFixture(t, auditWebUIFixture)
	before := readSavedJSON(t, path)

	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	after := readSavedJSON(t, path)
	for _, key := range sortedKeys(t, before) {
		if _, ok := after[key]; !ok {
			t.Errorf("top-level key %q lost by an unchanged save (after: %v)", key, sortedKeys(t, after))
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("unchanged round-trip changed the document:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestSaveEditableDocumentRefusesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	const corrupt = `{"language": "es",`
	if err := os.WriteFile(path, []byte(corrupt), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := SaveEditableDocument(path, defaultEditableDocument())
	if err == nil {
		t.Fatal("SaveEditableDocument over an unparsable file succeeded; want an error")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(after) != corrupt {
		t.Errorf("unparsable config was overwritten:\n%s", after)
	}
}
