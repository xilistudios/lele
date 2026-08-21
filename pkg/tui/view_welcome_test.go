package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// welcomeViewModel returns a TUI Model configured to render the welcome home
// screen (no current session, onboarding switched off, deterministic color and
// terminal dimensions).
func welcomeViewModel(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.showWelcome = true
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	return m
}

// TestWelcomeView_RendersBase verifies the welcome screen renders the logo,
// the model/agent selector line and the mode tabs when groups are disabled.
func TestWelcomeView_RendersBase(t *testing.T) {
	m := welcomeViewModel(t)
	out := m.View()

	for _, want := range []string{
		"______", // logo underline border
		i18n.T("tui.model"),
		i18n.T("tui.agent"),
		i18n.T("tui.modeChat"),
		i18n.T("tui.modeAgent"),
		i18n.T("tui.typeMessage"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("welcome view missing %q", want)
		}
	}
	if strings.Contains(out, i18n.T("tui.modeGroup")) {
		t.Errorf("welcome view should not show group tab when groups disabled")
	}
}

// TestWelcomeView_GroupsEnabledChatMode verifies the group tab appears and the
// group profile selector renders when the current mode is Group.
func TestWelcomeView_GroupsEnabledGroupMode(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Groups.Enabled = true
	cfg.Groups.List = []config.GroupProfile{
		{ID: "moa1", Strategy: "moa", Participants: []string{"primary", "critic", "editor"}},
	}
	m := newTestModelWithConfig(t, cfg, true)
	m.showWelcome = true
	m.currentMode = ModeGroup
	m.groupProfileIdx = 0
	m.width = 120
	m.height = 40
	forceTrueColor(t)

	out := m.View()

	for _, want := range []string{
		i18n.T("tui.modeChat"),
		i18n.T("tui.modeAgent"),
		i18n.T("tui.modeGroup"),
		i18n.T("tui.groupSelectProfile"),
		"moa1",
		"moa",
		", 3 agents)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("group welcome view missing %q", want)
		}
	}
}

// TestWelcomeView_GroupModeNoProfiles verifies the "no profiles" message when
// groups are enabled but the profile list is empty.
func TestWelcomeView_GroupModeNoProfiles(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Groups.Enabled = true
	cfg.Groups.List = nil
	m := newTestModelWithConfig(t, cfg, true)
	m.showWelcome = true
	m.currentMode = ModeGroup
	m.width = 120
	m.height = 40
	forceTrueColor(t)

	out := m.View()
	if !strings.Contains(out, i18n.T("tui.noGroupProfiles")) {
		t.Errorf("expected no-group-profiles message, got:\n%s", out)
	}
}

// TestWelcomeView_AutocompleteActive verifies the autocomplete dropdown is
// rendered while typing a command prefix.
func TestWelcomeView_AutocompleteActive(t *testing.T) {
	m := welcomeViewModel(t)
	m.showAutocomplete = true
	m.autocompleteItems = []commandInfo{
		{name: "/sessions", description: "Switch session"},
		{name: "/new", description: "New session"},
	}
	m.autocompleteIdx = 1

	out := m.View()
	for _, want := range []string{"/sessions", "/new", "Switch session"} {
		if !strings.Contains(out, want) {
			t.Errorf("autocomplete view missing %q", want)
		}
	}
}

// TestWelcomeView_PendingAgentAndModel verifies that pendingAgent/pendingModel
// override the default agent/model in the selector line.
func TestWelcomeView_PendingAgentAndModel(t *testing.T) {
	m := welcomeViewModel(t)
	m.pendingAgent = "critic"
	m.pendingModel = "mixtral-8x7b"

	out := m.View()
	for _, want := range []string{"critic", "mixtral-8x7b"} {
		if !strings.Contains(out, want) {
			t.Errorf("pending agent/model not rendered, missing %q", want)
		}
	}
}

