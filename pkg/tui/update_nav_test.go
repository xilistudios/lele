package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// updateKeys drives the model through the given key presses via Update,
// asserting no panic and returning the final model.
func updateKeys(t *testing.T, m *Model, keys ...tea.Msg) *Model {
	t.Helper()
	cur := tea.Model(m)
	for _, k := range keys {
		updated, _ := cur.Update(k)
		cur = updated
	}
	return cur.(*Model)
}

func keyRunes(r string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)}
}

func keyEnter() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

func keyEsc() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEsc}
}

func keyQ() tea.KeyMsg {
	return keyRunes("q")
}

// TestUpdateSettingsNavigation exercises the settings menu entry, sub-menu
// navigation, and back-navigation through Update.
func TestUpdateSettingsNavigation(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/settings")
	if m.modalMode != ModalSettings {
		t.Fatalf("expected ModalSettings, got %v", m.modalMode)
	}

	// Navigate into Agents (index 0).
	m = updateKeys(t, m, keyEnter())
	if m.modalMode != ModalSettingsAgents {
		t.Fatalf("expected ModalSettingsAgents, got %v", m.modalMode)
	}

	// ESC goes back to top-level settings.
	m = updateKeys(t, m, keyEsc())
	if m.modalMode != ModalSettings {
		t.Fatalf("expected back to ModalSettings, got %v", m.modalMode)
	}

	// Navigate into System (index 1).
	m.modalSelectedIdx = 1
	m = updateKeys(t, m, keyEnter())
	if m.modalMode != ModalSettingsSystem {
		t.Fatalf("expected ModalSettingsSystem, got %v", m.modalMode)
	}

	// ESC back.
	m = updateKeys(t, m, keyEsc())
	if m.modalMode != ModalSettings {
		t.Fatalf("expected back to ModalSettings, got %v", m.modalMode)
	}

	// Navigate into Interface (index 2).
	m.modalSelectedIdx = 2
	m = updateKeys(t, m, keyEnter())
	if m.modalMode != ModalSettingsTUI {
		t.Fatalf("expected ModalSettingsTUI, got %v", m.modalMode)
	}
	// ESC back.
	m = updateKeys(t, m, keyEsc())
	if m.modalMode != ModalSettings {
		t.Fatalf("expected back to ModalSettings, got %v", m.modalMode)
	}
}

// TestUpdateSystemSubViewNavigation drives into each system sub-group via
// Update and back out.
func TestUpdateSystemSubViewNavigation(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/settings")
	m.modalSelectedIdx = 1
	m = updateKeys(t, m, keyEnter()) // -> ModalSettingsSystem

	groups := []struct {
		idx  int
		load func()
	}{
		{sysGroupSession, nil},
		{sysGroupTools, nil},
		{sysGroupLogs, nil},
		{sysGroupLanguage, nil},
		{sysGroupGoal, nil},
		{sysGroupUpdates, nil},
	}
	for _, g := range groups {
		m.modalSelectedIdx = g.idx
		m = updateKeys(t, m, keyEnter()) // enter sub-view
		if m.modalMode != ModalSettingsSystemEdit {
			t.Fatalf("group %d: expected ModalSettingsSystemEdit, got %v", g.idx, m.modalMode)
		}
		if len(m.modalItems) == 0 {
			t.Errorf("group %d: expected modal items", g.idx)
		}
		// ESC back to system list.
		m = updateKeys(t, m, keyEsc())
		if m.modalMode != ModalSettingsSystem {
			t.Fatalf("group %d: expected back to ModalSettingsSystem, got %v", g.idx, m.modalMode)
		}
	}
}

// TestUpdateSystemEditToggle tests a system sub-view inline toggle via Update.
func TestUpdateSystemEditToggle(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/settings")
	m.modalSelectedIdx = 1
	m = updateKeys(t, m, keyEnter()) // ModalSettingsSystem
	m.modalSelectedIdx = sysGroupSession
	m = updateKeys(t, m, keyEnter()) // ModalSettingsSystemEdit (session)
	m.modalSelectedIdx = 0           // ephemeral toggle
	m = updateKeys(t, m, keyEnter())
	if !m.cfg.Session.Ephemeral {
		t.Fatal("expected ephemeral toggled via Update")
	}
}

// TestUpdateAgentsNavigation drives the agents list into detail and back.
func TestUpdateAgentsNavigation(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/settings")
	m.modalSelectedIdx = 0
	m = updateKeys(t, m, keyEnter()) // ModalSettingsAgents

	// Enter on "Defaults" (index 0) opens the defaults detail view.
	m.modalSelectedIdx = 0
	m = updateKeys(t, m, keyEnter())
	if m.modalMode != ModalSettingsAgentEdit {
		t.Fatalf("expected ModalSettingsAgentEdit, got %v", m.modalMode)
	}
	// ESC back to agents list.
	m = updateKeys(t, m, keyEsc())
	if m.modalMode != ModalSettingsAgents {
		t.Fatalf("expected back to ModalSettingsAgents, got %v", m.modalMode)
	}
}

// TestUpdateSessionModalNavigation tests /sessions selection.
func TestUpdateSessionModalNavigation(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:upd-sess"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "hello")
	m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key

	m.executeCommand("/sessions")
	if m.modalMode != ModalSessions {
		t.Fatalf("expected ModalSessions, got %v", m.modalMode)
	}
	// ESC closes.
	m = updateKeys(t, m, keyEsc())
	if m.modalMode != ModalNone {
		t.Fatalf("expected ModalNone after esc, got %v", m.modalMode)
	}
}

// TestUpdateThinkModalNavigation tests /think modal.
func TestUpdateThinkModalNavigation(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/think")
	if m.modalMode != ModalThink {
		t.Fatalf("expected ModalThink, got %v", m.modalMode)
	}
	m = updateKeys(t, m, keyQ())
	if m.modalMode != ModalNone {
		t.Fatalf("expected ModalNone after q, got %v", m.modalMode)
	}
}

// TestUpdateLangModalNavigation tests /lang modal.
func TestUpdateLangModalNavigation(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/lang")
	if m.modalMode != ModalLang {
		t.Fatalf("expected ModalLang, got %v", m.modalMode)
	}
	m = updateKeys(t, m, keyQ())
	if m.modalMode != ModalNone {
		t.Fatalf("expected ModalNone after q, got %v", m.modalMode)
	}
}

// TestUpdateMouseNav exercises mouse wheel navigation in a modal list.
func TestUpdateMouseNav(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/think")
	m.modalItems = []string{"a", "b", "c", "d"}
	// Down navigation.
	m = updateKeys(t, m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	if m.modalSelectedIdx != 0 {
		// Wheel down doesn't change selection if already at 0; just verify no panic.
		t.Log("selection after wheel down:", m.modalSelectedIdx)
	}
	// Up navigation.
	m = updateKeys(t, m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
	})
	// Close modal.
	m = updateKeys(t, m, keyQ())
	if m.modalMode != ModalNone {
		t.Fatalf("expected ModalNone after q, got %v", m.modalMode)
	}
}
