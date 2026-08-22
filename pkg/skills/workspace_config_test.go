package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadWorkspaceConfig_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	leleDir := filepath.Join(tmpDir, ".lele")
	if err := os.MkdirAll(leleDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfgData := `{"skills":{"enabled":["github","weather"],"disabled":["hardware"]}}`
	if err := os.WriteFile(filepath.Join(leleDir, "workspace.json"), []byte(cfgData), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadWorkspaceConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Enabled) != 2 {
		t.Errorf("expected 2 enabled skills, got %d", len(cfg.Enabled))
	}
	if len(cfg.Disabled) != 1 {
		t.Errorf("expected 1 disabled skill, got %d", len(cfg.Disabled))
	}
	if cfg.Disabled[0] != "hardware" {
		t.Errorf("expected disabled skill 'hardware', got '%s'", cfg.Disabled[0])
	}
}

func TestLoadWorkspaceConfig_Missing(t *testing.T) {
	tmpDir := t.TempDir()

	cfg, err := LoadWorkspaceConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Enabled) != 0 {
		t.Errorf("expected 0 enabled skills, got %d", len(cfg.Enabled))
	}
	if len(cfg.Disabled) != 0 {
		t.Errorf("expected 0 disabled skills, got %d", len(cfg.Disabled))
	}
}

func TestLoadWorkspaceConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	leleDir := filepath.Join(tmpDir, ".lele")
	if err := os.MkdirAll(leleDir, 0755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(leleDir, "workspace.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWorkspaceConfig(tmpDir)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestSaveWorkspaceConfig_CreateDir(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &WorkspaceSkillsConfig{
		Enabled:  []string{"github", "weather"},
		Disabled: []string{"hardware"},
	}

	if err := SaveWorkspaceConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file was created
	cfgPath := filepath.Join(tmpDir, ".lele", "workspace.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse saved config: %v", err)
	}

	skills, ok := parsed["skills"].(map[string]interface{})
	if !ok {
		t.Fatal("expected 'skills' key in config")
	}

	enabled, ok := skills["enabled"].([]interface{})
	if !ok {
		t.Fatal("expected 'enabled' array in skills")
	}
	if len(enabled) != 2 {
		t.Errorf("expected 2 enabled skills, got %d", len(enabled))
	}
}

