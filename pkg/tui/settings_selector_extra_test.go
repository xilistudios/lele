package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
)

// --- settings_selector.go coverage ---

func TestStartSettingsSelector(t *testing.T) {
	m := &Model{}
	m.startSettingsSelector("field", "cur", []string{"A", "B", "C"}, []string{"a", "b", "c"})
	if !m.settingsSelectorActive {
		t.Error("expected selector active")
	}
	if m.settingsSelectorField != "field" || m.settingsSelectorOrig != "cur" {
		t.Errorf("selector state: field=%q orig=%q", m.settingsSelectorField, m.settingsSelectorOrig)
	}
	if m.settingsSelectorIdx != 0 {
		t.Errorf("expected initial idx 0, got %d", m.settingsSelectorIdx)
	}
	if len(m.settingsSelectorItems) != 3 || len(m.settingsSelectorValues) != 3 {
		t.Error("expected items/values populated")
	}
}

func TestStartSettingsSelectorPreselectsCurrent(t *testing.T) {
	m := &Model{}
	m.startSettingsSelector("f", "b", []string{"A", "B"}, []string{"a", "b"})
	if m.settingsSelectorIdx != 1 {
		t.Errorf("expected idx 1 (value b), got %d", m.settingsSelectorIdx)
	}
	// No matching current value → idx 0.
	m2 := &Model{}
	m2.startSettingsSelector("f", "nothere", []string{"A", "B"}, []string{"a", "b"})
	if m2.settingsSelectorIdx != 0 {
		t.Errorf("expected idx 0 when no match, got %d", m2.settingsSelectorIdx)
	}
}

func TestCloseSettingsSelector(t *testing.T) {
	m := &Model{}
	m.startSettingsSelector("f", "v", []string{"A"}, []string{"a"})
	m.closeSettingsSelector()
	if m.settingsSelectorActive {
		t.Error("expected selector inactive")
	}
	if m.settingsSelectorItems != nil || m.settingsSelectorValues != nil {
		t.Error("expected items/values cleared")
	}
}

func TestRenderSettingsSelector(t *testing.T) {
	m := &Model{width: 80, height: 24}
	m.startSettingsSelector("f", "b", []string{"A", "B", "C", "D"}, []string{"a", "b", "c", "d"})
	out := m.renderSettingsSelector("Title")
	if !strings.Contains(out, "Title") {
		t.Errorf("expected title in output, got %q", out)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("expected current value marked with ✓, got %q", out)
	}
}

func TestRenderSettingsSelectorSmallHeight(t *testing.T) {
	m := &Model{width: 80, height: 4}
	m.startSettingsSelector("f", "", []string{"A", "B", "C", "D", "E"}, []string{"a", "b", "c", "d", "e"})
	// Select an index that requires scroll-offset.
	m.settingsSelectorIdx = 4
	out := m.renderSettingsSelector("T")
	if out == "" {
		t.Fatal("expected non-empty output")
	}
}

func TestHandleSelectorNavigationInactive(t *testing.T) {
	m := &Model{}
	if m.handleSelectorNavigation(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Error("expected false when selector inactive")
	}
}

func TestHandleSelectorNavigationUpDown(t *testing.T) {
	m := &Model{settingsSelectorActive: true, settingsSelectorItems: []string{"A", "B", "C"}}
	// Down moves index up.
	if !m.handleSelectorNavigation(tea.KeyMsg{Type: tea.KeyDown}) {
		t.Error("expected down consumed")
	}
	if m.settingsSelectorIdx != 1 {
		t.Errorf("expected idx 1 after down, got %d", m.settingsSelectorIdx)
	}
	// j also down
	if !m.handleSelectorNavigation(tea.KeyMsg{Runes: []rune{'j'}, Type: tea.KeyRunes}) {
		t.Error("expected j consumed")
	}
	if m.settingsSelectorIdx != 2 {
		t.Errorf("expected idx 2 after j, got %d", m.settingsSelectorIdx)
	}
	// Down at bottom stays.
	m.handleSelectorNavigation(tea.KeyMsg{Type: tea.KeyDown})
	if m.settingsSelectorIdx != 2 {
		t.Errorf("expected idx clamped at 2, got %d", m.settingsSelectorIdx)
	}
	// Up / k.
	if !m.handleSelectorNavigation(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Error("expected up consumed")
	}
	m.handleSelectorNavigation(tea.KeyMsg{Runes: []rune{'k'}, Type: tea.KeyRunes})
	if m.settingsSelectorIdx != 0 {
		t.Errorf("expected idx 0 after up+k, got %d", m.settingsSelectorIdx)
	}
	// Up at top stays at 0.
	m.handleSelectorNavigation(tea.KeyMsg{Type: tea.KeyUp})
	if m.settingsSelectorIdx != 0 {
		t.Errorf("expected idx 0 after up at top, got %d", m.settingsSelectorIdx)
	}
	// Other keys not consumed.
	if m.handleSelectorNavigation(tea.KeyMsg{Runes: []rune{'x'}, Type: tea.KeyRunes}) {
		t.Error("expected other key not consumed")
	}
}

