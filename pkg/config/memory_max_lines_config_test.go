package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// agents.defaults.memory_max_lines is the cap passed to the update_memory tool
// (pkg/tools/memory_tool.go) when NewAgentInstance registers it. Like
// subagent_retention_minutes it must survive every copy path, because this
// package lost fields of exactly this shape before (max_read_lines,
// subagent_max_iterations — silent drops in the hand-written copy blocks):
//
//	doc -> ToConfig (editable -> runtime)             : hand-written copy  <-- risk
//	cfg -> editableDocumentFromConfig (runtime->doc)  : hand-written copy  <-- risk
//	doc -> toSerializable -> disk (WebUI save)        : whole-struct emit
//	file -> LoadConfig (runtime Config)               : struct tag + env tag
func TestMemoryMaxLinesDefaultConfig(t *testing.T) {
	if got := DefaultConfig().Agents.Defaults.MemoryMaxLines; got != 500 {
		t.Fatalf("DefaultConfig memory_max_lines = %d, want 500", got)
	}
}

// TestMemoryMaxLinesRoundTrip pins both hand-written copy blocks with a
// non-default value: runtime -> document and document -> runtime must each
// carry it, or a WebUI/TUI save silently reverts the knob to the default.
func TestMemoryMaxLinesRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Agents.Defaults.MemoryMaxLines = 120
	doc := editableDocumentFromConfig(cfg)
	if got := doc.Agents.Defaults.MemoryMaxLines; got != 120 {
		t.Fatalf("editableDocumentFromConfig dropped/corrupted memory_max_lines: got %d, want 120", got)
	}
	back, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}
	if got := back.Agents.Defaults.MemoryMaxLines; got != 120 {
		t.Errorf("doc -> ToConfig dropped/corrupted memory_max_lines: got %d, want 120", got)
	}
}

// TestMemoryMaxLinesSerializedDefaults guards the whole-struct emit of
// toSerializable: the key must be present in what a WebUI/TUI save writes to
// disk, otherwise the value is dropped on every save cycle even though the
// in-memory conversion above passes.
func TestMemoryMaxLinesSerializedDefaults(t *testing.T) {
	doc := editableDocumentFromConfig(DefaultConfig())
	raw, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal serializable: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal serializable: %v", err)
	}
	defaults := parsed["agents"].(map[string]interface{})["defaults"].(map[string]interface{})
	if _, ok := defaults["memory_max_lines"]; !ok {
		t.Error("memory_max_lines missing from serialized agents.defaults — would be silently dropped on save")
	}
}

// TestMemoryMaxLinesLoadFromFile: an explicit value in config.json wins over
// the code default (LoadConfig unmarshals over DefaultConfig).
func TestMemoryMaxLinesLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"agents":{"defaults":{"provider":"openai","model":"gpt-4o","memory_max_lines":42}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := loaded.Agents.Defaults.MemoryMaxLines; got != 42 {
		t.Errorf("LoadConfig memory_max_lines = %d, want 42", got)
	}
}

// TestMemoryMaxLinesAbsentInheritsDefault pins the inherit rule on both load
// paths: a config.json without the key falls back to the code default (500),
// and the editable document — where an explicit 0 cannot be told apart from
// unset — inherits the default too instead of disabling the cap.
func TestMemoryMaxLinesAbsentInheritsDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"agents":{"defaults":{"provider":"openai","model":"gpt-4o"}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := loaded.Agents.Defaults.MemoryMaxLines; got != 500 {
		t.Errorf("LoadConfig without the key = %d, want the default 500", got)
	}

	if got := defaultEditableDocument().Agents.Defaults.MemoryMaxLines; got != 500 {
		t.Errorf("defaultEditableDocument memory_max_lines = %d, want 500", got)
	}
	if got := applyDefaults(&EditableDocument{}).Agents.Defaults.MemoryMaxLines; got != 500 {
		t.Errorf("applyDefaults left memory_max_lines = %d, want the default 500", got)
	}
}

// TestMemoryMaxLinesEnvOverride covers the env tag: LoadConfig runs
// env.Parse(cfg), so LELE_AGENTS_DEFAULTS_MEMORY_MAX_LINES must reach the
// nested AgentDefaults field.
func TestMemoryMaxLinesEnvOverride(t *testing.T) {
	t.Setenv("LELE_AGENTS_DEFAULTS_MEMORY_MAX_LINES", "7")

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := loaded.Agents.Defaults.MemoryMaxLines; got != 7 {
		t.Errorf("LoadConfig memory_max_lines = %d despite env override, want 7", got)
	}
}