func TestSaveWorkspaceConfig_PreservesOtherFields(t *testing.T) {
	tmpDir := t.TempDir()
	leleDir := filepath.Join(tmpDir, ".lele")
	if err := os.MkdirAll(leleDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write existing config with non-skills fields
	existingData := `{"skills":{"enabled":["old"]},"other_field":"preserved"}`
	if err := os.WriteFile(filepath.Join(leleDir, "workspace.json"), []byte(existingData), 0644); err != nil {
		t.Fatal(err)
	}

	newCfg := &WorkspaceSkillsConfig{
		Enabled:  []string{"github"},
		Disabled: []string{"hardware"},
	}

	if err := SaveWorkspaceConfig(tmpDir, newCfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify other field is preserved
	data, err := os.ReadFile(filepath.Join(leleDir, "workspace.json"))
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	otherField, ok := parsed["other_field"].(string)
	if !ok || otherField != "preserved" {
		t.Errorf("expected 'other_field' to be 'preserved', got '%v'", parsed["other_field"])
	}
}

func TestIsEnabled_Default(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{}

	// Unknown skills should default to enabled
	if !cfg.IsEnabled("unknown-skill") {
		t.Error("expected unknown skill to be enabled by default")
	}
}

func TestIsEnabled_Disabled(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{
		Disabled: []string{"hardware"},
	}

	if cfg.IsEnabled("hardware") {
		t.Error("expected 'hardware' to be disabled")
	}
	if !cfg.IsEnabled("github") {
		t.Error("expected 'github' to be enabled")
	}
}

func TestIsEnabled_ExplicitlyEnabled(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{
		Enabled: []string{"github"},
	}

	if !cfg.IsEnabled("github") {
		t.Error("expected 'github' to be enabled")
	}
}

func TestSetEnabled_FromDisabled(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{
		Enabled:  []string{"weather"},
		Disabled: []string{"hardware", "github"},
	}

	cfg.SetEnabled("github")

	if !cfg.IsEnabled("github") {
		t.Error("expected 'github' to be enabled after SetEnabled")
	}
	// Should be removed from disabled
	for _, d := range cfg.Disabled {
		if d == "github" {
			t.Error("'github' should have been removed from disabled list")
		}
	}
	// Should be in enabled list
	found := false
	for _, e := range cfg.Enabled {
		if e == "github" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'github' should be in enabled list")
	}
}

func TestSetDisabled_FromEnabled(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{
		Enabled:  []string{"github", "weather"},
		Disabled: []string{"hardware"},
	}

	cfg.SetDisabled("github")

	if cfg.IsEnabled("github") {
		t.Error("expected 'github' to be disabled after SetDisabled")
	}
	// Should be removed from enabled
	for _, e := range cfg.Enabled {
		if e == "github" {
			t.Error("'github' should have been removed from enabled list")
		}
	}
	// Should be in disabled list
	found := false
	for _, d := range cfg.Disabled {
		if d == "github" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'github' should be in disabled list")
	}
}

func TestToggle(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{}

	// Toggle unknown skill -> disabled
	enabled := cfg.Toggle("github")
	if enabled {
		t.Error("expected 'github' to be disabled after first toggle")
	}
	if cfg.IsEnabled("github") {
		t.Error("expected 'github' to be disabled")
	}

	// Toggle again -> enabled
	enabled = cfg.Toggle("github")
	if !enabled {
		t.Error("expected 'github' to be enabled after second toggle")
	}
	if !cfg.IsEnabled("github") {
		t.Error("expected 'github' to be enabled")
	}
}

func TestSetEnabled_Idempotent(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{
		Enabled: []string{"github"},
	}

	cfg.SetEnabled("github") // Already enabled

	// Should not duplicate
	count := 0
	for _, e := range cfg.Enabled {
		if e == "github" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'github' to appear once in enabled list, got %d", count)
	}
}

func TestSetDisabled_Idempotent(t *testing.T) {
	cfg := &WorkspaceSkillsConfig{
		Disabled: []string{"hardware"},
	}

	cfg.SetDisabled("hardware") // Already disabled

	// Should not duplicate
	count := 0
	for _, d := range cfg.Disabled {
		if d == "hardware" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 'hardware' to appear once in disabled list, got %d", count)
	}
}

func TestRemoveFromSlice(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		item     string
		expected []string
	}{
		{"remove existing", []string{"a", "b", "c"}, "b", []string{"a", "c"}},
		{"remove first", []string{"a", "b", "c"}, "a", []string{"b", "c"}},
		{"remove last", []string{"a", "b", "c"}, "c", []string{"a", "b"}},
		{"remove missing", []string{"a", "b"}, "c", []string{"a", "b"}},
		{"empty slice", []string{}, "a", []string{}},
		{"nil slice", nil, "a", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeFromSlice(tt.slice, tt.item)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected length %d, got %d", len(tt.expected), len(result))
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("expected [%d] = %q, got %q", i, tt.expected[i], v)
				}
			}
		})
	}
}