func TestHandleSelectorConfirmInactive(t *testing.T) {
	m := &Model{}
	if cmd := m.handleSelectorConfirm(); cmd != nil {
		t.Error("expected nil when inactive")
	}
}

func TestHandleSelectorConfirmOutOfRange(t *testing.T) {
	m := &Model{settingsSelectorActive: true, settingsSelectorValues: []string{"a"}, settingsSelectorIdx: 5}
	if cmd := m.handleSelectorConfirm(); cmd != nil {
		t.Error("expected nil when idx out of range")
	}
}

func TestHandleSelectorConfirmSystem(t *testing.T) {
	cfg := testModelConfig(t)
	m := newTestModelWithConfig(t, cfg, true)
	m.modalMode = ModalSettingsSystemEdit
	m.startSettingsSelector("ephemeralThreshold", "", []string{"30"}, []string{"30"})
	m.settingsEditField = "ephemeralThreshold"
	cmd := m.handleSelectorConfirm()
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	if m.settingsSelectorActive {
		t.Error("expected selector closed after confirm")
	}
	if m.cfg.Session.EphemeralThreshold != 30 {
		t.Errorf("expected threshold 30, got %d", m.cfg.Session.EphemeralThreshold)
	}
}

func TestHandleSelectorConfirmAgent(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Agents.List = []config.AgentConfig{{ID: "a1", Name: "Agent One"}}
	cfg.Agents.Defaults.Temperature = nil
	m := newTestModelWithConfig(t, cfg, true)
	m.modalMode = ModalSettingsAgentEdit
	m.startSettingsSelector("agentField", "", []string{"1"}, []string{"1"})
	m.settingsEditField = "agentTemperature"
	cmd := m.handleSelectorConfirm()
	if cmd != nil {
		t.Error("expected nil cmd")
	}
	// agentTemperature parsed into agent.Temperature via handleAgentSettingsInput.
	if m.settingsEditField != "" {
		t.Errorf("expected settingsEditField cleared, got %q", m.settingsEditField)
	}
}

func TestHandleSelectorCancel(t *testing.T) {
	cfg := testModelConfig(t)
	m := newTestModelWithConfig(t, cfg, true)
	m.modalMode = ModalSettingsSystemEdit
	m.settingsSection = sysSubViewName(sysGroupSession)
	m.startSettingsSelector("f", "", []string{"A"}, []string{"a"})
	m.handleSelectorCancel()
	if m.settingsSelectorActive {
		t.Error("expected selector closed")
	}
	if m.settingsEditField != "" {
		t.Error("expected edit field cleared")
	}
	if len(m.modalItems) == 0 {
		t.Error("expected sub-view reloaded")
	}
}

func TestHandleSelectorCancelInactive(t *testing.T) {
	m := &Model{}
	m.handleSelectorCancel() // no-op, no panic
}

func TestHandleSelectorCancelAgent(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Agents.List = []config.AgentConfig{{ID: "a1"}}
	m := newTestModelWithConfig(t, cfg, true)
	m.modalMode = ModalSettingsAgentEdit
	m.settingsAgentID = "a1"
	m.startSettingsSelector("f", "", []string{"A"}, []string{"a"})
	m.handleSelectorCancel()
	if m.settingsSelectorActive {
		t.Error("expected selector closed")
	}
	if m.settingsAgentID != "a1" {
		t.Error("expected agent detail reloaded")
	}
}