// TestWelcomeView_InitializingWhenNoSize verifies the initializing message is
// returned when the model has no terminal dimensions yet.
func TestWelcomeView_InitializingWhenNoSize(t *testing.T) {
	m := welcomeViewModel(t)
	m.width = 0
	m.height = 0
	if got := m.View(); !strings.Contains(got, i18n.T("tui.initializing")) {
		t.Errorf("expected initializing message, got:\n%s", got)
	}
}// ── Welcome-screen modal overlay branches ───────────────────────────────

// TestWelcomeView_ModalOverlaySettingsAgents drives View() when a settings
// modal is open on the welcome screen, exercising the modal title switch and
// the settings modal rendering branches.
func TestWelcomeView_ModalOverlaySettingsAgents(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsAgents
	m.loadAgentsSettings()
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.settings.agents")) {
		t.Errorf("expected agents settings title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalOverlaySettingsSystem covers the system title branch.
func TestWelcomeView_ModalOverlaySettingsSystem(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsSystem
	m.loadSystemSettings()
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.settings.system")) {
		t.Errorf("expected system settings title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalOverlaySettingsTUI covers the interface title branch.
func TestWelcomeView_ModalOverlaySettingsTUI(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsTUI
	m.loadTUISettings()
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.settings.interface")) {
		t.Errorf("expected interface settings title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalOverlaySkillPicker covers the skill picker branch.
func TestWelcomeView_ModalOverlaySkillPicker(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSkillPicker
	m.skillsScanResults = []channels.ScannedSkill{{Name: "alpha", Description: "desc"}}
	m.skillsSelectedMap = map[int]bool{0: true}
	m.skillsScanRepo = "owner/repo"
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.selectSkills")) {
		t.Errorf("expected skill picker title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalOverlaySkillInstall covers the skill-install form modal.
func TestWelcomeView_ModalOverlaySkillInstall(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSkillInstall
	m.formStepIndex = 0
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.installSkill")) {
		t.Errorf("expected skill install title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalOverlaySecrets covers the secrets list rendering.
func TestWelcomeView_ModalOverlaySecrets(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSecrets
	m.secretsModalKeys = []string{"k1"}
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty secrets view")
	}
}

// TestWelcomeView_ModalOverlayAgentEditSelector covers the agent-edit modal with
// an active inline selector (settings selector rendering branch).
func TestWelcomeView_ModalOverlayAgentEditSelector(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = "coder"
	m.settingsSelectorActive = true
	m.settingsSelectorItems = []string{"a", "b"}
	m.settingsSelectorValues = []string{"a", "b"}
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty agent-edit with selector")
	}
}

// TestWelcomeView_ModalOverlayAgentEditEditField covers the agent-edit modal
// with an active inline text edit (agent edit input rendering).
func TestWelcomeView_ModalOverlayAgentEditEditField(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = "coder"
	m.settingsEditField = "agentName"
	m.textInput.SetValue("value")
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty agent-edit with edit field")
	}
}

// TestWelcomeView_ModalOverlaySystemEditSelector covers the system-edit modal
// with an active settings selector.
func TestWelcomeView_ModalOverlaySystemEditSelector(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsSystemEdit
	m.settingsSelectorActive = true
	m.settingsSelectorItems = []string{"a", "b"}
	m.settingsSelectorValues = []string{"a", "b"}
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty system-edit with selector")
	}
}

// TestWelcomeView_ModalOverlaySystemEditEditField covers the system-edit modal
// with an active inline text edit (system settings edit rendering).
func TestWelcomeView_ModalOverlaySystemEditEditField(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSettingsSystemEdit
	m.settingsEditField = "maxTokens"
	m.textInput.SetValue("4096")
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.settings.system")) {
		t.Errorf("expected system title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalOverlayBasicModals drives the generic modal title switch
// for several simple modal modes rendered on the welcome screen.
func TestWelcomeView_ModalOverlayBasicModals(t *testing.T) {
	modes := []struct {
		mode  modalType
		title string
	}{
		{ModalAgent, i18n.T("tui.selectAgent")},
		{ModalModel, i18n.T("tui.selectModel")},
		{ModalSessions, i18n.T("tui.selectChat")},
		{ModalThink, i18n.T("tui.selectThinkLevel")},
		{ModalLang, i18n.T("tui.selectLanguage")},
		{ModalProviders, i18n.T("tui.selectProvider")},
		{ModalSkills, i18n.T("tui.skills")},
	}
	for _, tt := range modes {
		m := welcomeViewModel(t)
		m.modalMode = tt.mode
		m.modalItems = []string{"item one", "item two"}
		out := m.View()
		if !strings.Contains(out, tt.title) {
			t.Errorf("modal %d expected title %q, got:\n%s", tt.mode, tt.title, out)
		}
	}
}

// TestWelcomeView_ModalOverlaySubagentsEmpty covers the subagents modal title on
// the welcome screen with the no-subagents message.
func TestWelcomeView_ModalOverlaySubagentsEmpty(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSubagents
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.selectSubagent")) {
		t.Errorf("expected subagents title, got:\n%s", out)
	}
}// ── Additional welcome-screen branch coverage ──────────────────────────

// TestWelcomeView_CurrentKeySet uses an active current key so the welcome
// screen reads session agent/model (instead of default/pending).
func TestWelcomeView_CurrentKeySet(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:wk-cur"
	m.sessionMgr.GetOrCreate(m.currentKey)
	_ = m.sessionMgr.SetMode(m.currentKey, "agent")
	m.currentMode = ModeAgent
	m.showWelcome = true
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	out := m.View()
	if out == "" {
		t.Fatal("expected non-empty welcome view with current key")
	}
}

// TestWelcomeView_ModeChatTabGroupsEnabled renders the ModeChat tab highlight
// when groups are enabled.
func TestWelcomeView_ModeChatTabGroupsEnabled(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Groups.Enabled = true
	cfg.Groups.List = []config.GroupProfile{{ID: "g", Strategy: "moa", Participants: []string{"a"}}}
	m := newTestModelWithConfig(t, cfg, true)
	m.showWelcome = true
	m.currentMode = ModeChat
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.modeGroup")) {
		t.Errorf("expected group tab present, got:\n%s", out)
	}
}

// TestWelcomeView_ModeChatTabGroupsDisabled renders ModeChat tab when groups
// are disabled.
func TestWelcomeView_ModeChatTabGroupsDisabled(t *testing.T) {
	m := welcomeViewModel(t)
	m.currentMode = ModeChat
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.modeAgent")) {
		t.Errorf("expected agent tab present, got:\n%s", out)
	}
}

// TestWelcomeView_ModalAddProviderOnWelcome renders the add-provider form
// modal over the welcome screen (form modal branch on non-session screen).
func TestWelcomeView_ModalAddProviderOnWelcome(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalAddProvider
	m.formStepIndex = 0
	m.formValues = make([]string, 10)
	m.formValues[0] = "openai"
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.addProvider")) {
		t.Errorf("expected add-provider title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalSkillInstallFormOnWelcome renders skill-install form on
// welcome screen with an active repo input value.
func TestWelcomeView_ModalSkillInstallFormOnWelcome(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalSkillInstall
	m.formStepIndex = 0
	m.textInput.SetValue("owner/repo")
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.installSkill")) {
		t.Errorf("expected install-skill title, got:\n%s", out)
	}
}

// TestWelcomeView_ModalBgExecsOnWelcome renders background-execs modal overlay
// on the welcome screen.
func TestWelcomeView_ModalBgExecsOnWelcome(t *testing.T) {
	m := welcomeViewModel(t)
	m.modalMode = ModalBackgroundExecs
	m.modalItems = []string{"p1  echo hello  running"}
	out := m.View()
	if !strings.Contains(out, i18n.T("tui.backgroundProcesses")) {
		t.Errorf("expected bg execs title, got:\n%s", out)
	}

	// bgExecViewMode path on welcome early-returns full output.
	m2 := welcomeViewModel(t)
	m2.modalMode = ModalBackgroundExecs
	m2.bgExecViewMode = true
	out2 := m2.View()
	if out2 == "" {
		t.Fatal("expected non-empty bg exec output on welcome")
	}
}