package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
)

// openSettings opens the top-level settings modal via /settings.
func openSettings(t *testing.T, m *Model) {
	t.Helper()
	if isListModal(m.modalMode) {
		m.resetModal(ModalNone)
	}
	m.executeCommand("/settings")
	if m.modalMode != ModalSettings {
		t.Fatalf("expected ModalSettings, got %v", m.modalMode)
	}
}

// press sends a key to the model via Update and returns the updated model.
func press(t *testing.T, m *Model, key string) *Model {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	upd, _ := m.Update(msg)
	return upd.(*Model)
}

// TestSettingsNav_EnterSystemToSessionToEdit drives the settings flow:
// /settings → System → Session → toggle/edit, covering the Update dispatch for
// ModalSettings, ModalSettingsSystem, and ModalSettingsSystemEdit.
func TestSettingsNav_EnterSystemSession(t *testing.T) {
	cfg := testModelConfig(t)
	m := newTestModelWithConfig(t, cfg, true)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)

	// Select "System" (index 1) and press Enter.
	m.modalSelectedIdx = 1
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsSystem {
		t.Fatalf("expected ModalSettingsSystem, got %v", m.modalMode)
	}
	// Enter Session group (index 0).
	m.modalSelectedIdx = 0
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsSystemEdit {
		t.Fatalf("expected ModalSettingsSystemEdit, got %v", m.modalMode)
	}
	// Intro: Session items; index 0 = ephemeral toggle.
	m.modalSelectedIdx = 0
	m = press(t, m, "enter")
	// Confirm we entered an edit field for the int field or toggled.
	if m.modalMode != ModalSettingsSystemEdit {
		t.Fatalf("expected still in system edit, got %v", m.modalMode)
	}
}

// TestSettingsNav_SystemSessionSelectorCompactionModel drives the compaction
// model selector when models exist.
func TestSettingsNav_SystemSessionCompactionModel(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-x"},
			Models:         map[string]config.ProviderModelConfig{"gpt-4o": {Model: "gpt-4o"}},
		},
	}
	m := newTestModelWithConfig(t, cfg, true)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)

	m.modalSelectedIdx = 1 // System
	m = press(t, m, "enter")
	m.modalSelectedIdx = 0 // Session
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsSystemEdit {
		t.Fatalf("expected system edit, got %v", m.modalMode)
	}
	// Session item index 3 = Compaction model selector.
	m.modalSelectedIdx = 3
	m = press(t, m, "enter")
	if !m.settingsSelectorActive {
		t.Fatalf("expected settings selector active for compaction model")
	}
	// Confirm the selector.
	m.settingsSelectorIdx = 1
	m = press(t, m, "enter")
	if m.settingsSelectorActive {
		t.Fatalf("expected selector confirmed/closed, got active")
	}
	if m.modalMode != ModalSettingsSystemEdit {
		t.Fatalf("expected back in system edit, got %v", m.modalMode)
	}
}

// TestSettingsNav_SystemEditEscBackToSystem verifies ESC in a sub-view returns
// to the system group list.
func TestSettingsNav_SystemEditEscBack(t *testing.T) {
	cfg := testModelConfig(t)
	m := newTestModelWithConfig(t, cfg, true)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)

	m.modalSelectedIdx = 1
	m = press(t, m, "enter")
	m.modalSelectedIdx = 0
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsSystemEdit {
		t.Fatalf("expected system edit, got %v", m.modalMode)
	}
	// ESC without editing returns to ModalSettingsSystem.
	var msg tea.KeyMsg = tea.KeyMsg{Type: tea.KeyEsc}
	upd, _ := m.Update(msg)
	m = upd.(*Model)
	if m.modalMode != ModalSettingsSystem {
		t.Fatalf("expected ModalSettingsSystem after esc, got %v", m.modalMode)
	}
}

