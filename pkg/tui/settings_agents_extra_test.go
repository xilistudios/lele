package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// --- settings_agents.go additional coverage ---

func TestHandleAgentEditEnterReadOnlyFields(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")
	// Field 0 (ID) read-only.
	m.modalSelectedIdx = 0
	m.handleAgentEditEnter()
	if m.settingsEditField != "" {
		t.Errorf("ID should not enter edit mode, got %q", m.settingsEditField)
	}
	// Field 6 (Skills) read-only.
	m.modalSelectedIdx = 6
	m.handleAgentEditEnter()
	if m.settingsEditField != "" {
		t.Errorf("skills should not enter edit mode, got %q", m.settingsEditField)
	}
	// Field 7 (Subagents) read-only.
	m.modalSelectedIdx = 7
	m.handleAgentEditEnter()
	if m.settingsEditField != "" {
		t.Errorf("subagents should not enter edit mode, got %q", m.settingsEditField)
	}
}

func TestHandleAgentEditEnterDescriptionWorkspace(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")
	m.modalSelectedIdx = 2
	m.handleAgentEditEnter()
	if m.settingsEditField != "agentDescription" {
		t.Errorf("expected agentDescription, got %q", m.settingsEditField)
	}
	m.modalSelectedIdx = 3
	m.handleAgentEditEnter()
	if m.settingsEditField != "agentWorkspace" {
		t.Errorf("expected agentWorkspace, got %q", m.settingsEditField)
	}
}

func TestHandleAgentEditEnterAgentModelFallback(t *testing.T) {
	// Use a real model whose providers have no configured models so the
	// selector path falls back to text input (via listProviderModels → empty).
	cfg := testModelConfig(t)
	cfg.Agents.List = []config.AgentConfig{{ID: "a1", Name: "Agent One"}}
	cfg.Providers = &config.ProvidersConfig{}
	m := newTestModelWithConfig(t, cfg, true)
	m.settingsAgentID = "a1"
	m.loadAgentDetail("a1")
	// No models configured → fall back to text input.
	m.modalSelectedIdx = 4
	m.handleAgentEditEnter()
	if m.settingsEditField != "agentModel" {
		t.Errorf("expected agentModel text-input fallback, got %q", m.settingsEditField)
	}
}

func TestHandleAgentEditEnterSetDefault(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")
	m.modalSelectedIdx = 8 // set as default
	m.handleAgentEditEnter()
	if !m.cfg.Agents.List[0].Default {
		t.Error("coder should be default after set-default")
	}
}

func TestHandleAgentEditEnterDelete(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "coder"
	m.loadAgentDetail("coder")
	m.modalSelectedIdx = 9 // delete
	m.handleAgentEditEnter()
	if m.settingsEditField != "confirmDelete" {
		t.Errorf("expected confirmDelete, got %q", m.settingsEditField)
	}
	if m.formError == "" {
		t.Error("expected deletion confirmation error message")
	}
}

func TestHandleAgentEditEnterMissingAgent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "ghost"
	m.handleAgentEditEnter() // agent==nil → no-op, no panic
}

func TestHandleAgentEditEnterDefaultsProviderSelector(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "k"},
			Models: map[string]config.ProviderModelConfig{
				"gpt-4o": {Model: "gpt-4o"},
			},
		},
	}
	m := newTestModelWithConfig(t, cfg, true)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 0 // Provider
	m.handleDefaultsEditEnter()
	if !m.settingsSelectorActive {
		t.Error("expected provider selector active")
	}
	if m.settingsSelectorField != "defaultProvider" {
		t.Errorf("expected defaultProvider field, got %q", m.settingsSelectorField)
	}
}

func TestHandleDefaultsEditEnterModelFallback(t *testing.T) {
	cfg := testModelConfig(t) // no named providers → listProviderModels empty → text fallback
	m := newTestModelWithConfig(t, cfg, true)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	// No providers → text input fallback.
	m.modalSelectedIdx = 1
	m.handleDefaultsEditEnter()
	if m.settingsEditField != "defaultModel" {
		t.Errorf("expected defaultModel text-input fallback, got %q", m.settingsEditField)
	}
}

