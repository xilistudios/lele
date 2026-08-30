package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
)

// newFocusTestModel builds a minimal Model for focus-state tests.
// syncChatInputFocus and applyThemeToInputs only touch chatInput/textInput
// styles, so a bare literal is sufficient.
func newFocusTestModel() *Model {
	m := &Model{chatInput: textarea.New()}
	m.chatInput.Focus()
	return m
}

func TestSyncChatInputFocus(t *testing.T) {
	tests := []struct {
		name            string
		modalMode       modalType
		pendingApproval string
		onboarding      bool
		wantFocused     bool
		wantCmd         bool
	}{
		{
			name:        "active state keeps focus without cmd",
			wantFocused: true,
			wantCmd:     false,
		},
		{
			name:        "modal open blurs input",
			modalMode:   ModalSessions,
			wantFocused: false,
			wantCmd:     false,
		},
		{
			name:            "pending approval blurs input",
			pendingApproval: "approval-1",
			wantFocused:     false,
			wantCmd:         false,
		},
		{
			name:        "onboarding blurs input",
			onboarding:  true,
			wantFocused: false,
			wantCmd:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newFocusTestModel()
			m.modalMode = tt.modalMode
			m.pendingApprovalID = tt.pendingApproval
			m.onboardingActive = tt.onboarding

			cmd := m.syncChatInputFocus()

			if got := m.chatInput.Focused(); got != tt.wantFocused {
				t.Fatalf("Focused() = %v, want %v", got, tt.wantFocused)
			}
			if got := cmd != nil; got != tt.wantCmd {
				t.Fatalf("sync returned cmd = %v, want %v", got, tt.wantCmd)
			}
		})
	}
}

func TestSyncChatInputFocus_RefocusAfterModalReturnsCmd(t *testing.T) {
	m := newFocusTestModel()

	// Open a modal: input must blur.
	m.modalMode = ModalSessions
	if cmd := m.syncChatInputFocus(); cmd != nil {
		t.Fatalf("blur transition returned a cmd, want nil")
	}
	if m.chatInput.Focused() {
		t.Fatal("input still focused while modal is open")
	}

	// Close the modal: input must re-focus and return a blink cmd.
	m.modalMode = ModalNone
	cmd := m.syncChatInputFocus()
	if cmd == nil {
		t.Fatal("refocus transition returned nil cmd, want non-nil blink cmd")
	}
	if !m.chatInput.Focused() {
		t.Fatal("input not focused after modal closed")
	}
}

func TestSyncChatInputFocus_Idempotent(t *testing.T) {
	m := newFocusTestModel()

	// Already focused: repeated syncs are stable and return no cmd.
	if cmd := m.syncChatInputFocus(); cmd != nil {
		t.Fatalf("first sync on focused input returned cmd, want nil")
	}
	if cmd := m.syncChatInputFocus(); cmd != nil {
		t.Fatalf("second sync on focused input returned cmd, want nil")
	}
	if !m.chatInput.Focused() {
		t.Fatal("input lost focus after repeated syncs")
	}

	// Already blurred: repeated syncs are stable and return no cmd.
	m.modalMode = ModalSessions
	_ = m.syncChatInputFocus()
	_ = m.syncChatInputFocus()
	if m.chatInput.Focused() {
		t.Fatal("input regained focus while modal stayed open")
	}
}

func TestApplyThemeToInputs_PreservesFocusState(t *testing.T) {
	t.Run("focused stays focused", func(t *testing.T) {
		m := newFocusTestModel()
		m.applyThemeToInputs()
		if !m.chatInput.Focused() {
			t.Fatal("applyThemeToInputs blurred a focused input")
		}
	})

	t.Run("blurred stays blurred", func(t *testing.T) {
		m := newFocusTestModel()
		m.chatInput.Blur()
		m.applyThemeToInputs()
		if m.chatInput.Focused() {
			t.Fatal("applyThemeToInputs focused a blurred input")
		}
	})
}