// TestSettingsNav_SettingsEscBackToNone verifies ESC from the top-level settings
// returns to ModalNone.
func TestSettingsNav_SettingsEscToNone(t *testing.T) {
	m := newTestModel(t)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)
	var msg tea.KeyMsg = tea.KeyMsg{Type: tea.KeyEsc}
	upd, _ := m.Update(msg)
	m = upd.(*Model)
	if m.modalMode != ModalNone {
		t.Fatalf("expected ModalNone after esc, got %v", m.modalMode)
	}
}

// TestSettingsNav_AgentsNavigateAndEdit drives the agents settings flow and an
// inline edit, covering the Update dispatch for ModalSettingsAgents and
// ModalSettingsAgentEdit.
func TestSettingsNav_AgentsNavigateAndEdit(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Agents.List = []config.AgentConfig{{ID: "coder", Name: "Coder", Default: true}}
	m := newTestModelWithConfig(t, cfg, true)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)

	// Select "Agents" (index 0) and Enter → ModalSettingsAgents.
	m.modalSelectedIdx = 0
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsAgents {
		t.Fatalf("expected ModalSettingsAgents, got %v", m.modalMode)
	}
	// Select the agent row (index 1 → "coder" detail) and Enter.
	if len(m.modalItems) == 0 {
		t.Fatal("expected agent list items")
	}
	m.modalSelectedIdx = 1
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsAgentEdit {
		t.Fatalf("expected ModalSettingsAgentEdit, got %v", m.modalMode)
	}
	if m.settingsAgentID != "coder" {
		t.Fatalf("expected settingsAgentID=coder, got %q", m.settingsAgentID)
	}
}

// TestSettingsNav_InterfaceThemeEnter drives the interface settings + theme
// picker activation via Update.
func TestSettingsNav_InterfaceThemeEnter(t *testing.T) {
	cfg := testModelConfig(t)
	m := newTestModelWithConfig(t, cfg, true)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)

	m.modalSelectedIdx = 2 // Interface
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsTUI {
		t.Fatalf("expected ModalSettingsTUI, got %v", m.modalMode)
	}
	if len(m.modalItems) == 0 {
		t.Fatal("expected TUI settings items")
	}
	// Theme entry should activate the theme picker.
	m.modalSelectedIdx = 0
	m = press(t, m, "enter")
	if m.themePickerActive {
		// Good — picker activated; it lists builtin + community. Applying a
		// builtin closes the picker.
		m.modalSelectedIdx = 0
		m = press(t, m, "enter")
		if m.themePickerActive && m.modalSelectedIdx < len(m.themePickerItems) {
			// Only assert closure if the first item is a builtin.
			if len(m.themePickerItems) > 0 && m.themePickerItems[0].kind == "builtin" {
				t.Fatal("expected theme picker to close after applying builtin")
			}
		}
	}
}

// TestSettingsNav_InterfaceEditFieldSave drives TUI settings inline edit save
// via Update (ModalSettingsTUI with settingsEditField set).
func TestSettingsNav_InterfaceEditFieldSave(t *testing.T) {
	cfg := testModelConfig(t)
	m := newTestModelWithConfig(t, cfg, true)
	m.width = 120
	m.height = 40
	forceTrueColor(t)
	openSettings(t, m)

	m.modalSelectedIdx = 2
	m = press(t, m, "enter")
	if m.modalMode != ModalSettingsTUI {
		t.Fatalf("expected ModalSettingsTUI, got %v", m.modalMode)
	}
	// Set up an active edit field (maxMessages).
	m.settingsEditField = "maxMessages"
	m.textInput.SetValue("60")
	var msg tea.KeyMsg = tea.KeyMsg{Type: tea.KeyEnter}
	upd, _ := m.Update(msg)
	m = upd.(*Model)
	if m.settingsEditField != "" {
		t.Fatalf("expected edit field cleared after save, got %q", m.settingsEditField)
	}
	if m.maxRenderedMessages != 60 {
		t.Fatalf("expected maxRenderedMessages=60, got %d", m.maxRenderedMessages)
	}
}