func TestHandleDefaultsEditEnterTemperature(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = ""
	m.loadAgentDetail("")
	m.modalSelectedIdx = 3
	m.handleDefaultsEditEnter()
	if m.settingsEditField != "defaultTemperature" {
		t.Errorf("expected defaultTemperature, got %q", m.settingsEditField)
	}
}

func TestHandleDefaultsEditEnterNumericFields(t *testing.T) {
	cases := []struct {
		idx  int
		want string
	}{
		{2, "defaultMaxTokens"},
		{4, "defaultMaxToolIterations"},
		{5, "defaultMaxReadLines"},
		{6, "defaultSubagentTimeout"},
		{7, "defaultSubagentMaxConcurrent"},
		{8, "defaultLLMLoopTimeout"},
	}
	for _, tc := range cases {
		m := newAgentsTestModel(t)
		m.settingsAgentID = ""
		m.loadAgentDetail("")
		m.modalSelectedIdx = tc.idx
		m.handleDefaultsEditEnter()
		if m.settingsEditField != tc.want {
			t.Errorf("idx %d: expected %q, got %q", tc.idx, tc.want, m.settingsEditField)
		}
	}
}

func TestHandleAgentSettingsInputDeleteConfirm(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "confirmDelete"
	m.handleAgentSettingsInput("y")
	if m.findAgent("writer") != nil {
		t.Error("expected writer deleted after confirm")
	}
	if m.settingsEditField != "" {
		t.Error("expected edit field cleared")
	}
}

func TestHandleAgentSettingsInputDeleteAbort(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "confirmDelete"
	m.handleAgentSettingsInput("n")
	if m.findAgent("writer") == nil {
		t.Error("writer should remain when not confirmed")
	}
	if m.settingsEditField != "" {
		t.Error("expected edit field cleared")
	}
}

func TestApplyAgentReloadNilLoop(t *testing.T) {
	m := &Model{}
	m.applyAgentReload() // no panic when agentLoop nil
}

func TestHandleAgentFieldEditWorkspace(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentWorkspace"
	m.handleAgentFieldEdit(m.findAgent("writer"), "/tmp/new")
	if ag := m.findAgent("writer"); ag.Workspace != "/tmp/new" {
		t.Errorf("expected workspace /tmp/new, got %q", ag.Workspace)
	}
}

func TestHandleAgentFieldEditDescription(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentDescription"
	m.handleAgentFieldEdit(m.findAgent("writer"), "A writing agent")
	if ag := m.findAgent("writer"); ag.Description != "A writing agent" {
		t.Errorf("expected description set, got %q", ag.Description)
	}
}

func TestHandleAgentFieldEditClearModel(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentModel"
	m.handleAgentFieldEdit(m.findAgent("writer"), "")
	ag := m.findAgent("writer")
	if ag.Model != nil {
		t.Error("empty model should clear Model")
	}
}

func TestHandleAgentFieldEditTemperatureEmpty(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsAgentID = "writer"
	m.settingsEditField = "agentTemperature"
	m.handleAgentFieldEdit(m.findAgent("writer"), "")
	if ag := m.findAgent("writer"); ag.Temperature != nil {
		t.Error("empty temperature should clear Temperature")
	}
}

func TestHandleDefaultsFieldEditProviderModel(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsEditField = "defaultProvider"
	m.handleDefaultsFieldEdit("openai")
	if m.cfg.Agents.Defaults.Provider != "openai" {
		t.Errorf("defaultProvider = %q", m.cfg.Agents.Defaults.Provider)
	}
	m.settingsEditField = "defaultModel"
	m.handleDefaultsFieldEdit("gpt-4o")
	if m.cfg.Agents.Defaults.Model != "gpt-4o" {
		t.Errorf("defaultModel = %q", m.cfg.Agents.Defaults.Model)
	}
}

func TestHandleDefaultsFieldEditInvalidNumber(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsEditField = "defaultMaxTokens"
	m.handleDefaultsFieldEdit("not-a-number")
	if m.formError == "" {
		t.Error("expected invalid number error")
	}
	// Negative rejected.
	m2 := newAgentsTestModel(t)
	m2.settingsEditField = "defaultMaxReadLines"
	m2.handleDefaultsFieldEdit("-1")
	if m2.formError == "" {
		t.Error("expected error for negative")
	}
}

