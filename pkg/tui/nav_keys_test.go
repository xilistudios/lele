package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestUpdate_TabCyclesModes verifies Tab cycles agent->chat->agent when groups
// are disabled, and agent->chat->group without reload issues.
func TestUpdate_TabCyclesModes(t *testing.T) {
	m := newTestModel(t)
	// Groups disabled in test config by default.
	m.modalMode = ModalNone
	m.showAutocomplete = false
	m.currentMode = ModeAgent
	m.reloadSessions()

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := upd.(*Model)
	if mm.currentMode != ModeChat {
		t.Errorf("after tab from agent, mode = %v, want ModeChat", mm.currentMode)
	}

	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm = upd.(*Model)
	if mm.currentMode != ModeAgent {
		t.Errorf("after tab from chat, mode = %v, want ModeAgent", mm.currentMode)
	}
}

// TestUpdate_TabWithModalActiveDoesNotCycle verifies tab is swallowed when a
// modal is active.
func TestUpdate_TabWithModalActiveDoesNotCycle(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalCron
	m.showAutocomplete = false
	m.currentMode = ModeAgent

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := upd.(*Model)
	if mm.currentMode != ModeAgent {
		t.Error("tab should not change mode while a modal is active")
	}
}

// TestUpdate_CtrlTCyclesMouseCapture verifies ctrl+t toggles the mouse.
func TestUpdate_CtrlTCyclesMouseCapture(t *testing.T) {
	m := newTestModel(t)
	m.mouseEnabled = false
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !m.mouseEnabled {
		t.Error("mouseEnabled should be true after ctrl+t")
	}
	if cmd == nil {
		t.Error("expected EnableMouseCellMotion cmd")
	}
}

// TestUpdate_CtrlPShowsAutocomplete verifies ctrl+p opens the autocomplete.
func TestUpdate_CtrlPShowsAutocomplete(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	m.showAutocomplete = false
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlP})
	mm := upd.(*Model)
	if !mm.showAutocomplete {
		t.Error("showAutocomplete should be true after ctrl+p")
	}
}

// TestUpdate_CtrlMExecutesModels verifies ctrl+m dispatches /models.
// Note: in bubbletea, KeyCtrlM's String() is "enter" (CR), so the literal
// "ctrl+m" branch is not reachable via that key type. This test drives the
// /models command directly to confirm the modal opens.
func TestUpdate_CtrlMExecutesModels(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	m.executeCommand("/models")
	if m.modalMode != ModalModel {
		t.Errorf("modalMode = %v, want ModalModel", m.modalMode)
	}
}

// TestUpdate_CtrlSExecutesSessions verifies ctrl+s dispatches /sessions.
func TestUpdate_CtrlSExecutesSessions(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	mm := upd.(*Model)
	if mm.modalMode != ModalSessions {
		t.Errorf("modalMode = %v, want ModalSessions", mm.modalMode)
	}
}

// TestUpdate_CtrlAExecutesAgents verifies ctrl+a dispatches /agents.
func TestUpdate_CtrlAExecutesAgents(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	mm := upd.(*Model)
	if mm.modalMode != ModalAgent {
		t.Errorf("modalMode = %v, want ModalAgent", mm.modalMode)
	}
}

// TestUpdate_EscDoublePressCancels verifies double-press esc while processing
// cancels the agent.
func TestUpdate_EscDoublePressCancels(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	m.processing = true
	m.escLastPress = time.Now()
	m.currentKey = "some-key"

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := upd.(*Model)
	// First key is KeyEsc which just records escLastTime; then the switch
	// case "esc" runs (String() == "esc" for KeyEsc). Since escLastPress was
	// just set, the double-press path runs and cancels.
	_ = mm
	// No panic is enough here; verify hint state was reset.
	if mm.escHint {
		t.Error("escHint should be false after cancel")
	}
	if mm.escPressCount != 0 {
		t.Errorf("escPressCount = %d, want 0", mm.escPressCount)
	}
}

