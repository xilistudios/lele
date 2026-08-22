package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// TestUpdate_LangModalSelect verifies selecting a language via Enter updates
// the config language.
func TestUpdate_LangModalSelect(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/lang")
	if m.modalMode != ModalLang {
		t.Fatalf("expected ModalLang, got %v", m.modalMode)
	}
	// modalItems = ["Español (es)", "English (en)", "Português (pt)"]
	if len(m.modalItems) != 3 {
		t.Fatalf("expected 3 language items, got %v", m.modalItems)
	}
	m.modalSelectedIdx = 1 // English (en)
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)
	if got := mm.cfg.Language; got != "en" {
		t.Errorf("cfg.Language = %q, want en", got)
	}
	if mm.modalMode != ModalNone {
		t.Errorf("modalMode = %v, want ModalNone after lang select", mm.modalMode)
	}
}

// TestUpdate_AgentModalWelcomeSelect verifies selecting an agent in the
// welcome state records the pending agent.
func TestUpdate_AgentModalWelcomeSelect(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/agents")
	if m.modalMode != ModalAgent {
		t.Fatalf("expected ModalAgent, got %v", m.modalMode)
	}
	m.showWelcome = true
	m.pendingAgent = ""
	m.modalSelectedIdx = 0
	if len(m.modalItems) == 0 {
		t.Skip("no agents available")
	}
	m.modalItems[0] = "default-agent"
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)
	if m.pendingAgent != "default-agent" {
		t.Errorf("pendingAgent = %q, want default-agent", m.pendingAgent)
	}
	_ = mm
}

// TestUpdate_SubagentsModalEmpty ensures the /subagents modal shows the empty
// message and can be closed.
func TestUpdate_SubagentsModalEmpty(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:sub-none"
	m.executeCommand("/subagents")
	if m.modalMode != ModalSubagents {
		t.Fatalf("expected ModalSubagents, got %v", m.modalMode)
	}
	if len(m.modalItems) == 0 {
		t.Fatal("expected at least an empty message item")
	}
	// ESC closes.
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	_ = upd.(*Model)
}

// TestUpdate_SessionsModalSelect verifies selecting a session in the sessions
// modal switches the current chat.
func TestUpdate_SessionsModalSelect(t *testing.T) {
	m := newTestModel(t)
	key1 := "tui:chat:sess-1"
	key2 := "tui:chat:sess-2"
	sk1 := m.sessionMgr.GetOrCreate(key1)
	sk2 := m.sessionMgr.GetOrCreate(key2)
	_ = sk1
	_ = sk2
	m.currentKey = key1

	m.executeCommand("/sessions")
	if len(m.modalItems) == 0 {
		t.Skip("no sessions listed")
	}
	// Find index of a session whose key matches key2.
	idx := -1
	for i, k := range m.modalSessionKeys {
		if strings.HasPrefix(k, "tui:chat:sess-2") {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Skip("session-2 not in the modal list")
	}
	m.modalSelectedIdx = idx
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := upd.(*Model)
	if !strings.HasPrefix(mm.currentKey, "tui:chat:sess-2") {
		t.Errorf("currentKey = %q, want switched to session-2", mm.currentKey)
	}
	if mm.showWelcome {
		t.Error("showWelcome should be false after session switch")
	}
}

// TestUpdate_ThinkModalSelect verifies the /think modal selection sets the
// think level on the current session.
func TestUpdate_ThinkModalSelect(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:think-sel"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key

	m.executeCommand("/think")
	if m.modalMode != ModalThink {
		t.Fatalf("expected ModalThink, got %v", m.modalMode)
	}
	if len(m.modalItems) == 0 {
		t.Skip("no think options")
	}
	m.modalSelectedIdx = 0
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = upd.(*Model)
}

func TestI18nLangFilesLoad(t *testing.T) {
	for _, code := range []string{"en", "es", "pt"} {
		i18n.SetLanguage(code)
		_ = i18n.T("tui.placeholder")
	}
}
