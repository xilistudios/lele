package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/xilistudios/lele/pkg/config"
)

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
				MemoryMaxLines:    500,
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

	if len(m.modalItems) != 4 {
		t.Fatalf("expected 4 items, got %d", len(m.modalItems))
	}
	if len(m.settingsAgentKeys) != 4 {
		t.Fatalf("expected 4 keys, got %d", len(m.settingsAgentKeys))
	}
	if m.settingsAgentKeys[0] != "" {
		t.Errorf("first key should be empty (defaults), got %q", m.settingsAgentKeys[0])
	}
	if m.settingsAgentKeys[1] != "coder" || m.settingsAgentKeys[2] != "writer" {
		t.Errorf("agent keys mismatch: %v", m.settingsAgentKeys)
	}
	if m.settingsAgentKeys[3] != settingsAgentAddKey {
		t.Errorf("last key should be add action, got %q", m.settingsAgentKeys[3])
	}
	if !strings.Contains(m.modalItems[1], "★") {
		t.Errorf("default agent should be starred in label: %q", m.modalItems[1])
	}
}

func TestHandleAgentsEnterNavigatesToDetail(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentsSettings()

	m.modalSelectedIdx = 1
	m.handleAgentsEnter()
	if m.modalMode != ModalSettingsAgentEdit {
		t.Fatalf("expected navigation to AgentEdit modal, got %v", m.modalMode)
	}
	if m.settingsAgentID != "coder" {
		t.Fatalf("expected settingsAgentID coder, got %q", m.settingsAgentID)
	}
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

	m.modalSelectedIdx = 3
	m.handleAgentsEnter()
	if m.settingsEditField != "newAgentID" {
		t.Fatalf("expected newAgentID edit field, got %q", m.settingsEditField)
	}
}

func TestHandleAgentEditEnterEntersEditMode(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = 1
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
	ag := m.findAgent("sysadmin")
	if ag == nil {
		t.Fatal("expected to find sysadmin agent")
	}
	if ag.Name != "sysadmin" {
		t.Errorf("expected name sysadmin, got %q", ag.Name)
	}
	if ag.Default {
		t.Error("appended agent should not be default when list is non-empty")
	}
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
	if m.settingsAgentID != "" {
		t.Errorf("expected settingsAgentID cleared, got %q", m.settingsAgentID)
	}
	if len(m.modalItems) == 0 {
		t.Error("expected agents list to be reloaded")
	}
}

func TestDeleteAgentDefaultPromotesFirst(t *testing.T) {
	m := newAgentsTestModel(t)
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
	ag.Name = "Changed"
	if m.cfg.Agents.List[0].Name != "Changed" {
		t.Error("findAgent should return pointer into the config slice")
	}
}

func TestLoadAgentDetailDefaults(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentDetail("")
	if len(m.modalItems) != 11 {
		t.Fatalf("expected 11 defaults rows, got %d", len(m.modalItems))
	}
	if !strings.Contains(m.modalItems[1], "test-model") {
		t.Errorf("model row should show value: %q", m.modalItems[1])
	}
}