func TestWorkspaceConfigManager(t *testing.T) {
	tmpDir := t.TempDir()

	mgr, err := NewWorkspaceConfigManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// Default: everything enabled
	if !mgr.IsEnabled("github") {
		t.Error("expected 'github' to be enabled by default")
	}

	// Disable a skill
	if err := mgr.SetDisabled("github"); err != nil {
		t.Fatalf("failed to disable: %v", err)
	}
	if mgr.IsEnabled("github") {
		t.Error("expected 'github' to be disabled")
	}

	// Verify persistence
	mgr2, err := NewWorkspaceConfigManager(tmpDir)
	if err != nil {
		t.Fatalf("failed to create second manager: %v", err)
	}
	if mgr2.IsEnabled("github") {
		t.Error("expected 'github' to be disabled after reload")
	}

	// Toggle
	enabled, err := mgr2.Toggle("github")
	if err != nil {
		t.Fatalf("failed to toggle: %v", err)
	}
	if !enabled {
		t.Error("expected 'github' to be enabled after toggle")
	}

	// GetConfig returns a copy
	cfg := mgr2.GetConfig()
	if len(cfg.Disabled) != 0 {
		t.Error("expected empty disabled list after toggle")
	}
}

// ---- Additional workspace config coverage for error paths & SetEnabled ----

func TestWorkspaceConfigManager_SetEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewWorkspaceConfigManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetEnabled("github"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg := mgr.GetConfig()
	found := false
	for _, e := range cfg.Enabled {
		if e == "github" {
			found = true
		}
	}
	if !found {
		t.Error("expected 'github' in enabled list after SetEnabled")
	}
	if !mgr.IsEnabled("github") {
		t.Error("expected 'github' to be enabled")
	}
}

func TestLoadWorkspaceConfig_ReadError(t *testing.T) {
	// Make .lele a file so reading workspace.json fails with a non-not-exist error.
	tmpDir := t.TempDir()
	lelePath := filepath.Join(tmpDir, ".lele")
	if err := os.WriteFile(lelePath, []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadWorkspaceConfig(tmpDir)
	if err == nil {
		t.Error("expected error when .lele is a file")
	} else if !contains(err.Error(), "failed to read workspace config") {
		t.Errorf("expected read error, got %v", err)
	}
}

func TestLoadWorkspaceConfig_NoSkillsKey(t *testing.T) {
	tmpDir := t.TempDir()
	leleDir := filepath.Join(tmpDir, ".lele")
	if err := os.MkdirAll(leleDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Config present but no "skills" key.
	if err := os.WriteFile(filepath.Join(leleDir, "workspace.json"), []byte(`{"something_else": 1}`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWorkspaceConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Enabled) != 0 || len(cfg.Disabled) != 0 {
		t.Error("expected empty config when no skills key present")
	}
}

func TestSaveWorkspaceConfig_ErrorPaths(t *testing.T) {
	// .lele cannot be created because a file occupies the workspace path resolution.
	// Instead, verify marshal of empty config works and no error when dir exists.
	tmpDir := t.TempDir()
	cfg := &WorkspaceSkillsConfig{}
	if err := SaveWorkspaceConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invalid existing JSON for preservation is best-effort (no error).
	leleDir := filepath.Join(tmpDir, ".lele")
	if err := os.MkdirAll(leleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leleDir, "workspace.json"), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2 := &WorkspaceSkillsConfig{Enabled: []string{"github"}}
	if err := SaveWorkspaceConfig(tmpDir, cfg2); err != nil {
		t.Fatalf("unexpected error with invalid existing JSON: %v", err)
	}
}

func TestWorkspaceConfigManager_SetDisabled_Persists(t *testing.T) {
	tmpDir := t.TempDir()
	mgr, err := NewWorkspaceConfigManager(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetDisabled("hardware"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr.IsEnabled("hardware") {
		t.Error("expected 'hardware' disabled")
	}
}

func TestSaveWorkspaceConfig_MkdirError(t *testing.T) {
	tmpDir := t.TempDir()
	// A file occupies the .lele path -> MkdirAll fails.
	if err := os.WriteFile(filepath.Join(tmpDir, ".lele"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := &WorkspaceSkillsConfig{Enabled: []string{"github"}}
	err := SaveWorkspaceConfig(tmpDir, cfg)
	if err == nil {
		t.Error("expected error when .lele is a file")
	} else if !contains(err.Error(), "failed to create .lele directory") {
		t.Errorf("expected create-dir error, got %v", err)
	}
}
