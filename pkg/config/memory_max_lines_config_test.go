package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryMaxLinesDefaultConfig(t *testing.T) {
	if got := DefaultConfig().Agents.Defaults.MemoryMaxLines; got != 500 {
		t.Fatalf("DefaultConfig memory_max_lines = %d, want 500", got)
	}
}

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

func TestMemoryMaxLinesToConfigDirect(t *testing.T) {
	doc := defaultEditableDocument()
	doc.Agents.Defaults.MemoryMaxLines = 120

	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}
	if got := cfg.Agents.Defaults.MemoryMaxLines; got != 120 {
		t.Errorf("ToConfig dropped/corrupted memory_max_lines: got %d, want 120", got)
	}
}