func TestHandleDefaultsEditEnter(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")

	m.modalSelectedIdx = 2
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

func TestHandleDefaultsMemoryMaxLines(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")

	m.modalSelectedIdx = defaultsFieldMemoryMaxLines
	m.handleDefaultsEditEnter()
	if m.settingsEditField != "defaultMemoryMaxLines" {
		t.Fatalf("expected defaultMemoryMaxLines edit field, got %q", m.settingsEditField)
	}
	if got := m.textInput.Value(); got != "500" {
		t.Fatalf("expected 500 in input, got %q", got)
	}

	m.handleAgentSettingsInput("120")
	if m.cfg.Agents.Defaults.MemoryMaxLines != 120 {
		t.Fatalf("expected MemoryMaxLines 120, got %d", m.cfg.Agents.Defaults.MemoryMaxLines)
	}

	m.settingsEditField = "defaultMemoryMaxLines"
	m.handleAgentSettingsInput("abc")
	if m.formError == "" {
		t.Fatal("expected invalid-number error")
	}
	if m.cfg.Agents.Defaults.MemoryMaxLines != 120 {
		t.Fatalf("invalid input must leave the value unchanged, got %d", m.cfg.Agents.Defaults.MemoryMaxLines)
	}
}

func TestLoadAgentDetailSubagentsSplit(t *testing.T) {
	m := newAgentsTestModel(t)
	ag := m.findAgent("coder")
	ag.Subagents = &config.SubagentsConfig{
		AllowAgents:   []string{"writer"},
		MaxConcurrent: 3,
	}
	m.loadAgentDetail("coder")

	if len(m.modalItems) != 12 {
		t.Fatalf("expected 12 rows, got %d: %v", len(m.modalItems), m.modalItems)
	}
	if !strings.Contains(m.modalItems[agentFieldSubagentsAllow], "writer") {
		t.Errorf("subagents allow row should list writer: %q", m.modalItems[agentFieldSubagentsAllow])
	}
	if !strings.Contains(m.modalItems[agentFieldSubagentsMaxConcurrent], "3") {
		t.Errorf("subagents maxconcurrent row should show 3: %q", m.modalItems[agentFieldSubagentsMaxConcurrent])
	}
}

func TestHandleAgentEditEnterSubagentsAllowOpensPicker(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldSubagentsAllow
	m.handleAgentEditEnter()

	if !m.subagentPickerActive {
		t.Fatal("expected subagent picker to be active")
	}
	if len(m.subagentPickerItems) != 1 {
		t.Fatalf("expected 1 picker item (writer), got %d: %v", len(m.subagentPickerItems), m.subagentPickerItems)
	}
	if m.subagentPickerItems[0] != "writer" {
		t.Errorf("expected writer in picker, got %q", m.subagentPickerItems[0])
	}
	for _, id := range m.subagentPickerItems {
		if id == "coder" {
			t.Fatal("current agent must not be listed as its own subagent")
		}
	}
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

	m.subagentPickerSelected[0] = false
	m.saveSubagentPicker()

	ag = m.findAgent("coder")
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
	ag = m.findAgent("coder")
	if ag.Subagents != nil {
		t.Fatal("cancel must not create Subagents")
	}
}

func TestHandleAgentEditEnterSubagentsMaxConcurrent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldSubagentsMaxConcurrent
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

	m.modalSelectedIdx = agentFieldModel
	m.handleAgentEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected model selector to be active")
	}
	if m.settingsSelectorField != "agentModel" {
		t.Fatalf("expected selector field agentModel, got %q", m.settingsSelectorField)
	}
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

	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected defaults model selector to be active")
	}
	if m.settingsSelectorField != "defaultModel" {
		t.Fatalf("expected selector field defaultModel, got %q", m.settingsSelectorField)
	}
}

func TestAgentDetailRowIndexAlignment(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentDetail("coder")

	wantPrefix := map[int]string{
		agentFieldID:                     "ID:",
		agentFieldName:                   "Name:",
		agentFieldDescription:            "Description:",
		agentFieldWorkspace:              "Workspace:",
		agentFieldModel:                  "Model:",
		agentFieldTemperature:            "Temperature:",
		agentFieldThinkingLevel:          "Thinking:",
		agentFieldSkills:                 "Skills:",
		agentFieldSubagentsAllow:         "Subagents Allow:",
		agentFieldSubagentsMaxConcurrent: "Subagents MaxConcurrent:",
		agentFieldSetDefault:             "★",
		agentFieldDelete:                 "🗑",
	}
	if len(m.modalItems) != len(wantPrefix) {
		t.Fatalf("agent view has %d rows but %d field constants defined: %v",
			len(m.modalItems), len(wantPrefix), m.modalItems)
	}
	for idx, prefix := range wantPrefix {
		if !strings.HasPrefix(m.modalItems[idx], prefix) {
			t.Errorf("row %d = %q, want prefix %q", idx, m.modalItems[idx], prefix)
		}
	}
}

func TestDefaultsDetailRowIndexAlignment(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentDetail("")

	wantPrefix := map[int]string{
		defaultsFieldProvider:              "Provider:",
		defaultsFieldModel:                 "Model:",
		defaultsFieldMaxTokens:             "MaxTokens:",
		defaultsFieldTemperature:           "Temperature:",
		defaultsFieldThinkingLevel:         "Thinking:",
		defaultsFieldMaxToolIterations:     "MaxToolIterations:",
		defaultsFieldMaxReadLines:          "MaxReadLines:",
		defaultsFieldMemoryMaxLines:        "MemoryMaxLines:",
		defaultsFieldSubagentTimeout:       "SubagentTimeout:",
		defaultsFieldSubagentMaxConcurrent: "SubagentMaxConcurrent:",
		defaultsFieldLLMLoopTimeout:        "LLMLoopTimeout:",
	}
	if len(m.modalItems) != len(wantPrefix) {
		t.Fatalf("defaults view has %d rows but %d field constants defined: %v",
			len(m.modalItems), len(wantPrefix), m.modalItems)
	}
	for idx, prefix := range wantPrefix {
		if !strings.HasPrefix(m.modalItems[idx], prefix) {
			t.Errorf("row %d = %q, want prefix %q", idx, m.modalItems[idx], prefix)
		}
	}
}

