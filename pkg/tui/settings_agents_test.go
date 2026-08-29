package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
)

// newAgentsTestModel builds a Model owned by a temp config dir (so mutations
// can persist via saveConfigToDisk) with two sample agents and no agent loop
// (applyAgentReload is nil-guarded).
func newAgentsTestModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	temp0 := 0.7
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         t.TempDir(),
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 25,
				MaxReadLines:      500,
			},
			List: []config.AgentConfig{
				{ID: "coder", Name: "Coder", Default: true, Workspace: "/tmp/cw", Temperature: &temp0},
				{ID: "writer", Name: "Writer"},
			},
		},
		Providers: &config.ProvidersConfig{},
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
		modalScrollOffset: 0,
		settingsAgentID:   "",
		settingsEditField: "",
		textInput:         ti,
	}
}

func TestLoadAgentsSettings(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentsSettings()

	// Defaults + 2 agents + add action = 4 items
	if len(m.modalItems) != 4 {
		t.Fatalf("expected 4 items, got %d", len(m.modalItems))
	}
	if len(m.settingsAgentKeys) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(m.settingsAgentKeys))
	}
	// First is defaults
	if m.settingsAgentKeys[0] != "" {
		t.Errorf("first key should be empty (defaults), got %q", m.settingsAgentKeys[0])
	}
	// Agents follow
	if m.settingsAgentKeys[1] != "coder" || m.settingsAgentKeys[2] != "writer" {
		t.Errorf("agent keys mismatch: %v", m.settingsAgentKeys)
	}
	// Add action last
	if m.settingsAgentKeys[3] != settingsAgentAddKey {
		t.Errorf("last key should be add action, got %q", m.settingsAgentKeys[3])
	}
	// The default agent is marked with ★ in its label
	if !strings.Contains(m.modalItems[1], "★") {
		t.Errorf("default agent should be starred in label: %q", m.modalItems[1])
	}
}

func TestHandleAgentsEnterNavigatesToDetail(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentsSettings()

	m.modalSelectedIdx = 1 // select "coder"
	m.handleAgentsEnter()
	if m.modalMode != ModalSettingsAgentEdit {
		t.Fatalf("expected navigation to AgentEdit modal, got %v", m.modalMode)
	}
	if m.settingsAgentID != "coder" {
		t.Fatalf("expected settingsAgentID coder, got %q", m.settingsAgentID)
	}
	// Detail view should contain the agent ID and editable fields
	if len(m.modalItems) == 0 {
		t.Fatal("expected detail modal items")
	}
	if !strings.Contains(m.modalItems[0], "coder") {
		t.Errorf("detail should show ID row: %q", m.modalItems[0])
	}
}

func TestHandleAgentsEnterStartsAddFlow(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentsSettings()

	m.modalSelectedIdx = 3 // add action
	m.handleAgentsEnter()
	if m.settingsEditField != "newAgentID" {
		t.Fatalf("expected newAgentID edit field, got %q", m.settingsEditField)
	}
}

func TestHandleAgentEditEnterEntersEditMode(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = 1 // Name
	m.handleAgentEditEnter()
	if m.settingsEditField != "agentName" {
		t.Fatalf("expected agentName edit field, got %q", m.settingsEditField)
	}
	if got := m.textInput.Value(); got != "Coder" {
		t.Fatalf("expected text input value Coder, got %q", got)
	}
}

func TestHandleAgentSettingsInputCreatesNewAgent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentsSettings()

	m.settingsEditField = "newAgentID"
	m.handleAgentSettingsInput("sysadmin")
	if len(m.cfg.Agents.List) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(m.cfg.Agents.List))
	}
	// findAgent should return the new agent with Name set to its ID
	ag := m.findAgent("sysadmin")
	if ag == nil {
		t.Fatal("expected to find sysadmin agent")
	}
	if ag.Name != "sysadmin" {
		t.Errorf("expected name sysadmin, got %q", ag.Name)
	}
	// Only set as default if it was the first agent
	if ag.Default {
		t.Error("appended agent should not be default when list is non-empty")
	}
	// Edit field cleared and list reloaded
	if m.settingsEditField != "" {
		t.Errorf("expected edit field cleared, got %q", m.settingsEditField)
	}
	if len(m.modalItems) != 5 {
		t.Fatalf("expected 5 list items after adding, got %d", len(m.modalItems))
	}
}

