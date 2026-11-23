package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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
			Enabled:  false,
			Path:     "",
			MaxDays:  7,
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

func TestLoadSystemSettingsGroups(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.loadSystemSettings()
	if len(m.modalItems) != len(systemGroupTitles()) {
		t.Fatalf("expected %d system groups, got %d", len(systemGroupTitles()), len(m.modalItems))
	}
	for idx, title := range systemGroupTitles() {
		if !strings.EqualFold(m.modalItems[idx], title) {
			t.Errorf("group %d should be %q, got %q", idx, title, m.modalItems[idx])
		}
	}
}

func systemGroupTitles() []string {
	return []string{
		i18n.T("tui.settings.session"),
		i18n.T("tui.settings.tools"),
		i18n.T("tui.settings.logs"),
		i18n.T("tui.settings.language"),
		i18n.T("tui.settings.goal"),
		i18n.T("tui.settings.updates"),
		i18n.T("tui.settings.gateway"),
		i18n.T("tui.settings.keyring"),
	}
}

func TestLoadSessionSettingsReadsConfig(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.cfg.Session.Ephemeral = true
	m.cfg.Session.CompactionThresholdPercent = 80
	m.loadSessionSettings()
	if len(m.modalItems) != sessionFieldDMScope+1 {
		t.Fatalf("expected a row per session field, got %d", len(m.modalItems))
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
	m.handleSessionEnter()
	if m.cfg.Session.Ephemeral {
		t.Fatal("expected ephemeral toggled back to false")
	}

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
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Session.CompactionThresholdPercent != 80 {
		t.Fatalf("expected persisted compaction 80, got %d", reloaded.Session.CompactionThresholdPercent)
	}

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

	m.modalSelectedIdx = 1
	m.handleLanguageEnter()
	if m.cfg.Language != "en" {
		t.Fatalf("expected language en, got %q", m.cfg.Language)
	}
	if i18n.GetLanguage() != "en" {
		t.Fatalf("expected i18n locale en, got %q", i18n.GetLanguage())
	}

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.Language != "en" {
		t.Fatalf("expected persisted language en, got %q", reloaded.Language)
	}

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
	m.loadUpdatesSettings()
	if !strings.Contains(m.modalItems[0], "✓") {
		t.Errorf("expected updates enabled checkmark: %q", m.modalItems[0])
	}
}

func TestSessionPointerTogglesPersist(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)
	m.loadSessionSettings()

	m.modalSelectedIdx = sessionFieldDurableInbound
	m.handleSessionEnter()
	m.modalSelectedIdx = sessionFieldDurableOutbound
	m.handleSessionEnter()
	m.modalSelectedIdx = sessionFieldResume
	m.handleSessionEnter()

	if !m.cfg.Session.DurableInboundEnabled() || !m.cfg.Session.DurableOutboundEnabled() || !m.cfg.Session.ResumeEnabled() {
		t.Fatalf("expected all three flags enabled, got %+v", m.cfg.Session)
	}

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.Session.DurableInboundEnabled() || !reloaded.Session.ResumeEnabled() {
		t.Errorf("session flags were not persisted: %+v", reloaded.Session)
	}

	m.loadSessionSettings()
	if !strings.Contains(m.modalItems[sessionFieldResume], "✓") {
		t.Errorf("resume row should show the enabled mark: %q", m.modalItems[sessionFieldResume])
	}
}

func TestSessionEvictExcludedToggle(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupSession)
	m.cfg.Session.EvictExcludedFromMemory = true
	m.loadSessionSettings()

	m.modalSelectedIdx = sessionFieldEvictExcluded
	m.handleSessionEnter()
	if m.cfg.Session.EvictExcludedFromMemory {
		t.Fatal("evict_excluded_from_memory should be toggled off")
	}
	if !strings.Contains(m.modalItems[sessionFieldEvictExcluded], "✗") {
		t.Errorf("row should reflect the disabled state: %q", m.modalItems[sessionFieldEvictExcluded])
	}
}

func TestSessionDMScopeSelector(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.modalMode = ModalSettingsSystemEdit
	m.settingsSection = sysSubViewName(sysGroupSession)
	m.loadSessionSettings()

	m.modalSelectedIdx = sessionFieldDMScope
	m.handleSessionEnter()
	if !m.settingsSelectorActive {
		t.Fatal("dm_scope should open a selector")
	}

	m.settingsSelectorIdx = 2
	m.handleSelectorConfirm()
	if m.cfg.Session.DMScope != "per-peer" {
		t.Fatalf("dm_scope = %q, want per-peer", m.cfg.Session.DMScope)
	}
}

func TestToolsWhitelistCommandsPersist(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupTools)
	m.loadToolsSettings()

	m.modalSelectedIdx = toolsFieldWhitelistCommands
	m.handleToolsEnter()
	if m.settingsEditField != "whitelistCommands" {
		t.Fatalf("expected the whitelist input, got %q", m.settingsEditField)
	}

	m.handleSystemSettingsInput(" git status , ls ")
	if len(m.cfg.Tools.Exec.WhitelistCommands) != 2 {
		t.Fatalf("expected 2 whitelisted commands, got %v", m.cfg.Tools.Exec.WhitelistCommands)
	}
	if m.cfg.Tools.Exec.WhitelistCommands[0] != "git status" {
		t.Errorf("first command should be trimmed: %q", m.cfg.Tools.Exec.WhitelistCommands[0])
	}

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if len(reloaded.Tools.Exec.WhitelistCommands) != 2 {
		t.Errorf("whitelist was not persisted: %v", reloaded.Tools.Exec.WhitelistCommands)
	}
}