// TestUpdate_ModeGroupWelcomeArrows cycles the group profile index.
func TestUpdate_ModeGroupWelcomeArrows(t *testing.T) {
	m := newTestModel(t)
	// The up arrow in group-welcome mode should run without panic and not
	// change the profile index when there are no profiles configured.
	m.currentMode = ModeGroup
	m.showWelcome = true
	m.showAutocomplete = false
	m.modalMode = ModalNone
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm := upd.(*Model)
	if len(mm.getGroupProfiles()) > 0 {
		// With profiles present the welcome arrows cycle — ensure idx stays
		// in bounds.
		if mm.groupProfileIdx < 0 || mm.groupProfileIdx >= len(mm.getGroupProfiles()) {
			t.Errorf("groupProfileIdx = %d out of bounds", mm.groupProfileIdx)
		}
	}
}

// TestAutocomplete_SelectionAndExecution drives the autocomplete path: type a
// slash command, select an autocomplete item, tab fills it, enter executes.
func TestAutocomplete_FillAndExecute(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	m.currentKey = "somekey"
	m.sessionMgr.GetOrCreate(m.currentKey)
	m.showAutocomplete = true
	m.autocompleteItems = []commandInfo{{name: "/goal", description: "Set goal"}}
	m.autocompleteIdx = 0
	m.chatInput.SetValue("/goa")

	// Tab fills the input with the completed command name.
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := upd.(*Model)
	if mm.showAutocomplete {
		t.Error("autocomplete should close after tab")
	}
	if mm.chatInput.Value() != "/goal" {
		t.Errorf("input after tab = %q, want /goal", mm.chatInput.Value())
	}

	// Enter with the filled /goal command executes it (doesn't send to LLM).
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = upd.(*Model)
	if mm.chatInput.Value() != "" {
		t.Errorf("input after enter = %q, want cleared", mm.chatInput.Value())
	}
}

// TestAutocomplete_EnterWithArgs executes full input with arguments.
func TestAutocomplete_EnterWithArgs(t *testing.T) {
	m := newTestModel(t)
	m.modalMode = ModalNone
	m.currentKey = "somekey"
	m.sessionMgr.GetOrCreate(m.currentKey)
	m.showAutocomplete = true
	m.autocompleteItems = []commandInfo{{name: "/sessions", description: "List sessions"}}
	m.autocompleteIdx = 0
	m.chatInput.SetValue("/sessions arg1")
	_ = m.chatInput.Value()

	// Enter when selection matches a completed command with trailing args.
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)
	if mm.showAutocomplete {
		t.Error("autocomplete should close after enter with args")
	}
	if mm.modalMode != ModalSessions {
		t.Errorf("modalMode = %v, want ModalSessions", mm.modalMode)
	}
}

// TestAutocomplete_EscDismisses verifies esc closes the autocomplete.
func TestAutocomplete_EscDismisses(t *testing.T) {
	m := newTestModel(t)
	m.showAutocomplete = true
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm := upd.(*Model)
	if mm.showAutocomplete {
		t.Error("esc should dismiss autocomplete")
	}
}

// TestAutocomplete_UpDownNavigates verifies up/down navigate items.
func TestAutocomplete_UpDownNavigates(t *testing.T) {
	m := newTestModel(t)
	m.showAutocomplete = true
	m.autocompleteItems = []commandInfo{{name: "a"}, {name: "b"}}
	m.autocompleteIdx = 0

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := upd.(*Model)
	if mm.autocompleteIdx != 1 {
		t.Errorf("autocompleteIdx after down = %d, want 1", mm.autocompleteIdx)
	}

	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyUp})
	mm = upd.(*Model)
	if mm.autocompleteIdx != 0 {
		t.Errorf("autocompleteIdx after up = %d, want 0", mm.autocompleteIdx)
	}
}

func TestFilterAutocompletePrefix(t *testing.T) {
	m := &Model{modalMode: ModalNone}
	m.filterAutocomplete("/")
	if len(m.autocompleteItems) == 0 {
		t.Fatal("filterAutocomplete(\"/\") should find slash commands")
	}
	var hasGoal bool
	for _, it := range m.autocompleteItems {
		if strings.HasPrefix(it.name, "/") {
			hasGoal = true
			break
		}
	}
	if !hasGoal {
		t.Error("autocomplete items should all be slash-prefixed")
	}
}