func TestHandleAgentSettingsInputDuplicateID(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsEditField = "newAgentID"
	m.handleAgentSettingsInput("coder")
	if len(m.cfg.Agents.List) != 2 {
		t.Fatalf("duplicate ID must not append, got %d", len(m.cfg.Agents.List))
	}
	if m.formError == "" {
		t.Fatal("expected duplicate-id error")
	}
}

func TestHandleAgentSettingsInputName(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentName"
	m.handleAgentSettingsInput("  Author  ")
	ag := m.findAgent("writer")
	if ag == nil || ag.Name != "Author" {
		t.Fatalf("expected name Author, got %q", ag.Name)
	}
	if m.settingsEditField != "" {
		t.Fatalf("expected edit field cleared, got %q", m.settingsEditField)
	}
}

func TestHandleAgentSettingsInputTemperature(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentTemperature"
	m.handleAgentSettingsInput("1.25")
	ag := m.findAgent("writer")
	if ag == nil || ag.Temperature == nil {
		t.Fatal("expected temperature set")
	}
	if *ag.Temperature != 1.25 {
		t.Fatalf("expected temperature 1.25, got %v", *ag.Temperature)
	}
}

func TestHandleAgentSettingsInputInvalidTemperature(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentTemperature"
	m.handleAgentSettingsInput("abc")
	if m.formError == "" {
		t.Fatal("expected invalid-number error")
	}
	ag := m.findAgent("writer")
	if ag.Temperature != nil {
		t.Fatal("invalid input must not set temperature")
	}
}

func TestHandleAgentSettingsInputModel(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentModel"
	m.handleAgentSettingsInput("gpt-4o")
	ag := m.findAgent("writer")
	if ag == nil || ag.Model == nil {
		t.Fatal("expected model set")
	}
	if ag.Model.Primary != "gpt-4o" {
		t.Fatalf("expected primary gpt-4o, got %q", ag.Model.Primary)
	}
}

func TestSetAgentDefault(t *testing.T) {
	m := newAgentsTestModel(t)
	m.setAgentDefault("writer")
	if m.cfg.Agents.List[0].Default {
		t.Error("coder should no longer be default")
	}
	if !m.cfg.Agents.List[1].Default {
		t.Error("writer should now be default")
	}
}

func TestDeleteAgent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.deleteAgent("writer")
	if len(m.cfg.Agents.List) != 1 {
		t.Fatalf("expected 1 agent after delete, got %d", len(m.cfg.Agents.List))
	}
	if m.findAgent("writer") != nil {
		t.Fatal("writer should no longer exist")
	}
	if m.cfg.Agents.List[0].ID != "coder" {
		t.Fatalf("expected coder to remain, got %q", m.cfg.Agents.List[0].ID)
	}
	// Back to agents list
	if m.settingsAgentID != "" {
		t.Errorf("expected settingsAgentID cleared, got %q", m.settingsAgentID)
	}
	if len(m.modalItems) == 0 {
		t.Error("expected agents list to be reloaded")
	}
}

func TestDeleteAgentDefaultPromotesFirst(t *testing.T) {
	m := newAgentsTestModel(t)
	// coder is the default; delete it
	m.deleteAgent("coder")
	if len(m.cfg.Agents.List) != 1 {
		t.Fatalf("expected 1 agent after delete, got %d", len(m.cfg.Agents.List))
	}
	if !m.cfg.Agents.List[0].Default {
		t.Error("first remaining agent should become default")
	}
}

func TestFindAgent(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	if ag == nil {
		t.Fatal("expected to find coder")
	}
	if m.findAgent("nope") != nil {
		t.Fatal("expected nil for missing agent")
	}
	// findAgent returns a pointer into the slice (in-place mutation works)
	ag.Name = "Changed"
	if m.cfg.Agents.List[0].Name != "Changed" {
		t.Error("findAgent should return pointer into the config slice")
	}
}

