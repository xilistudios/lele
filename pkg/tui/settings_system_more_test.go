package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// settings_system_more_test.go covers the remaining branch paths in the
// system-settings handlers that need a configured provider (via a real agent
// loop's config snapshot).

func TestHandleSessionEnterCompactionModelSelector(t *testing.T) {
	// Configure a provider with a model so listProviderModels returns one.
	cfg := testModelConfig(t)
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-x"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Model: "gpt-4o"},
			},
		},
	}
	cfg.Agents.Defaults.Provider = "openai"
	m := newTestModelWithConfig(t, cfg, true)

	m.settingsSection = sysSubViewName(sysGroupSession)
	m.modalSelectedIdx = 3 // compaction model
	m.handleSessionEnter()
	if !m.settingsSelectorActive {
		t.Fatal("expected settings selector active for compaction model")
	}
	if m.settingsSelectorField != "compactionModel" {
		t.Errorf("selector field = %q, want compactionModel", m.settingsSelectorField)
	}
}

func TestHandleSystemSubEnterDispatchToolsLogsGoalUpdates(t *testing.T) {
	_ = newCompactSystemModel(t)

	tests := []struct {
		name    string
		group   int
		idx     int
		check   func(m *Model) bool
		spinsUp string // expected selector field if a selector is opened
	}{
		{"tools-toggle", sysGroupTools, 1, func(m *Model) bool { return m.cfg.Tools.Exec.EnableDenyPatterns }, ""},
		{"logs-toggle", sysGroupLogs, 0, func(m *Model) bool { return m.cfg.Logs.Enabled }, ""},
		{"language", sysGroupLanguage, 1, func(m *Model) bool { return m.cfg.Language == "en" }, ""},
		{"goal-selector", sysGroupGoal, 0, func(m *Model) bool { return m.settingsSelectorActive }, "goalJudgeMode"},
		{"updates-toggle", sysGroupUpdates, 0, func(m *Model) bool { return m.cfg.Updates.Enabled }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newCompactSystemModel(t)
			m.settingsSection = sysSubViewName(tt.group)
			m.modalSelectedIdx = tt.idx
			m.handleSystemSubEnter()
			if !tt.check(m) {
				t.Errorf("dispatch check failed for %s", tt.name)
			}
			if tt.spinsUp != "" && !m.settingsSelectorActive {
				t.Errorf("expected selector %s to open for %s", tt.spinsUp, tt.name)
			}
		})
	}
}

func TestHandleLogsEnterEditFields(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupLogs)

	// Index 1: logs path edit.
	m.modalSelectedIdx = 1
	m.handleLogsEnter()
	if m.settingsEditField != "logsPath" {
		t.Errorf("expected logsPath field, got %q", m.settingsEditField)
	}
	m.textInput.SetValue("/var/log/lele")
	m.handleSystemSettingsInput(m.textInput.Value())
	if m.cfg.Logs.Path != "/var/log/lele" {
		t.Errorf("logs path not applied: %q", m.cfg.Logs.Path)
	}

	// Index 2: logs max days edit.
	m.modalSelectedIdx = 2
	m.handleLogsEnter()
	if m.settingsEditField != "logsMaxDays" {
		t.Errorf("expected logsMaxDays field, got %q", m.settingsEditField)
	}
	m.textInput.SetValue("14")
	m.handleSystemSettingsInput(m.textInput.Value())
	if m.cfg.Logs.MaxDays != 14 {
		t.Errorf("logsMaxDays not applied: %d", m.cfg.Logs.MaxDays)
	}
}

func TestHandleLogsEnterInvalidSelectorPathStillWorks(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupLogs)
	// Index 3 opens rotation selector with small model.
	m.modalSelectedIdx = 3
	m.handleLogsEnter()
	if !m.settingsSelectorActive {
		t.Error("expected rotation selector active")
	}
	if m.settingsSelectorField != "logsRotation" {
		t.Errorf("selector field = %q, want logsRotation", m.settingsSelectorField)
	}
}

func TestHandleSessionEnterCompactionPercentAndEphemeral(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)

	// Ephemeral threshold edit.
	m.modalSelectedIdx = 1
	m.handleSessionEnter()
	if m.settingsEditField != "ephemeralThreshold" {
		t.Errorf("expected ephemeralThreshold, got %q", m.settingsEditField)
	}
	m.textInput.SetValue("100")
	m.handleSystemSettingsInput(m.textInput.Value())
	if m.cfg.Session.EphemeralThreshold != 100 {
		t.Errorf("ephemeral threshold not applied: %d", m.cfg.Session.EphemeralThreshold)
	}

	// Compaction percent edit with valid value.
	m.modalSelectedIdx = 2
	m.handleSessionEnter()
	m.textInput.SetValue("90")
	m.handleSystemSettingsInput(m.textInput.Value())
	if m.cfg.Session.CompactionThresholdPercent != 90 {
		t.Errorf("compaction percent not applied: %d", m.cfg.Session.CompactionThresholdPercent)
	}
}

func TestHandleSystemSettingsInputRotationsAndModes(t *testing.T) {
	cases := []struct {
		section string
		field   string
		value   string
		check   func(m *Model) bool
	}{
		{sysSubViewName(sysGroupLogs), "logsRotation", "weekly", func(m *Model) bool { return m.cfg.Logs.Rotation == "weekly" }},
		{sysSubViewName(sysGroupGoal), "goalJudgeMode", "subagent", func(m *Model) bool { return m.cfg.Goal.Judge.Mode == "subagent" }},
		{sysSubViewName(sysGroupGoal), "goalJudgeAgent", "coder", func(m *Model) bool { return m.cfg.Goal.Judge.Agent == "coder" }},
		{sysSubViewName(sysGroupUpdates), "updatesChannel", "stable", func(m *Model) bool { return m.cfg.Updates.Channel == "stable" }},
		{sysSubViewName(sysGroupSession), "compactionModel", "claude", func(m *Model) bool { return m.cfg.Session.CompactionModel == "claude" }},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			m := newCompactSystemModel(t)
			m.settingsSection = tc.section
			m.settingsEditField = tc.field
			m.handleSystemSettingsInput(tc.value)
			if !tc.check(m) {
				t.Errorf("%s not applied", tc.field)
			}
			if m.formError != "" {
				t.Errorf("%s: unexpected form error %q", tc.field, m.formError)
			}
		})
	}
}

func TestHandleSystemSettingsInputInvalidNumberNegative(t *testing.T) {
	cases := []struct {
		section string
		field   string
		value   string
	}{
		{sysSubViewName(sysGroupSession), "ephemeralThreshold", "-1"},
		{sysSubViewName(sysGroupTools), "execTimeout", "abc"},
		{sysSubViewName(sysGroupLogs), "logsMaxDays", "-2"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			m := newCompactSystemModel(t)
			m.settingsSection = tc.section
			m.settingsEditField = tc.field
			m.handleSystemSettingsInput(tc.value)
			if m.formError == "" {
				t.Errorf("expected error for %s=%q", tc.field, tc.value)
			}
		})
	}
}
