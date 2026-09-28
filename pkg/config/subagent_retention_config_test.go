package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// agents.defaults.subagent_retention_minutes is the config knob behind
// SubagentManager.SetRetentionPeriod: the window a finished subagent task (and
// its session) stays in memory before the periodic retention sweeper reaps it.
//
// It is pinned here because this package lost fields of exactly this shape
// before (agents Temperature, agents.defaults max_read_lines /
// subagent_max_iterations — all silent drops in the hand-written copy blocks).
// Every path that copies agents.defaults must carry it:
//
//	doc -> ToConfig (editable -> runtime)             : hand-written copy  <-- risk
//	cfg -> editableDocumentFromConfig (runtime->doc)  : hand-written copy  <-- risk
//	doc -> toSerializable -> disk (WebUI save)        : whole-struct emit
//	file -> LoadConfig (runtime Config)               : struct tag
func TestSubagentRetentionMinutesConfigRoundTrip(t *testing.T) {
	// Code default: 5m, the same window SubagentManager uses on its own.
	if got := DefaultConfig().Agents.Defaults.SubagentRetentionMinutes; got != 5 {
		t.Fatalf("DefaultConfig subagent_retention_minutes = %d, want 5", got)
	}

	// doc -> runtime, and runtime -> doc, must both carry a custom value.
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

	// The serialized document (what a WebUI/TUI save writes) must contain the key.
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

// TestSubagentRetentionMinutesDefaultApplies pins the tri-state: an unset (0)
// value inherits the 5m default — never "no retention at all", because a zero
// that disabled the sweep would let the task map grow without bound again.
func TestSubagentRetentionMinutesDefaultApplies(t *testing.T) {
	doc := defaultEditableDocument()
	if got := doc.Agents.Defaults.SubagentRetentionMinutes; got != 5 {
		t.Fatalf("defaultEditableDocument subagent_retention_minutes = %d, want 5", got)
	}

	doc = applyDefaults(&EditableDocument{})
	if got := doc.Agents.Defaults.SubagentRetentionMinutes; got != 5 {
		t.Errorf("applyDefaults left subagent_retention_minutes = %d, want the default 5", got)
	}

	// File -> runtime: an explicit value in config.json wins.
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
