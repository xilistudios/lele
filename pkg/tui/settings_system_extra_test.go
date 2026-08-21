package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
)


// exercising the pure system-settings handlers that only touch cfg/textInput.
func newCompactSystemModel(t *testing.T) *Model {
	t.Helper()
	ti := textinput.New()
	ti.Focus()
	return &Model{
		cfg: &config.Config{
			Session: config.SessionConfig{},
			Tools:   config.ToolsConfig{Exec: config.ExecConfig{}},
			Logs:    config.LogsConfig{},
			Goal:    config.GoalConfig{Judge: config.GoalJudgeConfig{}},
			Updates: config.UpdatesConfig{},
			Providers: &config.ProvidersConfig{},
		},
		textInput: ti,
	}
}

func TestValueOr(t *testing.T) {
	if got := valueOr("", "fb"); got != "fb" {
		t.Errorf("valueOr empty = %q, want fb", got)
	}
	if got := valueOr("x", "fb"); got != "x" {
		t.Errorf("valueOr set = %q, want x", got)
	}
}

func TestCheckMark(t *testing.T) {
	if got := checkMark(true); got != "✓" {
		t.Errorf("checkMark true = %q", got)
	}
	if got := checkMark(false); got != "" {
		t.Errorf("checkMark false = %q", got)
	}
}

func TestSysGroupFromSection(t *testing.T) {
	if got := sysGroupFromSection("sys_0"); got != 0 {
		t.Errorf("sys_0 = %d, want 0", got)
	}
	if got := sysGroupFromSection("sys_x"); got != -1 {
		t.Errorf("sys_x = %d, want -1", got)
	}
	if got := sysGroupFromSection(""); got != -1 {
		t.Errorf("empty = %d, want -1", got)
	}
}

