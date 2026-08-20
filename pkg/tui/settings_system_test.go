package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// newSystemSettingsTestModel builds a Model owned by a temp config dir so that
// load/persist through saveConfigToDisk work end to end.
func newSystemSettingsTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: t.TempDir(),
				Model:     "test-model",
			},
		},
		Providers: &config.ProvidersConfig{},
		Session: config.SessionConfig{
			Ephemeral:                  false,
			EphemeralThreshold:         560,
			CompactionThresholdPercent: 75,
			CompactionModel:            "",
		},
		Tools: config.ToolsConfig{
			Exec: config.ExecConfig{
				TimeoutSeconds:     60,
				EnableDenyPatterns: false,
			},
		},
		Logs: config.LogsConfig{
			Enabled: false,
			Path:    "",
			MaxDays: 7,
			Rotation: "",
		},
		Goal: config.GoalConfig{
			Judge: config.GoalJudgeConfig{Mode: "", Agent: ""},
		},
		Updates: config.UpdatesConfig{
			Enabled: false,
			Channel: "",
		},
		Language: "es",
	}
	if err := config.SaveConfig(config.DefaultConfigPath(), cfg); err != nil {
		t.Fatalf("saving initial config: %v", err)
	}
	ti := textinput.New()
	ti.Focus()
	return &Model{
		cfg:               cfg,
		modalItems:        nil,
		modalSelectedIdx:  0,
		settingsEditField: "",
		textInput:         ti,
	}
}

func TestLoadSystemSettingsSixGroups(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.loadSystemSettings()
	if len(m.modalItems) != 6 {
		t.Fatalf("expected 6 system groups, got %d", len(m.modalItems))
	}
	// Order: Session, Tools, Logs, Language, Goal, Updates.
	if !strings.EqualFold(m.modalItems[0], i18n.T("tui.settings.session")) {
		t.Errorf("group 0 should be Session: %q", m.modalItems[0])
	}
	if !strings.EqualFold(m.modalItems[5], i18n.T("tui.settings.updates")) {
		t.Errorf("group 5 should be Updates: %q", m.modalItems[5])
	}
}

func TestLoadSessionSettingsReadsConfig(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.cfg.Session.Ephemeral = true
	m.cfg.Session.CompactionThresholdPercent = 80
	m.loadSessionSettings()
	if len(m.modalItems) != 4 {
		t.Fatalf("expected 4 session items, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[0], "✓") {
		t.Errorf("ephemeral should show checkmark: %q", m.modalItems[0])
	}
	if !strings.Contains(m.modalItems[2], "80%") {
		t.Errorf("compaction percent should show 80%%: %q", m.modalItems[2])
	}
}

func TestHandleSessionEnterTogglesEphemeral(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.modalSelectedIdx = 0
	m.handleSessionEnter()
	if !m.cfg.Session.Ephemeral {
		t.Fatal("expected ephemeral toggled to true")
	}
	// Toggling again turns it off.
	m.handleSessionEnter()
	if m.cfg.Session.Ephemeral {
		t.Fatal("expected ephemeral toggled back to false")
	}

	// Entering an int field switches to edit mode.
	m.modalSelectedIdx = 1
	m.handleSessionEnter()
	if m.settingsEditField != "ephemeralThreshold" {
		t.Fatalf("expected edit field ephemeralThreshold, got %q", m.settingsEditField)
	}
	if m.textInput.Value() != "560" {
		t.Fatalf("expected text input 560, got %q", m.textInput.Value())
	}
}

func TestHandleSystemSettingsInputValidatesAndPersists(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)

	// Valid range for compaction percent.
	m.settingsEditField = "compactionPercent"
	m.handleSystemSettingsInput("80")
	if m.cfg.Session.CompactionThresholdPercent != 80 {
		t.Fatalf("expected compaction 80, got %d", m.cfg.Session.CompactionThresholdPercent)
	}
	if m.formError != "" {
		t.Fatalf("expected no error, got %q", m.formError)
	}
	if m.settingsEditField != "" {
		t.Fatalf("expected edit field cleared, got %q", m.settingsEditField)
	}
	// Reload config from disk to confirm persistence.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Session.CompactionThresholdPercent != 80 {
		t.Fatalf("expected persisted compaction 80, got %d", reloaded.Session.CompactionThresholdPercent)
	}

	// Out-of-range percent is rejected.
	m.settingsEditField = "compactionPercent"
	m.handleSystemSettingsInput("150")
	if m.cfg.Session.CompactionThresholdPercent != 80 {
		t.Fatal("invalid percent must not change the stored value")
	}
	if m.settingsEditField == "" {
		t.Fatal("edit field should stay active on invalid input")
	}
	if m.formError == "" {
		t.Fatal("expected a form error for out-of-range percent")
	}
}