func TestLoadAgentDetailDefaults(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentDetail("")
	// Defaults view has 9 rows
	if len(m.modalItems) != 9 {
		t.Fatalf("expected 9 defaults rows, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[1], "test-model") {
		t.Errorf("model row should show value: %q", m.modalItems[1])
	}
}

func TestHandleDefaultsEditEnter(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")

	m.modalSelectedIdx = 2 // MaxTokens
	m.handleDefaultsEditEnter()
	if m.settingsEditField != "defaultMaxTokens" {
		t.Fatalf("expected defaultMaxTokens, got %q", m.settingsEditField)
	}
	if got := m.textInput.Value(); got != "4096" {
		t.Fatalf("expected 4096 in input, got %q", got)
	}
}

func TestHandleDefaultsFieldEdit(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsEditField = "defaultMaxTokens"
	m.handleDefaultsFieldEdit("8192")
	if m.cfg.Agents.Defaults.MaxTokens != 8192 {
		t.Fatalf("expected MaxTokens 8192, got %d", m.cfg.Agents.Defaults.MaxTokens)
	}
	if m.settingsEditField != "" {
		t.Fatalf("expected edit field cleared, got %q", m.settingsEditField)
	}
}

func TestLoadAgentDetailSubagentsSplit(t *testing.T) {
	m := newAgentsTestModel(t)
	// Give coder a subagent allow-list and max concurrent.
	ag := m.findAgent("coder")
	ag.Subagents = &config.SubagentsConfig{
		AllowAgents:   []string{"writer"},
		MaxConcurrent: 3,
	}
	m.loadAgentDetail("coder")

	// 11 rows: 9 fields + set-as-default + delete
	if len(m.modalItems) != 11 {
		t.Fatalf("expected 11 rows, got %d: %v", len(m.modalItems), m.modalItems)
	}
	if !strings.Contains(m.modalItems[7], "writer") {
		t.Errorf("subagents allow row should list writer: %q", m.modalItems[7])
	}
	if !strings.Contains(m.modalItems[8], "3") {
		t.Errorf("subagents maxconcurrent row should show 3: %q", m.modalItems[8])
	}
}

func TestHandleAgentEditEnterSubagentsAllowOpensPicker(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldSubagentsAllow // 7
	m.handleAgentEditEnter()

	if !m.subagentPickerActive {
		t.Fatal("expected subagent picker to be active")
	}
	// Picker should list all agents except self (coder). Only writer remains.
	if len(m.subagentPickerItems) != 1 {
		t.Fatalf("expected 1 picker item (writer), got %d: %v", len(m.subagentPickerItems), m.subagentPickerItems)
	}
	if m.subagentPickerItems[0] != "writer" {
		t.Errorf("expected writer in picker, got %q", m.subagentPickerItems[0])
	}
	// Self must not appear.
	for _, id := range m.subagentPickerItems {
		if id == "coder" {
			t.Fatal("current agent must not be listed as its own subagent")
		}
	}
	// No allow-list configured → writer unchecked.
	if m.subagentPickerSelected[0] {
		t.Error("writer should be unchecked when no allow-list is set")
	}
}

func TestStartSubagentPickerPreselectsAllowed(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	ag.Subagents = &config.SubagentsConfig{AllowAgents: []string{"writer"}}
	m.settingsAgentID = "coder"

	m.startSubagentPicker(ag)

	if len(m.subagentPickerItems) != 1 {
		t.Fatalf("expected 1 picker item, got %d", len(m.subagentPickerItems))
	}
	if !m.subagentPickerSelected[0] {
		t.Error("writer should be pre-selected because it is in AllowAgents")
	}
}

func TestToggleSubagentPicker(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	m.settingsAgentID = "coder"
	m.startSubagentPicker(ag)

	// Initially unchecked.
	if m.subagentPickerSelected[0] {
		t.Fatal("expected initially unchecked")
	}
	m.toggleSubagentPicker()
	if !m.subagentPickerSelected[0] {
		t.Fatal("expected toggled to checked")
	}
	m.toggleSubagentPicker()
	if m.subagentPickerSelected[0] {
		t.Fatal("expected toggled back to unchecked")
	}
}

func TestSaveSubagentPickerSetsAllowAgents(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	m.settingsAgentID = "coder"
	m.startSubagentPicker(ag)

	// Select writer.
	m.subagentPickerSelected[0] = true
	m.saveSubagentPicker()

	if m.subagentPickerActive {
		t.Fatal("picker should be closed after save")
	}
	ag = m.findAgent("coder")
	if ag.Subagents == nil {
		t.Fatal("expected Subagents to be created")
	}
	if len(ag.Subagents.AllowAgents) != 1 || ag.Subagents.AllowAgents[0] != "writer" {
		t.Fatalf("expected AllowAgents [writer], got %v", ag.Subagents.AllowAgents)
	}
}

func TestSaveSubagentPickerEmptyClearsAndNils(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	ag.Subagents = &config.SubagentsConfig{AllowAgents: []string{"writer"}}
	m.settingsAgentID = "coder"
	m.startSubagentPicker(ag)

	// Deselect everything (writer currently selected → toggle off).
	m.subagentPickerSelected[0] = false
	m.saveSubagentPicker()

	ag = m.findAgent("coder")
	// With no other meaningful fields, Subagents should be nil to keep config clean.
	if ag.Subagents != nil {
		t.Fatalf("expected Subagents to be nil when empty, got %+v", ag.Subagents)
	}
}

func TestSaveSubagentPickerEmptyKeepsMaxConcurrent(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	ag.Subagents = &config.SubagentsConfig{AllowAgents: []string{"writer"}, MaxConcurrent: 5}
	m.settingsAgentID = "coder"
	m.startSubagentPicker(ag)

	m.subagentPickerSelected[0] = false
	m.saveSubagentPicker()

	ag = m.findAgent("coder")
	if ag.Subagents == nil {
		t.Fatal("expected Subagents to remain (has MaxConcurrent)")
	}
	if len(ag.Subagents.AllowAgents) != 0 {
		t.Errorf("expected empty AllowAgents, got %v", ag.Subagents.AllowAgents)
	}
	if ag.Subagents.MaxConcurrent != 5 {
		t.Errorf("expected MaxConcurrent preserved, got %d", ag.Subagents.MaxConcurrent)
	}
}

func TestCancelSubagentPicker(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	m.settingsAgentID = "coder"
	m.startSubagentPicker(ag)
	m.subagentPickerSelected[0] = true

	m.cancelSubagentPicker()

	if m.subagentPickerActive {
		t.Fatal("picker should be inactive after cancel")
	}
	if m.subagentPickerItems != nil || m.subagentPickerLabels != nil || m.subagentPickerSelected != nil {
		t.Fatal("picker state should be cleared after cancel")
	}
	// Cancel must not mutate the config.
	ag = m.findAgent("coder")
	if ag.Subagents != nil {
		t.Fatal("cancel must not create Subagents")
	}
}

func TestHandleAgentEditEnterSubagentsMaxConcurrent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldSubagentsMaxConcurrent // 8
	m.handleAgentEditEnter()
	if m.settingsEditField != "agentSubagentsMaxConcurrent" {
		t.Fatalf("expected agentSubagentsMaxConcurrent, got %q", m.settingsEditField)
	}
}