func TestLoadToolsSettingsToggle(t *testing.T) {
	m := newCompactSystemModel(t)
	m.cfg.Tools.Exec.EnableDenyPatterns = true
	m.cfg.Tools.Exec.CustomDenyPatterns = []string{"rm", "git push"}
	m.cfg.Tools.Exec.TimeoutSeconds = 45
	m.loadToolsSettings()
	if len(m.modalItems) != 3 {
		t.Fatalf("expected 3 tool items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "45s") {
		t.Errorf("timeout item: %q", m.modalItems[0])
	}
	if !strings.Contains(m.modalItems[1], "✓") {
		t.Errorf("deny toggle should be ✓: %q", m.modalItems[1])
	}
	if !strings.Contains(m.modalItems[2], "rm, git push") {
		t.Errorf("custom patterns item: %q", m.modalItems[2])
	}
}

func TestLoadLogsSettings(t *testing.T) {
	m := newCompactSystemModel(t)
	m.cfg.Logs.Enabled = true
	m.cfg.Logs.Path = "/tmp/logs"
	m.cfg.Logs.MaxDays = 14
	m.loadLogsSettings()
	if len(m.modalItems) != 4 {
		t.Fatalf("expected 4 log items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "✓") {
		t.Errorf("logs enabled: %q", m.modalItems[0])
	}
	if !strings.Contains(m.modalItems[1], "/tmp/logs") {
		t.Errorf("logs path: %q", m.modalItems[1])
	}
}

func TestLoadLanguageSettingsMarks(t *testing.T) {
	m := newCompactSystemModel(t)
	m.cfg.Language = "en"
	m.loadLanguageSettings()
	if len(m.modalItems) != 3 {
		t.Fatalf("expected 3 language items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[1], "✓") {
		t.Errorf("english should be marked: %q", m.modalItems[1])
	}
}

func TestLoadGoalAndUpdatesSettings(t *testing.T) {
	m := newCompactSystemModel(t)
	m.cfg.Goal.Judge.Mode = "subagent"
	m.loadGoalSettings()
	if len(m.modalItems) != 2 {
		t.Fatalf("expected 2 goal items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "subagent") {
		t.Errorf("goal judge mode: %q", m.modalItems[0])
	}

	m.cfg.Updates.Enabled = true
	m.loadUpdatesSettings()
	if len(m.modalItems) != 2 {
		t.Fatalf("expected 2 updates items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "✓") {
		t.Errorf("updates enabled: %q", m.modalItems[0])
	}
}

func TestHandleSystemSubEnterDispatch(t *testing.T) {
	m := newCompactSystemModel(t)

	// Unknown section returns nil cmd.
	m.settingsSection = "nope"
	if cmd := m.handleSystemSubEnter(); cmd != nil {
		t.Error("expected nil cmd for unknown section")
	}

	// Session group -> handleSessionEnter (index 0 toggles ephemeral).
	m.settingsSection = sysSubViewName(sysGroupSession)
	m.modalSelectedIdx = 0
	m.handleSystemSubEnter()
	if !m.cfg.Session.Ephemeral {
		t.Error("expected ephemeral toggled via dispatch")
	}
}

func TestHandleSessionEnterOtherFields(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)

	// Compaction percent opens edit field.
	m.modalSelectedIdx = 2
	m.handleSessionEnter()
	if m.settingsEditField != "compactionPercent" {
		t.Errorf("expected compactionPercent edit, got %q", m.settingsEditField)
	}
	if m.textInput.Value() != "0" {
		t.Errorf("expected default 0 compaction, got %q", m.textInput.Value())
	}

	// Unknown index -> no-op.
	m.modalSelectedIdx = 99
	m.handleSessionEnter()
	if m.settingsEditField != "compactionPercent" {
		t.Error("setting field should be unchanged for out of range index")
	}
}

func TestHandleToolsEnter(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupTools)

	// Index 0: exec timeout field.
	m.modalSelectedIdx = 0
	m.handleToolsEnter()
	if m.settingsEditField != "execTimeout" {
		t.Errorf("expected execTimeout, got %q", m.settingsEditField)
	}

	// Index 1: deny patterns toggle.
	m.modalSelectedIdx = 1
	m.handleToolsEnter()
	if !m.cfg.Tools.Exec.EnableDenyPatterns {
		t.Error("expected deny patterns toggled")
	}

	// Index 2: custom deny patterns field.
	m.modalSelectedIdx = 2
	m.handleToolsEnter()
	if m.settingsEditField != "customDenyPatterns" {
		t.Errorf("expected customDenyPatterns, got %q", m.settingsEditField)
	}
}

func TestHandleLogsEnterSelectsRotation(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupLogs)

	// Index 0 toggles enabled.
	m.modalSelectedIdx = 0
	m.handleLogsEnter()
	if !m.cfg.Logs.Enabled {
		t.Error("expected logs enabled toggled")
	}

	// Index 3 opens the rotation selector.
	m.modalSelectedIdx = 3
	m.handleLogsEnter()
	if !m.settingsSelectorActive {
		t.Errorf("expected selector active for logs rotation")
	}
}

func TestHandleGoalEnterSelectors(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupGoal)

	// Index 0: judge mode selector.
	m.modalSelectedIdx = 0
	m.handleGoalEnter()
	if !m.settingsSelectorActive {
		t.Error("expected selector for judge mode")
	}

	// Index 1: judge agent selector.
	m.modalSelectedIdx = 1
	m.handleGoalEnter()
	if !m.settingsSelectorActive {
		t.Error("expected selector for judge agent")
	}
}

func TestHandleUpdatesEnterChannel(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupUpdates)

	m.modalSelectedIdx = 1
	m.handleUpdatesEnter()
	if !m.settingsSelectorActive {
		t.Error("expected selector for updates channel")
	}
}