func TestHandleSystemSettingsInputCustomDenyPatterns(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupTools)
	m.settingsEditField = "customDenyPatterns"
	m.handleSystemSettingsInput("git push,  rm -rf /")
	if len(m.cfg.Tools.Exec.CustomDenyPatterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(m.cfg.Tools.Exec.CustomDenyPatterns))
	}
	if m.cfg.Tools.Exec.CustomDenyPatterns[0] != "git push" {
		t.Errorf("pattern 0 should trim spaces: %q", m.cfg.Tools.Exec.CustomDenyPatterns[0])
	}
	if m.cfg.Tools.Exec.CustomDenyPatterns[1] != "rm -rf /" {
		t.Errorf("pattern 1 should trim spaces: %q", m.cfg.Tools.Exec.CustomDenyPatterns[1])
	}

	// Empty clears the patterns.
	m.settingsEditField = "customDenyPatterns"
	m.handleSystemSettingsInput("")
	if m.cfg.Tools.Exec.CustomDenyPatterns != nil {
		t.Fatalf("expected nil patterns after clearing, got %v", m.cfg.Tools.Exec.CustomDenyPatterns)
	}
}

func TestHandleLanguageEnterPersistsToConfig(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.cfg.Language = "es"
	m.settingsSection = sysSubViewName(sysGroupLanguage)
	m.loadLanguageSettings()

	// Select "English (en)" — index 1.
	m.modalSelectedIdx = 1
	m.handleLanguageEnter()
	if m.cfg.Language != "en" {
		t.Fatalf("expected language en, got %q", m.cfg.Language)
	}
	if i18n.GetLanguage() != "en" {
		t.Fatalf("expected i18n locale en, got %q", i18n.GetLanguage())
	}

	// Confirm persistence on disk.
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Language != "en" {
		t.Fatalf("expected persisted language en, got %q", reloaded.Language)
	}

	// The sub-view should be reloaded with the checkmark on English.
	m.loadLanguageSettings()
	if !strings.Contains(m.modalItems[1], "✓") {
		t.Errorf("expected English to be marked after selection: %q", m.modalItems[1])
	}
}

func TestSysSubViewNameRoundTrip(t *testing.T) {
	if got := sysSubViewName(sysGroupSession); got != "sys_0" {
		t.Fatalf("expected sys_0, got %q", got)
	}
	if got := sysGroupFromSection("sys_5"); got != sysGroupUpdates {
		t.Fatalf("expected %d, got %d", sysGroupUpdates, got)
	}
	if got := sysGroupFromSection("notasys"); got != -1 {
		t.Fatalf("expected -1 for invalid section, got %d", got)
	}
}

func TestHandleUpdatesToggleWhenNotEditing(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupUpdates)
	m.modalSelectedIdx = 0
	cmd := m.handleUpdatesEnter()
	if !m.cfg.Updates.Enabled {
		t.Fatal("expected updates enabled toggled to true")
	}
	if cmd != nil {
		t.Fatal("expected nil cmd for pure toggle")
	}
	// Reparse modal items to reflect the toggle.
	m.loadUpdatesSettings()
	if !strings.Contains(m.modalItems[0], "✓") {
		t.Errorf("expected updates enabled checkmark: %q", m.modalItems[0])
	}
}