func TestHandleAgentSettingsInputSubagentsMaxConcurrent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentSubagentsMaxConcurrent"
	m.handleAgentSettingsInput("4")

	ag := m.findAgent("writer")
	if ag.Subagents == nil {
		t.Fatal("expected Subagents to be created")
	}
	if ag.Subagents.MaxConcurrent != 4 {
		t.Fatalf("expected MaxConcurrent 4, got %d", ag.Subagents.MaxConcurrent)
	}
}

func TestHandleAgentSettingsInputSubagentsMaxConcurrentInvalid(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentSubagentsMaxConcurrent"
	m.handleAgentSettingsInput("abc")
	if m.formError == "" {
		t.Fatal("expected invalid-number error")
	}
	ag := m.findAgent("writer")
	if ag.Subagents != nil {
		t.Fatal("invalid input must not create Subagents")
	}
}

func TestHandleAgentEditEnterModelAlwaysSelector(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldModel // 4
	m.handleAgentEditEnter()

	// Even with no models configured, the selector must open (never text input).
	if !m.settingsSelectorActive {
		t.Fatal("expected model selector to be active")
	}
	if m.settingsSelectorField != "agentModel" {
		t.Fatalf("expected selector field agentModel, got %q", m.settingsSelectorField)
	}
	// Options: (default), (custom...) — with no models just those two.
	if len(m.settingsSelectorValues) != 2 {
		t.Fatalf("expected 2 selector values, got %d: %v", len(m.settingsSelectorValues), m.settingsSelectorValues)
	}
	if m.settingsSelectorValues[0] != "" || m.settingsSelectorValues[1] != "__custom__" {
		t.Fatalf("unexpected selector values: %v", m.settingsSelectorValues)
	}
}

func TestHandleSelectorConfirmCustomOpensTextInput(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldModel
	m.handleAgentEditEnter()
	// Select the last option "(custom...)".
	m.settingsSelectorIdx = len(m.settingsSelectorValues) - 1
	m.handleSelectorConfirm()

	if m.settingsSelectorActive {
		t.Fatal("selector should be closed after picking custom")
	}
	if m.settingsEditField != "agentModel" {
		t.Fatalf("expected agentModel text input, got %q", m.settingsEditField)
	}
}

func TestHandleDefaultsEditEnterModelAlwaysSelector(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")

	m.modalSelectedIdx = 1 // Model
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected defaults model selector to be active")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Fatalf("expected selector field defaultModel, got %q", m.settingsSelectorField)
	}
}