func TestThinkingLevelRowDisplay(t *testing.T) {
	m := newAgentsTestModel(t)

	m.loadAgentDetail("writer")
	if got := m.modalItems[agentFieldThinkingLevel]; got != "Thinking: default" {
		t.Errorf("nil level should render default: %q", got)
	}

	high := "high"
	m.findAgent("writer").ThinkingLevel = &high
	m.loadAgentDetail("writer")
	if got := m.modalItems[agentFieldThinkingLevel]; got != "Thinking: high" {
		t.Errorf("explicit level should render value: %q", got)
	}

	m.loadAgentDetail("")
	if got := m.modalItems[defaultsFieldThinkingLevel]; got != "Thinking: default" {
		t.Errorf("defaults nil level should render default: %q", got)
	}
	m.cfg.Agents.Defaults.ThinkingLevel = &high
	m.loadAgentDetail("")
	if got := m.modalItems[defaultsFieldThinkingLevel]; got != "Thinking: high" {
		t.Errorf("defaults explicit level should render value: %q", got)
	}
}

func TestHandleAgentEditEnterThinkingOpensSelector(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")

	m.modalSelectedIdx = agentFieldThinkingLevel
	m.handleAgentEditEnter()

	if !m.settingsSelectorActive {
		t.Fatal("expected thinking-level selector to be active")
	}
	if m.settingsSelectorField != "agentThinkingLevel" {
		t.Fatalf("selector field = %q, want agentThinkingLevel", m.settingsSelectorField)
	}
	if len(m.settingsSelectorValues) != 5 || len(m.settingsSelectorItems) != 5 {
		t.Fatalf("expected 5 options, got %d/%d: %v / %v",
			len(m.settingsSelectorItems), len(m.settingsSelectorValues),
			m.settingsSelectorItems, m.settingsSelectorValues)
	}
	if m.settingsSelectorIdx != 0 {
		t.Errorf("nil level should pre-select index 0, got %d", m.settingsSelectorIdx)
	}
	m.closeSettingsSelector()

	medium := "medium"
	m.findAgent("coder").ThinkingLevel = &medium
	m.modalSelectedIdx = agentFieldThinkingLevel
	m.handleAgentEditEnter()
	if m.settingsSelectorIdx != 3 {
		t.Errorf("medium should pre-select index 3, got %d", m.settingsSelectorIdx)
	}
}

func TestSelectorConfirmThinkingLevelEndToEnd(t *testing.T) {
	m := newAgentsTestModel(t)
	m.resetModal(ModalSettingsAgentEdit)
	m.settingsAgentID = "writer"
	m.loadAgentDetail("writer")

	m.modalSelectedIdx = agentFieldThinkingLevel
	m.handleAgentEditEnter()

	m.settingsSelectorIdx = 1
	m.handleSelectorConfirm()

	if m.settingsSelectorActive {
		t.Fatal("selector should be closed after confirm")
	}
	ag := m.findAgent("writer")
	if ag.ThinkingLevel == nil || *ag.ThinkingLevel != "off" {
		t.Fatalf("expected thinking level off, got %v", ag.ThinkingLevel)
	}

	m.modalSelectedIdx = agentFieldThinkingLevel
	m.handleAgentEditEnter()
	m.settingsSelectorIdx = 0
	m.handleSelectorConfirm()

	ag = m.findAgent("writer")
	if ag.ThinkingLevel != nil {
		t.Fatalf("(default) must clear the field to nil, got %q", *ag.ThinkingLevel)
	}
}

func TestHandleAgentFieldEditThinkingLevel(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"

	m.settingsEditField = "agentThinkingLevel"
	m.handleAgentSettingsInput("high")
	ag := m.findAgent("writer")
	if ag.ThinkingLevel == nil || *ag.ThinkingLevel != "high" {
		t.Fatalf("expected high, got %v", ag.ThinkingLevel)
	}
	if m.formError != "" {
		t.Errorf("unexpected error: %q", m.formError)
	}

	m.settingsEditField = "agentThinkingLevel"
	m.handleAgentSettingsInput("bogus")
	if m.formError == "" {
		t.Fatal("expected invalid-thinking-level error")
	}
	ag = m.findAgent("writer")
	if ag.ThinkingLevel == nil || *ag.ThinkingLevel != "high" {
		t.Fatalf("invalid input must leave field unchanged, got %v", ag.ThinkingLevel)
	}

	m.settingsEditField = "agentThinkingLevel"
	m.handleAgentSettingsInput("")
	ag = m.findAgent("writer")
	if ag.ThinkingLevel != nil {
		t.Fatalf("expected nil after clear, got %q", *ag.ThinkingLevel)
	}
	if m.formError != "" {
		t.Errorf("clear must not set an error: %q", m.formError)
	}
}