func TestHandleDefaultsFieldEditTemperature(t *testing.T) {
	m := newAgentsTestModel(t)
	m.settingsEditField = "defaultTemperature"
	m.handleDefaultsFieldEdit("0.5")
	if m.cfg.Agents.Defaults.Temperature == nil || *m.cfg.Agents.Defaults.Temperature != 0.5 {
		t.Error("expected default temperature 0.5")
	}
	m2 := newAgentsTestModel(t)
	m2.settingsEditField = "defaultTemperature"
	m2.handleDefaultsFieldEdit("")
	if m2.cfg.Agents.Defaults.Temperature != nil {
		t.Error("empty temperature should clear")
	}
	m3 := newAgentsTestModel(t)
	m3.settingsEditField = "defaultTemperature"
	m3.handleDefaultsFieldEdit("abc")
	if m3.formError == "" {
		t.Error("expected error for invalid temperature")
	}
}

func TestHandleDefaultsFieldEditAllNumeric(t *testing.T) {
	cases := []struct {
		field string
		val   string
	}{
		{"defaultMaxToolIterations", "42"},
		{"defaultMaxReadLines", "777"},
		{"defaultSubagentTimeout", "33"},
		{"defaultSubagentMaxConcurrent", "8"},
		{"defaultLLMLoopTimeout", "120"},
	}
	for _, tc := range cases {
		m := newAgentsTestModel(t)
		m.settingsEditField = tc.field
		m.handleDefaultsFieldEdit(tc.val)
		if m.formError != "" {
			t.Errorf("%s: unexpected error %q", tc.field, m.formError)
		}
		if m.settingsEditField != "" {
			t.Errorf("%s: edit field not cleared", tc.field)
		}
	}
}

func TestLoadAgentDetailAgentView(t *testing.T) {
	cfg := testModelConfig(t)
	f := 0.9
	cfg.Agents.List = []config.AgentConfig{
		{ID: "a1", Name: "Agent One", Default: true,
			Model:       &config.AgentModelConfig{Primary: "gpt-4o", Fallbacks: []string{"gpt-4o-mini"}},
			Temperature: &f,
			Skills:      []string{"skill-a", "skill-b"},
			Subagents:   &config.SubagentsConfig{AllowAgents: []string{"a2"}, MaxConcurrent: 3},
		},
	}
	cfg.Providers = &config.ProvidersConfig{}
	m := newTestModelWithConfig(t, cfg, true)
	m.loadAgentDetail("a1")
	if len(m.modalItems) != 10 {
		t.Fatalf("expected 10 detail rows, got %d", len(m.modalItems))
	}
	joined := strings.Join(m.modalItems, "\n")
	if !strings.Contains(joined, "★") {
		t.Error("expected default mark")
	}
	if !strings.Contains(joined, "gpt-4o (+gpt-4o-mini)") {
		t.Errorf("expected model with fallbacks, got %q", m.modalItems[4])
	}
	if !strings.Contains(joined, "skill-a, skill-b") {
		t.Errorf("expected skills, got %q", m.modalItems[6])
	}
	if !strings.Contains(joined, "allow: a2") || !strings.Contains(joined, "max:3") {
		t.Errorf("expected subagents info, got %q", m.modalItems[7])
	}
}

func TestLoadAgentDetailMissingAgent(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentDetail("does-not-exist")
	expect := i18n.T("tui.settings.agentNotFound")
	if len(m.modalItems) == 0 || m.modalItems[0] != expect {
		t.Errorf("expected %q message, got %v", expect, m.modalItems)
	}
}

func TestFormatFloatPtr(t *testing.T) {
	if formatFloatPtr(nil) != "default" {
		t.Error("nil should render as default")
	}
	f := 1.5
	if formatFloatPtr(&f) != "1.50" {
		t.Errorf("formatFloatPtr = %q", formatFloatPtr(&f))
	}
}

func TestHandleAgentsEnterOutOfRange(t *testing.T) {
	m := newAgentsTestModel(t)
	m.loadAgentsSettings()
	m.modalSelectedIdx = 99
	m.handleAgentsEnter() // no panic
}