func TestToolsCronExecTimeoutEdit(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupTools)
	m.loadToolsSettings()

	m.modalSelectedIdx = toolsFieldCronExecTimeout
	m.handleToolsEnter()
	m.handleSystemSettingsInput("12")

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if reloaded.Tools.Cron.ExecTimeoutMinutes != 12 {
		t.Fatalf("cron exec timeout = %d, want 12", reloaded.Tools.Cron.ExecTimeoutMinutes)
	}
}

func TestGatewaySettingsLoadAndPersist(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.cfg.Gateway.Host = "127.0.0.1"
	m.cfg.Gateway.Port = 18789
	m.settingsSection = sysSubViewName(sysGroupGateway)
	m.loadGatewaySettings()

	if !strings.Contains(m.modalItems[gatewayFieldHost], "127.0.0.1") {
		t.Errorf("host row should show the configured host: %q", m.modalItems[gatewayFieldHost])
	}
	if !strings.Contains(m.modalItems[gatewayFieldPort], "18789") {
		t.Errorf("port row should show the configured port: %q", m.modalItems[gatewayFieldPort])
	}

	m.modalSelectedIdx = gatewayFieldPort
	m.handleGatewayEnter()
	if m.textInput.Value() != "18789" {
		t.Fatalf("port input should be prefilled, got %q", m.textInput.Value())
	}

	m.handleSystemSettingsInput("70000")
	if m.cfg.Gateway.Port != 18789 {
		t.Fatal("out-of-range port must not change the stored value")
	}
	if m.formError == "" {
		t.Fatal("expected a form error for an out-of-range port")
	}

	m.handleSystemSettingsInput("19000")
	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if reloaded.Gateway.Port != 19000 {
		t.Fatalf("persisted port = %d, want 19000", reloaded.Gateway.Port)
	}
}

func TestKeyringSettingsLoadAndToggle(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.cfg.Keyring = config.KeyringConfig{
		Enabled:       true,
		Path:          "/tmp/keyring.json",
		Backend:       "file",
		AuditLogSize:  128,
		AllowAgentSet: false,
	}
	m.settingsSection = sysSubViewName(sysGroupKeyring)
	m.loadKeyringSettings()

	if !strings.Contains(m.modalItems[keyringFieldBackend], "file") {
		t.Errorf("backend row should show the configured backend: %q", m.modalItems[keyringFieldBackend])
	}
	if !strings.Contains(m.modalItems[keyringFieldAuditLogSize], "128") {
		t.Errorf("audit log size row should show the configured value: %q", m.modalItems[keyringFieldAuditLogSize])
	}

	m.modalSelectedIdx = keyringFieldAllowAgentSet
	m.handleKeyringEnter()
	if !m.cfg.Keyring.AllowAgentSet {
		t.Fatal("allow_agent_set should be toggled on")
	}

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if !reloaded.Keyring.AllowAgentSet {
		t.Error("allow_agent_set toggle was not persisted")
	}
}

func TestKeyringBackendSelector(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.modalMode = ModalSettingsSystemEdit
	m.settingsSection = sysSubViewName(sysGroupKeyring)
	m.loadKeyringSettings()

	m.modalSelectedIdx = keyringFieldBackend
	m.handleKeyringEnter()
	if !m.settingsSelectorActive {
		t.Fatal("keyring backend should open a selector")
	}

	m.settingsSelectorIdx = 3
	m.handleSelectorConfirm()
	if m.cfg.Keyring.Backend != "keychain" {
		t.Fatalf("backend = %q, want keychain", m.cfg.Keyring.Backend)
	}
}

func TestUpdatesRepoEdit(t *testing.T) {
	m := newSystemSettingsTestModel(t)
	m.settingsSection = sysSubViewName(sysGroupUpdates)
	m.loadUpdatesSettings()

	m.modalSelectedIdx = updatesFieldRepo
	m.handleUpdatesEnter()
	m.handleSystemSettingsInput("xilistudios/lele")

	reloaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if reloaded.Updates.Repo != "xilistudios/lele" {
		t.Fatalf("updates repo = %q, want xilistudios/lele", reloaded.Updates.Repo)
	}
}

func TestReloadSystemSubViewFollowsSection(t *testing.T) {
	cases := []struct {
		group int
		want  string
	}{
		{sysGroupSession, i18n.T("tui.settings.ephemeral")},
		{sysGroupTools, i18n.T("tui.settings.execTimeout")},
		{sysGroupLogs, i18n.T("tui.settings.logsEnabled")},
		{sysGroupGoal, i18n.T("tui.settings.goalJudgeMode")},
		{sysGroupUpdates, i18n.T("tui.settings.updatesEnabled")},
		{sysGroupGateway, i18n.T("tui.settings.gatewayHost")},
		{sysGroupKeyring, i18n.T("tui.settings.keyringEnabled")},
	}

	for _, tc := range cases {
		m := newSystemSettingsTestModel(t)
		m.settingsSection = sysSubViewName(tc.group)
		m.reloadSystemSubView()
		if len(m.modalItems) == 0 {
			t.Fatalf("group %d reloaded no rows", tc.group)
		}
		if !strings.Contains(m.modalItems[0], tc.want) {
			t.Errorf("group %d first row = %q, want it to start with %q", tc.group, m.modalItems[0], tc.want)
		}
	}
}