func TestHandleAgentFieldEditThinkingLevelNormalized(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentThinkingLevel"
	m.handleAgentSettingsInput("  MeDiUm ")
	ag := m.findAgent("writer")
	if ag.ThinkingLevel == nil || *ag.ThinkingLevel != "medium" {
		t.Fatalf("expected normalized medium, got %v", ag.ThinkingLevel)
	}
}

func TestHandleDefaultsThinkingLevel(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")

	m.modalSelectedIdx = defaultsFieldThinkingLevel
	m.handleDefaultsEditEnter()
	if !m.settingsSelectorActive {
		t.Fatal("expected defaults thinking selector to be active")
	}
	if m.settingsSelectorField != "defaultThinkingLevel" {
		t.Fatalf("selector field = %q, want defaultThinkingLevel", m.settingsSelectorField)
	}
	if len(m.settingsSelectorValues) != 5 {
		t.Fatalf("expected 5 options, got %d", len(m.settingsSelectorValues))
	}
	m.closeSettingsSelector()

	m.settingsEditField = "defaultThinkingLevel"
	m.handleDefaultsFieldEdit("low")
	d := m.cfg.Agents.Defaults.ThinkingLevel
	if d == nil || *d != "low" {
		t.Fatalf("expected defaults low, got %v", d)
	}

	m.settingsEditField = "defaultThinkingLevel"
	m.handleDefaultsFieldEdit("nope")
	if m.formError == "" {
		t.Fatal("expected invalid-thinking-level error")
	}
	d = m.cfg.Agents.Defaults.ThinkingLevel
	if d == nil || *d != "low" {
		t.Fatalf("invalid input must leave defaults unchanged, got %v", d)
	}

	m.settingsEditField = "defaultThinkingLevel"
	m.handleDefaultsFieldEdit("")
	if m.cfg.Agents.Defaults.ThinkingLevel != nil {
		t.Fatalf("expected nil after clear, got %q", *m.cfg.Agents.Defaults.ThinkingLevel)
	}
}

func TestDefaultsSelectorConfirmThinkingLevelEndToEnd(t *testing.T) {
	m := newAgentsTestModel(t)
	m.resetModal(ModalSettingsAgentEdit)
	m.settingsAgentID = ""
	m.loadAgentDetail("")

	m.modalSelectedIdx = defaultsFieldThinkingLevel
	m.handleAgentEditEnter()

	m.settingsSelectorIdx = 1
	m.handleSelectorConfirm()

	d := m.cfg.Agents.Defaults.ThinkingLevel
	if d == nil || *d != "off" {
		t.Fatalf("expected defaults off, got %v", d)
	}
}

func TestAddAgentFlowRendersInput(t *testing.T) {
	m := newAgentsTestModel(t)
	m.resetModal(ModalSettingsAgents)
	m.loadAgentsSettings()
	m.width, m.height = 100, 30

	m.modalSelectedIdx = len(m.settingsAgentKeys) - 1
	m.handleAgentsEnter()
	if m.settingsEditField != "newAgentID" {
		t.Fatalf("expected newAgentID edit field, got %q", m.settingsEditField)
	}

	frame := m.renderAgentEditInput()
	if !strings.Contains(frame, "Agent ID") {
		t.Fatalf("add-agent view must render the 'Agent ID' input prompt, got:\n%s", frame)
	}

	view := m.renderActiveModal()
	if !strings.Contains(view, "Agent ID") {
		t.Fatal("modal dispatch for ModalSettingsAgents + newAgentID must render the input prompt")
	}
	if strings.Contains(view, "+ Add Agent") {
		t.Fatal("edit view must replace the list while the add-agent input is active")
	}
}

func TestAddAgentFlowRendersErrors(t *testing.T) {
	m := newAgentsTestModel(t)
	m.resetModal(ModalSettingsAgents)
	m.loadAgentsSettings()
	m.width, m.height = 100, 30
	m.settingsEditField = "newAgentID"

	m.handleAgentSettingsInput("coder")
	if m.formError == "" {
		t.Fatal("expected duplicate-id error to be set")
	}
	view := m.renderActiveModal()
	if !strings.Contains(view, m.formError) {
		t.Fatalf("modal dispatch must show the add-agent validation error %q", m.formError)
	}
}