func TestHandleSystemSettingsInputAllFields(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)

	tests := []struct {
		field string
		value string
		check func() bool
	}{
		{"ephemeralThreshold", "120", func() bool { return m.cfg.Session.EphemeralThreshold == 120 }},
		{"compactionPercent", "85", func() bool { return m.cfg.Session.CompactionThresholdPercent == 85 }},
		{"compactionModel", "foo", func() bool { return m.cfg.Session.CompactionModel == "foo" }},
	}
	for _, tc := range tests {
		m.settingsEditField = tc.field
		m.handleSystemSettingsInput(tc.value)
		if !tc.check() {
			t.Errorf("field %s: value not applied", tc.field)
		}
		if m.formError != "" {
			t.Errorf("field %s: unexpected error %q", tc.field, m.formError)
		}
	}

	// Tools field edits.
	m.settingsSection = sysSubViewName(sysGroupTools)
	m.settingsEditField = "execTimeout"
	m.handleSystemSettingsInput("30")
	if m.cfg.Tools.Exec.TimeoutSeconds != 30 {
		t.Errorf("execTimeout not applied: %d", m.cfg.Tools.Exec.TimeoutSeconds)
	}

	// Logs fields.
	m.settingsSection = sysSubViewName(sysGroupLogs)
	m.settingsEditField = "logsPath"
	m.handleSystemSettingsInput("/var/log")
	if m.cfg.Logs.Path != "/var/log" {
		t.Errorf("logsPath not applied: %q", m.cfg.Logs.Path)
	}
	m.settingsEditField = "logsMaxDays"
	m.handleSystemSettingsInput("30")
	if m.cfg.Logs.MaxDays != 30 {
		t.Errorf("logsMaxDays not applied: %d", m.cfg.Logs.MaxDays)
	}

	// Goal fields.
	m.settingsSection = sysSubViewName(sysGroupGoal)
	m.settingsEditField = "goalJudgeMode"
	m.handleSystemSettingsInput("inline")
	if m.cfg.Goal.Judge.Mode != "inline" {
		t.Errorf("goalJudgeMode not applied: %q", m.cfg.Goal.Judge.Mode)
	}
	m.settingsEditField = "goalJudgeAgent"
	m.handleSystemSettingsInput("a1")
	if m.cfg.Goal.Judge.Agent != "a1" {
		t.Errorf("goalJudgeAgent not applied: %q", m.cfg.Goal.Judge.Agent)
	}

	// Updates channel.
	m.settingsSection = sysSubViewName(sysGroupUpdates)
	m.settingsEditField = "updatesChannel"
	m.handleSystemSettingsInput("stable")
	if m.cfg.Updates.Channel != "stable" {
		t.Errorf("updatesChannel not applied: %q", m.cfg.Updates.Channel)
	}
}

func TestHandleSystemSettingsInputInvalidNumber(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)

	m.settingsEditField = "ephemeralThreshold"
	m.handleSystemSettingsInput("abc")
	if m.formError == "" {
		t.Error("expected error for invalid number")
	}
	m.formError = ""

	m.settingsEditField = "ephemeralThreshold"
	m.handleSystemSettingsInput("-5")
	if m.formError == "" {
		t.Error("expected error for negative number")
	}
}

func TestHandleSystemSettingsInputInvalidPercent(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)
	m.settingsEditField = "compactionPercent"
	m.handleSystemSettingsInput("150")
	if m.formError == "" {
		t.Error("expected error for out of range percent")
	}
}

func TestReloadSystemSubView(t *testing.T) {
	m := newCompactSystemModel(t)
	for _, g := range []int{sysGroupSession, sysGroupTools, sysGroupLogs, sysGroupLanguage, sysGroupGoal, sysGroupUpdates} {
		m.settingsSection = sysSubViewName(g)
		m.reloadSystemSubView()
		if len(m.modalItems) == 0 {
			t.Errorf("group %d: expected reloaded items", g)
		}
	}
	// Unknown section does nothing and does not panic.
	m.settingsSection = "unknown"
	m.reloadSystemSubView()
}

func TestSetLanguageMarkingAfterHandle(t *testing.T) {
	m := newCompactSystemModel(t)
	m.settingsSection = sysSubViewName(sysGroupLanguage)
	m.loadLanguageSettings()
	if len(m.modalItems) != 3 {
		t.Fatalf("expected 3 language items")
	}
}

