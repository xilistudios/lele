package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSubagentRetentionMinutesConfigRoundTrip(t *testing.T) {
	if got := DefaultConfig().Agents.Defaults.SubagentRetentionMinutes; got != 5 {
		t.Fatalf("DefaultConfig subagent_retention_minutes = %d, want 5", got)
	}

	cfg := DefaultConfig()
	cfg.Agents.Defaults.SubagentRetentionMinutes = 20
	doc := editableDocumentFromConfig(cfg)
	if got := doc.Agents.Defaults.SubagentRetentionMinutes; got != 20 {
		t.Fatalf("editableDocumentFromConfig dropped/corrupted subagent_retention_minutes: got %d, want 20", got)
	}
	back, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig failed: %v", err)
	}
	if got := back.Agents.Defaults.SubagentRetentionMinutes; got != 20 {
		t.Errorf("doc -> ToConfig dropped/corrupted subagent_retention_minutes: got %d, want 20", got)
	}

	raw, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal serializable: %v", err)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal serializable: %v", err)
	}
	defaults := parsed["agents"].(map[string]interface{})["defaults"].(map[string]interface{})
	if _, ok := defaults["subagent_retention_minutes"]; !ok {
		t.Error("subagent_retention_minutes missing from serialized agents.defaults — would be silently dropped on save")
	}
}

func TestSubagentRetentionMinutesDefaultApplies(t *testing.T) {
	doc := defaultEditableDocument()
	if got := doc.Agents.Defaults.SubagentRetentionMinutes; got != 5 {
		t.Fatalf("defaultEditableDocument subagent_retention_minutes = %d, want 5", got)
	}

	doc = applyDefaults(&EditableDocument{})
	if got := doc.Agents.Defaults.SubagentRetentionMinutes; got != 5 {
		t.Errorf("applyDefaults left subagent_retention_minutes = %d, want the default 5", got)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"agents":{"defaults":{"provider":"openai","model":"gpt-4o","subagent_retention_minutes":3}}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := loaded.Agents.Defaults.SubagentRetentionMinutes; got != 3 {
		t.Errorf("LoadConfig subagent_retention_minutes = %d, want 3", got)
	}
}
